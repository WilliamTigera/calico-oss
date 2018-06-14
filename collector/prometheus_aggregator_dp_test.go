// Copyright (c) 2017-2018 Tigera, Inc. All rights reserved.

package collector

import (
	"math"
	"net"
	"reflect"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/projectcalico/felix/calc"
	"github.com/projectcalico/felix/rules"
)

var (
	ingressRulePolicy3Deny = &calc.RuleID{
		Action:    rules.RuleActionDeny,
		Index:     0,
		IndexStr:  "0",
		Name:      "policy3",
		Tier:      "default",
		Namespace: "",
		Direction: rules.RuleDirIngress,
	}
	ingressRulePolicy4Deny = &calc.RuleID{
		Action:    rules.RuleActionDeny,
		Index:     0,
		IndexStr:  "0",
		Name:      "policy4",
		Tier:      "default",
		Namespace: "",
		Direction: rules.RuleDirIngress,
	}
)

var (
	denyPacketTuple1DenyT3 = MetricUpdate{
		updateType:   UpdateTypeReport,
		tuple:        tuple1,
		isConnection: true,
		ruleID:       ingressRulePolicy3Deny,
		inMetric: MetricValue{
			deltaPackets: 1,
			deltaBytes:   1,
		},
	}
	denyPacketTuple2DenyT3 = MetricUpdate{
		updateType:   UpdateTypeReport,
		tuple:        tuple2,
		isConnection: true,
		ruleID:       ingressRulePolicy3Deny,
		inMetric: MetricValue{
			deltaPackets: 1,
			deltaBytes:   1,
		},
	}
	denyPacketTuple3DenyT4 = MetricUpdate{
		updateType:   UpdateTypeReport,
		tuple:        tuple3,
		isConnection: true,
		ruleID:       ingressRulePolicy4Deny,
		inMetric: MetricValue{
			deltaPackets: 1,
			deltaBytes:   1,
		},
	}
)

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

