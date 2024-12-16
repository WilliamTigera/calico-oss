// Copyright (c) 2020 Tigera, Inc. All rights reserved.
package cluster

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/docopt/docopt-go"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/projectcalico/calico/calicoctl/calicoctl/commands/argutils"
	"github.com/projectcalico/calico/calicoctl/calicoctl/commands/clientmgr"
	"github.com/projectcalico/calico/calicoctl/calicoctl/commands/common"
	"github.com/projectcalico/calico/calicoctl/calicoctl/commands/constants"
	"github.com/projectcalico/calico/libcalico-go/lib/set"
)

type diagOpts struct {
	// Even though we already know, in this file, that we are doing the "calicoctl cluster
	// diags" command, these two fields must be present or else Bind returns an error and fails
	// to fill in the fields that we really do need.
	Cluster bool // Only needed for Bind to work.
	Diags   bool // Only needed for Bind to work.

	// Fields that we really want Bind to fill in.
	Help                 bool
	Config               string
	Since                string
	MaxLogs              int
	MaxParallelism       int
	FocusNodes           string
	AllowVersionMismatch bool
	SkipTempDirCleanup   bool
}

var usage = `Usage:
  calicoctl cluster diags [options]

Options:
  -h --help                    Show this screen.
     --since=<SINCE>           Only collect logs newer than provided relative
                               duration, in seconds (s), minutes (m) or hours (h).
     --max-logs=<MAXLOGS>      Only collect up to this number of logs, for each
                               kind of Calico component. [default: 5]
     --max-parallelism=<MAXPARALLELISM> Maximum number of parallel threads to use for
                               collecting logs. [default: 10]
     --focus-nodes=<NODES>     Comma-separated list of nodes from which we should
                               try first to collect logs.
  -c --config=<CONFIG>         Path to connection configuration file.
                               [default: ` + constants.DefaultConfigPath + `]
     --allow-version-mismatch  Allow client and cluster versions mismatch.
     --skip-temp-dir-cleanup   Don't clean up the temporary directory (useful 
                               for development).
`

var doc = constants.DatastoreIntro + usage + `
Description:
  The cluster diags command collects a snapshot of diagnostic info and logs related
  to Calico for the given cluster.  It generates a .tar.gz file containing all the
  diags.

  By default, in order to keep the .tar.gz file to a reasonable size, this command
  only collects up to 5 sets of logs for each kind of Calico pod (for example,
  for calico-node, or Typha, or the intrusion detection controller).  To collect
  more (or fewer) sets of logs, use the --max-logs option.

  To tell calicoctl to try to collect logs first from particular nodes of interest,
  set the --focus-nodes option to the relevant node names, comma-separated.  For a
  Calico component with pods on multiple nodes, calicoctl will first collect logs
  from the pods (if any) on the focus nodes, then from other nodes in the cluster.

  To collect logs only for the last few hours, minutes, or seconds, set the --since
  option to indicate the desired period.
`

// Diags executes a series of kubectl exec commands to retrieve logs and resource information
// for the configured cluster.
func Diags(args []string) error {
	return diagsTestable(args, fmt.Print, collectDiags)
}

func diagsTestable(args []string, print func(a ...any) (int, error), continuation func(*diagOpts) error) error {
	// Make our own Parser so we can print out options when bad options are given.
	parser := &docopt.Parser{HelpHandler: docopt.NoHelpHandler, SkipHelpFlags: true}
	parsedArgs, err := parser.ParseArgs(doc, args, "")
	if err != nil {
		return fmt.Errorf("Invalid option: 'calicoctl %s'.\n\n%v", strings.Join(args, " "), usage)
	}

	var opts diagOpts
	err = parsedArgs.Bind(&opts)
	if err != nil {
		return fmt.Errorf("error understanding options: %w", err)
	}

	if opts.Help {
		_, _ = print(doc)
		return nil
	}

	// Default --since to "0s", which kubectl understands as meaning all logs.
	if opts.Since == "" {
		opts.Since = "0s"
	}

	return continuation(&opts)
}

