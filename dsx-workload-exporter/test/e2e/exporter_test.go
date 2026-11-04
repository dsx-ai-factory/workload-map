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

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

const namespace = "exporter-workloads-a"

func TestExporter(t *testing.T) {
	cluster := os.Getenv("EXPORTER_E2E_CLUSTER")
	if cluster == "" {
		cluster = "dsx-exporter-e2e"
	}
	expected := "kind-" + cluster
	current := strings.TrimSpace(command(t, "", "config", "current-context"))
	if current != expected {
		t.Fatalf("refusing context %q, expected dedicated %q", current, expected)
	}
	command(t, "", "delete", "namespace", "exporter-workloads-a", "exporter-workloads-b", "exporter-workloads-denied", "--ignore-not-found", "--wait=true", "--timeout=60s")
	command(t, "", "delete", "karta", "exporter-e2e-deployment", "--ignore-not-found")

	for _, shard := range []string{"a", "b"} {
		ns := "exporter-workloads-" + shard
		apply(t, fmt.Sprintf("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n  labels:\n    exporter-e2e-shard: %s\n", ns, shard))
		t.Cleanup(func() {
			if !t.Failed() {
				command(t, "", "delete", "namespace", ns, "--ignore-not-found", "--wait=false")
			}
		})
	}
	t.Run("standalone Pod lifecycle and deletion", func(t *testing.T) {
		apply(t, `apiVersion: v1
kind: Pod
metadata:
  name: lifecycle
  namespace: exporter-workloads-a
spec:
  restartPolicy: Never
  volumes:
    - name: gates
      emptyDir: {}
  initContainers:
    - name: initialize
      image: busybox:1.37.0
      command: [sh, -c, 'until test -f /gates/start; do sleep 0.1; done']
      volumeMounts: [{name: gates, mountPath: /gates}]
  containers:
    - name: work
      image: busybox:1.37.0
      command: [sh, -c, 'until test -f /gates/finish; do sleep 0.1; done']
      volumeMounts: [{name: gates, mountPath: /gates}]
`)
		phase(t, "lifecycle", "Initializing")
		eventually(t, "init container running", func() bool {
			return strings.TrimSpace(command(t, "", "get", "pod", "lifecycle", "-n", namespace, "-o", "jsonpath={.status.initContainerStatuses[0].state.running.startedAt}")) != ""
		})
		command(t, "", "exec", "-n", namespace, "lifecycle", "-c", "initialize", "--", "touch", "/gates/start")
		phase(t, "lifecycle", "Running")
		fs := metrics(t, "a")
		uid := strings.TrimSpace(command(t, "", "get", "pod", "lifecycle", "-n", namespace, "-o", "jsonpath={.metadata.uid}"))
		eventually(t, "standalone Pod identity", func() bool {
			rows := matching(metrics(t, "a"), "dsx_workload_pod_info", map[string]string{"pod_uid": uid, "workload_uid": uid, "component": "pod"})
			return len(rows) == 1 && rows[0].GetGauge().GetValue() == 1
		})

		if len(matching(fs, "dsx_workload_status", map[string]string{"workload_uid": uid})) != 9 {
			t.Fatal("expected nine phase gauges")
		}
		created := matching(fs, "dsx_workload_created_timestamp_seconds", map[string]string{"workload_uid": uid})
		stamp := strings.TrimSpace(command(t, "", "get", "pod", "lifecycle", "-n", namespace, "-o", "jsonpath={.metadata.creationTimestamp}"))
		at, err := time.Parse(time.RFC3339, stamp)
		if err != nil {
			t.Fatal(err)
		}
		if len(created) != 1 || created[0].GetGauge().GetValue() != float64(at.Unix()) {
			t.Fatal("creation timestamp differs from API")
		}
		if len(matching(fs, "dsx_exporter_observation_valid", map[string]string{"scope": "workload", "workload_uid": uid})) != 0 {
			t.Fatal("healthy workload must not emit health row")
		}
		if len(matching(metrics(t, "b"), "dsx_workload_pod_info", map[string]string{"pod_uid": uid})) != 0 {
			t.Fatal("Pod leaked to other shard")
		}
		command(t, "", "exec", "-n", namespace, "lifecycle", "-c", "work", "--", "touch", "/gates/finish")
		phase(t, "lifecycle", "Completed")
		command(t, "", "delete", "pod", "lifecycle", "-n", namespace, "--wait=true")
		eventually(t, "deleted Pod metrics removed", func() bool {
			fs := metrics(t, "a")
			for _, name := range []string{"dsx_workload_pod_info", "dsx_workload_status", "dsx_workload_created_timestamp_seconds"} {
				if len(matching(fs, name, map[string]string{"workload_uid": uid})) != 0 {
					return false
				}
			}
			return true
		})
	})
	t.Run("failed Pod", func(t *testing.T) {
		apply(t, "apiVersion: v1\nkind: Pod\nmetadata:\n  name: failed\n  namespace: "+namespace+"\nspec:\n  restartPolicy: Never\n  containers:\n    - name: fail\n      image: busybox:1.37.0\n      command: [sh, -c, 'exit 1']\n")
		phase(t, "failed", "Failed")
	})
	t.Run("root without Pods and dynamic definition", func(t *testing.T) {
		apply(t, `apiVersion: apps/v1
kind: Deployment
metadata:
  name: empty
  namespace: exporter-workloads-a
  labels: {phase: ready}
spec:
  replicas: 0
  selector:
    matchLabels: {app: empty}
  template:
    metadata:
      labels: {app: empty}
    spec:
      containers:
        - name: work
          image: busybox:1.37.0
          command: [sh, -c, 'sleep 3600']
`)
		eventually(t, "unknown root has dense zero statuses", func() bool {
			fs := metrics(t, "a")
			rows := matching(fs, "dsx_workload_status", map[string]string{"workload_name": "empty"})
			if len(rows) != 9 {
				return false
			}
			for _, r := range rows {
				if r.GetGauge().GetValue() != 0 {
					return false
				}
			}
			return len(matching(fs, "dsx_workload_created_timestamp_seconds", map[string]string{"workload_name": "empty"})) == 1
		})
		before := command(t, "", "get", "pods", "-n", "exporter-e2e-a", "-o", "jsonpath={.items[*].metadata.uid}{.items[*].status.containerStatuses[*].restartCount}")
		apply(t, `apiVersion: run.ai/v1alpha1
kind: Karta
metadata:
  name: exporter-e2e-deployment
spec:
  structureDefinition:
    rootComponent:
      name: deployment
      kind: {group: apps, version: v1, kind: Deployment}
      statusDefinition:
        phaseDefinition:
          path: .metadata.labels.phase
        statusMappings:
          running: [{byPhase: ready}]
`)
		t.Cleanup(func() {
			if !t.Failed() {
				command(t, "", "delete", "karta", "exporter-e2e-deployment", "--ignore-not-found")
			}
		})
		phase(t, "empty", "Running")
		command(t, "", "patch", "karta", "exporter-e2e-deployment", "--type=merge", "-p", `{"spec":{"structureDefinition":{"rootComponent":{"statusDefinition":{"statusMappings":{"running":[],"completed":[{"byPhase":"ready"}]}}}}}}`)
		phase(t, "empty", "Completed")
		command(t, "", "patch", "karta", "exporter-e2e-deployment", "--type=merge", "-p", `{"spec":{"structureDefinition":{"rootComponent":{"statusDefinition":{"phaseDefinition":{"path":"error(\"e2e evaluation failure\")"}}}}}}`)
		eventually(t, "workload evaluation failure", func() bool {
			rows := matching(metrics(t, "a"), "dsx_exporter_observation_valid", map[string]string{"namespace": namespace, "scope": "workload", "workload_name": "empty", "reason": "evaluation_failed"})
			return len(rows) == 1 && rows[0].GetGauge().GetValue() == 0
		})
		command(t, "", "delete", "karta", "exporter-e2e-deployment")
		eventually(t, "removed definition clears phases", func() bool {
			fs := metrics(t, "a")
			if len(matching(fs, "dsx_exporter_observation_valid", map[string]string{"namespace": namespace, "scope": "workload", "workload_name": "empty"})) != 0 {
				return false
			}
			rows := matching(fs, "dsx_workload_status", map[string]string{"workload_name": "empty"})
			if len(rows) != 9 {
				return false
			}
			for _, r := range rows {
				if r.GetGauge().GetValue() != 0 {
					return false
				}
			}
			return true
		})

		after := command(t, "", "get", "pods", "-n", "exporter-e2e-a", "-o", "jsonpath={.items[*].metadata.uid}{.items[*].status.containerStatuses[*].restartCount}")
		if before != after {
			t.Fatal("exporter restarted during definition update")
		}
		command(t, "", "scale", "deployment/empty", "-n", namespace, "--replicas=1")
		eventually(t, "Deployment Pod resolves through ReplicaSet", func() bool {
			return len(matching(metrics(t, "a"), "dsx_workload_pod_info", map[string]string{"namespace": namespace, "workload_name": "empty", "workload_kind": "Deployment", "owner_resolution": "resolved"})) == 1
		})
	})

	t.Run("namespace shards and resource health", func(t *testing.T) {
		for _, shard := range []string{"a", "b"} {
			ns := "exporter-workloads-" + shard
			apply(t, "apiVersion: v1\nkind: Pod\nmetadata:\n  name: shard-probe\n  namespace: "+ns+"\nspec:\n  containers:\n    - name: work\n      image: busybox:1.37.0\n      command: [sh, -c, 'sleep 3600']\n")
			eventually(t, "positive shard "+shard, func() bool {
				return len(matching(metrics(t, shard), "dsx_workload_pod_info", map[string]string{"namespace": ns, "pod_name": "shard-probe"})) == 1
			})
			other := "a"
			if shard == "a" {
				other = "b"
			}
			for _, family := range []string{"dsx_workload_pod_info", "dsx_workload_status", "dsx_workload_created_timestamp_seconds", "dsx_exporter_observation_valid"} {
				if len(matching(metrics(t, other), family, map[string]string{"namespace": ns})) != 0 {
					t.Fatalf("namespace leaked in %s", family)
				}
			}
			rows := matching(metrics(t, shard), "dsx_exporter_observation_valid", map[string]string{"namespace": ns, "scope": "resource_type", "resource_kind": "Pod"})
			if len(rows) != 1 || rows[0].GetGauge().GetValue() != 1 {
				t.Fatal("resource health missing")
			}
		}
	})
	t.Run("slow Job lifecycle", func(t *testing.T) {
		apply(t, `apiVersion: batch/v1
kind: Job
metadata:
  name: slow-job
  namespace: exporter-workloads-a
spec:
  template:
    spec:
      restartPolicy: Never
      volumes:
        - name: gates
          emptyDir: {}
      initContainers:
        - name: initialize
          image: busybox:1.37.0
          command: [sh, -c, 'until test -f /gates/start; do sleep 0.1; done']
          volumeMounts: [{name: gates, mountPath: /gates}]
      containers:
        - name: work
          image: busybox:1.37.0
          command: [sh, -c, 'until test -f /gates/finish; do sleep 0.1; done']
          volumeMounts: [{name: gates, mountPath: /gates}]
`)
		phase(t, "slow-job", "Initializing")
		var pod string
		eventually(t, "Job init running", func() bool {
			pod = strings.TrimSpace(command(t, "", "get", "pods", "-n", namespace, "-l", "job-name=slow-job", "-o", "jsonpath={.items[0].metadata.name}"))
			return strings.TrimSpace(command(t, "", "get", "pod", pod, "-n", namespace, "-o", "jsonpath={.status.initContainerStatuses[0].state.running.startedAt}")) != ""
		})
		command(t, "", "exec", "-n", namespace, pod, "-c", "initialize", "--", "touch", "/gates/start")
		phase(t, "slow-job", "Running")
		jobUID := strings.TrimSpace(command(t, "", "get", "job", "slow-job", "-n", namespace, "-o", "jsonpath={.metadata.uid}"))
		eventually(t, "Job Pod identity", func() bool {
			rows := matching(metrics(t, "a"), "dsx_workload_pod_info", map[string]string{"namespace": namespace, "pod_name": pod, "workload_uid": jobUID, "workload_kind": "Job"})
			return len(rows) == 1 && rows[0].GetGauge().GetValue() == 1
		})
		command(t, "", "exec", "-n", namespace, pod, "-c", "work", "--", "touch", "/gates/finish")
		phase(t, "slow-job", "Completed")
	})

	t.Run("permission failure and recovery", func(t *testing.T) {
		roleName := "dsx-workload-exporter-exporter-e2e-a"
		original := command(t, "", "get", "clusterrole", roleName, "-o", "json")
		var role map[string]any
		if err := json.Unmarshal([]byte(original), &role); err != nil {
			t.Fatal(err)
		}
		rules := role["rules"].([]any)
		restorePatch, err := json.Marshal(map[string]any{"rules": rules})
		if err != nil {
			t.Fatal(err)
		}
		kept := []any{}
		for _, rule := range rules {
			r := rule.(map[string]any)
			if !slices.Contains(r["apiGroups"].([]any), "batch") {
				kept = append(kept, rule)
			}
		}
		patch, _ := json.Marshal(map[string]any{"rules": kept})
		t.Cleanup(func() {
			command(t, "", "patch", "clusterrole", roleName, "--type=merge", "-p", string(restorePatch))
		})
		command(t, "", "patch", "clusterrole", roleName, "--type=merge", "-p", string(patch))
		// Existing watches survive revocation. A new namespace forces a new LIST.
		apply(t, "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: exporter-workloads-denied\n  labels: {exporter-e2e-shard: a}\n")
		t.Cleanup(func() {
			if !t.Failed() {
				command(t, "", "delete", "namespace", "exporter-workloads-denied", "--ignore-not-found", "--wait=false")
			}
		})
		health := func(value float64) bool {
			rows := matching(metrics(t, "a"), "dsx_exporter_observation_valid", map[string]string{"namespace": "exporter-workloads-denied", "scope": "resource_type", "resource_kind": "Job", "resource_group": "batch", "resource_version": "v1"})
			if len(rows) != 1 || rows[0].GetGauge().GetValue() != value {
				return false
			}
			want := "none"
			if value == 0 {
				want = "permission_denied"
			}
			for _, l := range rows[0].Label {
				if l.GetName() == "reason" {
					return l.GetValue() == want
				}
			}
			return false
		}
		eventually(t, "forbidden Job collection", func() bool { return health(0) })
		command(t, "", "patch", "clusterrole", roleName, "--type=merge", "-p", string(restorePatch))
		eventually(t, "Job collection recovered", func() bool { return health(1) })
		apply(t, "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: recovered\n  namespace: exporter-workloads-denied\nspec:\n  template:\n    spec:\n      restartPolicy: Never\n      containers:\n        - name: work\n          image: busybox:1.37.0\n          command: [sh, -c, 'sleep 3600']\n")
		eventually(t, "recovered namespace Job published", func() bool {
			rows := matching(metrics(t, "a"), "dsx_workload_status", map[string]string{"namespace": "exporter-workloads-denied", "workload_name": "recovered", "phase": "Running"})
			return len(rows) == 1 && rows[0].GetGauge().GetValue() == 1
		})
	})

}

