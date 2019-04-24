// Copyright (c) 2019 Tigera, Inc. SelectAll rights reserved.
package config

import (
	"testing"

	"github.com/projectcalico/libcalico-go/lib/testutils"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestXrefCache(t *testing.T) {
	testutils.HookLogrusForGinkgo()
	RegisterFailHandler(Fail)
	RunSpecs(t, "Config Suite")
}
