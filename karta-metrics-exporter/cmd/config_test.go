// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/urfave/cli/v3"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/submitterextractor"
)

const multiLineExpression = `("karta.run.ai/submitted-by" in annotations)
  ? annotations["karta.run.ai/submitted-by"]
  : ""
`

var defaultLimits = AttributionLimits{
	CostLimit:               submitterextractor.DefaultCostLimit,
	MaxExpressionBytes:      submitterextractor.DefaultMaxExpressionBytes,
	MaxIdentityBytes:        submitterextractor.DefaultMaxIdentityBytes,
	InterruptCheckFrequency: submitterextractor.DefaultInterruptCheckFrequency,
}

func writeConfig(content string) string {
	path := filepath.Join(GinkgoT().TempDir(), "config.yaml")
	Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
	return path
}

func loadWithArgs(args ...string) (Config, error) {
	var cfg Config
	cmd := &cli.Command{
		Name:      "test",
		Flags:     configFlags(),
		Writer:    io.Discard,
		ErrWriter: io.Discard,
		Action: func(_ context.Context, cmd *cli.Command) error {
			var err error
			cfg, err = loadConfig(cmd)
			return err
		},
	}
	err := cmd.Run(context.Background(), append([]string{"test"}, args...))
	return cfg, err
}

func setEnv(key, value string) {
	GinkgoHelper()
	DeferCleanup(os.Unsetenv, key)
	Expect(os.Setenv(key, value)).To(Succeed())
}

var _ = Describe("config loading", func() {
	It("uses the defaults without a file or flags", func() {
		cfg, err := loadWithArgs()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Attribution).To(Equal(AttributionConfig{Limits: defaultLimits}))
	})

	It("keeps the defaults for an empty file", func() {
		cfg, err := loadWithArgs("--config", writeConfig(""))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Attribution).To(Equal(AttributionConfig{Limits: defaultLimits}))
	})

	It("reads a multi-line expression and limits from the file", func() {
		path := writeConfig(`attribution:
  submittedByExpression: |
    ("karta.run.ai/submitted-by" in annotations)
      ? annotations["karta.run.ai/submitted-by"]
      : ""
  limits:
    costLimit: 2000000
    maxIdentityBytes: 64
    interruptCheckFrequency: 10
`)

		cfg, err := loadWithArgs("--config", path)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Attribution.SubmittedByExpression).To(Equal(multiLineExpression))
		Expect(cfg.Attribution.Limits).To(Equal(AttributionLimits{
			CostLimit:               2000000,
			MaxExpressionBytes:      submitterextractor.DefaultMaxExpressionBytes,
			MaxIdentityBytes:        64,
			InterruptCheckFrequency: 10,
		}))
	})

	It("lets explicit flags override the file", func() {
		path := writeConfig(`attribution:
  submittedByExpression: '"from-file"'
  limits:
    costLimit: 5
    maxIdentityBytes: 64
`)

		cfg, err := loadWithArgs("--config", path,
			"--"+flagSubmittedByExpression, `"from-flag"`,
			"--"+flagCostLimit, "7",
			"--"+flagInterruptCheckFrequency, "3")
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Attribution.SubmittedByExpression).To(Equal(`"from-flag"`))
		Expect(cfg.Attribution.Limits).To(Equal(AttributionLimits{
			CostLimit:               7,
			MaxExpressionBytes:      submitterextractor.DefaultMaxExpressionBytes,
			MaxIdentityBytes:        64,
			InterruptCheckFrequency: 3,
		}))
	})

	It("does not let unset flag defaults override the file", func() {
		cfg, err := loadWithArgs("--config", writeConfig("attribution:\n  limits:\n    costLimit: 5\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Attribution.Limits.CostLimit).To(BeEquivalentTo(5))
	})

	It("works with flags alone", func() {
		cfg, err := loadWithArgs("--"+flagSubmittedByExpression, `labels["owner"]`, "--"+flagMaxIdentityBytes, "32")
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Attribution.SubmittedByExpression).To(Equal(`labels["owner"]`))
		Expect(cfg.Attribution.Limits.MaxIdentityBytes).To(Equal(32))
	})

	Context("environment variables", func() {
		It("reads the config path and settings from the environment", func() {
			setEnv("KARTA_EXPORTER_CONFIG", writeConfig("attribution:\n  limits:\n    maxIdentityBytes: 64\n"))
			setEnv("KARTA_EXPORTER_SUBMITTED_BY_EXPRESSION", `labels["owner"]`)
			setEnv("KARTA_EXPORTER_SUBMITTER_COST_LIMIT", "7")

			cfg, err := loadWithArgs()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Attribution.SubmittedByExpression).To(Equal(`labels["owner"]`))
			Expect(cfg.Attribution.Limits.CostLimit).To(BeEquivalentTo(7))
			Expect(cfg.Attribution.Limits.MaxIdentityBytes).To(Equal(64))
		})

		It("overrides the file", func() {
			setEnv("KARTA_EXPORTER_SUBMITTER_MAX_IDENTITY_BYTES", "32")

			cfg, err := loadWithArgs("--config", writeConfig("attribution:\n  limits:\n    maxIdentityBytes: 64\n"))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Attribution.Limits.MaxIdentityBytes).To(Equal(32))
		})

		It("is overridden by a flag", func() {
			setEnv("KARTA_EXPORTER_SUBMITTER_COST_LIMIT", "7")

			cfg, err := loadWithArgs("--"+flagCostLimit, "9")
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Attribution.Limits.CostLimit).To(BeEquivalentTo(9))
		})
	})

	DescribeTable("rejects a zero interrupt check frequency",
		func(args func() []string) {
			_, err := loadWithArgs(args()...)
			Expect(err).To(MatchError(errZeroInterruptCheckFrequency))
		},
		Entry("from the file", func() []string {
			return []string{"--config", writeConfig("attribution:\n  limits:\n    interruptCheckFrequency: 0\n")}
		}),
		Entry("from a flag", func() []string { return []string{"--" + flagInterruptCheckFrequency, "0"} }),
		Entry("from the environment", func() []string {
			setEnv("KARTA_EXPORTER_SUBMITTER_INTERRUPT_CHECK_FREQUENCY", "0")
			return nil
		}),
	)

	DescribeTable("rejects invalid input",
		func(args func() []string) {
			_, err := loadWithArgs(args()...)
			Expect(err).To(HaveOccurred())
		},
		Entry("missing file", func() []string {
			return []string{"--config", filepath.Join(GinkgoT().TempDir(), "absent.yaml")}
		}),
		Entry("unknown key", func() []string {
			return []string{"--config", writeConfig("attribution:\n  submittedBy: x\n")}
		}),
		Entry("wrong type", func() []string {
			return []string{"--config", writeConfig("attribution:\n  limits:\n    costLimit: lots\n")}
		}),
		Entry("negative interrupt check frequency in the file", func() []string {
			return []string{"--config", writeConfig("attribution:\n  limits:\n    interruptCheckFrequency: -1\n")}
		}),
		Entry("malformed yaml", func() []string { return []string{"--config", writeConfig("attribution: [\n")} }),
		Entry("multiple documents", func() []string {
			return []string{"--config", writeConfig("attribution: {}\n---\nattribution:\n  submittedByExpression: '\"x\"'\n")}
		}),
		Entry("malformed second document", func() []string {
			return []string{"--config", writeConfig("attribution: {}\n---\nattribution: [\n")}
		}),
		Entry("negative interrupt check frequency flag", func() []string {
			return []string{"--" + flagInterruptCheckFrequency, "-1"}
		}),
	)
})
