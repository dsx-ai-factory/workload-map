// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package grafana_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Shared demo installers", func() {
	const exporter = "hack/e2e/dsx-workload-exporter/install.sh"
	const fakeGPU = "hack/e2e/infra/fake-gpu-operator/install.sh"

	DescribeTable("installs the exporter and restarts existing releases before waiting", func(installed bool) {
		events, err := runDemoInstaller(exporter, []string{
			"demo", "demo-ns", "values with spaces.yaml", "--skip-crds",
			"--set-string", "exporter.image.tag=unique-tag", "--timeout", "5m",
		}, "DEMO_TEST_INSTALLED="+strconv.FormatBool(installed))
		Expect(err).NotTo(HaveOccurred())
		Expect(events[0]).To(Equal("helm <status> <demo> <-n> <demo-ns>"))
		Expect(events[1]).To(ContainSubstring("<upgrade> <--install> <demo>"))
		Expect(events[1]).To(ContainSubstring("<-f> <values with spaces.yaml>"))
		Expect(events[1]).To(HaveSuffix("<--skip-crds> <--set-string> <exporter.image.tag=unique-tag> <--timeout> <5m>"))
		Expect(events[1]).NotTo(ContainSubstring("<--wait>"))
		if installed {
			Expect(events).To(HaveLen(4))
			Expect(events[2]).To(Equal("kubectl <-n> <demo-ns> <rollout> <restart> <deployment/dsx-workload-exporter>"))
		} else {
			Expect(events).To(HaveLen(3))
		}
		Expect(events[len(events)-1]).To(Equal("kubectl <-n> <demo-ns> <rollout> <status> <deployment/dsx-workload-exporter> <--timeout=180s>"))
	}, Entry("new release", false), Entry("existing release", true))

	It("rejects missing exporter arguments before touching the cluster", func() {
		events, err := runDemoInstaller(exporter, []string{"demo", "demo-ns"})
		Expect(err).To(HaveOccurred())
		Expect(events).To(BeEmpty())
	})

	DescribeTable("stops when an exporter install step fails", func(step string, eventCount int) {
		events, err := runDemoInstaller(exporter, []string{"demo", "demo-ns", "values.yaml"},
			"DEMO_TEST_INSTALLED=true", "DEMO_TEST_FAIL="+step)
		Expect(err).To(HaveOccurred())
		Expect(events).To(HaveLen(eventCount))
	}, Entry("Helm upgrade", "upgrade", 2), Entry("restart", "restart", 3), Entry("rollout", "rollout", 4))

	DescribeTable("shares fake GPU installation with caller overrides", func(custom bool) {
		var args, env []string
		if custom {
			args = []string{"-f", "demo values.yaml", "--set-string", "statusUpdater.image.tag=unique-tag",
				"--set", "computeDomainDraPlugin.enabled=false", "--timeout", "5m"}
			env = []string{"FAKE_GPU_NAMESPACE=dashboard-gpu"}
		}
		events, err := runDemoInstaller(fakeGPU, args, env...)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(2))
		Expect(events[0]).To(Equal("kubectl <label> <nodes> <-l> <!node-role.kubernetes.io/control-plane> <run.ai/simulated-gpu-node-pool=default> <--overwrite>"))
		Expect(events[1]).To(ContainSubstring("<upgrade> <-i> <fake-gpu-operator> <oci://ghcr.io/run-ai/fake-gpu-operator/fake-gpu-operator>"))
		Expect(events[1]).To(ContainSubstring("<--version> <0.0.72>"))
		Expect(events[1]).To(ContainSubstring("<--set> <computeDomainDraPlugin.enabled=true> <--wait> <--timeout> <3m>"))
		if custom {
			Expect(events[1]).To(ContainSubstring("<-n> <dashboard-gpu>"))
			Expect(events[1]).To(HaveSuffix("<-f> <demo values.yaml> <--set-string> <statusUpdater.image.tag=unique-tag> <--set> <computeDomainDraPlugin.enabled=false> <--timeout> <5m>"))
		} else {
			Expect(events[1]).To(ContainSubstring("<-n> <gpu-operator>"))
		}
	}, Entry("e2e defaults", false), Entry("demo values", true))
})

func runDemoInstaller(script string, args []string, env ...string) ([]string, error) {
	root, err := filepath.Abs("../../../..")
	Expect(err).NotTo(HaveOccurred())
	dir := GinkgoT().TempDir()
	logPath := filepath.Join(dir, "commands")
	stub := `#!/usr/bin/env bash
set -eu
test "$KUBECONFIG" = "$DEMO_TEST_KUBECONFIG"
tool=${0##*/}
printf '%s' "$tool" >> "$DEMO_TEST_LOG"
printf ' <%s>' "$@" >> "$DEMO_TEST_LOG"
printf '\n' >> "$DEMO_TEST_LOG"
if [[ "$tool" == helm && "$1" == status ]]; then
  "$DEMO_TEST_INSTALLED"
elif [[ "$tool" == helm && "$1" == upgrade && "$DEMO_TEST_FAIL" == upgrade ]] ||
     [[ "$*" == *"rollout restart"* && "$DEMO_TEST_FAIL" == restart ]] ||
     [[ "$*" == *"rollout status"* && "$DEMO_TEST_FAIL" == rollout ]]; then
  exit 31
fi
`
	for _, tool := range []string{"helm", "kubectl"} {
		Expect(os.WriteFile(filepath.Join(dir, tool), []byte(stub), 0o700)).To(Succeed())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", append([]string{filepath.Join(root, script)}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"KUBECONFIG="+filepath.Join(dir, "kubeconfig"), "DEMO_TEST_KUBECONFIG="+filepath.Join(dir, "kubeconfig"),
		"DEMO_TEST_LOG="+logPath, "DEMO_TEST_INSTALLED=false", "DEMO_TEST_FAIL=",
		"FAKE_GPU_NAMESPACE=gpu-operator", "FAKE_GPU_VERSION=0.0.72")
	cmd.Env = append(cmd.Env, env...)
	output, runErr := cmd.CombinedOutput()
	_, _ = GinkgoWriter.Write(output)
	log, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return nil, runErr
	}
	Expect(err).NotTo(HaveOccurred())
	return strings.Split(strings.TrimSpace(string(log)), "\n"), runErr
}
