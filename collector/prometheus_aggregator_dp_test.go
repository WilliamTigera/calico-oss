// Copyright (c) 2017-2018 Tigera, Inc. All rights reserved.

package collector

import (
	"fmt"
	"math"
	"net"
	"reflect"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/projectcalico/felix/rules"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/set"
)

var dummyWlEpKey = model.WorkloadEndpointKey{
	Hostname:       "localhost",
	OrchestratorID: "orchestrator",
	WorkloadID:     "workloadid",
	EndpointID:     "epid",
}

var (
	ingressRulePolicy1Allow = &rules.RuleIDs{
		Action:    rules.ActionAllow,
		Index:     "0",
		Policy:    "policy1",
		Tier:      "default",
		Namespace: rules.NamespaceGlobal,
		Direction: rules.RuleDirIngress,
	}
	ingressRulePolicy2Allow = &rules.RuleIDs{
		Action:    rules.ActionAllow,
		Index:     "0",
		Policy:    "policy1",
		Tier:      "default",
		Namespace: rules.NamespaceGlobal,
		Direction: rules.RuleDirIngress,
	}
	ingressRulePolicy3Deny = &rules.RuleIDs{
		Action:    rules.ActionDeny,
		Index:     "0",
		Policy:    "policy3",
		Tier:      "default",
		Namespace: rules.NamespaceGlobal,
		Direction: rules.RuleDirIngress,
	}
	ingressRulePolicy4Deny = &rules.RuleIDs{
		Action:    rules.ActionDeny,
		Index:     "0",
		Policy:    "policy4",
		Tier:      "default",
		Namespace: rules.NamespaceGlobal,
		Direction: rules.RuleDirIngress,
	}
)

var (
	denyPacketTuple1DenyT3 = &MetricUpdate{
		updateType:   UpdateTypeReport,
		tuple:        tuple1,
		isConnection: true,
		ruleIDs:      ingressRulePolicy3Deny,
		inMetric: MetricValue{
			deltaPackets: 1,
			deltaBytes:   1,
		},
	}
	denyPacketTuple2DenyT3 = &MetricUpdate{
		updateType:   UpdateTypeReport,
		tuple:        tuple2,
		isConnection: true,
		ruleIDs:      ingressRulePolicy3Deny,
		inMetric: MetricValue{
			deltaPackets: 1,
			deltaBytes:   1,
		},
	}
	denyPacketTuple3DenyT4 = &MetricUpdate{
		updateType:   UpdateTypeReport,
		tuple:        tuple3,
		isConnection: true,
		ruleIDs:      ingressRulePolicy4Deny,
		inMetric: MetricValue{
			deltaPackets: 1,
			deltaBytes:   1,
		},
	}
)

func getPolicyName(r *rules.RuleIDs) string {
	if r.Namespace == rules.NamespaceGlobal {
		return fmt.Sprintf("%s|%s|%s|%s", r.Tier, r.Policy, r.Index, r.Action)
	} else {
		return fmt.Sprintf("%s|%s/%s|%s|%s", r.Tier, r.Namespace, r.Policy, r.Index, r.Action)
	}
}

func getMetricNumber(m prometheus.Gauge) int {
	// The actual number stored inside a prometheus metric is surprisingly hard to
	// get to.
	if m == nil {
		return -1
	}
	v := reflect.ValueOf(m).Elem()
	valBits := v.FieldByName("valBits")
	return int(math.Float64frombits(valBits.Uint()))
}

