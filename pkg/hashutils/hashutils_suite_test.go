// Copyright (c) 2019 Tigera, Inc. All rights reserved.

package hashutils_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"

	"github.com/projectcalico/calico/libcalico-go/lib/testutils"
)

func init() {
	testutils.HookLogrusForGinkgo()
}

func TestHashutils(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Hashutils Suite")
}