func collectDiags(opts *diagOpts) error {
	common.MaxParallelism = opts.MaxParallelism

	// Ensure since value is valid with proper time unit
	argutils.ValidateSinceDuration(opts.Since)

	// Ensure max-logs value is non-negative
	argutils.ValidateMaxLogs(opts.MaxLogs)

	if err := common.CheckVersionMismatch(opts.Config, opts.AllowVersionMismatch); err != nil {
		return err
	}

	// Ensure kubectl command is available (since we need it to access BGP information)
	if err := common.KubectlExists(); err != nil {
		return fmt.Errorf("missing dependency: %s", err)
	}

	fmt.Println("==== Begin collecting diagnostics. ====")

	// Create a temp folder to house all diagnostic files. Use empty string for dir parameter.
	// TempDir will use the default directory for temporary files (see os.TempDir).
	tempDir, err := os.MkdirTemp("", "")
	if err != nil {
		return err
	}
	fmt.Println("Created temporary directory:", tempDir)
	if !opts.SkipTempDirCleanup {
		// Clean up the temporary directory.
		defer func() {
			_ = os.RemoveAll(tempDir)
		}()
	}

	// Within temp dir create a folder that will be used to zip everything up in the end
	directoryName := "calico-diagnostics-" + time.Now().UTC().Format("20060102_150405")
	archiveName := directoryName + ".tar.gz"
	dir := fmt.Sprintf("%s/%s", tempDir, directoryName)

	collectGlobalClusterInformation(dir + "/cluster")
	collectSelectedNodeLogs(dir+"/nodes", dir+"/links", opts)
	createArchive(tempDir, directoryName, archiveName)

	return nil
}

func collectSelectedNodeLogs(dir, linkDir string, opts *diagOpts) {
	// Create Kubernetes client from config or env vars.
	kubeClient, _, _, err := clientmgr.GetClients(opts.Config)
	if err != nil {
		fmt.Printf("ERROR creating clients: %v\n", err)
		return
	}
	if kubeClient == nil {
		fmt.Println("ERROR: can't create Kubernetes client on etcd datastore")
		return
	}

	// If --focus-nodes is specified, put those node names at the start of the node list.
	nodeList := strings.Split(opts.FocusNodes, ",")

	// Keep track of nodes already in the list.
	nodesAlreadyListed := set.New[string]()
	for _, nodeName := range nodeList {
		nodesAlreadyListed.Add(nodeName)
	}

	// Add all other nodes into the list.
	nl, err := kubeClient.CoreV1().Nodes().List(context.TODO(), v1.ListOptions{})
	if err != nil {
		fmt.Printf("ERROR listing all nodes in cluster: %v\n", err)
		// Continue because we can still use the --focus-nodes, if specified.
	} else {
		for _, node := range nl.Items {
			if !nodesAlreadyListed.Contains(node.Name) {
				nodeList = append(nodeList, node.Name)
			}
		}
	}

	// Iterate through all Calico/Tigera namespaces.
	nsl, err := kubeClient.CoreV1().Namespaces().List(context.TODO(), v1.ListOptions{})
	if err != nil {
		fmt.Printf("ERROR listing namespaces: %v\n", err)
		// Fatal, can't identify our namespaces.
		return
	}
	for _, ns := range nsl.Items {
		if !(strings.Contains(ns.Name, "calico") || strings.Contains(ns.Name, "tigera")) {
			continue
		}

		fmt.Printf("Collecting detailed diags for namespace %v...\n", ns.Name)

		// Iterate through DaemonSets in this namespace.
		dsl, err := kubeClient.AppsV1().DaemonSets(ns.Name).List(context.TODO(), v1.ListOptions{})
		if err != nil {
			fmt.Printf("ERROR listing DaemonSets in namespace %v: %v\n", ns.Name, err)
			// Continue because deployments or other namespaces might work.
		} else {
			for _, ds := range dsl.Items {
				collectDiagsForSelectedPods(dir, linkDir, opts, kubeClient, nodeList, ns.Name, ds.Spec.Selector)
			}
		}

		// Iterate through Deployments in this namespace.
		dl, err := kubeClient.AppsV1().Deployments(ns.Name).List(context.TODO(), v1.ListOptions{})
		if err != nil {
			fmt.Printf("ERROR listing Deployments in namespace %v: %v\n", ns.Name, err)
			// Continue because other namespaces might work.
		} else {
			for _, d := range dl.Items {
				collectDiagsForSelectedPods(dir, linkDir, opts, kubeClient, nodeList, ns.Name, d.Spec.Selector)
			}
		}

		// Iterate through StatefulSets in this namespace.
		sl, err := kubeClient.AppsV1().StatefulSets(ns.Name).List(context.TODO(), v1.ListOptions{})
		if err != nil {
			fmt.Printf("ERROR listing StatefulSets in namespace %v: %v\n", ns.Name, err)
			// Continue because other namespaces might work.
		} else {
			for _, s := range sl.Items {
				collectDiagsForSelectedPods(dir, linkDir, opts, kubeClient, nodeList, ns.Name, s.Spec.Selector)
			}
		}
	}
}

