// Copyright (c) 2019 Tigera, Inc. All rights reserved.

package v3

import (
	"time"

	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/tigera/api/pkg/apis/projectcalico/v3"
)

var _ = DescribeTable("GlobalAlert extractVariablesFromDescriptionTemplate",
	func(s string, e []string, ok bool) {
		a, err := extractVariablesFromDescriptionTemplate(s)
		if ok {
			Expect(err).ShouldNot(HaveOccurred())
			Expect(a).Should(Equal(e))
		} else {
			Expect(err).Should(HaveOccurred())
		}
	},
	Entry("empty string", "", nil, true),
	Entry("no variables", "foo bar", nil, true),
	Entry("empty variable name", "${}", []string{""}, true),
	Entry("variable name", "${abc}", []string{"abc"}, true),
	Entry("no variables but contains dollar", "foo $bar", nil, true),
	Entry("well formed with some variables", "foo ${bar} $baz ${abc}", []string{"bar", "abc"}, true),
	Entry("adjacent variables", "${bar}${abc}", []string{"bar", "abc"}, true),
	Entry("nested variables", "${bar${abc}}", []string{"bar${abc"}, true),
	Entry("non-terminated variable", "${bar", nil, false),
	Entry("just ${", "${", nil, false),
)

var _ = DescribeTable("GlobalAlert Validator",
	func(input interface{}, valid bool) {
		if valid {
			Expect(Validate(input)).NotTo(HaveOccurred(),
				"expected value to be valid")
		} else {
			Expect(Validate(input)).To(HaveOccurred(),
				"expected value to be invalid")
		}
	},

	Entry("minimal valid",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "dns",
			},
		},
		true,
	),

	Entry("missing description",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Severity: 100,
				DataSet:  "dns",
			},
		},
		false,
	),
	Entry("invalid description",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Severity:    100,
				DataSet:     "dns",
				Description: "${foo",
			},
		},
		false,
	),
	Entry("description with naked variable",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Severity:    100,
				DataSet:     "dns",
				Description: "$foo",
			},
		},
		true,
	),
	Entry("description with empty variable name",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Severity:    100,
				DataSet:     "dns",
				Description: "${}",
			},
		},
		false,
	),
	Entry("description referencing unknown field",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test ${unknown}",
				Severity:    100,
				DataSet:     "dns",
			},
		},
		false,
	),
	Entry("description referencing aggregation",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test ${source_ip}",
				Severity:    100,
				DataSet:     "dns",
				AggregateBy: []string{"source_ip"},
			},
		},
		true,
	),
	Entry("description referencing metric",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test ${count}",
				Severity:    100,
				DataSet:     "dns",
				Metric:      "count",
				Condition:   "eq",
			},
		},
		true,
	),

	Entry("summary present, description missing",
		&api.GlobalAlertSpec{
			Summary:     "foo",
			Description: "",
			DataSet:     "flows",
			Severity:    100,
		},
		false,
	),
	Entry("summary present, description present",
		&api.GlobalAlertSpec{
			Summary:     "foo",
			Description: "bar",
			DataSet:     "flows",
			Severity:    100,
		},
		true,
	),
	Entry("summary references a missing variable",
		&api.GlobalAlertSpec{
			Summary:     "foo ${bar}",
			Description: "bar",
			DataSet:     "flows",
			Severity:    100,
		},
		false,
	),
	Entry("summary malformed",
		&api.GlobalAlertSpec{
			Summary:     "foo ${bar",
			Description: "bar",
			DataSet:     "flows",
			Severity:    100,
		},
		false,
	),
	Entry("summary references a valid variable",
		&api.GlobalAlertSpec{
			Summary:     "foo ${dest_namespace}",
			Description: "bar",
			DataSet:     "flows",
			Severity:    100,
			AggregateBy: []string{"dest_namespace"},
		},
		true,
	),

	Entry("Severity too low",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    0,
				DataSet:     "dns",
			},
		},
		false,
	),
	Entry("Severity at minimum",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    1,
				DataSet:     "dns",
			},
		},
		true,
	),
	Entry("Severity at maximum",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "dns",
			},
		},
		true,
	),
	Entry("Severity too high",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    101,
				DataSet:     "dns",
			},
		},
		false,
	),

	Entry("valid period and lookback",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "dns",
				Period:      &v1.Duration{Duration: api.GlobalAlertMinPeriod},
				Lookback:    &v1.Duration{Duration: api.GlobalAlertMinLookback},
			},
		},
		true,
	),
	Entry("period too short",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "dns",
				Period:      &v1.Duration{Duration: api.GlobalAlertMinPeriod - time.Second},
				Lookback:    &v1.Duration{Duration: api.GlobalAlertMinLookback},
			},
		},
		false,
	),
	Entry("lookback too short",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "dns",
				Period:      &v1.Duration{Duration: api.GlobalAlertMinPeriod},
				Lookback:    &v1.Duration{Duration: api.GlobalAlertMinLookback - time.Second},
			},
		},
		false,
	),

	Entry("dataset audit",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "audit",
			},
		},
		true,
	),
	Entry("dataset dns",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "dns",
			},
		},
		true,
	),
	Entry("dataset flows",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
			},
		},
		true,
	),
	Entry("dataset invalid",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "test",
			},
		},
		false,
	),
	Entry("dataset missing",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
			},
		},
		false,
	),

	Entry("no query",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "dns",
			},
		},
		true,
	),
	Entry("non parsable query",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "audit",
				Query:       "verb = ",
			},
		},
		false,
	),
	Entry("audit query",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "audit",
				Query:       "verb = get",
			},
		},
		true,
	),
	Entry("invalid audit query",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "audit",
				Query:       "verb = test",
			},
		},
		false,
	),
	Entry("dns query",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "dns",
				Query:       "count = 0",
			},
		},
		true,
	),
	Entry("invalid dns query",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "dns",
				Query:       "count = test",
			},
		},
		false,
	),
	Entry("flows query",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
				Query:       "num_flows = 0",
			},
		},
		true,
	),
	Entry("invalid flows query",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
				Query:       "num_flows = test",
			},
		},
		false,
	),

	Entry("no aggregations",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
				AggregateBy: []string{},
			},
		},
		true,
	),
	Entry("1 aggregation",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
				AggregateBy: []string{"foo"},
			},
		},
		true,
	),
	Entry("2 aggregations",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
				AggregateBy: []string{"foo", "bar"},
			},
		},
		true,
	),

	Entry("no metric",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
			},
		},
		true,
	),
	Entry("count metric no field",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
				Metric:      "count",
				Condition:   "eq",
			},
		},
		true,
	),
	Entry("count metric with field",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
				Metric:      "count",
				Field:       "foo",
				Condition:   "eq",
			},
		},
		false,
	),
	Entry("avg metric no field",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
				Metric:      "avg",
				Condition:   "eq",
			},
		},
		false,
	),
	Entry("avg metric with field",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
				Metric:      "avg",
				Field:       "foo",
				Condition:   "eq",
			},
		},
		true,
	),
	Entry("max metric no field",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
				Metric:      "max",
				Condition:   "eq",
			},
		},
		false,
	),
	Entry("max metric with field",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
				Metric:      "max",
				Field:       "foo",
				Condition:   "eq",
			},
		},
		true,
	),
	Entry("min metric no field",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
				Metric:      "min",
				Condition:   "eq",
			},
		},
		false,
	),
	Entry("min metric with field",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
				Metric:      "min",
				Field:       "foo",
				Condition:   "eq",
			},
		},
		true,
	),
	Entry("max metric no field",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
				Metric:      "max",
				Condition:   "eq",
			},
		},
		false,
	),
	Entry("max metric with field",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
				Metric:      "max",
				Field:       "foo",
				Condition:   "eq",
			},
		},
		true,
	),
	Entry("invalid metric",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
				Metric:      "test",
			},
		},
		false,
	),
	Entry("field without metric",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Description: "test",
				Severity:    100,
				DataSet:     "flows",
				Field:       "test",
			},
		},
		false,
	),
	Entry("metric without condition (CNX-11120)",
		&api.GlobalAlert{
			ObjectMeta: v1.ObjectMeta{Name: "badalert"},
			Spec: api.GlobalAlertSpec{
				Description: "Bad alert",
				Severity:    100,
				DataSet:     "flows",
				Query:       `dest_namespace="tigera-internal" AND "dest_labels.labels"="app=tigera-internal-1"`,
				Metric:      "count",
			},
		},
		false,
	),
)

var _ = DescribeTable("GlobalAlertTemplate Validator",
	func(input interface{}, valid bool) {
		if valid {
			Expect(Validate(input)).NotTo(HaveOccurred(),
				"expected value to be valid")
		} else {
			Expect(Validate(input)).To(HaveOccurred(),
				"expected value to be invalid")
		}
	},

	Entry("valid template",
		&api.GlobalAlertTemplate{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Summary:     "foo",
				Description: "test",
				Severity:    100,
				DataSet:     "dns",
			},
		},
		true,
	),
	Entry("invalid GlobalAlertSpec",
		&api.GlobalAlertTemplate{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Summary:     "bar",
				Description: "test",
				Severity:    100,
			},
		},
		false,
	),
	Entry("empty summary",
		&api.GlobalAlertTemplate{
			ObjectMeta: v1.ObjectMeta{Name: "sandwiches"},
			Spec: api.GlobalAlertSpec{
				Summary:     "",
				Description: "test",
				Severity:    100,
				DataSet:     "dns",
			},
		},
		true,
	),
)
