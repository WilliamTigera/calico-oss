// Copyright (c) 2024 Tigera, Inc. All rights reserved.
package parser

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestWAFMiddleware(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "WAF Middleware test suite.")
}
