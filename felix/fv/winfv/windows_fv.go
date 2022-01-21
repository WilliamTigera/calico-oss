// Copyright (c) 2021 Tigera, Inc. All rights reserved.

package winfv

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/tigera/windows-networking/pkg/testutils"

	"github.com/projectcalico/calico/felix/collector"

	log "github.com/sirupsen/logrus"
)

type CalicoBackEnd string

const (
	CalicoBackendBGP   CalicoBackEnd = "bgp"
	CalicoBackendVXLAN               = "vxlan"
)

type WinFV struct {
	rootDir    string
	flowLogDir string
	configFile string

	dnsCacheFile string

	// The original content of config.ps1.
	originalConfig string

	backend CalicoBackEnd
}

func NewWinFV(rootDir, flowLogDir, dnsCacheFile string) (*WinFV, error) {
	configFile := filepath.Join(rootDir, "config.ps1")
	b, err := ioutil.ReadFile(configFile) // just pass the file name
	if err != nil {
		return nil, err
	}

	var backend CalicoBackEnd
	networkType := testutils.Powershell(`Get-HnsNetwork | Where name -EQ Calico | Select Type`)
	log.Infof("Windows network type %s", networkType)
	if strings.Contains(strings.ToLower(networkType), "l2bridge") {
		backend = CalicoBackendBGP
	} else if strings.Contains(strings.ToLower(networkType), "overlay") {
		backend = CalicoBackendVXLAN
	} else {
		return nil, fmt.Errorf("Wrong Windows network type")
	}

	return &WinFV{
		rootDir:        rootDir,
		flowLogDir:     flowLogDir,
		dnsCacheFile:   dnsCacheFile,
		configFile:     configFile,
		originalConfig: string(b),
		backend:        backend,
	}, nil
}

func (f *WinFV) GetBackendType() CalicoBackEnd {
	return f.backend
}

func (f *WinFV) Restart() {
	log.Infof("Restarting Felix...")
	testutils.Powershell(filepath.Join(f.rootDir, "restart-felix.ps1"))
	log.Infof("Felix Restarted.")
}

func (f *WinFV) RestartFelix() {
	log.Infof("Restarting Felix...")
	testutils.Powershell(filepath.Join(f.rootDir, "restart-felix.ps1"))
	log.Infof("Felix Restarted.")
}

func (f *WinFV) RestoreConfig() error {
	err := ioutil.WriteFile(f.configFile, []byte(f.originalConfig), 0644)
	if err != nil {
		return err
	}
	return nil
}

// Add config items to config.ps1.
func (f *WinFV) AddConfigItems(configs map[string]interface{}) error {
	var entry, items string

	items = f.originalConfig
	// Convert config map to string
	for name, value := range configs {
		switch c := value.(type) {
		case int:
			entry = fmt.Sprintf("$env:FELIX_%s = %d", name, c)
		case string:
			entry = fmt.Sprintf("$env:FELIX_%s = %q", name, c)
		default:
			return fmt.Errorf("wrong config value type")
		}

		items = fmt.Sprintf("%s\n%s\n", items, entry)
	}

	err := ioutil.WriteFile(f.configFile, []byte(items), 0644)
	if err != nil {
		return err
	}
	return nil
}

func (f *WinFV) ReadFlowLogs(output string) ([]collector.FlowLog, error) {
	switch output {
	case "file":
		return f.ReadFlowLogsFile()
	default:
		panic("unrecognized flow log output")
	}
}

func (f *WinFV) ReadFlowLogsFile() ([]collector.FlowLog, error) {
	var flowLogs []collector.FlowLog
	flowLogDir := f.flowLogDir
	log.WithField("dir", flowLogDir).Info("Reading Flow Logs from file")
	filePath := filepath.Join(flowLogDir, collector.FlowLogFilename)
	logFile, err := os.Open(filePath)
	if err != nil {
		return flowLogs, err
	}
	defer logFile.Close()

	s := bufio.NewScanner(logFile)
	for s.Scan() {
		var fljo collector.FlowLogJSONOutput
		err = json.Unmarshal(s.Bytes(), &fljo)
		if err != nil {
			all, _ := ioutil.ReadFile(filePath)
			return flowLogs, fmt.Errorf("Error unmarshaling flow log: %v\nLog:\n%s\nFile:\n%s", err, string(s.Bytes()), string(all))
		}
		fl, err := fljo.ToFlowLog()
		if err != nil {
			return flowLogs, fmt.Errorf("Error converting to flow log: %v\nLog: %s", err, string(s.Bytes()))
		}
		flowLogs = append(flowLogs, fl)
	}
	return flowLogs, nil
}

type JsonMappingV1 struct {
	LHS    string
	RHS    string
	Expiry string
	Type   string
}

func (f *WinFV) ReadDnsCacheFile() ([]JsonMappingV1, error) {
	result := []JsonMappingV1{}

	log.WithField("file", f.dnsCacheFile).Info("Reading DNS Cache from file")
	logFile, err := os.Open(f.dnsCacheFile)
	if err != nil {
		return result, err
	}
	defer logFile.Close()

	s := bufio.NewScanner(logFile)
	for s.Scan() {
		var m JsonMappingV1

		// filter out anything other than a valid entry
		if !strings.Contains(s.Text(), "LHS") {
			continue
		}
		err = json.Unmarshal(s.Bytes(), &m)
		if err != nil {
			all, _ := ioutil.ReadFile(f.dnsCacheFile)
			return result, fmt.Errorf("Error unmarshaling dns log: %v\nLog:\n%s\nFile:\n%s", err, string(s.Bytes()), string(all))
		}
		result = append(result, m)
	}
	return result, nil
}
