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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/yaml"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/config"
)

var _ = Describe("Shared demo installers", func() {
	const exporter = "hack/e2e/dsx-workload-exporter/install.sh"
	const fakeGPU = "hack/e2e/infra/fake-gpu-operator/install.sh"

	It("renders the demo exporter with discovery limited to its namespace", func() {
		helm, err := exec.LookPath("helm")
		if err != nil {
			Skip("helm is needed for chart rendering")
		}
		root, err := filepath.Abs("../../..")
		Expect(err).NotTo(HaveOccurred())
		out, err := exec.Command(helm, "template", "dsx", filepath.Join(root, "charts/karta"),
			"--namespace", "dashboard-lab", "-f", filepath.Join(root, "hack/demo-grafana-exporter/exporter-values.yaml"),
			"--show-only", "templates/exporter-configmap.yaml").CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out))
		var cm corev1.ConfigMap
		Expect(yaml.Unmarshal(out, &cm)).To(Succeed())
		var cfg config.Config
		Expect(yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg)).To(Succeed())
		Expect(cfg.Validate()).To(Succeed())
		selector, err := metav1.LabelSelectorAsSelector(cfg.Selection.NamespaceSelector)
		Expect(err).NotTo(HaveOccurred())
		Expect(selector.Matches(labels.Set{"kubernetes.io/metadata.name": "dashboard-lab"})).To(BeTrue())
		Expect(selector.Matches(labels.Set{"kubernetes.io/metadata.name": "other-workloads"})).To(BeFalse())
		Expect(cfg.KartaDefinitionSource.Cluster.Enabled).To(BeFalse())
	})

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
		Expect(events[2]).To(Equal("kubectl <-n> <demo-ns> <get> <deployment> <-l> <app.kubernetes.io/name=dsx-workload-exporter,app.kubernetes.io/instance=demo> <-o> <name>"))
		if installed {
			Expect(events).To(HaveLen(5))
			Expect(events[3]).To(Equal("kubectl <-n> <demo-ns> <rollout> <restart> <deployment.apps/dsx-workload-exporter>"))
		} else {
			Expect(events).To(HaveLen(4))
		}
		Expect(events[len(events)-1]).To(Equal("kubectl <-n> <demo-ns> <rollout> <status> <deployment.apps/dsx-workload-exporter> <--timeout=180s>"))
	}, Entry("new release", false), Entry("existing release", true))

	DescribeTable("selects the exporter rendered by the chart regardless of its name", func(override string, fromValues, installed bool) {
		helm, err := exec.LookPath("helm")
		if err != nil {
			Skip("helm is needed for chart rendering")
		}
		root, err := filepath.Abs("../../..")
		Expect(err).NotTo(HaveOccurred())
		valuesPath := filepath.Join(GinkgoT().TempDir(), "exporter values.yaml")
		values := "exporter:\n  enabled: true\noperator:\n  enabled: true\n"
		var extra []string
		if fromValues {
			values += "fullnameOverride: " + override + "\n"
		} else if override != "" {
			extra = []string{"--set-string", "fullnameOverride=" + override}
		}
		Expect(os.WriteFile(valuesPath, []byte(values), 0o600)).To(Succeed())
		args := []string{"template", "dashboard", filepath.Join(root, "charts/karta"),
			"--namespace", "dashboard-ns", "-f", valuesPath, "--show-only", "templates/exporter-deployment.yaml"}
		out, err := exec.Command(helm, append(args, extra...)...).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out))
		var deployment appsv1.Deployment
		Expect(yaml.Unmarshal(out, &deployment)).To(Succeed())
		Expect(deployment.Name).NotTo(BeEmpty())
		if override != "" {
			Expect(deployment.Name).NotTo(Equal("dsx-workload-exporter"))
		}
		args[len(args)-1] = "templates/deployment.yaml"
		out, err = exec.Command(helm, append(args, extra...)...).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out))
		var operator appsv1.Deployment
		Expect(yaml.Unmarshal(out, &operator)).To(Succeed())
		Expect(operator.Name).NotTo(BeEmpty())

		events, err := runDemoInstaller(exporter, append([]string{"dashboard", "dashboard-ns", valuesPath}, extra...),
			"DEMO_TEST_INSTALLED="+strconv.FormatBool(installed), "DEMO_TEST_DEPLOYMENTS=deployment.apps/"+deployment.Name)
		Expect(err).NotTo(HaveOccurred())
		if installed {
			Expect(events).To(HaveLen(5))
		} else {
			Expect(events).To(HaveLen(4))
		}
		Expect(events[2]).To(HavePrefix("kubectl <-n> <" + deployment.Namespace + "> <get>"))
		_, selectorText, found := strings.Cut(events[2], "<deployment> <-l> <")
		Expect(found).To(BeTrue(), events[2])
		selectorText, _, _ = strings.Cut(selectorText, ">")
		selector, err := labels.Parse(selectorText)
		Expect(err).NotTo(HaveOccurred())
		Expect(selector.Matches(labels.Set(deployment.Labels))).To(BeTrue(), deployment.Name)
		otherRelease := deployment.DeepCopy()
		otherRelease.Labels["app.kubernetes.io/instance"] = "another-release"
		Expect(selector.Matches(labels.Set(otherRelease.Labels))).To(BeFalse())
		Expect(selector.Matches(labels.Set(operator.Labels))).To(BeFalse())
		for _, event := range events[3:] {
			Expect(event).To(HavePrefix("kubectl <-n> <" + deployment.Namespace + "> <rollout>"))
			Expect(event).To(ContainSubstring("<deployment.apps/" + deployment.Name + ">"))
		}
	},
		Entry("default name on a new release", "", false, false),
		Entry("values override on a new release", "dashboard", true, false),
		Entry("values override on an existing release", "dashboard", true, true),
		Entry("command-line override on an existing release", "custom-dashboard", false, true),
		Entry("long override on an existing release", strings.Repeat("dashboard-", 6), false, true),
	)

	It("rejects missing exporter arguments before touching the cluster", func() {
		events, err := runDemoInstaller(exporter, []string{"demo", "demo-ns"})
		Expect(err).To(HaveOccurred())
		Expect(events).To(BeEmpty())
	})

	DescribeTable("stops when an exporter install step fails", func(step string, eventCount int) {
		events, err := runDemoInstaller(exporter, []string{"demo", "demo-ns", "values.yaml"},
			"DEMO_TEST_INSTALLED=true", "DEMO_TEST_FAIL="+step)
		Expect(err).To(HaveOccurred())
		Expect(err.(*exec.ExitError).ExitCode()).To(Equal(31))
		Expect(events).To(HaveLen(eventCount))
	}, Entry("Helm upgrade", "upgrade", 2), Entry("deployment discovery", "discovery", 3),
		Entry("restart", "restart", 4), Entry("rollout", "rollout", 5))

	DescribeTable("rejects missing or ambiguous exporter deployments before rollout", func(deployments string) {
		events, err := runDemoInstaller(exporter, []string{"demo", "demo-ns", "values.yaml"},
			"DEMO_TEST_INSTALLED=true", "DEMO_TEST_DEPLOYMENTS="+deployments)
		Expect(err).To(HaveOccurred())
		Expect(events).To(HaveLen(3))
	}, Entry("no deployment", ""), Entry("multiple deployments", "deployment.apps/first\ndeployment.apps/second"))

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
	root, err := filepath.Abs("../../..")
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
     [[ "$*" == *"get deployment"* && "$DEMO_TEST_FAIL" == discovery ]] ||
     [[ "$*" == *"rollout restart"* && "$DEMO_TEST_FAIL" == restart ]] ||
     [[ "$*" == *"rollout status"* && "$DEMO_TEST_FAIL" == rollout ]]; then
  exit 31
elif [[ "$*" == *"get deployment"* ]]; then
  printf '%s\n' "$DEMO_TEST_DEPLOYMENTS"
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
		"DEMO_TEST_DEPLOYMENTS=deployment.apps/dsx-workload-exporter",
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
