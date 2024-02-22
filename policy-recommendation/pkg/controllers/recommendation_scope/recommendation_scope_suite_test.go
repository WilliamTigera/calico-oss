// Copyright (c) 2024 Tigera, Inc. All rights reserved.
package recommendation_scope_controller_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/projectcalico/calico/libcalico-go/lib/testutils"
)

func TestPolicyRecommendationController(t *testing.T) {
	testutils.HookLogrusForGinkgo()
	RegisterFailHandler(Fail)
	RunSpecs(t, "PolicyRecommendationScope Controllers Suite")
}
