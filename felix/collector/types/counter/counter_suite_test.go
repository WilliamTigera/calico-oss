// Copyright (c) 2024 Tigera, Inc. All rights reserved.

package counter

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"

	"github.com/onsi/ginkgo/reporters"
	"github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/libcalico-go/lib/logutils"
	"github.com/projectcalico/calico/libcalico-go/lib/testutils"
)

func init() {
	testutils.HookLogrusForGinkgo()
	logrus.AddHook(&logutils.ContextHook{})
	logrus.SetFormatter(&logutils.Formatter{})
}

func TestCounter(t *testing.T) {
	RegisterFailHandler(Fail)
	junitReporter := reporters.NewJUnitReporter("../../../report/counter_suite.xml")
	RunSpecsWithDefaultAndCustomReporters(t, "Counter Suite", []Reporter{junitReporter})
}
