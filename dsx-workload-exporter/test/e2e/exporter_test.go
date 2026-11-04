//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

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
		apply(t, fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
  labels:
    exporter-e2e-shard: %s
`, ns, shard))
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
		waitContainer(t, "lifecycle", "initialize", true)
		command(t, "", "exec", "-n", namespace, "lifecycle", "-c", "initialize", "--", "touch", "/gates/start")
		phase(t, "lifecycle", "Running")
		uid := strings.TrimSpace(command(t, "", "get", "pod", "lifecycle", "-n", namespace, "-o", "jsonpath={.metadata.uid}"))
		eventually(t, "standalone Pod identity", func() bool {
			fs := pollMetrics(t, "a")
			if fs == nil {
				return false
			}
			rows := matching(fs, "dsx_workload_pod_info", map[string]string{"pod_uid": uid, "workload_uid": uid, "component": "pod"})
			return len(rows) == 1 && rows[0].GetGauge().GetValue() == 1
		})

		fs := metrics(t, "a")
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
		waitContainer(t, "lifecycle", "work", false)
		command(t, "", "exec", "-n", namespace, "lifecycle", "-c", "work", "--", "touch", "/gates/finish")
		phase(t, "lifecycle", "Completed")
		command(t, "", "delete", "pod", "lifecycle", "-n", namespace, "--wait=true")
		eventually(t, "deleted Pod metrics removed", func() bool {
			fs := pollMetrics(t, "a")
			if fs == nil {
				return false
			}
			for _, name := range []string{"dsx_workload_pod_info", "dsx_workload_status", "dsx_workload_created_timestamp_seconds"} {
				if len(matching(fs, name, map[string]string{"workload_uid": uid})) != 0 {
					return false
				}
			}
			return true
		})
	})
	t.Run("failed Pod", func(t *testing.T) {
		apply(t, `apiVersion: v1
kind: Pod
metadata:
  name: failed
  namespace: `+namespace+`
spec:
  restartPolicy: Never
  containers:
    - name: fail
      image: busybox:1.37.0
      command: [sh, -c, 'exit 1']
`)
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
      terminationGracePeriodSeconds: 1
      containers:
        - name: work
          image: busybox:1.37.0
          command: [sh, -c, 'sleep 3600']
`)
		eventually(t, "unknown root has dense zero statuses", func() bool {
			fs := pollMetrics(t, "a")
			if fs == nil {
				return false
			}
			if !hasPhases(fs, map[string]string{"namespace": namespace, "workload_name": "empty"}) {
				return false
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
		eventually(t, "workload evaluation failure retains Completed", func() bool {
			fs := pollMetrics(t, "a")
			if fs == nil {
				return false
			}
			rows := matching(fs, "dsx_exporter_observation_valid", map[string]string{"namespace": namespace, "scope": "workload", "workload_name": "empty", "reason": "evaluation_failed"})
			return len(rows) == 1 && rows[0].GetGauge().GetValue() == 0 && hasPhases(fs, map[string]string{"namespace": namespace, "workload_name": "empty", "observation_valid": "false", "karta": "exporter-e2e-deployment", "karta_source": "cluster"}, "Completed")
		})
		command(t, "", "patch", "karta", "exporter-e2e-deployment", "--type=merge", "-p", `{"spec":{"structureDefinition":{"rootComponent":{"statusDefinition":{"phaseDefinition":{"path":".metadata.labels.phase"}}}}}}`)
		phase(t, "empty", "Completed")
		eventually(t, "evaluation failure cleared after repair", func() bool {
			fs := pollMetrics(t, "a")
			if fs == nil {
				return false
			}
			return len(matching(fs, "dsx_exporter_observation_valid", map[string]string{"namespace": namespace, "scope": "workload", "workload_name": "empty"})) == 0
		})
		command(t, "", "delete", "karta", "exporter-e2e-deployment")
		eventually(t, "removed definition clears phases", func() bool {
			fs := pollMetrics(t, "a")
			if fs == nil {
				return false
			}
			if len(matching(fs, "dsx_exporter_observation_valid", map[string]string{"namespace": namespace, "scope": "workload", "workload_name": "empty"})) != 0 {
				return false
			}
			return hasPhases(fs, map[string]string{"namespace": namespace, "workload_name": "empty"})
		})

		after := command(t, "", "get", "pods", "-n", "exporter-e2e-a", "-o", "jsonpath={.items[*].metadata.uid}{.items[*].status.containerStatuses[*].restartCount}")
		if before != after {
			t.Fatal("exporter restarted during definition update")
		}
		command(t, "", "scale", "deployment/empty", "-n", namespace, "--replicas=1")
		eventually(t, "Deployment Pod resolves through ReplicaSet", func() bool {
			fs := pollMetrics(t, "a")
			if fs == nil {
				return false
			}
			return len(matching(fs, "dsx_workload_pod_info", map[string]string{"namespace": namespace, "workload_name": "empty", "workload_kind": "Deployment", "owner_resolution": "resolved"})) == 1
		})
	})

	t.Run("namespace selector handoff", testNamespaceHandoff)
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
			var pods corev1.PodList
			if !readObject(t, &pods, "pods", "-l", "job-name=slow-job") || len(pods.Items) != 1 {
				return false
			}
			pod = pods.Items[0].Name
			return containerRunning(pods.Items[0].Status.InitContainerStatuses, "initialize")
		})
		command(t, "", "exec", "-n", namespace, pod, "-c", "initialize", "--", "touch", "/gates/start")
		phase(t, "slow-job", "Running")
		jobUID := strings.TrimSpace(command(t, "", "get", "job", "slow-job", "-n", namespace, "-o", "jsonpath={.metadata.uid}"))
		eventually(t, "Job Pod identity", func() bool {
			fs := pollMetrics(t, "a")
			if fs == nil {
				return false
			}
			rows := matching(fs, "dsx_workload_pod_info", map[string]string{"namespace": namespace, "pod_name": pod, "workload_uid": jobUID, "workload_kind": "Job"})
			return len(rows) == 1 && rows[0].GetGauge().GetValue() == 1
		})
		waitContainer(t, pod, "work", false)
		command(t, "", "exec", "-n", namespace, pod, "-c", "work", "--", "touch", "/gates/finish")
		phase(t, "slow-job", "Completed")
	})

	t.Run("JobSet components and completion", testJobSet)
	t.Run("LeaderWorkerSet groups and scale down", testLeaderWorkerSet)

	t.Run("permission failure and recovery", func(t *testing.T) {
		// Keep the original namespace authorized if its Job watch reconnects.
		command(t, "", "create", "role", "healthy-jobs", "-n", namespace, "--verb=get,list,watch", "--resource=jobs.batch")
		command(t, "", "create", "rolebinding", "healthy-jobs", "-n", namespace, "--role=healthy-jobs", "--serviceaccount=exporter-e2e-a:dsx-workload-exporter")
		jobUID := strings.TrimSpace(command(t, "", "get", "job", "slow-job", "-n", namespace, "-o", "jsonpath={.metadata.uid}"))
		podUID := strings.TrimSpace(command(t, "", "get", "pods", "-l", "job-name=slow-job", "-n", namespace, "-o", "jsonpath={.items[0].metadata.uid}"))
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
		apply(t, `apiVersion: v1
kind: Namespace
metadata:
  name: exporter-workloads-denied
  labels: {exporter-e2e-shard: a}
`)
		t.Cleanup(func() {
			if !t.Failed() {
				command(t, "", "delete", "namespace", "exporter-workloads-denied", "--ignore-not-found", "--wait=false")
			}
		})
		health := func(value float64) bool {
			fs := pollMetrics(t, "a")
			if fs == nil {
				return false
			}
			rows := matching(fs, "dsx_exporter_observation_valid", map[string]string{"namespace": "exporter-workloads-denied", "scope": "resource_type", "resource_kind": "Job", "resource_group": "batch", "resource_version": "v1"})
			if len(rows) != 1 || rows[0].GetGauge().GetValue() != value {
				return false
			}
			want := "none"
			if value == 0 {
				want = "permission_denied"
			}
			reason, exists := labelValue(rows[0], "reason")
			if !exists || reason != want {
				return false
			}
			pods := matching(fs, "dsx_workload_pod_info", map[string]string{"pod_uid": podUID, "workload_uid": jobUID, "component": "job", "observation_valid": "true"})
			return len(pods) == 1 && pods[0].GetGauge().GetValue() == 1 &&
				hasPhases(fs, map[string]string{"namespace": namespace, "workload_uid": jobUID, "workload_kind": "Job", "observation_valid": "true"}, "Completed")
		}
		eventually(t, "forbidden Job collection remains observable while unready", func() bool {
			_, err := kubectl("", "get", "--raw", "/api/v1/namespaces/exporter-e2e-a/services/dsx-workload-exporter:8080/proxy/readyz")
			return err != nil && strings.Contains(err.Error(), "ServiceUnavailable") && health(0)
		})
		apply(t, `apiVersion: batch/v1
kind: Job
metadata:
  name: recovered
  namespace: exporter-workloads-denied
spec:
  template:
    spec:
      restartPolicy: Never
      terminationGracePeriodSeconds: 1
      containers:
        - name: work
          image: busybox:1.37.0
          command: [sh, -c, 'sleep 3600']
`)
		recoveredJobUID := strings.TrimSpace(command(t, "", "get", "job", "recovered", "-n", "exporter-workloads-denied", "-o", "jsonpath={.metadata.uid}"))
		var recoveredPodUID string
		discoveryLabels := map[string]string{"namespace": "exporter-workloads-denied", "scope": "discovery"}
		eventually(t, "unreadable Job owner emits discovery health without a guessed root", func() bool {
			body, err := kubectl("", "get", "pods", "-n", "exporter-workloads-denied", "-l", "job-name=recovered", "-o", "json")
			var pods corev1.PodList
			if err != nil || json.Unmarshal([]byte(body), &pods) != nil || len(pods.Items) != 1 {
				return false
			}
			pod := pods.Items[0]
			if pod.UID == "" || len(pod.OwnerReferences) != 1 {
				return false
			}
			owner := pod.OwnerReferences[0]
			if owner.Controller == nil || !*owner.Controller || owner.APIVersion != "batch/v1" || owner.Kind != "Job" || owner.Name != "recovered" || string(owner.UID) != recoveredJobUID {
				return false
			}
			recoveredPodUID = string(pod.UID)
			fs := pollMetrics(t, "a")
			if fs == nil || len(matching(fs, "dsx_exporter_observation_valid", discoveryLabels)) != 1 {
				return false
			}
			rows := matching(fs, "dsx_exporter_observation_valid", map[string]string{"namespace": "exporter-workloads-denied", "scope": "discovery", "resource_group": "", "resource_version": "v1", "resource_kind": "Pod", "workload_name": "", "workload_uid": "", "reason": "permission_denied"})
			if len(rows) != 1 || rows[0].GetGauge().GetValue() != 0 {
				return false
			}
			for _, family := range []string{"dsx_workload_pod_info", "dsx_workload_status", "dsx_workload_created_timestamp_seconds"} {
				if len(matching(fs, family, map[string]string{"namespace": "exporter-workloads-denied"})) != 0 {
					return false
				}
			}
			return true
		})
		command(t, "", "patch", "clusterrole", roleName, "--type=merge", "-p", string(restorePatch))
		eventually(t, "Job collection and readiness recovered", func() bool {
			_, err := kubectl("", "get", "--raw", "/api/v1/namespaces/exporter-e2e-a/services/dsx-workload-exporter:8080/proxy/readyz")
			return err == nil && health(1)
		})
		eventually(t, "recovered namespace Job published", func() bool {
			fs := pollMetrics(t, "a")
			if fs == nil {
				return false
			}
			pods := matching(fs, "dsx_workload_pod_info", map[string]string{"namespace": "exporter-workloads-denied", "pod_uid": recoveredPodUID, "workload_uid": recoveredJobUID, "workload_kind": "Job", "component": "job", "owner_resolution": "resolved", "observation_valid": "true"})
			rows := matching(fs, "dsx_workload_status", map[string]string{"namespace": "exporter-workloads-denied", "workload_name": "recovered", "workload_uid": recoveredJobUID, "observation_valid": "true", "phase": "Running"})
			return len(matching(fs, "dsx_exporter_observation_valid", discoveryLabels)) == 0 &&
				len(pods) == 1 && pods[0].GetGauge().GetValue() == 1 && len(rows) == 1 && rows[0].GetGauge().GetValue() == 1
		})
	})

}
