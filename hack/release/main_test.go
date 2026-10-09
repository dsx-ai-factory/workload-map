// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Release validation", func() {
	Describe("module versions", func() {
		It("accepts synchronized inline requirements", func() {
			root := GinkgoT().TempDir()
			writeModuleFiles(root, "require "+rootModule+" v1.2.3\n")
			Expect(validateModuleVersions(root, "1.2.3")).To(Succeed())
		})

		It("accepts synchronized block requirements", func() {
			root := GinkgoT().TempDir()
			writeModuleFiles(root, "require (\n\t"+rootModule+" v1.2.3\n)\n")
			Expect(validateModuleVersions(root, "1.2.3")).To(Succeed())
		})

		It("validates modules from an explicit repository root", func() {
			root := GinkgoT().TempDir()
			writeModuleFiles(root, "require "+rootModule+" v1.2.3\n")
			Expect(runValidateVersion([]string{"--root", root, "--version", "1.2.3"})).To(Succeed())
		})

		It("rejects a stale workspace replacement", func() {
			root := GinkgoT().TempDir()
			writeModuleFiles(root, "require "+rootModule+" v1.2.3\n")
			writeWorkspace(root, "v1.2.2")
			Expect(validateModuleVersions(root, "1.2.3")).To(MatchError(ContainSubstring("go.work must replace")))
		})

		DescribeTable("rejects publication-unsafe module files",
			func(body, message string) {
				root := GinkgoT().TempDir()
				writeModuleFiles(root, body)
				Expect(validateModuleVersions(root, "1.2.3")).To(MatchError(ContainSubstring(message)))
			},
			Entry("mismatched root version",
				"require "+rootModule+" v1.2.2\n", "v1.2.2, want v1.2.3"),
			Entry("single-line replacement",
				"require "+rootModule+" v1.2.3\nreplace "+rootModule+" => ../\n", "replace directive"),
			Entry("block replacement after the requirement",
				"require "+rootModule+" v1.2.3\nreplace (\n\t"+rootModule+" => ../\n)\n", "replace directive"),
			Entry("block replacement before the requirement",
				"replace (\n\t"+rootModule+" => ../\n)\nrequire "+rootModule+" v1.2.3\n", "replace directive"),
			Entry("exclusion",
				"exclude "+rootModule+" v1.2.2\nrequire "+rootModule+" v1.2.3\n", "exclude directive"),
		)
	})

	It("validates synchronized tags from an explicit repository root", func() {
		root := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(root, "tracked"), []byte("contents"), 0o644)).To(Succeed())
		commands := [][]string{
			{"init"},
			{"add", "tracked"},
			{"-c", "user.name=Karta Test", "-c", "user.email=karta@example.com", "-c", "commit.gpgsign=false", "commit", "-m", "test"},
			{"-c", "tag.gpgSign=false", "tag", "v1.2.3"},
			{"-c", "tag.gpgSign=false", "tag", "cli/v1.2.3"},
		}
		for _, args := range commands {
			_, err := commandOutput("git", append([]string{"-C", root}, args...)...)
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(validateTags(root, "1.2.3")).To(Succeed())
	})

	It("rejects a version that has already been released", func() {
		root := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(root, "tracked"), []byte("contents"), 0o644)).To(Succeed())
		commands := [][]string{
			{"init"},
			{"add", "tracked"},
			{"-c", "user.name=Karta Test", "-c", "user.email=karta@example.com", "-c", "commit.gpgsign=false", "commit", "-m", "released"},
			{"-c", "tag.gpgSign=false", "tag", "v1.2.3"},
			{"-c", "tag.gpgSign=false", "tag", "v1.2.4-rc.1"},
		}
		for _, args := range commands {
			_, err := commandOutput("git", append([]string{"-C", root}, args...)...)
			Expect(err).NotTo(HaveOccurred())
		}

		// A second commit so the released tag is behind HEAD, as it is on a
		// branch that has not been tagged yet.
		Expect(os.WriteFile(filepath.Join(root, "tracked"), []byte("more"), 0o644)).To(Succeed())
		for _, args := range [][]string{
			{"add", "tracked"},
			{"-c", "user.name=Karta Test", "-c", "user.email=karta@example.com", "-c", "commit.gpgsign=false", "commit", "-m", "next"},
		} {
			_, err := commandOutput("git", append([]string{"-C", root}, args...)...)
			Expect(err).NotTo(HaveOccurred())
		}

		Expect(validateVersionIsUnreleased(root, "1.2.4")).To(Succeed())
		Expect(validateVersionIsUnreleased(root, "1.2.3")).To(
			MatchError(ContainSubstring("not newer than the released v1.2.3")))
		Expect(validateVersionIsUnreleased(root, "1.2.2")).To(
			MatchError(ContainSubstring("not newer than the released v1.2.3")))

		// The tag being cut sits at HEAD during the release itself, so it must
		// not count as already released.
		_, err := commandOutput("git", "-C", root, "-c", "tag.gpgSign=false", "tag", "v1.2.4")
		Expect(err).NotTo(HaveOccurred())
		Expect(validateVersionIsUnreleased(root, "1.2.4")).To(Succeed())
	})

	It("scopes the released comparison to this line and older", func() {
		root := GinkgoT().TempDir()
		git := func(args ...string) {
			GinkgoHelper()
			_, err := commandOutput("git", append([]string{"-C", root}, args...)...)
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(os.WriteFile(filepath.Join(root, "tracked"), []byte("one"), 0o644)).To(Succeed())
		git("init", "-b", "main")
		git("add", "-A")
		git("-c", "user.name=Karta Test", "-c", "user.email=karta@example.com",
			"-c", "commit.gpgsign=false", "commit", "-m", "history")
		for _, tag := range []string{"v0.2.9", "v0.3.0"} {
			git("-c", "tag.gpgSign=false", "tag", tag)
		}
		// A second commit, so neither tag sits on HEAD and is mistaken for the
		// release being cut.
		Expect(os.WriteFile(filepath.Join(root, "tracked"), []byte("two"), 0o644)).To(Succeed())
		git("add", "-A")
		git("-c", "user.name=Karta Test", "-c", "user.email=karta@example.com",
			"-c", "commit.gpgsign=false", "commit", "-m", "next")

		// A maintenance release of the older line clears the newer one.
		Expect(validateVersionIsUnreleased(root, "0.2.10")).To(Succeed())

		// Its own line still constrains it.
		Expect(validateVersionIsUnreleased(root, "0.2.9")).To(
			MatchError(ContainSubstring("not newer than the released v0.2.9")))

		// A pin left behind while the line shipped on is still caught, which
		// reachability would miss: release tags are cut from the release
		// branch, not from main.
		Expect(validateVersionIsUnreleased(root, "0.2.1")).To(
			MatchError(ContainSubstring("not newer than the released v0.2.9")))

		// The newer line constrains itself as usual.
		Expect(validateVersionIsUnreleased(root, "0.3.1")).To(Succeed())
		Expect(validateVersionIsUnreleased(root, "0.3.0")).To(
			MatchError(ContainSubstring("not newer than the released v0.3.0")))
	})

	It("falls back to the cli pin when no version is given", func() {
		root := GinkgoT().TempDir()
		writeModuleFiles(root, "require "+rootModule+" v1.2.3\n")
		Expect(pinnedVersion(root)).To(Equal("1.2.3"))
		Expect(runValidateVersion([]string{"--root", root})).To(Succeed())

		// A module that drifts from the pin is what presubmit must catch.
		operator := filepath.Join(root, "operator", "go.mod")
		contents, err := os.ReadFile(operator)
		Expect(err).NotTo(HaveOccurred())
		stale := strings.Replace(string(contents), "v1.2.3", "v1.2.2", 1)
		Expect(os.WriteFile(operator, []byte(stale), 0o644)).To(Succeed())
		Expect(runValidateVersion([]string{"--root", root})).To(
			MatchError(ContainSubstring("v1.2.2, want v1.2.3")))
	})

	It("resolves GoReleaser artifact paths from the project root", func() {
		root := GinkgoT().TempDir()
		dist := filepath.Join(root, "dist")
		Expect(os.MkdirAll(dist, 0o755)).To(Succeed())
		contents := `[{"name":"karta","path":"dist/karta_linux_amd64/karta","type":"Binary"}]`
		Expect(os.WriteFile(filepath.Join(dist, "artifacts.json"), []byte(contents), 0o644)).To(Succeed())

		artifacts, err := readArtifacts(dist)
		Expect(err).NotTo(HaveOccurred())
		Expect(artifacts).To(HaveLen(1))
		Expect(artifacts[0].Path).To(Equal(filepath.Join(dist, "karta_linux_amd64", "karta")))
	})

	It("verifies host executable versions", func() {
		path := filepath.Join(GinkgoT().TempDir(), "version-command")
		Expect(os.WriteFile(path, []byte("#!/bin/sh\nprintf '1.2.3\\n'\n"), 0o755)).To(Succeed())
		artifacts := []artifact{
			{Path: path, Goos: runtime.GOOS, Goarch: runtime.GOARCH, Type: "Binary", Extra: map[string]any{"ID": "karta"}},
		}
		verified, skipped, err := verifyHostVersions(artifacts, "1.2.3")
		Expect(err).NotTo(HaveOccurred())
		Expect(verified).To(Equal([]string{"karta"}))
		Expect(skipped).To(BeEmpty())

		_, _, err = verifyHostVersions(artifacts, "1.2.4")
		Expect(err).To(MatchError(ContainSubstring("want \"1.2.4\"")))

	})
})

func writeModuleFiles(root, body string) {
	GinkgoHelper()
	for _, module := range []string{"cli", "operator"} {
		Expect(os.MkdirAll(filepath.Join(root, module), 0o755)).To(Succeed())
		contents := "module " + rootModule + "/" + module + "\n\ngo 1.26.3\n\n" + body
		Expect(os.WriteFile(filepath.Join(root, module, "go.mod"), []byte(contents), 0o644)).To(Succeed())
	}
	writeWorkspace(root, "v1.2.3")
}

func writeWorkspace(root, version string) {
	GinkgoHelper()
	contents := "go 1.26.3\n\nreplace " + rootModule + " " + version + " => .\n"
	Expect(os.WriteFile(filepath.Join(root, "go.work"), []byte(contents), 0o644)).To(Succeed())
}
