// Copyright (c) 2020 Tigera, Inc. All rights reserved.

package commands

import (
	"fmt"
	"strings"

	"github.com/docopt/docopt-go"

	"github.com/projectcalico/calicoctl/v3/calicoctl/commands/cluster"
	"github.com/projectcalico/calicoctl/v3/calicoctl/commands/constants"
)

// Cluster includes any cluster-level subcommands.
func Cluster(args []string) error {
	doc := constants.DatastoreIntro + `Usage:
  calicoctl cluster <command> [<args>...]

    diags            Collect snapshot of diagnostic info and logs related to Calico at the cluster-level.

Options:
  -h --help      Show this screen.

Description:
  Commands for accessing Cluster related information.

  See 'calicoctl cluster <command> --help' to read about a specific subcommand.`

	arguments, err := docopt.Parse(doc, args, true, "", true, false)
	if err != nil {
		return fmt.Errorf("Invalid option: 'calicoctl %s'. Use flag '--help' to read about a specific subcommand.", strings.Join(args, " "))
	}
	if arguments["<command>"] == nil {
		return nil
	}

	command := arguments["<command>"].(string)
	args = append([]string{"cluster", command}, arguments["<args>"].([]string)...)

	switch command {
	case "diags":
		return cluster.Diags(args)
	default:
		fmt.Println(doc)
	}

	return nil
}
