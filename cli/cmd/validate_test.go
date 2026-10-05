// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// pytorchCatalog is the committed definition the single-file specs validate.
// Taking the valid fixture from the catalog rather than writing one inline keeps
// the specs honest: they pass only against a definition the project ships.
const pytorchCatalog = "kubeflow-org-pytorchjob-v1.yaml"

// brokenDefinition carries the two defects the issue's acceptance names: a child
// whose required owner ref is missing, and an unparseable JQ expression. It stays
// inline because the whole point is the defects, which no catalog file has.
const brokenDefinition = `apiVersion: run.ai/v1alpha1
kind: Karta
metadata:
  name: example
spec:
  structureDefinition:
    rootComponent:
      name: pytorchjob
      kind:
        group: kubeflow.org
        version: v1
        kind: PyTorchJob
      statusDefinition:
        statusMappings:
          running:
            - byPhase: Running
    childComponents:
      - name: runner
        kind:
          group: ""
          version: v1
          kind: Pod
        scaleDefinition:
          replicasPath: '.spec.x['
`

var _ = Describe("kli validate", func() {
	Describe("a valid definition", func() {
		It("reports the workload type it maps and succeeds", func() {
			stdout, stderr, err := runValidate("", catalogPath(pytorchCatalog))

			Expect(err).NotTo(HaveOccurred())
			Expect(stdout).To(ContainSubstring("OK: "))
			Expect(stdout).To(ContainSubstring(
				"is a valid Karta definition (GVK: kubeflow.org/v1/PyTorchJob)"))
			Expect(stderr).To(BeEmpty())
		})

		It("accepts every definition in the built-in catalog", func() {
			files, err := filepath.Glob(filepath.Join(catalogDir(), "*.yaml"))
			Expect(err).NotTo(HaveOccurred())
			Expect(files).NotTo(BeEmpty())

			for _, path := range files {
				stdout, _, err := runValidate("", path)
				Expect(err).NotTo(HaveOccurred(), path)
				Expect(stdout).To(HavePrefix("OK: "), path)
			}
		})

		It("reads the definition from stdin", func() {
			stdout, _, err := runValidate(readCatalog(pytorchCatalog), "-")

			Expect(err).NotTo(HaveOccurred())
			Expect(stdout).To(HavePrefix("OK: stdin is a valid Karta definition"))
		})
	})

	Describe("an invalid definition", func() {
		It("lists every finding and fails", func() {
			path := writeDefinition(brokenDefinition)

			stdout, _, err := runValidate("", path)

			Expect(exitStatus(err)).To(Equal(ExitError))
			Expect(err).To(MatchError(ContainSubstring("2 finding(s)")))
			Expect(stdout).To(HavePrefix("INVALID: " + path))
			Expect(stdout).To(ContainSubstring("child component 'runner' has no owner ref"))
			Expect(stdout).To(ContainSubstring("failed to parse JQ expression '.spec.x['"))
		})

		It("puts one finding per line, indented under the header", func() {
			stdout, _, err := runValidate(brokenDefinition, "-")

			Expect(err).To(HaveOccurred())
			lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
			Expect(lines).To(HaveLen(3))
			for _, line := range lines[1:] {
				Expect(line).To(HavePrefix("  "))
				Expect(strings.TrimSpace(line)).NotTo(BeEmpty())
			}
		})
	})

	Describe("input the command cannot read", func() {
		DescribeTable("is a usage failure",
			func(arg string) {
				_, _, err := runValidate("", arg)
				Expect(exitStatus(err)).To(Equal(ExitUsage))
			},
			Entry("a path that does not exist", "./no-such-file.yaml"),
			Entry("a directory", "."),
		)

		It("rejects content that is not a YAML mapping", func() {
			_, _, err := runValidate("just a string\n", "-")

			Expect(exitStatus(err)).To(Equal(ExitUsage))
			Expect(err).To(MatchError(ContainSubstring("parse stdin")))
		})

		DescribeTable("rejects the wrong number of arguments",
			func(args ...string) {
				_, _, err := runValidate("", args...)
				Expect(exitStatus(err)).To(Equal(ExitUsage))
			},
			Entry("none"),
			Entry("two", "a.yaml", "b.yaml"),
		)
	})

	Describe("the inherited output flag", func() {
		It("is refused, since the report has only one format", func() {
			_, err := runValidateFromRoot("validate", catalogPath(pytorchCatalog), "-o", "json")

			Expect(exitStatus(err)).To(Equal(ExitUsage))
			Expect(err).To(MatchError(ContainSubstring("--output is not supported by kli validate")))
		})

		It("still validates when the format came from the environment", func() {
			GinkgoT().Setenv("KARTA_OUTPUT", "json")

			out, err := runValidateFromRoot("validate", catalogPath(pytorchCatalog))

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(HavePrefix("OK: "))
		})
	})

	It("never needs a cluster", func() {
		GinkgoT().Setenv("KUBECONFIG", filepath.Join(GinkgoT().TempDir(), "absent"))

		_, err := runValidateFromRoot("validate", catalogPath(pytorchCatalog))

		Expect(err).NotTo(HaveOccurred())
	})
})

// runValidate builds the command on its own, the way the root does, and captures
// the streams separately so a spec can assert what lands on each.
func runValidate(stdin string, args ...string) (string, string, error) {
	cmd := newValidateCommand()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetFlagErrorFunc(usageError)

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetContext(context.Background())
	// Cobra reads os.Args when handed a nil slice, which a zero-argument variadic
	// call produces, so it would parse the go test and ginkgo flags.
	cmd.SetArgs(append([]string{}, args...))

	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

// runValidateFromRoot goes through the whole tree, which is the only place the
// persistent --output flag validate rejects actually exists.
func runValidateFromRoot(args ...string) (string, error) {
	GinkgoT().Setenv("HOME", GinkgoT().TempDir())
	GinkgoT().Setenv(configEnvVar, "")

	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{}, args...))

	err := cmd.Execute()
	return out.String(), err
}

func writeDefinition(content string) string {
	path := filepath.Join(GinkgoT().TempDir(), "karta.yaml")
	Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
	return path
}

// catalogDir locates the generated definitions relative to this file, since the
// working directory a spec runs in is the package, not the repository root.
func catalogDir() string {
	_, file, _, ok := runtime.Caller(0)
	Expect(ok).To(BeTrue())
	return filepath.Join(filepath.Dir(file), "..", "..", "docs", "catalog")
}

func catalogPath(name string) string { return filepath.Join(catalogDir(), name) }

func readCatalog(name string) string {
	data, err := os.ReadFile(catalogPath(name))
	Expect(err).NotTo(HaveOccurred())
	return string(data)
}
