// Copyright (c) 2020 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Copyright (c) 2020  All rights reserved.

package tc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/libcalico-go/lib/set"

	"github.com/projectcalico/felix/bpf"
)

type AttachPoint struct {
	Type       EndpointType
	ToOrFrom   ToOrFromEp
	Hook       Hook
	Iface      string
	LogLevel   string
	HostIP     net.IP
	FIB        bool
	ToHostDrop bool
	DSR        bool
	TunnelMTU  uint16
}

var tcLock sync.RWMutex

type ErrAttachFailed struct {
	ExitCode int
	Stderr   string
}

func (e ErrAttachFailed) Error() string {
	return fmt.Sprintf("tc failed with exit code %d; stderr=%v", e.ExitCode, e.Stderr)
}

var ErrDeviceNotFound = errors.New("device not found")
var prefHandleRe = regexp.MustCompile(`pref ([^ ]+) .* handle ([^ ]+)`)

// AttachProgram attaches a BPF program from a file to the TC attach point
func (ap AttachPoint) AttachProgram() error {
	logCxt := log.WithField("attachPoint", ap)

	tempDir, err := ioutil.TempDir("", "calico-tc")
	if err != nil {
		return errors.Wrap(err, "failed to create temporary directory")
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	filename := ap.FileName()
	preCompiledBinary := path.Join(bpf.ObjectDir, filename)
	tempBinary := path.Join(tempDir, filename)

	err = ap.patchBinary(logCxt, preCompiledBinary, tempBinary)
	if err != nil {
		logCxt.WithError(err).Error("Failed to patch binary")
		return err
	}

	// Using the RLock allows multiple attach calls to proceed in parallel unless
	// CleanUpJumpMaps() (which takes the writer lock) is running.
	logCxt.Debug("AttachProgram waiting for lock...")
	tcLock.RLock()
	defer tcLock.RUnlock()
	logCxt.Debug("AttachProgram got lock.")

	err = EnsureQdisc(ap.Iface)
	if err != nil {
		return err
	}

	tcCmd := exec.Command("tc", "filter", "show", "dev", ap.Iface, string(ap.Hook))
	out, err := tcCmd.Output()
	if err != nil {
		return errors.WithMessage(err, "failed to list tc filters on interface "+ap.Iface)
	}
	// Lines look like this; the section name always includes calico.
	// filter protocol all pref 49152 bpf chain 0 handle 0x1 to_hep_no_log.o:[calico_to_host_ep] direct-action not_in_hw id 821 tag ee402594f8f85ac3 jited
	type progToCleanUp struct {
		pref   string
		handle string
	}
	var progsToClean []progToCleanUp
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "calico") {
			continue
		}
		// find the pref and the handle
		if sm := prefHandleRe.FindStringSubmatch(line); len(sm) > 0 {
			p := progToCleanUp{
				pref:   sm[1],
				handle: sm[2],
			}
			log.WithField("prog", p).Debug("Found old calico program")
			progsToClean = append(progsToClean, p)
		}
	}

	tcCmd = exec.Command("tc",
		"filter", "add", "dev", ap.Iface,
		string(ap.Hook),
		"bpf", "da", "obj", tempBinary,
		"sec", SectionName(ap.Type, ap.ToOrFrom))

	out, err = tcCmd.Output()
	if err != nil {
		if err, ok := err.(*exec.ExitError); ok {
			stderr := string(err.Stderr)
			if strings.Contains(stderr, "Cannot find device") {
				// Avoid a big, spammy log when the issue is that the interface isn't present.
				logCxt.WithField("iface", ap.Iface).Info(
					"Failed to attach BPF program; interface not found.  Will retry if it show up.")
				return ErrDeviceNotFound
			}
		}
		logCxt.WithError(err).WithFields(log.Fields{"out": out}).
			WithField("command", tcCmd).Error("Failed to attach BPF program")
		if err, ok := err.(*exec.ExitError); ok {
			// ExitError is really unhelpful dumped to the log, swap it for a custom one.
			return ErrAttachFailed{
				ExitCode: err.ExitCode(),
				Stderr:   string(err.Stderr),
			}
		}
		return errors.WithMessage(err, "failed to attach TC program")
	}

	// Success: clean up the old programs.
	var progErrs []error
	for _, p := range progsToClean {
		log.WithField("prog", p).Debug("Cleaning up old calico program")
		tcCmd := exec.Command("tc", "filter", "del", "dev", ap.Iface, string(ap.Hook), "pref", p.pref, "handle", p.handle, "bpf")
		err := tcCmd.Run()
		if err != nil {
			// TODO: filter error to avoid spam if interface is gone.
			log.WithError(err).WithField("prog", p).Warn("Failed to clean up old calico program.")
			progErrs = append(progErrs, err)
		}
	}

	if len(progErrs) != 0 {
		return fmt.Errorf("failed to clean up one or more old calico programs: %v", progErrs)
	}

	return nil
}

