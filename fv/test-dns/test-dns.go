// Copyright (c) 2020-2021 Tigera, Inc. All rights reserved.
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

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/docopt/docopt-go"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/felix/fv/cgroup"
)

const usage = `test-dns: test connection to a host name, for Felix FV testing.

Usage:
  test-dns <namespace-path> <host-name>

If connection is successful, test-dns exits successfully.

If connection is unsuccessful, test-dns panics and so exits with a failure status.`

func main() {
	log.SetLevel(log.InfoLevel)

	// If we've been told to, move into this felix's cgroup.
	cgroup.MaybeMoveToFelixCgroupv2()

	arguments, err := docopt.ParseArgs(usage, nil, "v0.1")
	if err != nil {
		println(usage)
		log.WithError(err).Fatal("Failed to parse usage")
	}
	log.WithField("args", arguments).Info("Parsed arguments")
	namespacePath := arguments["<namespace-path>"].(string)
	hostName := arguments["<host-name>"].(string)

	if namespacePath == "-" {
		err = tryConnect(hostName)
	} else {
		// Get the specified network namespace (representing a workload).
		var namespace ns.NetNS
		namespace, err = ns.GetNS(namespacePath)
		if err != nil {
			log.WithError(err).Fatal("Failed to get netns")
		}
		log.WithField("namespace", namespace).Debug("Got namespace")

		// Now, in that namespace, try connecting to the target.
		err = namespace.Do(func(_ ns.NetNS) error {
			return tryConnect(hostName)
		})
	}

	if err != nil {
		log.WithError(err).Fatal("Failed to connect")
	}
}

func tryConnect(hostName string) error {
	for try := 0; try < 4; try++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		addrs, err := net.DefaultResolver.LookupHost(ctx, hostName)
		cancel()
		log.WithField("addrs", addrs).WithError(err).Info("DNS lookup")
		if err == nil {
			for _, addr := range addrs {
				if !strings.Contains(addr, ":") {
					_, err := net.DialTimeout("tcp", addr+":80", 4*time.Second)
					log.WithError(err).Info("Connection attempt")
					return err
				}
			}
			return fmt.Errorf("No IPv4 addresses in %v", addrs)
		}
	}
	return errors.New("Failed 4 DNS lookups")
}
