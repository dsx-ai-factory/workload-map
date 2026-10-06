// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const libraryModule = "github.com/dsx-ai-factory/workload-map/karta"

var _ = Describe("Release validation", func() {
	Describe("publishable module", func() {
		It("accepts a go.mod without replace or exclude directives", func() {
			root := GinkgoT().TempDir()
			writeLibraryModule(root, "require k8s.io/api v0.37.1\n")
			Expect(checkPublishable(filepath.Join(root, "karta", "go.mod"))).To(Succeed())
		})

		DescribeTable("rejects publication-unsafe module files",
			func(body, message string) {
				root := GinkgoT().TempDir()
				writeLibraryModule(root, body)
				Expect(checkPublishable(filepath.Join(root, "karta", "go.mod"))).To(
					MatchError(ContainSubstring(message)))
			},
			Entry("single-line replacement",
				"require k8s.io/api v0.37.1\nreplace k8s.io/api => ../api\n", "replace directive"),
			Entry("block replacement",
				"require k8s.io/api v0.37.1\nreplace (\n\tk8s.io/api => ../api\n)\n", "replace directive"),
			Entry("exclusion",
				"exclude k8s.io/api v0.37.0\nrequire k8s.io/api v0.37.1\n", "exclude directive"),
		)

		It("requires the module file to check", func() {
			Expect(runCheckPublishable(nil)).To(MatchError(ContainSubstring("--modfile is required")))
		})
	})

	Describe("release version", func() {
		It("validates the library module from an explicit repository root", func() {
			root := GinkgoT().TempDir()
			writeLibraryModule(root, "")
			Expect(runValidateRelease([]string{"--root", root, "--version", "1.2.3"})).To(Succeed())
		})

		It("rejects a library module that carries a replace", func() {
			root := GinkgoT().TempDir()
			writeLibraryModule(root, "replace k8s.io/api => ../api\n")
			Expect(runValidateRelease([]string{"--root", root, "--version", "1.2.3"})).To(
				MatchError(ContainSubstring("replace directive")))
		})

		DescribeTable("rejects a version that is not X.Y.Z",
			func(version string) {
				root := GinkgoT().TempDir()
				writeLibraryModule(root, "")
				Expect(runValidateRelease([]string{"--root", root, "--version", version})).To(
					MatchError(ContainSubstring("must match X.Y.Z")))
			},
			Entry("empty", ""),
			Entry("leading v", "v1.2.3"),
			Entry("prerelease", "1.2.3-rc.1"),
			Entry("missing patch", "1.2"),
		)
	})

	Describe("release tags", func() {
		var root string

		git := func(args ...string) {
			GinkgoHelper()
			_, err := commandOutput("git", append([]string{"-C", root}, args...)...)
			Expect(err).NotTo(HaveOccurred())
		}
		commit := func(contents string) {
			GinkgoHelper()
			Expect(os.WriteFile(filepath.Join(root, "tracked"), []byte(contents), 0o644)).To(Succeed())
			git("add", "tracked")
			git("-c", "user.name=Karta Test", "-c", "user.email=karta@example.com",
				"-c", "commit.gpgsign=false", "commit", "-m", contents)
		}
		tag := func(name string) {
			GinkgoHelper()
			git("-c", "tag.gpgSign=false", "tag", name)
		}

		BeforeEach(func() {
			root = GinkgoT().TempDir()
			git("init")
			commit("first")
		})

		It("accepts the release and library tags on HEAD", func() {
			tag("v1.2.3")
			tag("karta/v1.2.3")
			Expect(validateTags(root, "1.2.3")).To(Succeed())
		})

		It("rejects a release without the library tag", func() {
			tag("v1.2.3")
			Expect(validateTags(root, "1.2.3")).To(MatchError(ContainSubstring("resolve tag karta/v1.2.3")))
		})

		It("rejects a library tag on another commit", func() {
			tag("karta/v1.2.3")
			commit("second")
			tag("v1.2.3")
			Expect(validateTags(root, "1.2.3")).To(MatchError(ContainSubstring("tag karta/v1.2.3 points to")))
		})
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

func writeLibraryModule(root, body string) {
	GinkgoHelper()
	Expect(os.MkdirAll(filepath.Join(root, "karta"), 0o755)).To(Succeed())
	contents := "module " + libraryModule + "\n\ngo 1.26.3\n\n" + body
	Expect(os.WriteFile(filepath.Join(root, "karta", "go.mod"), []byte(contents), 0o644)).To(Succeed())
}
