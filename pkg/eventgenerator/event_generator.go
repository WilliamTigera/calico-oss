// Copyright (c) 2021 Tigera, Inc. All rights reserved.

package eventgenerator

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tigera/lma/pkg/api"

	cache3 "github.com/tigera/deep-packet-inspection/pkg/cache"
	"github.com/tigera/deep-packet-inspection/pkg/config"
	"github.com/tigera/deep-packet-inspection/pkg/dpiupdater"
	"github.com/tigera/deep-packet-inspection/pkg/elastic"
	"github.com/tigera/deep-packet-inspection/pkg/fileutils"

	"github.com/hpcloud/tail"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/libcalico-go/lib/backend/model"
)

const (
	fileName          = "alert_fast.txt"
	tailRetryInterval = 30 * time.Second
	timeLayout        = "06/01/02-15:04:05"
	description       = "Encountered suspicious traffic matching snort rule for malicious activity"
	eventType         = "deep_packet_inspection"
)

type EventGenerator interface {
	// GenerateEventsForWEP reads, processes and sends the snort alerts in the files for the WEP
	GenerateEventsForWEP(wepKey model.WorkloadEndpointKey)

	// StopGeneratingEventsForWEP waits for the current tail process to reach EOF on alert file and
	// then stops tailing the file and deletes it.
	StopGeneratingEventsForWEP(wepKey model.WorkloadEndpointKey)

	// Close waits for all the running tail processes to reach EOF on alert files and then deletes the files.
	Close()
}

type eventGenerator struct {
	cfg            *config.Config
	esForwarder    elastic.ESForwarder
	wepCache       cache3.WEPCache
	dpiUpdater     dpiupdater.DPIStatusUpdater
	dpiKey         model.ResourceKey
	filePathToTail map[string]*tail.Tail
}

func NewEventGenerator(
	cfg *config.Config,
	esForwarder elastic.ESForwarder,
	dpiUpdater dpiupdater.DPIStatusUpdater,
	dpiKey model.ResourceKey,
	wepCache cache3.WEPCache,
) EventGenerator {
	r := &eventGenerator{
		cfg:            cfg,
		esForwarder:    esForwarder,
		filePathToTail: make(map[string]*tail.Tail),
		dpiUpdater:     dpiUpdater,
		dpiKey:         dpiKey,
		wepCache:       wepCache,
	}
	return r
}

// GenerateEventsForWEP reads, processes and sends the snort alerts in the files for the WEP
func (r *eventGenerator) GenerateEventsForWEP(wepKey model.WorkloadEndpointKey) {
	log.WithFields(log.Fields{"DPI": r.dpiKey, "WEP": wepKey}).Debugf("Starting to generate events on alert files.")
	fileRelativePath := fileutils.AlertFileRelativePath(r.dpiKey, wepKey)
	r.filePathToTail[fileRelativePath] = nil
	go r.readRotatedFiles(wepKey)
	go r.tailFile(wepKey)
}

// StopGeneratingEventsForWEP waits for the current tail process to reach EOF on alert file and
// then stops tailing the file and deletes it.
func (r *eventGenerator) StopGeneratingEventsForWEP(wepKey model.WorkloadEndpointKey) {
	log.WithFields(log.Fields{"DPI": r.dpiKey, "WEP": wepKey}).Debugf("Stop generating events on alert files.")
	fileRelativePath := fileutils.AlertFileRelativePath(r.dpiKey, wepKey)
	fileAbsolutePath := fileutils.AlertFileAbsolutePath(r.dpiKey, wepKey, r.cfg.SnortAlertFileBasePath)
	r.deleteFile(fileRelativePath, fileAbsolutePath)
}

// Close waits for all the running tail processes to reach EOF on alert files and then deletes the files.
func (r *eventGenerator) Close() {
	log.WithFields(log.Fields{"DPI": r.dpiKey}).Debugf("Stop generating events on alert files.")
	for fileRelativePath := range r.filePathToTail {
		r.deleteFile(fileRelativePath, fmt.Sprintf("%s/%s", r.cfg.SnortAlertFileBasePath, fileRelativePath))
	}
}

