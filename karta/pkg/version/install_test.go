// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package version

import (
	"archive/zip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const installTestVersion = "v1.2.3"

var _ = Describe("Versioned Go installation", func() {
	It("uses the installed CLI module version", func() {
		versionSource, err := os.ReadFile("version.go")
		Expect(err).NotTo(HaveOccurred())

		proxy := GinkgoT().TempDir()
		writeProxyModule(proxy, "github.com/dsx-ai-factory/workload-map", installTestVersion, map[string][]byte{
			"go.mod":                 []byte("module github.com/dsx-ai-factory/workload-map\n\ngo 1.26.3\n"),
			"pkg/version/version.go": versionSource,
		})
		writeProxyModule(proxy, "github.com/dsx-ai-factory/workload-map/cli", installTestVersion, map[string][]byte{
			"go.mod":          []byte("module github.com/dsx-ai-factory/workload-map/cli\n\ngo 1.26.3\n\nrequire github.com/dsx-ai-factory/workload-map v1.2.3\n"),
			"cmd/kli/main.go": []byte("package main\n\nimport (\n\t\"fmt\"\n\t\"github.com/dsx-ai-factory/workload-map/pkg/version\"\n)\n\nfunc main() { fmt.Print(version.String()) }\n"),
		})

		binDir := filepath.Join(GinkgoT().TempDir(), "bin")
		moduleCache := GinkgoT().TempDir()
		DeferCleanup(func() {
			_ = filepath.WalkDir(moduleCache, func(path string, entry os.DirEntry, err error) error {
				if err == nil && entry.IsDir() {
					_ = os.Chmod(path, 0o755)
				}
				return nil
			})
		})
		goRootOutput, err := exec.Command("go", "env", "GOROOT").Output()
		Expect(err).NotTo(HaveOccurred())
		goBinary := filepath.Join(strings.TrimSpace(string(goRootOutput)), "bin", "go")
		command := exec.Command(goBinary, "install", "github.com/dsx-ai-factory/workload-map/cli/cmd/kli@"+installTestVersion)
		command.Env = testEnvironment(os.Environ(),
			"GOBIN="+binDir,
			"GOMODCACHE="+moduleCache,
			"GONOPROXY=none",
			"GONOSUMDB=none",
			"GOPROXY=file://"+filepath.ToSlash(proxy),
			"GOPRIVATE=",
			"GOSUMDB=off",
			"GOTOOLCHAIN=local",
			"GOWORK=off",
		)
		output, err := command.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))

		binary := filepath.Join(binDir, "kli")
		if runtime.GOOS == "windows" {
			binary += ".exe"
		}
		output, err = exec.Command(binary).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		Expect(string(output)).To(Equal("1.2.3"))
	})
})

func testEnvironment(base []string, overrides ...string) []string {
	keys := map[string]struct{}{}
	for _, value := range overrides {
		key, _, _ := strings.Cut(value, "=")
		keys[key] = struct{}{}
	}
	result := make([]string, 0, len(base)+len(overrides))
	for _, value := range base {
		key, _, _ := strings.Cut(value, "=")
		if _, overridden := keys[key]; !overridden {
			result = append(result, value)
		}
	}
	return append(result, overrides...)
}

func writeProxyModule(proxy, module, version string, files map[string][]byte) {
	GinkgoHelper()
	directory := filepath.Join(proxy, filepath.FromSlash(module), "@v")
	Expect(os.MkdirAll(directory, 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(directory, "list"), []byte(version+"\n"), 0o644)).To(Succeed())
	info := []byte(fmt.Sprintf("{\"Version\":%q,\"Time\":\"2026-01-01T00:00:00Z\"}\n", version))
	Expect(os.WriteFile(filepath.Join(directory, version+".info"), info, 0o644)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(directory, version+".mod"), files["go.mod"], 0o644)).To(Succeed())

	archive, err := os.Create(filepath.Join(directory, version+".zip"))
	Expect(err).NotTo(HaveOccurred())
	zipWriter := zip.NewWriter(archive)
	for name, contents := range files {
		entry, err := zipWriter.Create(module + "@" + version + "/" + name)
		Expect(err).NotTo(HaveOccurred())
		_, err = entry.Write(contents)
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(zipWriter.Close()).To(Succeed())
	Expect(archive.Close()).To(Succeed())
}
