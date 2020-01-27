// Copyright (c) 2019-2020 Tigera, Inc. All rights reserved.

package users_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/projectcalico/kube-controllers/pkg/elasticsearch"
	"github.com/projectcalico/kube-controllers/pkg/elasticsearch/users"
)

var _ = Describe("ElasticseachUsers", func() {
	Context("management flag set to false", func() {
		It("the expected users and roles are available", func() {
			testElasticsearchUsers(users.ElasticsearchUsers("managed-cluster", false),
				map[users.ElasticsearchUserName]elasticsearch.User{
					"tigera-fluentd": {
						Username: "tigera-fluentd-managed-cluster",
						Roles: []elasticsearch.Role{{
							Name: "tigera-fluentd-managed-cluster",
							Definition: &elasticsearch.RoleDefinition{
								Cluster: []string{"monitor", "manage_index_templates"},
								Indices: []elasticsearch.RoleIndex{{
									Names:      []string{"tigera_secure_ee_*.managed-cluster.*"},
									Privileges: []string{"create_index", "write"},
								}},
							},
						}},
					},
					"tigera-eks-log-forwarder": {
						Username: "tigera-eks-log-forwarder-managed-cluster",
						Roles: []elasticsearch.Role{{
							Name: "tigera-eks-log-forwarder-managed-cluster",
							Definition: &elasticsearch.RoleDefinition{
								Cluster: []string{"monitor", "manage_index_templates"},
								Indices: []elasticsearch.RoleIndex{{
									Names:      []string{"tigera_secure_ee_audit_kube.managed-cluster.*"},
									Privileges: []string{"create_index", "read", "write"},
								}},
							},
						}},
					},
					"tigera-ee-compliance-benchmarker": {
						Username: "tigera-ee-compliance-benchmarker-managed-cluster",
						Roles: []elasticsearch.Role{{
							Name: "tigera-ee-compliance-benchmarker-managed-cluster",
							Definition: &elasticsearch.RoleDefinition{
								Cluster: []string{"monitor", "manage_index_templates"},
								Indices: []elasticsearch.RoleIndex{{
									Names:      []string{"tigera_secure_ee_benchmark_results.managed-cluster.*"},
									Privileges: []string{"create_index", "write", "view_index_metadata", "read"},
								}},
							},
						}},
					},
					"tigera-ee-compliance-controller": {
						Username: "tigera-ee-compliance-controller-managed-cluster",
						Roles: []elasticsearch.Role{{
							Name: "tigera-ee-compliance-controller-managed-cluster",
							Definition: &elasticsearch.RoleDefinition{
								Cluster: []string{"monitor", "manage_index_templates"},
								Indices: []elasticsearch.RoleIndex{{
									Names:      []string{"tigera_secure_ee_compliance_reports.managed-cluster.*"},
									Privileges: []string{"read"},
								}},
							},
						}},
					},
					"tigera-ee-compliance-reporter": {
						Username: "tigera-ee-compliance-reporter-managed-cluster",
						Roles: []elasticsearch.Role{{
							Name: "tigera-ee-compliance-reporter-managed-cluster",
							Definition: &elasticsearch.RoleDefinition{
								Cluster: []string{"monitor", "manage_index_templates"},
								Indices: []elasticsearch.RoleIndex{
									{
										Names:      []string{"tigera_secure_ee_audit_*.managed-cluster.*"},
										Privileges: []string{"read"},
									},
									{
										Names:      []string{"tigera_secure_ee_snapshots.managed-cluster.*"},
										Privileges: []string{"read"},
									},
									{
										Names:      []string{"tigera_secure_ee_benchmark_results.managed-cluster.*"},
										Privileges: []string{"read"},
									},
									{
										Names:      []string{"tigera_secure_ee_compliance_reports.managed-cluster.*"},
										Privileges: []string{"create_index", "write", "view_index_metadata", "read"},
									},
								},
							},
						}},
					},
					"tigera-ee-compliance-snapshotter": {
						Username: "tigera-ee-compliance-snapshotter-managed-cluster",
						Roles: []elasticsearch.Role{{
							Name: "tigera-ee-compliance-snapshotter-managed-cluster",
							Definition: &elasticsearch.RoleDefinition{
								Cluster: []string{"monitor", "manage_index_templates"},
								Indices: []elasticsearch.RoleIndex{{
									Names:      []string{"tigera_secure_ee_snapshots.managed-cluster.*"},
									Privileges: []string{"create_index", "write", "view_index_metadata", "read"},
								}},
							},
						}},
					},
					"tigera-ee-intrusion-detection": {
						Username: "tigera-ee-intrusion-detection-managed-cluster",
						Roles: []elasticsearch.Role{
							{
								Name: "tigera-ee-intrusion-detection-managed-cluster",
								Definition: &elasticsearch.RoleDefinition{
									Cluster: []string{"monitor", "manage_index_templates"},
									Indices: []elasticsearch.RoleIndex{
										{
											Names:      []string{"tigera_secure_ee_*.managed-cluster.*"},
											Privileges: []string{"read"},
										},
										{
											Names: []string{
												".tigera.ipset.managed-cluster.*",
												"tigera_secure_ee_events.managed-cluster.*",
												".tigera.domainnameset.managed-cluster.*"},
											Privileges: []string{"all"},
										},
									},
								},
							},
							{
								Name: "watcher_admin",
							},
						},
					},
					"tigera-ee-installer": {
						Username: "tigera-ee-installer-managed-cluster",
						Roles: []elasticsearch.Role{{
							Name: "tigera-ee-installer-managed-cluster",
							Definition: &elasticsearch.RoleDefinition{
								Cluster: []string{"manage_ml", "manage_watcher", "manage"},
								Indices: []elasticsearch.RoleIndex{
									{
										Names:      []string{"tigera_secure_ee_*.managed-cluster.*"},
										Privileges: []string{"read", "write"},
									},
								},
								Applications: []elasticsearch.Application{{
									Application: "kibana-.kibana",
									Privileges:  []string{"all"},
									Resources:   []string{"*"},
								}},
							},
						}},
					},
				},
			)
		})
	})
	Context("management flag set to true", func() {
		It("the expected users and roles are available", func() {
			testElasticsearchUsers(users.ElasticsearchUsers("cluster", true),
				map[users.ElasticsearchUserName]elasticsearch.User{
					"tigera-fluentd": {
						Username: "tigera-fluentd",
						Roles: []elasticsearch.Role{{
							Name: "tigera-fluentd",
							Definition: &elasticsearch.RoleDefinition{
								Cluster: []string{"monitor", "manage_index_templates"},
								Indices: []elasticsearch.RoleIndex{{
									Names:      []string{"tigera_secure_ee_*.cluster.*"},
									Privileges: []string{"create_index", "write"},
								}},
							},
						}},
					},
					"tigera-eks-log-forwarder": {
						Username: "tigera-eks-log-forwarder",
						Roles: []elasticsearch.Role{{
							Name: "tigera-eks-log-forwarder",
							Definition: &elasticsearch.RoleDefinition{
								Cluster: []string{"monitor", "manage_index_templates"},
								Indices: []elasticsearch.RoleIndex{{
									Names:      []string{"tigera_secure_ee_audit_kube.cluster.*"},
									Privileges: []string{"create_index", "read", "write"},
								}},
							},
						}},
					},
					"tigera-ee-compliance-benchmarker": {
						Username: "tigera-ee-compliance-benchmarker",
						Roles: []elasticsearch.Role{{
							Name: "tigera-ee-compliance-benchmarker",
							Definition: &elasticsearch.RoleDefinition{
								Cluster: []string{"monitor", "manage_index_templates"},
								Indices: []elasticsearch.RoleIndex{{
									Names:      []string{"tigera_secure_ee_benchmark_results.cluster.*"},
									Privileges: []string{"create_index", "write", "view_index_metadata", "read"},
								}},
							},
						}},
					},
					"tigera-ee-compliance-controller": {
						Username: "tigera-ee-compliance-controller",
						Roles: []elasticsearch.Role{{
							Name: "tigera-ee-compliance-controller",
							Definition: &elasticsearch.RoleDefinition{
								Cluster: []string{"monitor", "manage_index_templates"},
								Indices: []elasticsearch.RoleIndex{{
									Names:      []string{"tigera_secure_ee_compliance_reports.cluster.*"},
									Privileges: []string{"read"},
								}},
							},
						}},
					},
					"tigera-ee-compliance-reporter": {
						Username: "tigera-ee-compliance-reporter",
						Roles: []elasticsearch.Role{{
							Name: "tigera-ee-compliance-reporter",
							Definition: &elasticsearch.RoleDefinition{
								Cluster: []string{"monitor", "manage_index_templates"},
								Indices: []elasticsearch.RoleIndex{
									{
										Names:      []string{"tigera_secure_ee_audit_*.cluster.*"},
										Privileges: []string{"read"},
									},
									{
										Names:      []string{"tigera_secure_ee_snapshots.cluster.*"},
										Privileges: []string{"read"},
									},
									{
										Names:      []string{"tigera_secure_ee_benchmark_results.cluster.*"},
										Privileges: []string{"read"},
									},
									{
										Names:      []string{"tigera_secure_ee_compliance_reports.cluster.*"},
										Privileges: []string{"create_index", "write", "view_index_metadata", "read"},
									},
								},
							},
						}},
					},
					"tigera-ee-compliance-snapshotter": {
						Username: "tigera-ee-compliance-snapshotter",
						Roles: []elasticsearch.Role{{
							Name: "tigera-ee-compliance-snapshotter",
							Definition: &elasticsearch.RoleDefinition{
								Cluster: []string{"monitor", "manage_index_templates"},
								Indices: []elasticsearch.RoleIndex{{
									Names:      []string{"tigera_secure_ee_snapshots.cluster.*"},
									Privileges: []string{"create_index", "write", "view_index_metadata", "read"},
								}},
							},
						}},
					},
					"tigera-ee-intrusion-detection": {
						Username: "tigera-ee-intrusion-detection",
						Roles: []elasticsearch.Role{
							{
								Name: "tigera-ee-intrusion-detection",
								Definition: &elasticsearch.RoleDefinition{
									Cluster: []string{"monitor", "manage_index_templates"},
									Indices: []elasticsearch.RoleIndex{
										{
											Names:      []string{"tigera_secure_ee_*.cluster.*"},
											Privileges: []string{"read"},
										},
										{
											Names: []string{
												".tigera.ipset.cluster.*",
												"tigera_secure_ee_events.cluster.*",
												".tigera.domainnameset.cluster.*"},
											Privileges: []string{"all"},
										},
									},
								},
							},
							{
								Name: "watcher_admin",
							},
						},
					},
					"tigera-ee-installer": {
						Username: "tigera-ee-installer",
						Roles: []elasticsearch.Role{{
							Name: "tigera-ee-installer",
							Definition: &elasticsearch.RoleDefinition{
								Cluster: []string{"manage_ml", "manage_watcher", "manage"},
								Indices: []elasticsearch.RoleIndex{
									{
										Names:      []string{"tigera_secure_ee_*.cluster.*"},
										Privileges: []string{"read", "write"},
									},
								},
								Applications: []elasticsearch.Application{{
									Application: "kibana-.kibana",
									Privileges:  []string{"all"},
									Resources:   []string{"*"},
								}},
							},
						}},
					},
					"tigera-ee-compliance-server": {
						Username: "tigera-ee-compliance-server",
						Roles: []elasticsearch.Role{{
							Name: "tigera-ee-compliance-server",
							Definition: &elasticsearch.RoleDefinition{
								Cluster: []string{"monitor", "manage_index_templates"},
								Indices: []elasticsearch.RoleIndex{{
									Names:      []string{"tigera_secure_ee_compliance_reports.cluster.*"},
									Privileges: []string{"read"},
								}},
							},
						}},
					},
					"tigera-ee-manager": {
						Username: "tigera-ee-manager",
						Roles: []elasticsearch.Role{{
							Name: "tigera-ee-manager",
							Definition: &elasticsearch.RoleDefinition{
								Cluster: []string{"monitor"},
								Indices: []elasticsearch.RoleIndex{{
									Names:      []string{"tigera_secure_ee_*.cluster.*", ".kibana"},
									Privileges: []string{"read"},
								}},
							},
						}},
					},
					"tigera-ee-curator": {
						Username: "tigera-ee-curator",
						Roles: []elasticsearch.Role{{
							Name: "tigera-ee-curator",
							Definition: &elasticsearch.RoleDefinition{
								Cluster: []string{"monitor", "manage_index_templates"},
								Indices: []elasticsearch.RoleIndex{{
									// Curator needs to trim all the logs, so we don't set the cluster name on the index pattern
									Names:      []string{"tigera_secure_ee_*.*.*"},
									Privileges: []string{"all"},
								}},
							},
						}},
					},
				},
			)
		})
	})
})

func testElasticsearchUsers(esUsers, expectedESUsers map[users.ElasticsearchUserName]elasticsearch.User) {
	Expect(len(esUsers)).Should(Equal(len(expectedESUsers)))
	for expectedName, expectedUser := range expectedESUsers {
		esUser, exists := esUsers[expectedName]
		Expect(exists).Should(BeTrue())
		Expect(esUser.Username).Should(Equal(expectedUser.Username))

		Expect(len(esUser.Roles)).Should(Equal(len(expectedUser.Roles)))

		for _, expectedRole := range expectedUser.Roles {
			for _, role := range esUser.Roles {
				if expectedRole.Name == role.Name {
					Expect(expectedRole.Definition).Should(Equal(role.Definition))
				}
			}
		}
	}
}
