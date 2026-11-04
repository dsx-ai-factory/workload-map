// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const libraryModule = "github.com/dsx-ai-factory/workload-map/karta/sdk"

var _ = Describe("Release validation", func() {
	Describe("publishable module", func() {
		It("accepts a go.mod without replace or exclude directives", func() {
			root := GinkgoT().TempDir()
			writeLibraryModule(root, "require k8s.io/api v0.37.1\n")
			Expect(checkPublishable(filepath.Join(root, "karta", "sdk", "go.mod"))).To(Succeed())
		})

		DescribeTable("rejects publication-unsafe module files",
			func(body, message string) {
				root := GinkgoT().TempDir()
				writeLibraryModule(root, body)
				Expect(checkPublishable(filepath.Join(root, "karta", "sdk", "go.mod"))).To(
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
			tag("karta/sdk/v1.2.3")
			Expect(validateTags(root, "1.2.3")).To(Succeed())
		})

		It("rejects a release without the library tag", func() {
			tag("v1.2.3")
			Expect(validateTags(root, "1.2.3")).To(MatchError(ContainSubstring("resolve tag karta/sdk/v1.2.3")))
		})

		It("rejects a library tag on another commit", func() {
			tag("karta/sdk/v1.2.3")
			commit("second")
			tag("v1.2.3")
			Expect(validateTags(root, "1.2.3")).To(MatchError(ContainSubstring("tag karta/sdk/v1.2.3 points to")))
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

	Describe("Homebrew cask", func() {
		sums := map[string]string{
			"karta_1.2.3_darwin_arm64.tar.gz": strings.Repeat("a", 64),
			"karta_1.2.3_darwin_amd64.tar.gz": strings.Repeat("b", 64),
			"karta_1.2.3_linux_arm64.tar.gz":  strings.Repeat("c", 64),
			"karta_1.2.3_linux_amd64.tar.gz":  strings.Repeat("d", 64),
		}
		postflight := "  postflight_steps do\n    on_macos do\n      " + quarantineStep + "\n    end\n  end\n"

		It("accepts the cask GoReleaser generates", func() {
			Expect(verifyCask(generatedCask, "1.2.3", sums)).To(Succeed())
		})

		DescribeTable("rejects a cask that does not match the release",
			func(old, replacement, message string) {
				cask := strings.Replace(generatedCask, old, replacement, 1)
				Expect(cask).NotTo(Equal(generatedCask))
				Expect(verifyCask(cask, "1.2.3", sums)).To(MatchError(ContainSubstring(message)))
			},
			Entry("another version",
				`version "1.2.3"`, `version "1.2.2"`, `cask version is "1.2.2"`),
			Entry("a checksum from another build",
				strings.Repeat("a", 64), strings.Repeat("e", 64), "cask sha256 for darwin_arm64"),
			Entry("no Linux packages",
				generatedCask[strings.Index(generatedCask, "  on_linux do"):strings.Index(generatedCask, "  name ")], "",
				"cask has 2 packages, want 4"),
			Entry("the archive of another platform",
				"karta_#{version}_darwin_arm64", "karta_#{version}_linux_arm64", "cask url for darwin_arm64"),
			Entry("a download outside GitHub releases",
				"https://github.com/dsx-ai-factory/workload-map/releases/download/v#{version}/karta_#{version}_linux_amd64",
				"https://example.com/karta_#{version}_linux_amd64", "cask url for linux_amd64"),
			Entry("downloads from two repositories",
				"https://github.com/dsx-ai-factory/workload-map/releases/download/v#{version}/karta_#{version}_linux_amd64",
				"https://github.com/example/workload-map/releases/download/v#{version}/karta_#{version}_linux_amd64",
				"cask downloads from both"),
			Entry("a homepage on another repository",
				`homepage "https://github.com/dsx-ai-factory/workload-map"`, `homepage "https://github.com/example/karta"`,
				"cask homepage"),
			Entry("no binary", "  binary \"kli\"\n", "", "does not install the kli binary"),
			Entry("another binary", `binary "kli"`, `binary "karta"`, `cask installs binary "karta" in cask,`),
			Entry("an extra binary",
				`binary "kli"`, "binary \"kli\"\n  binary \"absent-binary\"", `cask installs binary "absent-binary" in cask,`),
			Entry("kli twice", `binary "kli"`, "binary \"kli\"\n  binary \"kli\"", "installs the kli binary twice"),
			Entry("kli inside on_linux",
				"  on_linux do\n", "  on_linux do\n    binary \"kli\"\n", `cask installs binary "kli" in cask > on_linux,`),
			Entry("no quarantine step", quarantineStep, "", "does not remove the macOS quarantine"),
			Entry("a quarantine step outside on_macos",
				"    on_macos do\n      "+quarantineStep+"\n    end\n", "    "+quarantineStep+"\n",
				"runs the quarantine step in cask > postflight_steps,"),
			Entry("a quarantine step wrapped in on_linux",
				postflight, "  on_linux do\n"+postflight+"  end\n",
				"runs the quarantine step in cask > on_linux > postflight_steps > on_macos,"),
			Entry("a quarantine step under contradictory OS blocks",
				quarantineStep, "on_linux do\n"+quarantineStep+"\nend",
				"runs the quarantine step in cask > postflight_steps > on_macos > on_linux,"),
			Entry("a package outside an architecture block",
				"    on_intel do\n      sha256 \""+strings.Repeat("d", 64)+"\"", "    sha256 \""+strings.Repeat("d", 64)+"\"\n    on_intel do",
				"cask sets sha256 outside an operating system and architecture block"),
			Entry("a checksum set twice",
				"      sha256 \""+strings.Repeat("a", 64)+"\"", "      sha256 \""+strings.Repeat("a", 64)+"\"\n      sha256 \"0\"",
				"cask sets sha256 twice for darwin_arm64"),
			Entry("an unclosed block", "  # No zap stanza required\nend\n", "", "cask has unclosed blocks: cask"),
		)

		It("verifies the archives and the cask GoReleaser left in dist", func() {
			root := GinkgoT().TempDir()
			dist := filepath.Join(root, "dist")
			Expect(os.MkdirAll(filepath.Join(dist, "homebrew", "Casks"), 0o755)).To(Succeed())
			cask := generatedCask
			var artifacts []artifact
			for name, fake := range sums {
				path := filepath.Join(dist, name)
				Expect(os.WriteFile(path, []byte(name), 0o644)).To(Succeed())
				sum := sha256.Sum256([]byte(name))
				cask = strings.Replace(cask, fake, hex.EncodeToString(sum[:]), 1)
				artifacts = append(artifacts, artifact{Name: name, Path: path, Type: "Archive", Extra: map[string]any{"ID": "karta"}})
			}
			caskPath := filepath.Join(dist, "homebrew", "Casks", "kli.rb")
			Expect(os.WriteFile(caskPath, []byte(cask), 0o644)).To(Succeed())
			writeArtifacts := func(items []artifact) {
				GinkgoHelper()
				contents, err := json.Marshal(items)
				Expect(err).NotTo(HaveOccurred())
				Expect(os.WriteFile(filepath.Join(dist, "artifacts.json"), contents, 0o644)).To(Succeed())
			}

			writeArtifacts(artifacts)
			Expect(runVerifyArtifacts([]string{"--dist", dist, "--version", "1.2.3"})).To(
				MatchError("found 0 Homebrew casks, want 1"))

			writeArtifacts(append(artifacts, artifact{Name: "kli.rb", Path: caskPath, Type: "Homebrew Cask"}))
			Expect(runVerifyArtifacts([]string{"--dist", dist, "--version", "1.2.3"})).To(Succeed())

			Expect(os.WriteFile(filepath.Join(dist, "karta_1.2.3_linux_amd64.tar.gz"), []byte("rebuilt"), 0o644)).To(Succeed())
			Expect(runVerifyArtifacts([]string{"--dist", dist, "--version", "1.2.3"})).To(
				MatchError(ContainSubstring("cask sha256 for linux_amd64")))
		})
	})
})

// generatedCask is GoReleaser v2.18.1 output for .goreleaser.yaml at version
// 1.2.3, with placeholder checksums.
const generatedCask = `# This file was generated by GoReleaser. DO NOT EDIT.
cask "kli" do
  postflight_steps do
    on_macos do
      run "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "{{staged_path}}/kli"]
    end
  end

  version "1.2.3"

  on_macos do
    on_arm do
      sha256 "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
      url "https://github.com/dsx-ai-factory/workload-map/releases/download/v#{version}/karta_#{version}_darwin_arm64.tar.gz"
    end
    on_intel do
      sha256 "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
      url "https://github.com/dsx-ai-factory/workload-map/releases/download/v#{version}/karta_#{version}_darwin_amd64.tar.gz"
    end
  end
  on_linux do
    on_arm do
      sha256 "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
      url "https://github.com/dsx-ai-factory/workload-map/releases/download/v#{version}/karta_#{version}_linux_arm64.tar.gz"
    end
    on_intel do
      sha256 "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
      url "https://github.com/dsx-ai-factory/workload-map/releases/download/v#{version}/karta_#{version}_linux_amd64.tar.gz"
    end
  end

  name "kli"
  desc "Workload-aware visibility for any Kubernetes workload type"
  homepage "https://github.com/dsx-ai-factory/workload-map"

  livecheck do
    skip "Auto-generated on release."
  end

  binary "kli"

  # No zap stanza required
end
`

func writeLibraryModule(root, body string) {
	GinkgoHelper()
	Expect(os.MkdirAll(filepath.Join(root, "karta", "sdk"), 0o755)).To(Succeed())
	contents := "module " + libraryModule + "\n\ngo 1.26.3\n\n" + body
	Expect(os.WriteFile(filepath.Join(root, "karta", "sdk", "go.mod"), []byte(contents), 0o644)).To(Succeed())
}
