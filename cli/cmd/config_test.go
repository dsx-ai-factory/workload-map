// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/generator"
)

var _ = Describe("config loading", func() {
	var dir string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		DeferCleanup(os.Setenv, "HOME", os.Getenv("HOME"))
		Expect(os.Setenv("HOME", GinkgoT().TempDir())).To(Succeed())
		for _, key := range []string{"KARTA_CONFIG", "KARTA_OUTPUT"} {
			DeferCleanup(os.Setenv, key, os.Getenv(key))
			Expect(os.Unsetenv(key)).To(Succeed())
		}
	})

	Context("config file resolution", func() {
		It("reads the default path from HOME", func() {
			home := GinkgoT().TempDir()
			Expect(os.Setenv("HOME", home)).To(Succeed())
			Expect(os.Setenv("KARTA_CONFIG", "")).To(Succeed())
			Expect(os.MkdirAll(filepath.Join(home, ".karta"), 0755)).To(Succeed())
			writeFile(filepath.Join(home, ".karta", "config.yaml"), "output: yaml\n")

			c, err := runWithConfig("noop")
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Output).To(Equal("yaml"))
		})

		It("uses KARTA_CONFIG when --config is absent", func() {
			cfg := filepath.Join(dir, "env.yaml")
			writeFile(cfg, "output: wide\n")
			DeferCleanup(os.Setenv, "KARTA_CONFIG", os.Getenv("KARTA_CONFIG"))
			Expect(os.Setenv("KARTA_CONFIG", cfg)).To(Succeed())

			c, err := runWithConfig("noop")
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Output).To(Equal("wide"))
		})

		It("prefers --config over KARTA_CONFIG", func() {
			envCfg := filepath.Join(dir, "env.yaml")
			writeFile(envCfg, "output: wide\n")
			flagCfg := filepath.Join(dir, "flag.yaml")
			writeFile(flagCfg, "output: json\n")
			DeferCleanup(os.Setenv, "KARTA_CONFIG", os.Getenv("KARTA_CONFIG"))
			Expect(os.Setenv("KARTA_CONFIG", envCfg)).To(Succeed())

			c, err := runWithConfig("--config", flagCfg, "noop")
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Output).To(Equal("json"))
		})

		It("ignores a missing default config", func() {
			Expect(os.Setenv("HOME", GinkgoT().TempDir())).To(Succeed())
			Expect(os.Setenv("KARTA_CONFIG", "")).To(Succeed())

			_, err := runWithConfig("noop")
			Expect(err).NotTo(HaveOccurred())
		})

		It("errors on a missing explicit config", func() {
			_, err := runWithConfig("--config", "/does/not/exist", "noop")
			Expect(err).To(HaveOccurred())
		})

		DescribeTable("errors on bad config content",
			func(content string) {
				cfg := filepath.Join(dir, "bad.yaml")
				writeFile(cfg, content)
				_, err := runWithConfig("--config", cfg, "noop")
				Expect(err).To(HaveOccurred())
			},
			Entry("malformed YAML", ":\tinvalid: yaml: [\n"),
			Entry("unknown key", "unknown_key: oops\n"),
			Entry("invalid enum value", "output: invalid\n"),
		)
	})

	Context("output precedence", func() {
		var cfg string

		BeforeEach(func() {
			cfg = filepath.Join(dir, "config.yaml")
		})

		It("defaults to table", func() {
			c, err := runWithConfig("noop")
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Output).To(Equal("table"))
		})

		It("config file overrides the default", func() {
			writeFile(cfg, "output: wide\n")
			c, err := runWithConfig("--config", cfg, "noop")
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Output).To(Equal("wide"))
		})

		It("env var overrides the config file", func() {
			writeFile(cfg, "output: wide\n")
			Expect(os.Setenv("KARTA_OUTPUT", "json")).To(Succeed())
			DeferCleanup(os.Unsetenv, "KARTA_OUTPUT")

			c, err := runWithConfig("--config", cfg, "noop")
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Output).To(Equal("json"))
		})

		It("explicit flag overrides the env var", func() {
			writeFile(cfg, "output: wide\n")
			Expect(os.Setenv("KARTA_OUTPUT", "json")).To(Succeed())
			DeferCleanup(os.Unsetenv, "KARTA_OUTPUT")

			c, err := runWithConfig("--config", cfg, "-o", "yaml", "noop")
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Output).To(Equal("yaml"))
		})

		// get and definitions register their own -o, which shadows the root's.
		// A correct Config is not enough; the flag the command reads must carry
		// the value too.
		It("reaches a command registering its own output flag", func() {
			writeFile(cfg, "output: json\n")

			root := NewRootCommand()
			sub := &cobra.Command{Use: "local", RunE: func(*cobra.Command, []string) error { return nil }}
			local := withOutput(sub, sub.Flags(), true)
			root.AddCommand(sub)
			root.SetArgs([]string{"--config", cfg, "local"})

			Expect(root.Execute()).To(Succeed())
			Expect(local.Get()).To(Equal(generator.OutputJSON))
		})

		It("explicit flag at its default value overrides the config file", func() {
			writeFile(cfg, "output: json\n")
			c, err := runWithConfig("--config", cfg, "-o", "table", "noop")
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Output).To(Equal("table"))
		})
	})

})

func runWithConfig(args ...string) (*Config, error) {
	root := NewRootCommand()
	root.AddCommand(&cobra.Command{
		Use:  "noop",
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	root.SetOut(nil)
	root.SetErr(nil)
	root.SetArgs(args)
	err := root.Execute()
	return config, err
}

func writeFile(path, content string) {
	GinkgoHelper()
	Expect(os.WriteFile(path, []byte(content), 0600)).To(Succeed())
}