// readRotatedFiles reads any previously rotated files, generates events using them
func (r *eventGenerator) readRotatedFiles(wepKey model.WorkloadEndpointKey) {
	log.WithFields(log.Fields{"DPI": r.dpiKey, "WEP": wepKey}).Info("Reading and processing all rotated alert files.")
	absolutePath := fileutils.AlertFileAbsolutePath(r.dpiKey, wepKey, r.cfg.SnortAlertFileBasePath)

	files, err := filepath.Glob(fmt.Sprintf("%s/%s.*", absolutePath, fileName))
	if err != nil {
		log.WithError(err).Info("No previous alert files to process")
		return
	}

	for _, fPath := range files {
		if fPath == fileName {
			// Ignore file that will be tailed.
			continue
		}

		f, err := os.Open(fPath)
		if err != nil {
			log.WithError(err).Errorf("Failed to open alert files from %s", fPath)
			continue
		}
		reader := bufio.NewReader(f)
		for {
			line, _, err := reader.ReadLine()
			if err == io.EOF {
				f.Close()
				if err := os.Remove(f.Name()); err != nil {
					log.WithError(err).Errorf("Failed to delete older alert files from %s", fPath)
					r.dpiUpdater.UpdateStatusWithError(context.Background(), r.dpiKey, true,
						fmt.Sprintf("failed to delete older alert files for %s with error '%s'", r.dpiKey, err.Error()))
				}
				break
			}
			lineStr := string(line[:])
			if lineStr != "" {
				r.esForwarder.Forward(r.convertAlertToSecurityEvent(lineStr))
			}
		}
	}
}

// tailFile tails the file to which snort process is actively writing to, generate events when snort writes a new line
// into the alert file and sends it to ESForwarder.
func (r *eventGenerator) tailFile(wepKey model.WorkloadEndpointKey) {
	fileRelativePath := fileutils.AlertFileRelativePath(r.dpiKey, wepKey)
	fileAbsolutePath := fileutils.AlertFileAbsolutePath(r.dpiKey, wepKey, r.cfg.SnortAlertFileBasePath)
	filePath := fmt.Sprintf("%s/%s", fileAbsolutePath, fileName)
	// loop and restart tailing unless parent context is closed
	// or if file path no longer exists in filePathToTail (meaning either WEP or DPI resource is not available).
	for {
		if _, ok := r.filePathToTail[fileRelativePath]; !ok {
			return
		}

		t, err := tail.TailFile(filePath, tail.Config{Follow: true, ReOpen: true})
		if err != nil {
			log.WithError(err).Error("Failed to tail file, will retry after interval.")
			r.dpiUpdater.UpdateStatusWithError(context.Background(), r.dpiKey, true,
				fmt.Sprintf("failed to tail file for %s and %s with error '%s'", r.dpiKey, wepKey, err.Error()))
			<-time.After(tailRetryInterval)
			continue
		}

		log.Infof("Started tailing files for %s and %s.", r.dpiKey, wepKey)
		r.filePathToTail[fileRelativePath] = t
		for line := range t.Lines {
			r.esForwarder.Forward(r.convertAlertToSecurityEvent(line.Text))
		}

		err = t.Wait()
		if err != nil {
			// If tailing was stopped due to EOF, it must be due to explicitly call made to stop tailing
			// (meaning either WEP or DPI resource is not available).
			if strings.Contains(err.Error(), "tail: stop at eof") {
				return
			}
			log.WithError(err).Errorf("Failed to tail file, retrying")
			r.dpiUpdater.UpdateStatusWithError(context.Background(), r.dpiKey, true,
				fmt.Sprintf("failed to tail file for %s and %s with error '%s'", r.dpiKey, wepKey, err.Error()))
			<-time.After(tailRetryInterval)
			continue
		}
		return

	}
}

func (r *eventGenerator) deleteFile(fileRelativePath, fileAbsolutePath string) {
	if t, ok := r.filePathToTail[fileRelativePath]; ok && t != nil {
		err := t.StopAtEOF()
		if err != nil && !strings.Contains(err.Error(), "tail: stop at eof") {
			log.WithError(err).Errorf("Failed to stop tailing the alert file in %s", fileRelativePath)
		}
	}
	delete(r.filePathToTail, fileRelativePath)
	err := os.Remove(fmt.Sprintf("%s/%s", fileAbsolutePath, fileName))
	if err != nil {
		log.WithError(err).Errorf("Failed to delete file in %s", fileRelativePath)
	}
}

