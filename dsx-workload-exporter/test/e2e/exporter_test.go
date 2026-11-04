//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
)

// The permission scenario reuses the completed Job from the lifecycle scenario.
var _ = Describe("Exporter", Serial, Ordered, ContinueOnFailure, func() {
	It("standalone Pod lifecycle and deletion", func() {
		apply(`apiVersion: v1
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
		phase("lifecycle", "Initializing")
		waitContainer("lifecycle", "initialize", true)
		command("", "exec", "-n", namespace, "lifecycle", "-c", "initialize", "--", "touch", "/gates/start")
		phase("lifecycle", "Running")
		uid := strings.TrimSpace(command("", "get", "pod", "lifecycle", "-n", namespace, "-o", "jsonpath={.metadata.uid}"))
		eventually("standalone Pod identity", func() bool {
			fs := pollMetrics("a")
			if fs == nil {
				return false
			}
			rows := matching(fs, "dsx_workload_pod_info", map[string]string{"pod_uid": uid, "workload_uid": uid, "component": "pod"})
			return len(rows) == 1 && rows[0].GetGauge().GetValue() == 1
		})

		fs := metrics("a")
		if len(matching(fs, "dsx_workload_status", map[string]string{"workload_uid": uid})) != 9 {
			Fail("expected nine phase gauges")
		}
		created := matching(fs, "dsx_workload_created_timestamp_seconds", map[string]string{"workload_uid": uid})
		stamp := strings.TrimSpace(command("", "get", "pod", "lifecycle", "-n", namespace, "-o", "jsonpath={.metadata.creationTimestamp}"))
		at, err := time.Parse(time.RFC3339, stamp)
		Expect(err).NotTo(HaveOccurred())
		if len(created) != 1 || created[0].GetGauge().GetValue() != float64(at.Unix()) {
			Fail("creation timestamp differs from API")
		}
		if len(matching(fs, "dsx_exporter_observation_valid", map[string]string{"scope": "workload", "workload_uid": uid})) != 0 {
			Fail("healthy workload must not emit health row")
		}
		if len(matching(metrics("b"), "dsx_workload_pod_info", map[string]string{"pod_uid": uid})) != 0 {
			Fail("Pod leaked to other shard")
		}
		waitContainer("lifecycle", "work", false)
		command("", "exec", "-n", namespace, "lifecycle", "-c", "work", "--", "touch", "/gates/finish")
		phase("lifecycle", "Completed")
		command("", "delete", "pod", "lifecycle", "-n", namespace, "--wait=true")
		eventually("deleted Pod metrics removed", func() bool {
			fs := pollMetrics("a")
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
	It("failed Pod", func() {
		apply(`apiVersion: v1
kind: Pod
metadata:
  name: failed
  namespace: ` + namespace + `
spec:
  restartPolicy: Never
  containers:
    - name: fail
      image: busybox:1.37.0
      command: [sh, -c, 'exit 1']
`)
		phase("failed", "Failed")
	})
	It("root without Pods and dynamic definition", func() {
		apply(`apiVersion: apps/v1
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
		eventually("unknown root has dense zero statuses", func() bool {
			fs := pollMetrics("a")
			if fs == nil {
				return false
			}
			if !hasPhases(fs, map[string]string{"namespace": namespace, "workload_name": "empty"}) {
				return false
			}
			return len(matching(fs, "dsx_workload_created_timestamp_seconds", map[string]string{"workload_name": "empty"})) == 1
		})
		before := command("", "get", "pods", "-n", "exporter-e2e-a", "-o", "jsonpath={.items[*].metadata.uid}{.items[*].status.containerStatuses[*].restartCount}")
		apply(`apiVersion: run.ai/v1alpha1
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
		DeferCleanup(func() {
			if !CurrentSpecReport().Failed() {
				command("", "delete", "karta", "exporter-e2e-deployment", "--ignore-not-found")
			}
		})
		phase("empty", "Running")
		command("", "patch", "karta", "exporter-e2e-deployment", "--type=merge", "-p", `{"spec":{"structureDefinition":{"rootComponent":{"statusDefinition":{"statusMappings":{"running":[],"completed":[{"byPhase":"ready"}]}}}}}}`)
		phase("empty", "Completed")
		command("", "patch", "karta", "exporter-e2e-deployment", "--type=merge", "-p", `{"spec":{"structureDefinition":{"rootComponent":{"statusDefinition":{"phaseDefinition":{"path":"error(\"e2e evaluation failure\")"}}}}}}`)
		eventually("workload evaluation failure retains Completed", func() bool {
			fs := pollMetrics("a")
			if fs == nil {
				return false
			}
			rows := matching(fs, "dsx_exporter_observation_valid", map[string]string{"namespace": namespace, "scope": "workload", "workload_name": "empty", "reason": "evaluation_failed"})
			return len(rows) == 1 && rows[0].GetGauge().GetValue() == 0 && hasPhases(fs, map[string]string{"namespace": namespace, "workload_name": "empty", "observation_valid": "false", "karta": "exporter-e2e-deployment", "karta_source": "cluster"}, "Completed")
		})
		command("", "patch", "karta", "exporter-e2e-deployment", "--type=merge", "-p", `{"spec":{"structureDefinition":{"rootComponent":{"statusDefinition":{"phaseDefinition":{"path":".metadata.labels.phase"}}}}}}`)
		phase("empty", "Completed")
		eventually("evaluation failure cleared after repair", func() bool {
			fs := pollMetrics("a")
			if fs == nil {
				return false
			}
			return len(matching(fs, "dsx_exporter_observation_valid", map[string]string{"namespace": namespace, "scope": "workload", "workload_name": "empty"})) == 0
		})
		command("", "delete", "karta", "exporter-e2e-deployment")
		eventually("removed definition clears phases", func() bool {
			fs := pollMetrics("a")
			if fs == nil {
				return false
			}
			if len(matching(fs, "dsx_exporter_observation_valid", map[string]string{"namespace": namespace, "scope": "workload", "workload_name": "empty"})) != 0 {
				return false
			}
			return hasPhases(fs, map[string]string{"namespace": namespace, "workload_name": "empty"})
		})

		after := command("", "get", "pods", "-n", "exporter-e2e-a", "-o", "jsonpath={.items[*].metadata.uid}{.items[*].status.containerStatuses[*].restartCount}")
		if before != after {
			Fail("exporter restarted during definition update")
		}
		command("", "scale", "deployment/empty", "-n", namespace, "--replicas=1")
		eventually("Deployment Pod resolves through ReplicaSet", func() bool {
			fs := pollMetrics("a")
			if fs == nil {
				return false
			}
			return len(matching(fs, "dsx_workload_pod_info", map[string]string{"namespace": namespace, "workload_name": "empty", "workload_kind": "Deployment", "owner_resolution": "resolved"})) == 1
		})
	})

	It("namespace selector handoff", testNamespaceHandoff)
	It("slow Job lifecycle", func() {
		apply(`apiVersion: batch/v1
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
		phase("slow-job", "Initializing")
		var pod string
		eventually("Job init running", func() bool {
			var pods corev1.PodList
			if !readObject(&pods, "pods", "-l", "job-name=slow-job") || len(pods.Items) != 1 {
				return false
			}
			pod = pods.Items[0].Name
			return containerRunning(pods.Items[0].Status.InitContainerStatuses, "initialize")
		})
		command("", "exec", "-n", namespace, pod, "-c", "initialize", "--", "touch", "/gates/start")
		phase("slow-job", "Running")
		jobUID := strings.TrimSpace(command("", "get", "job", "slow-job", "-n", namespace, "-o", "jsonpath={.metadata.uid}"))
		eventually("Job Pod identity", func() bool {
			fs := pollMetrics("a")
			if fs == nil {
				return false
			}
			rows := matching(fs, "dsx_workload_pod_info", map[string]string{"namespace": namespace, "pod_name": pod, "workload_uid": jobUID, "workload_kind": "Job"})
			return len(rows) == 1 && rows[0].GetGauge().GetValue() == 1
		})
		waitContainer(pod, "work", false)
		command("", "exec", "-n", namespace, pod, "-c", "work", "--", "touch", "/gates/finish")
		phase("slow-job", "Completed")
	})

	It("JobSet components and completion", testJobSet)
	It("LeaderWorkerSet groups and scale down", testLeaderWorkerSet)

	It("permission failure and recovery", func() {
		// Keep the original namespace authorized if its Job watch reconnects.
		command("", "create", "role", "healthy-jobs", "-n", namespace, "--verb=get,list,watch", "--resource=jobs.batch")
		command("", "create", "rolebinding", "healthy-jobs", "-n", namespace, "--role=healthy-jobs", "--serviceaccount=exporter-e2e-a:dsx-workload-exporter")
		jobUID := strings.TrimSpace(command("", "get", "job", "slow-job", "-n", namespace, "-o", "jsonpath={.metadata.uid}"))
		podUID := strings.TrimSpace(command("", "get", "pods", "-l", "job-name=slow-job", "-n", namespace, "-o", "jsonpath={.items[0].metadata.uid}"))
		roleName := "dsx-workload-exporter-exporter-e2e-a"
		original := command("", "get", "clusterrole", roleName, "-o", "json")
		var role map[string]any
		Expect(json.Unmarshal([]byte(original), &role)).To(Succeed())
		rules := role["rules"].([]any)
		restorePatch, err := json.Marshal(map[string]any{"rules": rules})
		Expect(err).NotTo(HaveOccurred())
		kept := []any{}
		for _, rule := range rules {
			r := rule.(map[string]any)
			if !slices.Contains(r["apiGroups"].([]any), "batch") {
				kept = append(kept, rule)
			}
		}
		patch, _ := json.Marshal(map[string]any{"rules": kept})
		DeferCleanup(func() {
			command("", "patch", "clusterrole", roleName, "--type=merge", "-p", string(restorePatch))
		})
		command("", "patch", "clusterrole", roleName, "--type=merge", "-p", string(patch))
		// Existing watches survive revocation. A new namespace forces a new LIST.
		apply(`apiVersion: v1
kind: Namespace
metadata:
  name: exporter-workloads-denied
  labels: {exporter-e2e-shard: a}
`)
		DeferCleanup(func() {
			if !CurrentSpecReport().Failed() {
				command("", "delete", "namespace", "exporter-workloads-denied", "--ignore-not-found", "--wait=false")
			}
		})
		health := func(value float64) bool {
			fs := pollMetrics("a")
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
		eventually("forbidden Job collection remains observable while unready", func() bool {
			_, err := kubectl("", "get", "--raw", "/api/v1/namespaces/exporter-e2e-a/services/dsx-workload-exporter:8080/proxy/readyz")
			return err != nil && strings.Contains(err.Error(), "ServiceUnavailable") && health(0)
		})
		apply(`apiVersion: batch/v1
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
		recoveredJobUID := strings.TrimSpace(command("", "get", "job", "recovered", "-n", "exporter-workloads-denied", "-o", "jsonpath={.metadata.uid}"))
		var recoveredPodUID string
		discoveryLabels := map[string]string{"namespace": "exporter-workloads-denied", "scope": "discovery"}
		eventually("unreadable Job owner emits discovery health without a guessed root", func() bool {
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
			fs := pollMetrics("a")
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
		command("", "patch", "clusterrole", roleName, "--type=merge", "-p", string(restorePatch))
		eventually("Job collection and readiness recovered", func() bool {
			_, err := kubectl("", "get", "--raw", "/api/v1/namespaces/exporter-e2e-a/services/dsx-workload-exporter:8080/proxy/readyz")
			return err == nil && health(1)
		})
		eventually("recovered namespace Job published", func() bool {
			fs := pollMetrics("a")
			if fs == nil {
				return false
			}
			pods := matching(fs, "dsx_workload_pod_info", map[string]string{"namespace": "exporter-workloads-denied", "pod_uid": recoveredPodUID, "workload_uid": recoveredJobUID, "workload_kind": "Job", "component": "job", "owner_resolution": "resolved", "observation_valid": "true"})
			rows := matching(fs, "dsx_workload_status", map[string]string{"namespace": "exporter-workloads-denied", "workload_name": "recovered", "workload_uid": recoveredJobUID, "observation_valid": "true", "phase": "Running"})
			return len(matching(fs, "dsx_exporter_observation_valid", discoveryLabels)) == 0 &&
				len(pods) == 1 && pods[0].GetGauge().GetValue() == 1 && len(rows) == 1 && rows[0].GetGauge().GetValue() == 1
		})
	})
})
