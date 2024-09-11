// Copyright (c) 2024 Tigera, Inc. All rights reserved.
package config

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestLinseedOutPluginConfig(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Linseed output plugin config test suite")
}