// convertAlertToSecurityEvent converts the alert created by snort into document that should be indexed into ElasticSearch.
//
// Sample Alert format:
// <time> <packet action> [**] [<generator_id)>:<signature_id>:<signature_revision>] "specs" "<msg_defined_in_signature>" [**] [Priority: <signature_priority>] <appID> {Protocol} <src_ip:port> -> <dst_ip:port>
// Sample Alert:
// 21/08/30-17:19:37.337831 [**] [1:1000005:1] "msg:1_alert_fast" [**] [Priority: 0] {ICMP} 74.125.124.100 -> 10.28.0.13
// Details about alert format is available in https://github.com/snort3/snort3/blob/35b6804f4506993029221450769a76e6281ae4ec/src/loggers/alert_fast.cc
func (r *eventGenerator) convertAlertToSecurityEvent(alertText string) elastic.SecurityEvent {
	event := elastic.SecurityEvent{
		EventsData: api.EventsData{
			EventsSearchFields: api.EventsSearchFields{
				Host:        r.cfg.NodeName,
				Type:        eventType,
				Origin:      fmt.Sprintf("dpi.%s/%s", r.dpiKey.Namespace, r.dpiKey.Name),
				Severity:    100,
				Description: description,
			},
		},
	}

	s := strings.Split(alertText, " ")
	index := 0

	tm, err := time.Parse(timeLayout, s[index])
	if err != nil {
		log.WithError(err).Errorf("Failed to parse time from alert")
	} else {
		index++
		// Time format in ElasticSearch events index is epoch_second
		event.Time = tm.Unix()
	}

	//skip through all optional fields till we get to signature information
	for i, k := range s {
		if k == "[**]" {
			index = i + 1
			break
		}
	}
	// Extract snort signature information
	sigInfo := strings.Split(s[index], ":")
	if len(sigInfo) == 3 {
		event.Record = elastic.Record{
			SnortSignatureID:       sigInfo[1],
			SnortSignatureRevision: strings.TrimSuffix(sigInfo[2], "]"),
			SnortAlert:             alertText,
		}
	} else {
		log.Errorf("Missing snort signature information in alert")
		event.Record = elastic.Record{
			SnortAlert: alertText,
		}
	}

	var srcIP, srcPort, destIP, destPort string
	if len(s) >= 3 && s[len(s)-2] == "->" {
		src := s[len(s)-3]
		if srcIP, srcPort, err = net.SplitHostPort(src); err != nil {
			if strings.Contains(err.Error(), "missing port in address") {
				srcIP = src
			} else {
				log.WithError(err).Errorf("Failed to parse source IP %s from snort alert", src)
			}
		} else {
			if intPort, err := strconv.ParseInt(srcPort, 10, 32); err != nil {
				log.WithError(err).Errorf("Failed to parse source Port %s from snort alert", src)
			} else {
				event.SourcePort = &intPort
			}
		}
		event.SourceIP = &srcIP

		dst := s[len(s)-1]
		if destIP, destPort, err = net.SplitHostPort(dst); err != nil {
			if strings.Contains(err.Error(), "missing port in address") {
				destIP = dst
			} else {
				log.WithError(err).Errorf("Failed to parse destination IP %s from snort alert", dst)
			}
		} else {
			if intPort, err := strconv.ParseInt(destPort, 10, 32); err != nil {
				log.WithError(err).Errorf("Failed to parse destination Port %s from snort alert", src)
			} else {
				event.DestPort = &intPort
			}
		}
		event.DestIP = &destIP

	} else {
		log.WithError(err).Errorf("Failed to parse source and destination IP from snort alert: %s", alertText)
	}

	_, event.SourceName, event.SourceNamespace = r.wepCache.Get(*event.SourceIP)
	_, event.DestName, event.DestNamespace = r.wepCache.Get(*event.DestIP)

	// Construct a unique document ID for the ElasticSearch document built.
	// Use _ as a separator as it's allowed in URLs, but not in any of the components of this ID
	event.DocID = fmt.Sprintf("%s_%s_%d_%s_%s_%s_%s_%s", r.dpiKey.Namespace, r.dpiKey.Name, tm.UnixNano(),
		srcIP, srcPort, destIP, destPort, event.Host)

	return event
}
