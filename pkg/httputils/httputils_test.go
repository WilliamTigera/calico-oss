package httputils_test

import (
	"testing"

	"github.com/projectcalico/libcalico-go/lib/testutils"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestUtils(t *testing.T) {
	testutils.HookLogrusForGinkgo()
	RegisterFailHandler(Fail)
	RunSpecs(t, "Utils Suite")
}
