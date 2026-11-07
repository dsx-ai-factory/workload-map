//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	corev1 "k8s.io/api/core/v1"
)

const namespace = "exporter-workloads-a"

var workloadFamilies = []string{"dsx_workload_pod_info", "dsx_workload_status", "dsx_workload_created_timestamp_seconds", "dsx_exporter_observation_valid"}

var setupCompleted bool

var _ = BeforeSuite(func() {
	By("verifying the dedicated Kind cluster context")
	cluster := os.Getenv("EXPORTER_E2E_CLUSTER")
	if cluster == "" {
		cluster = "dsx-exporter-e2e"
	}
	expected := "kind-" + cluster
	current := strings.TrimSpace(command("", "config", "current-context"))
	Expect(current).To(Equal(expected), "refusing context %q, expected dedicated %q", current, expected)

	By("removing stale exporter workload fixtures")
	command("", "delete", "namespace", "exporter-workloads-a", "exporter-workloads-b", "exporter-workloads-denied", "--ignore-not-found", "--wait=true", "--timeout=60s")
	command("", "delete", "karta", "exporter-e2e-deployment", "--ignore-not-found")

	By("creating the two shard-labeled workload namespaces")
	for _, shard := range []string{"a", "b"} {
		ns := "exporter-workloads-" + shard
		apply(fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
  labels:
    exporter-e2e-shard: %s
`, ns, shard))
	}
	setupCompleted = true
})

var _ = ReportAfterSuite("clean up successful suite fixtures", func(report Report) {
	if !setupCompleted || !report.SuiteSucceeded {
		return
	}
	command("", "delete", "namespace", "exporter-workloads-a", "exporter-workloads-b", "--ignore-not-found", "--wait=false")
})

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "DSX Workload Exporter E2E Suite")
}

func command(input string, args ...string) string {
	GinkgoHelper()
	out, err := kubectl(input, args...)
	Expect(err).NotTo(HaveOccurred())
	return out
}

func kubectl(input string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", append([]string{"--request-timeout=10s"}, args...)...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("kubectl %v: %w\n%s", args, err, out)
	}
	return string(out), nil
}

func apply(yaml string) {
	GinkgoHelper()
	command(yaml, "apply", "-f", "-")
}

func readObject(dst any, args ...string) bool {
	GinkgoHelper()
	out, err := kubectl("", append([]string{"get", "-n", namespace, "-o", "json"}, args...)...)
	if err == nil {
		err = json.Unmarshal([]byte(out), dst)
	}
	if err != nil {
		fmt.Fprintf(GinkgoWriter, "retrying object read: %v\n", err)
		return false
	}
	return true
}

func metrics(shard string) map[string]*dto.MetricFamily {
	GinkgoHelper()
	var fs map[string]*dto.MetricFamily
	eventually("metrics for shard "+shard, func() bool {
		fs = pollMetrics(shard)
		return fs != nil
	})
	return fs
}

func pollMetrics(shard string) map[string]*dto.MetricFamily {
	GinkgoHelper()
	body, err := kubectl("", "get", "--raw", fmt.Sprintf("/api/v1/namespaces/exporter-e2e-%s/services/dsx-workload-exporter:8080/proxy/metrics", shard))
	if err != nil {
		fmt.Fprintf(GinkgoWriter, "retrying metrics read: %v\n", err)
		return nil
	}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	fs, err := parser.TextToMetricFamilies(strings.NewReader(body))
	Expect(err).NotTo(HaveOccurred(), "parse metrics")
	for name, family := range fs {
		if !strings.HasPrefix(name, "dsx_") {
			continue
		}
		Expect(family.GetType()).To(Equal(dto.MetricType_GAUGE), "%s is not a gauge", name)
		for _, metric := range family.Metric {
			Expect(metric.Gauge).NotTo(BeNil(), "%s has no gauge value", name)
		}
	}
	return fs
}

func matching(fs map[string]*dto.MetricFamily, name string, labels map[string]string) []*dto.Metric {
	var found []*dto.Metric
	for _, m := range fs[name].GetMetric() {
		ok := true
		for key, want := range labels {
			value, exists := labelValue(m, key)
			if !exists || value != want {
				ok = false
				break
			}
		}
		if ok {
			found = append(found, m)
		}
	}
	return found
}

func hasSingleGauge(rows []*dto.Metric, want float64) bool {
	return len(rows) == 1 && rows[0].GetGauge().GetValue() == want
}

func labelValue(m *dto.Metric, key string) (string, bool) {
	for _, label := range m.Label {
		if label.GetName() == key {
			return label.GetValue(), true
		}
	}
	return "", false
}

func eventually(description string, check func() bool) {
	GinkgoHelper()
	Eventually(check, 90*time.Second, 250*time.Millisecond).Should(BeTrue(), description)
}

func waitForMetrics(description, shard string, check func(map[string]*dto.MetricFamily) bool) {
	GinkgoHelper()
	eventually(description, func() bool {
		fs := pollMetrics(shard)
		return fs != nil && check(fs)
	})
}

func waitForWorkloadPhases(name string, want ...string) {
	GinkgoHelper()
	waitForMetrics(name+" phases "+strings.Join(want, ","), "a", func(fs map[string]*dto.MetricFamily) bool {
		return hasPhases(fs, map[string]string{"namespace": namespace, "workload_name": name, "observation_valid": "true"}, want...)
	})
}

func hasPhases(fs map[string]*dto.MetricFamily, labels map[string]string, want ...string) bool {
	identity := map[string]string{"namespace": labels["namespace"], "workload_name": labels["workload_name"]}
	if uid := labels["workload_uid"]; uid != "" {
		identity = map[string]string{"workload_uid": uid}
	}
	all := matching(fs, "dsx_workload_status", identity)
	rows := matching(fs, "dsx_workload_status", labels)
	if len(all) != 9 || len(rows) != 9 {
		return false
	}
	seen := make(map[string]bool)
	for _, row := range rows {
		phase, ok := labelValue(row, "phase")
		if !ok || seen[phase] || !slices.Contains([]string{"Initializing", "Running", "Completed", "Failed", "Degraded", "Suspended", "Suspending", "Resuming", "Undefined"}, phase) {
			return false
		}
		seen[phase] = true
		expected := 0.0
		if slices.Contains(want, phase) {
			expected = 1
		}
		if row.GetGauge().GetValue() != expected {
			return false
		}
	}
	return true
}

func waitForRunningContainer(pod, container string, init bool) {
	GinkgoHelper()
	eventually(pod+" container "+container+" running", func() bool {
		var object corev1.Pod
		if !readObject(&object, "pod", pod) {
			return false
		}
		statuses := object.Status.ContainerStatuses
		if init {
			statuses = object.Status.InitContainerStatuses
		}
		return isContainerRunning(statuses, container)
	})
}

func isContainerRunning(statuses []corev1.ContainerStatus, name string) bool {
	for _, status := range statuses {
		if status.Name == name && status.State.Running != nil {
			return true
		}
	}
	return false
}
