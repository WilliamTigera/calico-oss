// Copyright (c) 2019 Tigera, Inc. All rights reserved.
package config

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"os"
	"time"
)

var (
	now         = time.Now()
	nowPlusHour = now.Add(time.Hour)
	reportName  = "my-report"
	start       = now.Format(time.RFC3339)
	end         = nowPlusHour.Format(time.RFC3339)
)

var _ = Describe("Load config from environments", func() {
	It("should parse valid configuration", func() {
		By("parsing with valid config")
		os.Setenv(ReportNameEnv, reportName)
		os.Setenv(ReportStartEnv, start)
		os.Setenv(ReportEndEnv, end)

		By("validating the environments parsed correct")
		cfg, err := LoadConfig()
		Expect(cfg).ToNot(BeNil())
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.ReportName).To(Equal(reportName))
		Expect(cfg.ParsedReportStart.Unix()).To(Equal(now.Unix()))
		Expect(cfg.ParsedReportEnd.Unix()).To(Equal(nowPlusHour.Unix()))
	})

	It("should handle relative times", func() {
		By("parsing with valid config")
		os.Setenv(ReportNameEnv, reportName)
		os.Setenv(ReportStartEnv, "now-14m")
		os.Setenv(ReportEndEnv, "now")

		By("validating the environments parsed correct")
		cfg, err := LoadConfig()
		Expect(cfg).ToNot(BeNil())
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.ReportName).To(Equal(reportName))

		// We expect the time difference to be 14m, but odd things may occur around daylight savings, so avoid test
		// failures by re-running with an additional hour removed from each time.
		if cfg.ParsedReportEnd.Sub(cfg.ParsedReportStart) != 14*time.Minute {
			By("parsing with valid config")
			os.Setenv(ReportNameEnv, reportName)
			os.Setenv(ReportStartEnv, "now-74m")
			os.Setenv(ReportEndEnv, "now - 60m")

			By("validating the environments parsed correct")
			cfg, err := LoadConfig()
			Expect(cfg).ToNot(BeNil())
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.ReportName).To(Equal(reportName))
			Expect(cfg.ParsedReportEnd.Sub(cfg.ParsedReportStart)).To(Equal(14 * time.Minute))
		}
	})

	It("should error with invalid configuration", func() {
		By("parsing with invalid start time")
		os.Setenv(ReportNameEnv, reportName)
		os.Setenv(ReportStartEnv, "this is not a valid time")
		os.Setenv(ReportEndEnv, end)
		cfg, err := LoadConfig()
		Expect(err).To(HaveOccurred())
		Expect(cfg).To(BeNil())

		By("parsing with invalid end time")
		os.Setenv(ReportNameEnv, reportName)
		os.Setenv(ReportStartEnv, start)
		os.Setenv(ReportEndEnv, "this is not a valid time")
		cfg, err = LoadConfig()
		Expect(cfg).To(BeNil())
		Expect(err).To(HaveOccurred())

		By("parsing with end time before start time")
		os.Setenv(ReportNameEnv, reportName)
		os.Setenv(ReportStartEnv, end)
		os.Setenv(ReportEndEnv, start)
		cfg, err = LoadConfig()
		Expect(cfg).To(BeNil())
		Expect(err).To(HaveOccurred())
	})
})
