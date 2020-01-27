package middleware

import (
	"io/ioutil"
	"net/http"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	invalidPreview = `{
   verb":"create",
   "networkPolicy:{
      "spec":{
         "Tier":test
      }
   }
}`
)

var (
	validSelectors = []string{
		`{
      "key":"key1",
      "operator":"=",
      "values":[
         "hi",
         "hello"
      ]
    }`,
		`{
	  "key":"key2",
	  "operator":"!=",
      "values":[
         "hi",
         "hello"
      ]
	}`,
	}
	validSelectorsBadOperators = []string{
		`{
      "key":"key1",
      "operator":"+",
      "values":[
         "hi",
         "hello"
      ]
   }`,
		`{
      "key":"key2",
      "operator":"-"
   }`,
	}
	invalidSelectors = []string{
		`{
      key":"key1",
      "operator:"=",
      "values":[
         "hi"
         "hello"
      ]
   }`,
		`{
      "key":"key2",
      "operator":"!="
   }`,
	}
)

var _ = Describe("Test flowlog request validation functions", func() {
	Context("Test that the extractLimitParam function behaves as expected", func() {
		It("should return a limit of 1000 when no limit param is included in url", func() {
			req, err := http.NewRequest(http.MethodGet, "", nil)
			Expect(err).NotTo(HaveOccurred())
			limit, err := extractLimitParam(req.URL.Query())
			Expect(limit).To(BeNumerically("==", 1000))
		})

		It("should return a limit of 1000 when a limit param of 0 is included in url", func() {
			req, err := newTestRequestWithParam(http.MethodGet, "limit", "0")
			Expect(err).NotTo(HaveOccurred())
			limit, err := extractLimitParam(req.URL.Query())
			Expect(err).NotTo(HaveOccurred())
			Expect(limit).To(BeNumerically("==", 1000))
		})

		It("should return a limit of 3500 when a limit param of 3500 is included in url", func() {
			req, err := newTestRequestWithParam(http.MethodGet, "limit", "3500")
			Expect(err).NotTo(HaveOccurred())
			limit, err := extractLimitParam(req.URL.Query())
			Expect(err).NotTo(HaveOccurred())
			Expect(limit).To(BeNumerically("==", 3500))
		})

		It("should return an errParseRequest when a limit param of -1 is included in url", func() {
			req, err := newTestRequestWithParam(http.MethodGet, "limit", "-1")
			Expect(err).NotTo(HaveOccurred())
			limit, err := extractLimitParam(req.URL.Query())
			Expect(err).To(BeEquivalentTo(errParseRequest))
			Expect(limit).To(BeZero())
		})

		It("should return an errParseRequest when a limit param of max int32 + 1 is included in url", func() {
			req, err := newTestRequestWithParam(http.MethodGet, "limit", "2147483648")
			Expect(err).NotTo(HaveOccurred())
			limit, err := extractLimitParam(req.URL.Query())
			Expect(err).To(BeEquivalentTo(errParseRequest))
			Expect(limit).To(BeZero())
		})

		It("should return an errParseRequest when a limit param of min int32 - 1 is included in url", func() {
			req, err := newTestRequestWithParam(http.MethodGet, "limit", "-2147483648")
			Expect(err).NotTo(HaveOccurred())
			limit, err := extractLimitParam(req.URL.Query())
			Expect(err).To(BeEquivalentTo(errParseRequest))
			Expect(limit).To(BeZero())
		})
	})

	Context("Test that the lowerCaseParams function behaves as expected", func() {
		It("should return an array of lower cased strings", func() {
			params := []string{"aLLow", "DENY", "UNKNown"}
			lowerCasedParams := lowerCaseParams(params)
			Expect(lowerCasedParams[0]).To(BeEquivalentTo("allow"))
			Expect(lowerCasedParams[1]).To(BeEquivalentTo("deny"))
			Expect(lowerCasedParams[2]).To(BeEquivalentTo("unknown"))
		})
	})

	Context("Test that the validateActions function behaves as expected", func() {
		It("should return true, indicating that actions are valid", func() {
			actions := []string{"allow", "deny", "unknown"}
			valid := validateActions(actions)
			Expect(valid).To(BeTrue())
		})

		It("should return true when passed an empty slice", func() {
			actions := []string{}
			valid := validateActions(actions)
			Expect(valid).To(BeTrue())
		})

		It("should return false when passed a slice with one incorrect action", func() {
			actions := []string{"allow", "deny", "unknownnn"}
			valid := validateActions(actions)
			Expect(valid).To(BeFalse())
		})
	})

	Context("Test that the getLabelSelectors and validateLabelSelector functionality behaves as expected", func() {
		It("should return an array of LabelSelectors when passed a valid json and pass the validation", func() {
			labelSelectors, err := getLabelSelectors(validSelectors)
			Expect(err).NotTo(HaveOccurred())
			Expect(labelSelectors[0].Key).To(BeEquivalentTo("key1"))
			Expect(labelSelectors[1].Key).To(BeEquivalentTo("key2"))
			Expect(labelSelectors[0].Operator).To(BeEquivalentTo("="))
			Expect(labelSelectors[1].Operator).To(BeEquivalentTo("!="))
			Expect(labelSelectors[0].Values[0]).To(BeEquivalentTo("hi"))
			Expect(labelSelectors[0].Values[1]).To(BeEquivalentTo("hello"))
			Expect(labelSelectors[1].Values[0]).To(BeEquivalentTo("hi"))
			Expect(labelSelectors[1].Values[1]).To(BeEquivalentTo("hello"))

			valid := validateLabelSelector(labelSelectors)
			Expect(valid).To(BeTrue())
		})

		It("should return an array of LabelSelectors when passed a valid json but fail validation due to a bad operator", func() {
			labelSelectors, err := getLabelSelectors(validSelectorsBadOperators)
			Expect(err).NotTo(HaveOccurred())
			Expect(labelSelectors[0].Key).To(BeEquivalentTo("key1"))
			Expect(labelSelectors[1].Key).To(BeEquivalentTo("key2"))
			Expect(labelSelectors[0].Operator).To(BeEquivalentTo("+"))
			Expect(labelSelectors[1].Operator).To(BeEquivalentTo("-"))
			Expect(labelSelectors[0].Values[0]).To(BeEquivalentTo("hi"))
			Expect(labelSelectors[0].Values[1]).To(BeEquivalentTo("hello"))
			Expect(labelSelectors[1].Values).To(BeNil())

			valid := validateLabelSelector(labelSelectors)
			Expect(valid).To(BeFalse())
		})

		It("should fail to return LabelSelectors due to bad json", func() {
			labelSelectors, err := getLabelSelectors(invalidSelectors)
			Expect(err).To(HaveOccurred())
			Expect(labelSelectors).To(BeNil())
		})
	})

	Context("Test that the validateFlowTypes function behaves as expected", func() {
		It("should return true, indicating that types are valid", func() {
			types := []string{"network", "networkset", "wep", "hep"}
			valid := validateFlowTypes(types)
			Expect(valid).To(BeTrue())
		})

		It("should return true when passed an empty slice", func() {
			types := []string{}
			valid := validateFlowTypes(types)
			Expect(valid).To(BeTrue())
		})

		It("should return false when passed a slice with incorrect types", func() {
			types := []string{"network", "networkSets", "weps", "heppp"}
			valid := validateFlowTypes(types)
			Expect(valid).To(BeFalse())
		})
	})

	Context("Test that the convertFlowTypes function behaves as expected", func() {
		It("should return a slice of converted flow types", func() {
			flowTypes := []string{"network", "networkset", "wep", "hep"}
			convertedTypes := convertFlowTypes(flowTypes)
			Expect(convertedTypes[0]).To(BeEquivalentTo("net"))
			Expect(convertedTypes[1]).To(BeEquivalentTo("ns"))
			Expect(convertedTypes[2]).To(BeEquivalentTo("wep"))
			Expect(convertedTypes[3]).To(BeEquivalentTo("hep"))
		})

		It("should return a slice of unconverted flow types because they didn't match the accepted inputs", func() {
			flowTypes := []string{"networks", "networksets", "wep", "hep"}
			convertedTypes := convertFlowTypes(flowTypes)
			Expect(convertedTypes[0]).To(BeEquivalentTo("networks"))
			Expect(convertedTypes[1]).To(BeEquivalentTo("networksets"))
			Expect(convertedTypes[2]).To(BeEquivalentTo("wep"))
			Expect(convertedTypes[3]).To(BeEquivalentTo("hep"))
		})
	})

	Context("Test that the validatePolicyPreview function behaves as expected", func() {
		It("should return true when passed a PolicyPreview with the verb create", func() {
			policyPreview := PolicyPreview{Verb: "create"}
			valid := validatePolicyPreview(policyPreview)
			Expect(valid).To(BeTrue())
		})

		It("should return true when passed a PolicyPreview with the verb update", func() {
			policyPreview := PolicyPreview{Verb: "update"}
			valid := validatePolicyPreview(policyPreview)
			Expect(valid).To(BeTrue())
		})

		It("should return true when passed a PolicyPreview with the verb delete", func() {
			policyPreview := PolicyPreview{Verb: "delete"}
			valid := validatePolicyPreview(policyPreview)
			Expect(valid).To(BeTrue())
		})

		It("should return false when passed a PolicyPreview with the verb read", func() {
			policyPreview := PolicyPreview{Verb: "read"}
			valid := validatePolicyPreview(policyPreview)
			Expect(valid).To(BeFalse())
		})
	})

	Context("Test that the getPolicyPreview function behaves as expected", func() {
		It("should return a PolicyPreview object when passed a valid preview string", func() {
			validPreview, err := ioutil.ReadFile("testdata/flow_logs_valid_preview.json")
			Expect(err).To(Not(HaveOccurred()))
			policyPreview, err := getPolicyPreview(string(validPreview))
			Expect(err).To(Not(HaveOccurred()))
			Expect(policyPreview.Verb).To(BeEquivalentTo("delete"))
			Expect(policyPreview.NetworkPolicy.Name).To(BeEquivalentTo("calico-node-alertmanager-mesh"))
			Expect(policyPreview.NetworkPolicy.Namespace).To(BeEquivalentTo("tigera-prometheus"))
		})

		It("should return an error when passed an invalid preview string", func() {
			policyPreview, err := getPolicyPreview(invalidPreview)
			Expect(err).To(HaveOccurred())
			Expect(policyPreview).To(BeNil())
		})
	})

	Context("Test that the validateActionsAndUnprotected function behaves as expected", func() {
		It("should return an error when passed a invalid combination of actions and uprotected", func() {
			actions := []string{"allow", "deny", "unknown"}
			unprotected := true
			valid := validateActionsAndUnprotected(actions, unprotected)
			Expect(valid).To(BeFalse())
		})

		It("should not return an error when passed a valid combination of actions and uprotected (no deny)", func() {
			actions := []string{"allow", "unknown"}
			unprotected := true
			valid := validateActionsAndUnprotected(actions, unprotected)
			Expect(valid).To(BeTrue())
		})

		It("should not return an error when passed a valid combination of actions and uprotected (unprotected false)", func() {
			actions := []string{"allow", "deny", "unknown"}
			unprotected := false
			valid := validateActionsAndUnprotected(actions, unprotected)
			Expect(valid).To(BeTrue())
		})

		It("should not return an error when passed a valid combination of actions and uprotected (empty actions)", func() {
			actions := []string{}
			unprotected := true
			valid := validateActionsAndUnprotected(actions, unprotected)
			Expect(valid).To(BeTrue())
		})
	})

})

func newTestRequestWithParam(method string, key string, value string) (*http.Request, error) {
	req, err := http.NewRequest(method, "", nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	q.Add(key, value)
	req.URL.RawQuery = q.Encode()
	return req, nil
}