func collectDiagsForSelectedPods(dir, linkDir string, opts *diagOpts, kubeClient *kubernetes.Clientset, nodeList []string, ns string, selector *v1.LabelSelector) {

	labelMap, err := v1.LabelSelectorAsMap(selector)
	if err != nil {
		fmt.Printf("ERROR forming pod selector: %v\n", err)
		return
	}
	selectorString := labels.SelectorFromSet(labelMap).String()

	// List pods matching the namespace and selector.
	pl, err := kubeClient.CoreV1().Pods(ns).List(context.TODO(), v1.ListOptions{LabelSelector: selectorString})
	if err != nil {
		fmt.Printf("ERROR listing pods in namespace %v matching '%v': %v\n", ns, selectorString, err)
		return
	}

	// Map the pod names against their node names.
	podNamesByNode := map[string][]string{}
	for _, p := range pl.Items {
		podNamesByNode[p.Spec.NodeName] = append(podNamesByNode[p.Spec.NodeName], p.Name)
	}

	nextNodeIndex := 0
	var cmds []common.Cmd
	for logsWanted := opts.MaxLogs; logsWanted > 0; {
		// Get the next node name to look at.
		if nextNodeIndex >= len(nodeList) {
			// There are no more nodes we can look at.
			break
		}
		nodeName := nodeList[nextNodeIndex]
		nextNodeIndex++

		for _, podName := range podNamesByNode[nodeName] {
			fmt.Printf("Collecting detailed diags for pod %v in namespace %v on node %v...\n", podName, ns, nodeName)
			if strings.HasPrefix(podName, "calico-node-") {
				nodeDir := dir + "/" + nodeName
				collectCalicoNodeDiags(nodeDir, opts, kubeClient, nodeName, ns, podName)
			}
			cmds = append(cmds, diagsCmdsForPod(dir, linkDir, opts, kubeClient, nodeName, ns, podName)...)
			logsWanted--
			if logsWanted <= 0 {
				break
			}
		}
	}
	common.ExecAllCmdsWriteToFile(cmds)
}

func collectCalicoResource(dir string) {
	commands := []common.Cmd{}
	for _, resource := range []string{
		"bgpconfigurations",
		"bgppeers",
		"blockaffinities",
		"clusterinformations",
		"felixconfigurations",
		"globalnetworkpolicies",
		"globalnetworksets",
		"hostendpoints",
		"ipamblocks",
		"ipamhandles",
		"ippools",
		"licensekeys",
		"networkpolicies",
		"networksets",
		"tiers",
		"egressgatewaypolicies",
	} {
		commands = append(commands, common.Cmd{
			Info:     fmt.Sprintf("Collect Calico %v (yaml)", resource),
			CmdStr:   fmt.Sprintf("kubectl get %v -o yaml", resource),
			FilePath: fmt.Sprintf("%s/%v.yaml", dir, resource),
		}, common.Cmd{
			Info:     fmt.Sprintf("Collect Calico %v (wide text)", resource),
			CmdStr:   fmt.Sprintf("kubectl get %v -o wide", resource),
			FilePath: fmt.Sprintf("%s/%v.txt", dir, resource),
		})
	}

	commands = append(commands, common.Cmd{
		Info:     fmt.Sprintf("Collect all in %s (yaml)", common.CalicoNamespace),
		CmdStr:   fmt.Sprintf("kubectl get all -n %s -o yaml", common.CalicoNamespace),
		FilePath: fmt.Sprintf("%s/calico-system.yaml", dir),
	}, common.Cmd{
		Info:     fmt.Sprintf("Collect all in %s (wide text)", common.CalicoNamespace),
		CmdStr:   fmt.Sprintf("kubectl get all -n %s -o wide", common.CalicoNamespace),
		FilePath: fmt.Sprintf("%s/calico-system.txt", dir),
	})
	common.ExecAllCmdsWriteToFile(commands)
}