func (ap AttachPoint) patchBinary(logCtx *log.Entry, ifile, ofile string) error {
	b, err := bpf.BinaryFromFile(ifile)
	if err != nil {
		return errors.Wrap(err, "failed to read pre-compiled BPF binary")
	}

	logCtx.WithField("ip", ap.HostIP).Debug("Patching in IP")
	err = b.PatchIPv4(ap.HostIP)
	if err != nil {
		return errors.WithMessage(err, "patching in IPv4")
	}

	b.PatchLogPrefix(ap.Iface)
	b.PatchTunnelMTU(ap.TunnelMTU)

	err = b.WriteToFile(ofile)
	if err != nil {
		return errors.Wrap(err, "failed to write patched BPF binary")
	}

	return nil
}

// ProgramName returnt the name of the program associated with this AttachPoint
func (ap AttachPoint) ProgramName() string {
	return SectionName(ap.Type, ap.ToOrFrom)
}

// FileName return the file the AttachPoint will load the program from
func (ap AttachPoint) FileName() string {
	return ProgFilename(ap.Type, ap.ToOrFrom, ap.ToHostDrop, ap.FIB, ap.DSR, ap.LogLevel)
}

// tcDirRegex matches tc's auto-created directory names so we can clean them up when removing maps without accidentally
// removing other user-created dirs..
var tcDirRegex = regexp.MustCompile(`[0-9a-f]{40}`)

