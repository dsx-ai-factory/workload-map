//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	corev1 "k8s.io/api/core/v1"
)

const namespace = "exporter-workloads-a"

var workloadFamilies = []string{"dsx_workload_pod_info", "dsx_workload_status", "dsx_workload_created_timestamp_seconds", "dsx_exporter_observation_valid"}

func command(t *testing.T, input string, args ...string) string {
	t.Helper()
	out, err := kubectl(input, args...)
	if err != nil {
		t.Fatal(err)
	}
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

func apply(t *testing.T, yaml string) {
	t.Helper()
	command(t, yaml, "apply", "-f", "-")
}

func readObject(t *testing.T, dst any, args ...string) bool {
	t.Helper()
	out, err := kubectl("", append([]string{"get", "-n", namespace, "-o", "json"}, args...)...)
	if err == nil {
		err = json.Unmarshal([]byte(out), dst)
	}
	if err != nil {
		t.Logf("retrying object read: %v", err)
		return false
	}
	return true
}

func metrics(t *testing.T, shard string) map[string]*dto.MetricFamily {
	t.Helper()
	var fs map[string]*dto.MetricFamily
	eventually(t, "metrics for shard "+shard, func() bool {
		fs = pollMetrics(t, shard)
		return fs != nil
	})
	return fs
}

func pollMetrics(t *testing.T, shard string) map[string]*dto.MetricFamily {
	t.Helper()
	body, err := kubectl("", "get", "--raw", fmt.Sprintf("/api/v1/namespaces/exporter-e2e-%s/services/dsx-workload-exporter:8080/proxy/metrics", shard))
	if err != nil {
		t.Logf("retrying metrics read: %v", err)
		return nil
	}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	fs, err := parser.TextToMetricFamilies(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse metrics: %v", err)
	}
	for name, family := range fs {
		if !strings.HasPrefix(name, "dsx_") {
			continue
		}
		if family.GetType() != dto.MetricType_GAUGE {
			t.Fatalf("%s is not a gauge", name)
		}
		for _, metric := range family.Metric {
			if metric.Gauge == nil {
				t.Fatalf("%s has no gauge value", name)
			}
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

func labelValue(m *dto.Metric, key string) (string, bool) {
	for _, label := range m.Label {
		if label.GetName() == key {
			return label.GetValue(), true
		}
	}
	return "", false
}

func eventually(t *testing.T, description string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", description)
}

func phase(t *testing.T, name string, want ...string) {
	t.Helper()
	eventually(t, name+" phases "+strings.Join(want, ","), func() bool {
		fs := pollMetrics(t, "a")
		return fs != nil && hasPhases(fs, map[string]string{"namespace": namespace, "workload_name": name, "observation_valid": "true"}, want...)
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

func waitContainer(t *testing.T, pod, container string, init bool) {
	t.Helper()
	eventually(t, pod+" container "+container+" running", func() bool {
		var object corev1.Pod
		if !readObject(t, &object, "pod", pod) {
			return false
		}
		statuses := object.Status.ContainerStatuses
		if init {
			statuses = object.Status.InitContainerStatuses
		}
		return containerRunning(statuses, container)
	})
}

func containerRunning(statuses []corev1.ContainerStatus, name string) bool {
	for _, status := range statuses {
		if status.Name == name && status.State.Running != nil {
			return true
		}
	}
	return false
}
