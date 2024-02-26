// Copyright (c) 2024 Tigera, Inc. All rights reserved.

package metrics_test

import (
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	licutils "github.com/projectcalico/calico/licensing/utils"

	"github.com/projectcalico/calico/license-agent/pkg/metrics"
)

var _ = Describe("Check License Validity", func() {
	maxLicensedNodes := 50
	It("Validates test license", func() {
		By("Checking timeDuration")
		min, err := time.ParseDuration("1m")
		Expect(err).ShouldNot(HaveOccurred())

		By("Checking Validity")
		lr := metrics.NewLicenseReporter("", "", "", "", min, 9081)
		lic := licutils.ValidEnterpriseTestLicense()
		isValid, _, maxNodes := lr.LicenseHandler(*lic)
		Expect(isValid).To(BeTrue(), "License Valid")
		Expect(maxNodes).Should(Equal(maxLicensedNodes))
	})
})
