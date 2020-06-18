// Copyright (c) 2016-2020 Tigera, Inc. All rights reserved.

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

package commands

import (
	"strings"

	"github.com/docopt/docopt-go"

	"fmt"

	"github.com/projectcalico/calicoctl/calicoctl/commands/common"
	"github.com/projectcalico/calicoctl/calicoctl/commands/constants"

	log "github.com/sirupsen/logrus"
)

func Replace(args []string) error {
	doc := constants.DatastoreIntro + `Usage:
  calicoctl replace --filename=<FILENAME> [--config=<CONFIG>] [--namespace=<NS>]

Examples:
  # Replace a policy using the data in policy.yaml.
  calicoctl replace -f ./policy.yaml

  # Replace a policy based on the JSON passed into stdin.
  cat policy.json | calicoctl replace -f -

Options:
  -h --help                  Show this screen.
  -f --filename=<FILENAME>   Filename to use to replace the resource.  If set
                             to "-" loads from stdin.
  -c --config=<CONFIG>       Path to the file containing connection
                             configuration in YAML or JSON format.
                             [default: ` + constants.DefaultConfigPath + `]
  -n --namespace=<NS>        Namespace of the resource.
                             Only applicable to NetworkPolicy, StagedNetworkPolicy, StagedKubernetesNetworkPolicy,
                             NetworkSet, and WorkloadEndpoint.
                             Uses the default namespace if not specified.

Description:
  The replace command is used to replace a set of resources by filename or
  stdin.  JSON and YAML formats are accepted.

  Valid resource types are:

    * bgpConfiguration
    * bgpPeer
    * felixConfiguration
    * globalNetworkPolicy
    * stagedGlobalNetworkPolicy
    * globalNetworkSet
    * globalThreatFeed
    * hostEndpoint
    * ipPool
    * kubeControllersConfiguration
    * tier
    * networkPolicy
    * stagedNetworkPolicy
    * stagedKubernetesNetworkPolicy
    * networkSet
    * node
    * profile
    * workloadEndpoint
    * remoteClusterConfiguration
    * licenseKey

  Attempting to replace a resource that does not exist is treated as a
  terminating error.

  The output of the command indicates how many resources were successfully
  replaced, and the error reason if an error occurred.

  The resources are replaced in the order they are specified.  In the event of
  a failure replacing a specific resource it is possible to work out which
  resource failed based on the number of resources successfully replaced.

  When replacing a resource, the complete resource spec must be provided, it is
  not sufficient to supply only the fields that are being updated.
`
	parsedArgs, err := docopt.Parse(doc, args, true, "", false, false)
	if err != nil {
		return fmt.Errorf("Invalid option: 'calicoctl %s'. Use flag '--help' to read about a specific subcommand.", strings.Join(args, " "))
	}
	if len(parsedArgs) == 0 {
		return nil
	}

	results := common.ExecuteConfigCommand(parsedArgs, common.ActionUpdate)
	log.Infof("results: %+v", results)

	if results.FileInvalid {
		return fmt.Errorf("Failed to execute command: %v", results.Err)
	} else if results.NumHandled == 0 {
		if results.NumResources == 0 {
			return fmt.Errorf("No resources specified in file")
		} else if results.NumResources == 1 {
			return fmt.Errorf("Failed to replace '%s' resource: %v", results.SingleKind, results.Err)
		} else if results.SingleKind != "" {
			return fmt.Errorf("Failed to replace any '%s' resources: %v", results.SingleKind, results.Err)
		} else {
			return fmt.Errorf("Failed to replace any resources: %v", results.Err)
		}
	} else if results.Err == nil {
		if results.SingleKind != "" {
			fmt.Printf("Successfully replaced %d '%s' resource(s)\n", results.NumHandled, results.SingleKind)
		} else {
			fmt.Printf("Successfully replaced %d resource(s)\n", results.NumHandled)
		}
	} else {
		fmt.Printf("Partial success: ")
		if results.SingleKind != "" {
			fmt.Printf("replaced the first %d out of %d '%s' resources:\n",
				results.NumHandled, results.NumResources, results.SingleKind)
		} else {
			fmt.Printf("replaced the first %d out of %d resources:\n",
				results.NumHandled, results.NumResources)
		}
		return fmt.Errorf("Hit error: %v", results.Err)
	}

	return nil
}