// CleanUpJumpMaps scans for cali_jump maps that are still pinned to the filesystem but no longer referenced by
// our BPF programs.
func CleanUpJumpMaps() {
	// So that we serialise with AttachProgram()
	log.Debug("CleanUpJumpMaps waiting for lock...")
	tcLock.Lock()
	defer tcLock.Unlock()
	log.Debug("CleanUpJumpMaps got lock, cleaning up...")

	// Find the maps we care about by walking the BPF filesystem.
	mapIDToPath := make(map[int]string)
	err := filepath.Walk("/sys/fs/bpf/tc", func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(info.Name(), "cali_jump") {
			log.WithField("path", p).Debug("Examining map")

			out, err := exec.Command("bpftool", "map", "show", "pinned", p).Output()
			if err != nil {
				log.WithError(err).Panic("Failed to show map")
			}
			log.WithField("dump", string(out)).Debug("Map show before deletion")
			idStr := string(bytes.Split(out, []byte(":"))[0])
			id, err := strconv.Atoi(idStr)
			if err != nil {
				log.WithError(err).WithField("dump", string(out)).Error("Failed to parse bpftool output.")
				return err
			}
			mapIDToPath[id] = p
		}
		return nil
	})
	if os.IsNotExist(err) {
		log.WithError(err).Warn("tc directory missing from BPF file system?")
		return
	}
	if err != nil {
		log.WithError(err).Error("Error while looking for maps.")
	}

	// Find all the programs that are attached to interfaces.
	out, err := exec.Command("bpftool", "net", "-j").Output()
	if err != nil {
		log.WithError(err).Panic("Failed to list attached bpf programs")
	}
	log.WithField("dump", string(out)).Debug("Attached BPF programs")

	var attached []struct {
		TC []struct {
			DevName string `json:"devname"`
			ID      int    `json:"id"`
		} `json:"tc"`
	}
	err = json.Unmarshal(out, &attached)
	if err != nil {
		log.WithError(err).WithField("dump", string(out)).Error("Failed to parse list of attached BPF programs")
	}
	attachedProgs := set.New()
	for _, prog := range attached[0].TC {
		log.WithField("prog", prog).Debug("Adding prog to attached set")
		attachedProgs.Add(prog.ID)
	}

	// Find all the maps that the attached programs refer to and remove them from consideration.
	progsJSON, err := exec.Command("bpftool", "prog", "list", "--json").Output()
	if err != nil {
		log.WithError(err).Info("Failed to list BPF programs, assuming there's nothing to clean up.")
		return
	}
	var progs []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		Maps []int  `json:"map_ids"`
	}
	err = json.Unmarshal(progsJSON, &progs)
	if err != nil {
		log.WithError(err).Info("Failed to parse bpftool output.  Assuming nothing to clean up.")
		return
	}
	for _, p := range progs {
		if !attachedProgs.Contains(p.ID) {
			log.WithField("prog", p).Debug("Prog is not in the attached set, skipping")
			continue
		}
		for _, id := range p.Maps {
			log.WithField("mapID", id).WithField("prog", p).Debug("Map is still in use")
			delete(mapIDToPath, id)
		}
	}

	// Remove the pins.
	for id, p := range mapIDToPath {
		log.WithFields(log.Fields{"id": id, "path": p}).Debug("Removing stale BPF map pin.")
		err := os.Remove(p)
		if err != nil {
			log.WithError(err).Warn("Removed stale BPF map pin.")
		}
		log.WithFields(log.Fields{"id": id, "path": p}).Info("Removed stale BPF map pin.")
	}

	// Look for empty dirs.
	emptyAutoDirs := set.New()
	err = filepath.Walk("/sys/fs/bpf/tc", func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && tcDirRegex.MatchString(info.Name()) {
			p := path.Clean(p)
			log.WithField("path", p).Debug("Found tc auto-created dir.")
			emptyAutoDirs.Add(p)
		} else {
			dirPath := path.Clean(path.Dir(p))
			if emptyAutoDirs.Contains(dirPath) {
				log.WithField("path", dirPath).Debug("tc dir is not empty.")
				emptyAutoDirs.Discard(dirPath)
			}
		}
		return nil
	})
	if os.IsNotExist(err) {
		log.WithError(err).Warn("tc directory missing from BPF file system?")
		return
	}
	if err != nil {
		log.WithError(err).Error("Error while looking for maps.")
	}

	emptyAutoDirs.Iter(func(item interface{}) error {
		p := item.(string)
		log.WithField("path", p).Debug("Removing empty dir.")
		err := os.Remove(p)
		if err != nil {
			log.WithError(err).Error("Error while removing empty dir.")
		}
		return nil
	})
}

// EnsureQdisc makes sure that qdisc is attached to the given interface
func EnsureQdisc(ifaceName string) error {
	hasQdisc, err := HasQdisc(ifaceName)
	if err != nil {
		return err
	}
	if hasQdisc {
		log.WithField("iface", ifaceName).Debug("Already have a clsact qdisc on this interface")
		return nil
	}
	return exec.Command("tc", "qdisc", "add", "dev", ifaceName, "clsact").Run()
}

func HasQdisc(ifaceName string) (bool, error) {
	cmd := exec.Command("tc", "qdisc", "show", "dev", ifaceName, "clsact")
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	if strings.Contains(string(out), "qdisc clsact") {
		return true, nil
	}
	return false, nil
}

// RemoveQdisc makes sure that there is no qdisc attached to the given interface
func RemoveQdisc(ifaceName string) error {
	hasQdisc, err := HasQdisc(ifaceName)
	if err != nil {
		return err
	}
	if !hasQdisc {
		return nil
	}

	return exec.Command("tc", "qdisc", "del", "dev", ifaceName, "clsact").Run()
}