var _ = Describe("Denied packets Prometheus PromAggregator", func() {
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
					refs  tupleSet
					ok    bool
				)
				BeforeEach(func() {
					key = DeniedPacketsAggregateKey{
						srcIP:  localIp1,
						policy: ingressRulePolicy3Deny.GetDeniedPacketRuleName(),
					}
					refs = NewTupleSet()
					refs.Add(tuple1)
					refs.Add(tuple2)
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
					Expect(func() tupleSet {
						value, ok = da.aggStats[key]
						// If we didn't find the key now, we'll
						// not want to look into the value.
						if !ok {
							return nil
						}
						return value.refs
					}()).To(Equal(refs))
				})
			})
			Describe("Different source IPs and Policies", func() {
				var (
					key1, key2     DeniedPacketsAggregateKey
					value1, value2 DeniedPacketsAggregateValue
					refs1, refs2   tupleSet
					ok             bool
				)
				BeforeEach(func() {
					key1 = DeniedPacketsAggregateKey{
						srcIP:  localIp1,
						policy: ingressRulePolicy3Deny.GetDeniedPacketRuleName(),
					}
					key2 = DeniedPacketsAggregateKey{
						srcIP:  localIp2,
						policy: ingressRulePolicy4Deny.GetDeniedPacketRuleName(),
					}
					refs1 = NewTupleSet()
					refs1.Add(tuple1)
					refs1.Add(tuple2)
					refs2 = NewTupleSet()
					refs2.Add(tuple3)
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
					Expect(func() tupleSet {
						value1, ok = da.aggStats[key1]
						// If we didn't find the key now, we'll
						// not want to look into the value.
						if !ok {
							return nil
						}
						return value1.refs
					}()).To(Equal(refs1))
					Expect(func() tupleSet {
						value2, ok = da.aggStats[key2]
						// If we didn't find the key now, we'll
						// not want to look into the value.
						if !ok {
							return nil
						}
						return value2.refs
					}()).To(Equal(refs2))
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
				policy: ingressRulePolicy3Deny.GetDeniedPacketRuleName(),
			}
			key2 = DeniedPacketsAggregateKey{
				srcIP:  localIp2,
				policy: ingressRulePolicy4Deny.GetDeniedPacketRuleName(),
			}
			label1 := prometheus.Labels{
				"srcIP":        net.IP(localIp1[:16]).String(),
				"policy":       ingressRulePolicy3Deny.GetDeniedPacketRuleName(),
				LABEL_INSTANCE: "testHost",
			}
			label2 := prometheus.Labels{
				"srcIP":        net.IP(localIp2[:16]).String(),
				"policy":       ingressRulePolicy4Deny.GetDeniedPacketRuleName(),
				LABEL_INSTANCE: "testHost",
			}
			value1 = DeniedPacketsAggregateValue{
				packets: gaugeDeniedPackets.With(label1),
				bytes:   gaugeDeniedBytes.With(label1),
				refs:    NewTupleSet(),
			}
			value1.refs.Add(tuple1)
			value1.refs.Add(tuple2)
			value1.packets.Set(3)
			value1.bytes.Set(3)
			value2 = DeniedPacketsAggregateValue{
				packets: gaugeDeniedPackets.With(label2),
				bytes:   gaugeDeniedBytes.With(label2),
				refs:    NewTupleSet(),
			}
			value2.refs.Add(tuple3)
			value2.packets.Set(2)
			value2.bytes.Set(4)
			da.aggStats[key1] = value1
			da.aggStats[key2] = value2
		})
		Describe("Delete a entry has more than one reference", func() {
			var (
				v1, v2       DeniedPacketsAggregateValue
				refs1, refs2 tupleSet
				ok           bool
			)
			BeforeEach(func() {
				refs1 = NewTupleSet()
				refs1.Add(tuple2)
				refs2 = NewTupleSet()
				refs2.Add(tuple3)
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
				Expect(func() tupleSet {
					v1, ok = da.aggStats[key1]
					// If we didn't find the key now, we'll
					// not want to look into the value.
					if !ok {
						return nil
					}
					return v1.refs
				}()).To(Equal(refs1))
				Expect(func() tupleSet {
					v2, ok = da.aggStats[key2]
					// If we didn't find the key now, we'll
					// not want to look into the value.
					if !ok {
						return nil
					}
					return v2.refs
				}()).To(Equal(refs2))
			})
		})
		Describe("Delete a entry has only one reference", func() {
			var (
				v1    DeniedPacketsAggregateValue
				refs1 tupleSet
				ok    bool
			)
			BeforeEach(func() {
				v1 = da.aggStats[key1]
				refs1 = NewTupleSet()
				refs1.Add(tuple1)
				refs1.Add(tuple2)
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
				Expect(func() tupleSet {
					v1, ok = da.aggStats[key1]
					// If we didn't find the key now, we'll
					// not want to look into the value.
					if !ok {
						return nil
					}
					return v1.refs
				}()).To(Equal(refs1))
			})
			It("should have the deleted entry as candidate for deletion", func() {
				Expect(da.retainedMetrics).Should(HaveKey(key2))
			})
		})
	})
})

var resKey DeniedPacketsAggregateKey

func BenchmarkCalicoDeniedPacketPolicyAggregateKey(b *testing.B) {
	var key DeniedPacketsAggregateKey
	rid := calc.NewRuleID("default", "policy1", "__GLOBAL__", 0, rules.RuleDirIngress, rules.RuleActionDeny)
	mu := MetricUpdate{
		updateType:   UpdateTypeReport,
		tuple:        tuple1,
		isConnection: true,
		ruleID:       rid,
		inMetric: MetricValue{
			deltaPackets: 1,
			deltaBytes:   1,
		},
	}
	b.ResetTimer()
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		key = getDeniedPacketsAggregateKey(mu)
	}
	resKey = key
}
