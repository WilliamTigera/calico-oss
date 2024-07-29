// Copyright (c) 2016-2018 Tigera, Inc. All rights reserved.

package flowlog

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
	logrus.SetLevel(logrus.DebugLevel)
}

func TestFlowlogs(t *testing.T) {
	RegisterFailHandler(Fail)
	junitReporter := reporters.NewJUnitReporter("../../report/flowlogs_suite.xml")
	RunSpecsWithDefaultAndCustomReporters(t, "Flow logs Suite", []Reporter{junitReporter})
}
