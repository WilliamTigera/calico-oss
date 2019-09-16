package benchmark_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	. "github.com/tigera/compliance/pkg/benchmark"
	"github.com/tigera/compliance/pkg/benchmark/mock"
	"github.com/tigera/compliance/pkg/config"
	api "github.com/tigera/lma/pkg/api"
)

var _ = Describe("Benchmark", func() {
	var (
		cfg       *config.Config
		mockStore *mock.DB
		mockExec  *mock.Executor
		healthy   func(bool)
		isHealthy bool
	)

	BeforeEach(func() {
		cfg = &config.Config{}
		mockStore = mock.NewMockDB()
		mockExec = new(mock.Executor)
		healthy = func(h bool) { isHealthy = h }
	})

	It("should execute a benchmark", func() {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-time.After(time.Second)
			cancel()
		}()
		Run(ctx, cfg, mockExec, mockStore, mockStore, healthy)
		Expect(mockStore.NStoreCalls).To(Equal(1))
		Expect(mockStore.NRetrieveCalls).To(Equal(1))
		Expect(isHealthy).To(BeTrue())
	})

	It("should properly compute Benchmarks equality", func() {
		By("empty benchmarks")
		Expect((api.Benchmarks{}).Equal(api.Benchmarks{})).To(BeTrue())

		By("error field")
		Expect((api.Benchmarks{Error: "an error"}).Equal(api.Benchmarks{Error: "an error"})).To(BeTrue())
		Expect((api.Benchmarks{Error: "an error"}).Equal(api.Benchmarks{Error: "diff error"})).To(BeFalse())

		By("metadata fields")
		Expect((api.Benchmarks{Version: "1.1"}).Equal(api.Benchmarks{Version: "1.1"})).To(BeTrue())
		Expect((api.Benchmarks{Version: "1.1"}).Equal(api.Benchmarks{Version: "1.1.1"})).To(BeFalse())
		Expect((api.Benchmarks{Type: api.TypeKubernetes}).Equal(api.Benchmarks{Type: api.TypeKubernetes})).To(BeTrue())
		Expect((api.Benchmarks{Type: api.TypeKubernetes}).Equal(api.Benchmarks{Type: "docker"})).To(BeFalse())
		Expect((api.Benchmarks{NodeName: "kadm-ms"}).Equal(api.Benchmarks{NodeName: "kadm-ms"})).To(BeTrue())
		Expect((api.Benchmarks{NodeName: "kadm-ms"}).Equal(api.Benchmarks{NodeName: "kadm-node-0"})).To(BeFalse())

		By("tests")
		Expect((api.Benchmarks{Tests: []api.BenchmarkTest{{"section", "sectionDesc", "testNum", "testDesc", "testInfo", "status", true}}}).Equal(
			api.Benchmarks{Tests: []api.BenchmarkTest{{"section", "sectionDesc", "testNum", "testDesc", "testInfo", "status", true}}},
		)).To(BeTrue())

		Expect((api.Benchmarks{Tests: []api.BenchmarkTest{{"section", "sectionDesc", "testNum", "testDesc", "testInfo", "status", true}}}).Equal(
			api.Benchmarks{Tests: []api.BenchmarkTest{{"section", "sectionDesc", "testNum", "testDesc", "testInfo", "status", false}}},
		)).To(BeFalse())
	})
})