func collectTigeraOperator(dir string) {
	commands := []common.Cmd{}
	for _, resource := range []string{
		"apiservers",
		"applicationlayers",
		"authentications.operator.tigera.io",
		"compliances",
		"egressgateways",
		"installations",
		"intrusiondetections",
		"logcollectors",
		"logstorages",
		"managementclusterconnections",
		"managers",
		"monitors",
		"packetcaptureapis",
		"policyrecommendations",
		"tigerastatuses",
	} {
		commands = append(commands, common.Cmd{
			Info:     fmt.Sprintf("Collect %v (yaml)", resource),
			CmdStr:   fmt.Sprintf("kubectl get %v -o yaml", resource),
			FilePath: fmt.Sprintf("%s/%v.yaml", dir, resource),
		}, common.Cmd{
			Info:     fmt.Sprintf("Collect %v (wide text)", resource),
			CmdStr:   fmt.Sprintf("kubectl get %v -o wide", resource),
			FilePath: fmt.Sprintf("%s/%v.txt", dir, resource),
		})
	}

	commands = append(commands, common.Cmd{
		Info:     fmt.Sprintf("Collect all in %s (yaml)", common.TigeraOperatorNamespace),
		CmdStr:   fmt.Sprintf("kubectl get all -n %s -o yaml", common.TigeraOperatorNamespace),
		FilePath: fmt.Sprintf("%s/tigera-operator.yaml", dir),
	}, common.Cmd{
		Info:     fmt.Sprintf("Collect all in %s (wide text)", common.TigeraOperatorNamespace),
		CmdStr:   fmt.Sprintf("kubectl get all -n %s -o wide", common.TigeraOperatorNamespace),
		FilePath: fmt.Sprintf("%s/tigera-operator.txt", dir),
	})
	common.ExecAllCmdsWriteToFile(commands)
}

func collectKubernetesResource(dir string) {
	fmt.Println("Collecting core kubernetes resources...")
	commands := []common.Cmd{}
	for _, resource := range []string{
		"configmaps",
		"daemonsets",
		"deployments",
		"endpoints",
		"endpointslices",
		"pods",
		"pv",
		"pvc",
		"sc",
		"services",
		"statefulsets",
	} {
		commands = append(commands, common.Cmd{
			Info:     fmt.Sprintf("Collect %v (yaml)", resource),
			CmdStr:   fmt.Sprintf("kubectl get %v --all-namespaces -o yaml", resource),
			FilePath: fmt.Sprintf("%s/%v.yaml", dir, resource),
		}, common.Cmd{
			Info:     fmt.Sprintf("Collect %v (wide text)", resource),
			CmdStr:   fmt.Sprintf("kubectl get %v --all-namespaces -o wide", resource),
			FilePath: fmt.Sprintf("%s/%v.txt", dir, resource),
		})
	}
	commands = append(commands, common.Cmd{
		Info:     "Collect nodes (yaml)",
		CmdStr:   "kubectl get nodes -o yaml",
		FilePath: fmt.Sprintf("%s/nodes.yaml", dir),
	}, common.Cmd{
		Info:     "Collect nodes (wide text)",
		CmdStr:   "kubectl get nodes -o wide",
		FilePath: fmt.Sprintf("%s/nodes.txt", dir),
	}, common.Cmd{
		Info:     "Collect namespaces (yaml)",
		CmdStr:   "kubectl get namespaces -o wide",
		FilePath: fmt.Sprintf("%s/namespaces.txt", dir),
	}, common.Cmd{
		Info:     "Collect namespaces (wide text)",
		CmdStr:   "kubectl get namespaces -o yaml",
		FilePath: fmt.Sprintf("%s/namespaces.yaml", dir),
	})
	common.ExecAllCmdsWriteToFile(commands)
}

// collectGlobalClusterInformation collects the Kubernetes resource, Calico Resource and Tigera operator details
func collectGlobalClusterInformation(dir string) {
	fmt.Println("Collecting kubernetes version...")
	common.ExecAllCmdsWriteToFile([]common.Cmd{
		{
			Info:     "Collect kubernetes Client and Server version",
			CmdStr:   "kubectl version -o yaml",
			FilePath: fmt.Sprintf("%s/version.yaml", dir),
		},
	})

	collectCalicoResource(dir + "/calico")
	collectTigeraOperator(dir + "/tigera")
	collectKubernetesResource(dir + "/kubernetes")
}