func command(t *testing.T, input string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", append([]string{"--request-timeout=20s"}, args...)...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl %v: %v\n%s", args, err, out)
	}
	return string(out)
}
func apply(t *testing.T, yaml string) { t.Helper(); command(t, yaml, "apply", "-f", "-") }
func metrics(t *testing.T, shard string) map[string]*dto.MetricFamily {
	t.Helper()
	body := command(t, "", "get", "--raw", fmt.Sprintf("/api/v1/namespaces/exporter-e2e-%s/services/dsx-workload-exporter:8080/proxy/metrics", shard))
	parser := expfmt.NewTextParser(model.UTF8Validation)
	fs, err := parser.TextToMetricFamilies(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
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
			value := ""
			for _, l := range m.Label {
				if l.GetName() == key {
					value = l.GetValue()
				}
			}
			if value != want {
				ok = false
			}
		}
		if ok {
			found = append(found, m)
		}
	}
	return found
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
func phase(t *testing.T, name, want string) {
	t.Helper()
	eventually(t, name+" phase "+want, func() bool {
		rows := matching(metrics(t, "a"), "dsx_workload_status", map[string]string{"namespace": namespace, "workload_name": name, "phase": want})
		if len(rows) != 1 || rows[0].GetGauge().GetValue() != 1 {
			return false
		}
		all := matching(metrics(t, "a"), "dsx_workload_status", map[string]string{"namespace": namespace, "workload_name": name})
		if len(all) != 9 {
			return false
		}
		for _, r := range all {
			for _, l := range r.Label {
				if l.GetName() == "phase" && l.GetValue() != want && r.GetGauge().GetValue() != 0 {
					return false
				}
			}
		}
		return true
	})
}