var _ = Describe("Denied packets Prometheus Aggregator", func() {
	var da *DeniedPacketsAggregator
	BeforeEach(func() {
		registry := prometheus.NewRegistry()
		da = NewDeniedPacketsAggregator(retentionTime, "testHost")
		da.RegisterMetrics(registry)
	})
	AfterEach(func() {
		gaugeDeniedPackets.Reset()
		gaugeDeniedBytes.Reset()
	})
	Describe("Test Report", func() {
		Context("No existing aggregated stats", func() {
			Describe("Same policy and source IP but different connections", func() {
				var (
					key   DeniedPacketsAggregateKey
					value DeniedPacketsAggregateValue
					refs  set.Set
					ok    bool
				)
				BeforeEach(func() {
					key = DeniedPacketsAggregateKey{
						srcIP:  localIp1,
						policy: getPolicyName(ingressRulePolicy3Deny),
					}
					refs = set.New()
					refs.AddAll([]Tuple{tuple1, tuple2})
					da.OnUpdate(denyPacketTuple1DenyT3)
					da.OnUpdate(denyPacketTuple2DenyT3)
				})
				It("should have 1 aggregated stats entry", func() {
					Expect(da.aggStats).Should(HaveLen(1))
				})
				It("should have correct packet and byte counts", func() {
					Expect(func() int {
						value, ok = da.aggStats[key]
						// If we didn't find the key now, we'll
						// not want to look into the value.
						if !ok {
							return -1
						}
						return getMetricNumber(value.packets)
					}()).Should(Equal(2))
					Expect(func() int {
						value, ok = da.aggStats[key]
						// If we didn't find the key now, we'll
						// not want to look into the value.
						if !ok {
							return -1
						}
						return getMetricNumber(value.bytes)
					}()).Should(Equal(2))
				})
				It("should have correct refs", func() {
					Expect(func() bool {
						value, ok = da.aggStats[key]
						// If we didn't find the key now, we'll
						// not want to look into the value.
						if !ok {
							return false
						}
						return value.refs.Equals(refs)
					}()).Should(BeTrue())
				})
			})
			Describe("Different source IPs and Policies", func() {
				var (
					key1, key2     DeniedPacketsAggregateKey
					value1, value2 DeniedPacketsAggregateValue
					refs1, refs2   set.Set
					ok             bool
				)
				BeforeEach(func() {
					key1 = DeniedPacketsAggregateKey{
						srcIP:  localIp1,
						policy: getPolicyName(ingressRulePolicy3Deny),
					}
					key2 = DeniedPacketsAggregateKey{
						srcIP:  localIp2,
						policy: getPolicyName(ingressRulePolicy4Deny),
					}
					refs1 = set.New()
					refs1.AddAll([]Tuple{tuple1, tuple2})
					refs2 = set.New()
					refs2.AddAll([]Tuple{tuple3})
					da.OnUpdate(denyPacketTuple1DenyT3)
					da.OnUpdate(denyPacketTuple2DenyT3)
					da.OnUpdate(denyPacketTuple3DenyT4)
				})
				It("should have 2 aggregated stats entries", func() {
					Expect(da.aggStats).Should(HaveLen(2))
				})
				It("should have correct packet and byte counts", func() {
					Expect(func() int {
						value1, ok = da.aggStats[key1]
						// If we didn't find the key now, we'll
						// not want to look into the value.
						if !ok {
							return -1
						}
						return getMetricNumber(value1.packets)
					}()).Should(Equal(2))
					Expect(func() int {
						value1, ok = da.aggStats[key1]
						// If we didn't find the key now, we'll
						// not want to look into the value.
						if !ok {
							return -1
						}
						return getMetricNumber(value1.bytes)
					}()).Should(Equal(2))
					Expect(func() int {
						value2, ok = da.aggStats[key2]
						// If we didn't find the key now, we'll
						// not want to look into the value.
						if !ok {
							return -1
						}
						return getMetricNumber(value2.packets)
					}()).Should(Equal(1))
					Expect(func() int {
						value2, ok = da.aggStats[key2]
						// If we didn't find the key now, we'll
						// not want to look into the value.
						if !ok {
							return -1
						}
						return getMetricNumber(value2.bytes)
					}()).Should(Equal(1))
				})
				It("should have correct refs", func() {
					Expect(func() bool {
						value1, ok = da.aggStats[key1]
						// If we didn't find the key now, we'll
						// not want to look into the value.
						if !ok {
							return false
						}
						return value1.refs.Equals(refs1)
					}()).Should(BeTrue())
					Expect(func() bool {
						value2, ok = da.aggStats[key2]
						// If we didn't find the key now, we'll
						// not want to look into the value.
						if !ok {
							return false
						}
						return value2.refs.Equals(refs2)
					}()).Should(BeTrue())
				})
			})
		})
	})
	Describe("Test Expire", func() {
		var key1, key2 DeniedPacketsAggregateKey
		var value1, value2 DeniedPacketsAggregateValue
		BeforeEach(func() {
			key1 = DeniedPacketsAggregateKey{
				srcIP:  localIp1,
				policy: getPolicyName(ingressRulePolicy3Deny),
			}
			key2 = DeniedPacketsAggregateKey{
				srcIP:  localIp2,
				policy: getPolicyName(ingressRulePolicy4Deny),
			}
			label1 := prometheus.Labels{
				"srcIP":        net.IP(localIp1[:16]).String(),
				"policy":       getPolicyName(ingressRulePolicy3Deny),
				LABEL_INSTANCE: "testHost",
			}
			label2 := prometheus.Labels{
				"srcIP":        net.IP(localIp2[:16]).String(),
				"policy":       getPolicyName(ingressRulePolicy4Deny),
				LABEL_INSTANCE: "testHost",
			}
			value1 = DeniedPacketsAggregateValue{
				packets: gaugeDeniedPackets.With(label1),
				bytes:   gaugeDeniedBytes.With(label1),
				refs:    set.FromArray([]Tuple{tuple1, tuple2}),
			}
			value1.packets.Set(3)
			value1.bytes.Set(3)
			value2 = DeniedPacketsAggregateValue{
				packets: gaugeDeniedPackets.With(label2),
				bytes:   gaugeDeniedBytes.With(label2),
				refs:    set.FromArray([]Tuple{tuple3}),
			}
			value2.packets.Set(2)
			value2.bytes.Set(4)
			da.aggStats[key1] = value1
			da.aggStats[key2] = value2
		})
		Describe("Delete a entry has more than one reference", func() {
			var (
				v1, v2       DeniedPacketsAggregateValue
				refs1, refs2 set.Set
				ok           bool
			)
			BeforeEach(func() {
				refs1 = set.New()
				refs1.AddAll([]Tuple{tuple2})
				refs2 = set.New()
				refs2.AddAll([]Tuple{tuple3})
				denyPacketTuple1DenyT3.inMetric.deltaPackets = 0
				denyPacketTuple1DenyT3.inMetric.deltaBytes = 0
				denyPacketTuple1DenyT3.updateType = UpdateTypeExpire
				da.OnUpdate(denyPacketTuple1DenyT3)
			})
			AfterEach(func() {
				denyPacketTuple1DenyT3.inMetric.deltaPackets = 1
				denyPacketTuple1DenyT3.inMetric.deltaBytes = 1
			})
			It("should have 2 aggregated stats entries", func() {
				Expect(da.aggStats).Should(HaveLen(2))
			})
			It("should have correct packet and byte counts", func() {
				Expect(func() int {
					v1, ok = da.aggStats[key1]
					// If we didn't find the key now, we'll
					// not want to look into the value.
					if !ok {
						return -1
					}
					return getMetricNumber(v1.packets)
				}()).Should(Equal(3))
				Expect(func() int {
					v1, ok = da.aggStats[key1]
					// If we didn't find the key now, we'll
					// not want to look into the value.
					if !ok {
						return -1
					}
					return getMetricNumber(v1.bytes)
				}()).Should(Equal(3))
				Expect(func() int {
					v2, ok = da.aggStats[key2]
					// If we didn't find the key now, we'll
					// not want to look into the value.
					if !ok {
						return -1
					}
					return getMetricNumber(v2.packets)
				}()).Should(Equal(2))
				Expect(func() int {
					v2, ok = da.aggStats[key2]
					// If we didn't find the key now, we'll
					// not want to look into the value.
					if !ok {
						return -1
					}
					return getMetricNumber(v2.bytes)
				}()).Should(Equal(4))
			})
			It("should have correct refs", func() {
				Expect(func() bool {
					v1, ok = da.aggStats[key1]
					// If we didn't find the key now, we'll
					// not want to look into the value.
					if !ok {
						return false
					}
					return v1.refs.Equals(refs1)
				}()).Should(BeTrue())
				Expect(func() bool {
					v2, ok = da.aggStats[key2]
					// If we didn't find the key now, we'll
					// not want to look into the value.
					if !ok {
						return false
					}
					return v2.refs.Equals(refs2)
				}()).Should(BeTrue())
			})
		})
		Describe("Delete a entry has only one reference", func() {
			var (
				v1    DeniedPacketsAggregateValue
				refs1 set.Set
				ok    bool
			)
			BeforeEach(func() {
				v1 = da.aggStats[key1]
				refs1 = set.FromArray([]Tuple{tuple1, tuple2})
				denyPacketTuple3DenyT4.updateType = UpdateTypeExpire
				da.OnUpdate(denyPacketTuple3DenyT4)
			})
			It("should have 2 stats entries", func() {
				Expect(da.aggStats).Should(HaveLen(2))
			})
			It("should have correct packet and byte counts", func() {
				Expect(func() int {
					v1, ok = da.aggStats[key1]
					// If we didn't find the key now, we'll
					// not want to look into the value.
					if !ok {
						return -1
					}
					return getMetricNumber(v1.packets)
				}()).Should(Equal(3))
				Expect(func() int {
					v1, ok = da.aggStats[key1]
					// If we didn't find the key now, we'll
					// not want to look into the value.
					if !ok {
						return -1
					}
					return getMetricNumber(v1.bytes)
				}()).Should(Equal(3))
			})
			It("should have correct refs", func() {
				Expect(func() bool {
					v1, ok = da.aggStats[key1]
					// If we didn't find the key now, we'll
					// not want to look into the value.
					if !ok {
						return false
					}
					return v1.refs.Equals(refs1)
				}()).Should(BeTrue())
			})
			It("should have the deleted entry as candidate for deletion", func() {
				Expect(da.retainedMetrics).Should(HaveKey(key2))
			})
		})
	})
})