// func diagsCmdsForPod(pod, namespace, dir /*node_name*/, sinceFlag string) {
func diagsCmdsForPod(dir, linkDir string, opts *diagOpts, kubeClient *kubernetes.Clientset, nodeName, namespace, podName string) []common.Cmd {
	nodeDir := dir + "/" + nodeName
	namespaceDir := nodeDir + "/" + namespace
	cmds := []common.Cmd{
		{
			Info:     fmt.Sprintf("Collect logs for pod %s", podName),
			CmdStr:   fmt.Sprintf("kubectl logs --since=%s -n %s %s --all-containers", opts.Since, namespace, podName),
			FilePath: fmt.Sprintf("%s/%s.log", namespaceDir, podName),
			SymLink:  fmt.Sprintf("%s/%s/%s.log", linkDir, namespace, podName),
		},
		{
			Info:     fmt.Sprintf("Collect describe for pod %s", podName),
			CmdStr:   fmt.Sprintf("kubectl -n %s describe pods %s", namespace, podName),
			FilePath: fmt.Sprintf("%s/%s.txt", namespaceDir, podName),
			SymLink:  fmt.Sprintf("%s/%s/%s.txt", linkDir, namespace, podName),
		},
	}
	return cmds
}

func collectCalicoNodeDiags(curNodeDir string, opts *diagOpts, kubeClient *kubernetes.Clientset, nodeName, namespace, podName string) {
	fmt.Printf("Collecting dataplane diags for calico-node: %s\n", podName)
	common.ExecAllCmdsWriteToFile([]common.Cmd{
		// ip diagnostics
		{
			Info:     fmt.Sprintf("Collect iptables (legacy) for node %s", nodeName),
			CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- iptables-legacy-save -c", namespace, podName),
			FilePath: fmt.Sprintf("%s/iptables-legacy-save.txt", curNodeDir),
		},
		{
			Info:     fmt.Sprintf("Collect iptables (nft) for node %s", nodeName),
			CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- iptables-nft-save -c", namespace, podName),
			FilePath: fmt.Sprintf("%s/iptables-nft-save.txt", curNodeDir),
		},
		{
			Info:     fmt.Sprintf("Collect nftables for node %s", nodeName),
			CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- nft -n -a list ruleset", namespace, podName),
			FilePath: fmt.Sprintf("%s/nft-ruleset.txt", curNodeDir),
		},
		{
			Info:     fmt.Sprintf("Collect ip routes for node %s", nodeName),
			CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- ip route show table all", namespace, podName),
			FilePath: fmt.Sprintf("%s/ip-route.txt", curNodeDir),
		},
		{
			Info:     fmt.Sprintf("Collect ipv6 routes for node %s", nodeName),
			CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- ip -6 route show table all", namespace, podName),
			FilePath: fmt.Sprintf("%s/ip-route-v6.txt", curNodeDir),
		},
		{
			Info:     fmt.Sprintf("Collect ip rule for node %s", nodeName),
			CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- ip rule", namespace, podName),
			FilePath: fmt.Sprintf("%s/ip-rule.txt", curNodeDir),
		},
		{
			Info:     fmt.Sprintf("Collect ip addr for node %s", nodeName),
			CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- ip addr", namespace, podName),
			FilePath: fmt.Sprintf("%s/ip-addr.txt", curNodeDir),
		},
		{
			Info:     fmt.Sprintf("Collect ip link for node %s", nodeName),
			CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- ip link", namespace, podName),
			FilePath: fmt.Sprintf("%s/ip-link.txt", curNodeDir),
		},
		{
			Info:     fmt.Sprintf("Collect ip neigh for node %s", nodeName),
			CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- ip neigh", namespace, podName),
			FilePath: fmt.Sprintf("%s/ip-neigh.txt", curNodeDir),
		},
		{
			Info:     fmt.Sprintf("Collect ipset list for node %s", nodeName),
			CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- ipset list", namespace, podName),
			FilePath: fmt.Sprintf("%s/ipset-list.txt", curNodeDir),
		},
		// eBPF diagnostics
		{
			Info:     fmt.Sprintf("Collect eBPF conntrack for node %s", nodeName),
			CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- calico-node -bpf conntrack dump", namespace, podName),
			FilePath: fmt.Sprintf("%s/bpf-conntrack.txt", curNodeDir),
		},
		{
			Info:     fmt.Sprintf("Collect eBPF ipsets for node %s", nodeName),
			CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- calico-node -bpf ipsets dump", namespace, podName),
			FilePath: fmt.Sprintf("%s/bpf-ipsets.txt", curNodeDir),
		},
		{
			Info:     fmt.Sprintf("Collect eBPF nat for node %s", nodeName),
			CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- calico-node -bpf nat dump", namespace, podName),
			FilePath: fmt.Sprintf("%s/bpf-nat.txt", curNodeDir),
		},
		{
			Info:     fmt.Sprintf("Collect eBPF routes for node %s", nodeName),
			CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- calico-node -bpf routes dump", namespace, podName),
			FilePath: fmt.Sprintf("%s/bpf-routes.txt", curNodeDir),
		},
		{
			Info:     fmt.Sprintf("Collect eBPF prog for node %s", nodeName),
			CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- bpftool prog list", namespace, podName),
			FilePath: fmt.Sprintf("%s/bpf-prog.txt", curNodeDir),
		},
		{
			Info:     fmt.Sprintf("Collect eBPF map for node %s", nodeName),
			CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- bpftool map list", namespace, podName),
			FilePath: fmt.Sprintf("%s/bpf-maps.txt", curNodeDir),
		},
		{
			Info:     fmt.Sprintf("Collect tc qdisc for node %s", nodeName),
			CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- tc qdisc show", namespace, podName),
			FilePath: fmt.Sprintf("%s/tc-qdisc.txt", curNodeDir),
		},
	})

	output, err := common.ExecCmd(fmt.Sprintf(
		"kubectl exec -n %s -t %s -c calico-node -- bpftool map list",
		namespace,
		podName,
	))
	if err != nil {
		fmt.Printf("Could not retrieve eBPF maps: %s\n", err)
	} else {
		bpfMaps := strings.Split(strings.TrimSpace(output.String()), "\n")
		log.Debugf("eBPF maps: %s\n", bpfMaps)

		// Output looks like this:
		//
		// 35: lru_hash  name cali_v4_srmsg  flags 0x0
		//	key 16B  value 8B  max_entries 510000  memlock 12242944B
		//	pids calico-node(28576)

		bpfInfoLineRe := regexp.MustCompile(`^(\d+):.*name (cali\w+)`)
		var bpfDumpCmds []common.Cmd
		for _, line := range bpfMaps {
			if m := bpfInfoLineRe.FindStringSubmatch(line); m != nil {
				id := m[1]
				name := m[2]
				bpfDumpCmds = append(bpfDumpCmds, common.Cmd{
					Info:     fmt.Sprintf("Collect eBPF map %s:%s for node %s", id, name, nodeName),
					CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- bpftool map dump id %s", namespace, podName, id),
					FilePath: fmt.Sprintf("%s/bpf-maps/%s-id_%s.txt", curNodeDir, name, id),
				})
			}
		}
		common.ExecAllCmdsWriteToFile(bpfDumpCmds)
	}

	// Collect all of the CNI logs
	output, err = common.ExecCmd(fmt.Sprintf(
		"kubectl exec -n %s -t %s -c calico-node -- ls /var/log/calico/cni",
		namespace,
		podName,
	))
	if err != nil {
		fmt.Printf("Error listing the Calico CNI logs at /var/log/calico/cni/: %s\n", err)
	} else {
		cniLogFiles := strings.Split(strings.TrimSpace(output.String()), "\n")
		var cmds []common.Cmd
		for _, logFile := range cniLogFiles {
			cmds = append(cmds, common.Cmd{
				Info:     fmt.Sprintf("Collect CNI log %s for the node %s", logFile, nodeName),
				CmdStr:   fmt.Sprintf("kubectl exec -n %s -t %s -c calico-node -- cat /var/log/calico/cni/%s", namespace, podName, logFile),
				FilePath: fmt.Sprintf("%s/%s.log", curNodeDir, logFile),
			})
		}
		common.ExecAllCmdsWriteToFile(cmds)
	}
}

// createArchive attempts to bundle all the diagnostics files into a single compressed archive.
func createArchive(tempDir string, directoryName string, archiveName string) {
	fmt.Println("\n==== Producing a diagnostics bundle. ====")

	// Attempt to remove archive file (if it previously existed)
	err := os.Remove(fmt.Sprintf("rm -f %s", archiveName))
	if err != nil {
		// Not an error case we need to show the user
		log.Debugf("Could not remove previous version of %s: %s\n", archiveName, err)
	}

	// Attempt to create new archive
	output, err := common.ExecCmd(fmt.Sprintf("tar cfz ./%s -C %s %s", archiveName, tempDir, directoryName))
	log.Debugf("creating archive %s: output %s", archiveName, output.String())
	if err != nil {
		fmt.Printf("Could not create new archive %s: %s\n", archiveName, err)
		return
	}

	fmt.Printf("Diagnostic bundle created at ./%s\n", archiveName)
}
