package fv

import (
	"github.com/projectcalico/calicoctl/calicoctl/resourcemgr"
	"github.com/tigera/calicoq/web/pkg/querycache/client"
)

func nodeTestQueryData() []testQueryData {
	return []testQueryData{
		{
			"single node",
			[]resourcemgr.ResourceObject{node1},
			client.QueryNodesReq{},
			&client.QueryNodesResp{
				Count: 1,
				Items: []client.Node{qcNode(node1, 0, 0)},
			},
		},
		{
			"single wep",
			[]resourcemgr.ResourceObject{wep4_n2_ns1},
			client.QueryNodesReq{},
			&client.QueryNodesResp{
				Count: 1,
				Items: []client.Node{qcNode(wep4_n2_ns1, 0, 1)},
			},
		},
		{
			"single hep",
			[]resourcemgr.ResourceObject{hep2_n3},
			client.QueryNodesReq{},
			&client.QueryNodesResp{
				Count: 1,
				Items: []client.Node{qcNode(hep2_n3, 1, 0)},
			},
		},
		{
			"single wep that will be filtered from policy out because it has no IPNetworks configured",
			[]resourcemgr.ResourceObject{wep2_n1_ns1_filtered_out},
			client.QueryNodesReq{},
			&client.QueryNodesResp{
				Count: 1,
				// Whilst it's filtered out in terms of policy, the WEP will still be included in the node count.
				Items: []client.Node{qcNode(wep2_n1_ns1_filtered_out, 0, 1)},
			},
		},
		{
			"multiple nodes",
			[]resourcemgr.ResourceObject{node1, node2, node3, node4},
			client.QueryNodesReq{},
			&client.QueryNodesResp{
				Count: 4,
				Items: []client.Node{qcNode(node4, 0, 0), qcNode(node1, 0, 0), qcNode(node2, 0, 0), qcNode(node3, 0, 0)},
			},
		},
		{
			"multiple nodes - page 1/2",
			[]resourcemgr.ResourceObject{node1, node2, node3, node4},
			client.QueryNodesReq{
				Page: &client.Page{
					PageNum:    0,
					NumPerPage: 3,
				},
			},
			&client.QueryNodesResp{
				Count: 4,
				Items: []client.Node{qcNode(node4, 0, 0), qcNode(node1, 0, 0), qcNode(node2, 0, 0)},
			},
		},
		{
			"multiple nodes - page 2/2",
			[]resourcemgr.ResourceObject{node1, node2, node3, node4},
			client.QueryNodesReq{
				Page: &client.Page{
					PageNum:    1,
					NumPerPage: 3,
				},
			},
			&client.QueryNodesResp{
				Count: 4,
				Items: []client.Node{qcNode(node3, 0, 0)},
			},
		},
		{
			"multiple nodes - page 3/2",
			[]resourcemgr.ResourceObject{node1, node2, node3, node4},
			client.QueryNodesReq{
				Page: &client.Page{
					PageNum:    2,
					NumPerPage: 3,
				},
			},
			&client.QueryNodesResp{
				Count: 4,
				Items: []client.Node{},
			},
		},
		{
			"multiple weps (large number of requests per page)",
			[]resourcemgr.ResourceObject{wep4_n2_ns1, wep3_n1_ns2, wep1_n1_ns1, wep5_n3_ns2_unlabelled},
			client.QueryNodesReq{
				Page: &client.Page{
					PageNum:    0,
					NumPerPage: 100000,
				},
			},
			&client.QueryNodesResp{
				Count: 3,
				Items: []client.Node{
					qcNode(wep1_n1_ns1, 0, 2), qcNode(wep4_n2_ns1, 0, 1), qcNode(wep5_n3_ns2_unlabelled, 0, 1),
				},
			},
		},
		{
			"multiple heps",
			[]resourcemgr.ResourceObject{hep2_n3, hep3_n4, hep1_n2, hep4_n4_unlabelled},
			client.QueryNodesReq{},
			&client.QueryNodesResp{
				Count: 3,
				Items: []client.Node{qcNode(hep3_n4, 2, 0), qcNode(hep1_n2, 1, 0), qcNode(hep2_n3, 1, 0)},
			},
		},
		{
			"multiple nodes, weps, heps",
			[]resourcemgr.ResourceObject{
				node1, node2, hep2_n3, hep3_n4, hep1_n2, hep4_n4_unlabelled, wep4_n2_ns1, wep3_n1_ns2,
				wep1_n1_ns1, wep5_n3_ns2_unlabelled,
			},
			client.QueryNodesReq{},
			&client.QueryNodesResp{
				Count: 4,
				Items: []client.Node{
					qcNode(hep3_n4, 2, 0), qcNode(node1, 0, 2), qcNode(node2, 1, 1), qcNode(hep2_n3, 1, 1),
				},
			},
		},
		{
			"multiple nodes, weps, heps - query single node",
			[]resourcemgr.ResourceObject{
				node1, node2, hep2_n3, hep3_n4, hep1_n2, hep4_n4_unlabelled, wep4_n2_ns1, wep3_n1_ns2,
				wep1_n1_ns1, wep5_n3_ns2_unlabelled,
			},
			client.QueryNodesReq{
				Node: resourceKey(node2),
			},
			&client.QueryNodesResp{
				Count: 1,
				Items: []client.Node{qcNode(node2, 1, 1)},
			},
		},
		{
			"multiple nodes, weps, heps - reverse sort",
			[]resourcemgr.ResourceObject{
				node1, node2, hep2_n3, hep3_n4, hep1_n2, hep4_n4_unlabelled, wep4_n2_ns1, wep3_n1_ns2,
				wep1_n1_ns1, wep5_n3_ns2_unlabelled,
			},
			client.QueryNodesReq{
				Sort: &client.Sort{
					Reverse: true,
				},
			},
			&client.QueryNodesResp{
				Count: 4,
				Items: []client.Node{
					qcNode(hep2_n3, 1, 1), qcNode(node2, 1, 1), qcNode(node1, 0, 2), qcNode(hep3_n4, 2, 0),
				},
			},
		},
		{
			"multiple nodes, weps, heps - sort by name",
			[]resourcemgr.ResourceObject{
				node1, node2, hep2_n3, hep3_n4, hep1_n2, hep4_n4_unlabelled, wep4_n2_ns1, wep3_n1_ns2,
				wep1_n1_ns1, wep5_n3_ns2_unlabelled,
			},
			client.QueryNodesReq{
				Sort: &client.Sort{
					SortBy: []string{"name"},
				},
			},
			&client.QueryNodesResp{
				Count: 4,
				Items: []client.Node{
					qcNode(hep3_n4, 2, 0), qcNode(node1, 0, 2), qcNode(node2, 1, 1), qcNode(hep2_n3, 1, 1),
				},
			},
		},
		{
			"multiple nodes, weps, heps - sort by numHostEndpoints",
			[]resourcemgr.ResourceObject{
				node1, node2, hep2_n3, hep3_n4, hep1_n2, hep4_n4_unlabelled, wep4_n2_ns1, wep3_n1_ns2,
				wep1_n1_ns1, wep5_n3_ns2_unlabelled,
			},
			client.QueryNodesReq{
				Sort: &client.Sort{
					SortBy: []string{"numHostEndpoints"},
				},
			},
			&client.QueryNodesResp{
				Count: 4,
				Items: []client.Node{
					qcNode(node1, 0, 2), qcNode(node2, 1, 1), qcNode(hep2_n3, 1, 1), qcNode(hep3_n4, 2, 0),
				},
			},
		},
		{
			"multiple nodes, weps, heps - sort by numWorkloadEndpoints",
			[]resourcemgr.ResourceObject{
				node1, node2, hep2_n3, hep3_n4, hep1_n2, hep4_n4_unlabelled, wep4_n2_ns1, wep3_n1_ns2,
				wep1_n1_ns1, wep5_n3_ns2_unlabelled,
			},
			client.QueryNodesReq{
				Sort: &client.Sort{
					SortBy: []string{"numWorkloadEndpoints"},
				},
			},
			&client.QueryNodesResp{
				Count: 4,
				Items: []client.Node{
					qcNode(hep3_n4, 2, 0), qcNode(node2, 1, 1), qcNode(hep2_n3, 1, 1), qcNode(node1, 0, 2),
				},
			},
		},
		{
			"multiple nodes, weps, heps - sort by numEndpoints",
			[]resourcemgr.ResourceObject{
				node1, node2, hep2_n3, hep3_n4, hep1_n2, hep4_n4_unlabelled, wep4_n2_ns1, wep3_n1_ns2,
				wep1_n1_ns1,
			},
			client.QueryNodesReq{
				Sort: &client.Sort{
					SortBy: []string{"numEndpoints"},
				},
			},
			&client.QueryNodesResp{
				Count: 4,
				Items: []client.Node{
					qcNode(hep2_n3, 1, 0), qcNode(hep3_n4, 2, 0), qcNode(node1, 0, 2), qcNode(node2, 1, 1),
				},
			},
		},
		{
			"multiple nodes, weps, heps - sort by bgpIPAddresses",
			[]resourcemgr.ResourceObject{
				node1, node2, hep2_n3, hep3_n4, hep1_n2, hep4_n4_unlabelled, wep4_n2_ns1, wep3_n1_ns2,
				wep1_n1_ns1, wep5_n3_ns2_unlabelled,
			},
			client.QueryNodesReq{
				Sort: &client.Sort{
					SortBy: []string{"bgpIPAddresses"},
				},
			},
			&client.QueryNodesResp{
				Count: 4,
				Items: []client.Node{
					qcNode(hep3_n4, 2, 0), qcNode(hep2_n3, 1, 1), qcNode(node1, 0, 2), qcNode(node2, 1, 1),
				},
			},
		},
		{
			"reset by removing all nodes, weps and heps",
			[]resourcemgr.ResourceObject{},
			client.QueryNodesReq{},
			&client.QueryNodesResp{
				Count: 0,
				Items: []client.Node{},
			},
		},
	}
}
