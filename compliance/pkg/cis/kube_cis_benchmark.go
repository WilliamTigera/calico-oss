package cis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/aquasecurity/kube-bench/check"
	log "github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/projectcalico/calico/compliance/pkg/benchmark"
	"github.com/projectcalico/calico/lma/pkg/api"
)

// Benchmarker implements benchmark.Executor
type Benchmarker struct {
	ConfigChecker func(string) bool
}

// NewBenchmarker returns a benchmark.Executor instance that can execute kubernetes cis benchmark tests
func NewBenchmarker() api.BenchmarksExecutor {
	return &Benchmarker{ConfigChecker: configExists}
}

// ExecuteBenchmarks determines the appropriate benchmarker to run for the given benchmark type.
func (b *Benchmarker) ExecuteBenchmarks(ctx context.Context, ct api.BenchmarkType, nodename string) (*api.Benchmarks, error) {
	if ct == api.TypeKubernetes {
		return b.executeKubeBenchmark(ctx, nodename)
	}
	return nil, fmt.Errorf("No handler found for benchmark type %s", ct)
}

func configExists(cfgPath string) bool {
	_, err := os.Stat(cfgPath)
	return !os.IsNotExist(err)
}

// executeKubeBenchmark executes kube-bench.
func (b *Benchmarker) executeKubeBenchmark(ctx context.Context, nodename string) (*api.Benchmarks, error) {
	var args []string

	args = append(args, "--json")
	log.WithField("cmd", args).Debug("executing benchmarker")

	// Execute the benchmarker
	ts := time.Now()
	cmd := exec.Command("kube-bench", args...)
	out, err := cmd.CombinedOutput()

	if err != nil {
		log.WithField("output", string(out)).WithError(err).Error("Failed to execute kubernetes benchmarker")
		return nil, err
	}
	res := bytes.Split(out, []byte("\n"))

	var totals *check.OverallControls
	for _, line := range res {
		totals = new(check.OverallControls)
		if err := json.Unmarshal(line, totals); err == nil {
			log.WithField("line", string(line)).Debug("successfully unmarshalled results json")
			break
		}
	}

	if totals == nil || len(totals.Controls) == 0 {
		return nil, fmt.Errorf("no results found on benchmarker execution")
	}

	return &api.Benchmarks{
		Version:           totals.Controls[0].Version,
		KubernetesVersion: totals.Controls[0].Version,
		Type:              api.TypeKubernetes,
		NodeName:          nodename,
		Timestamp:         metav1.Time{Time: ts},
		Tests:             benchmark.TestsFromKubeBenchControls(totals.Controls),
	}, nil
}
