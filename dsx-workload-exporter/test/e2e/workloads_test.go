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
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
)

// The permission scenario reuses the completed Job from the lifecycle scenario.
var _ = Describe("Native workloads", Serial, Ordered, ContinueOnFailure, func() {
	It("tracks a standalone Pod through initialization, running, completion and deletion", func() {
		By("submit a Pod held in initialization by a file gate")
		apply(lifecyclePodManifest)
		phase("lifecycle", "Initializing")

		By("release initialization and wait for Running")
		waitContainer("lifecycle", "initialize", true)
		command("", "exec", "-n", namespace, "lifecycle", "-c", "initialize", "--", "touch", "/gates/start")
		phase("lifecycle", "Running")

		By("verify Pod identity, phase gauges, creation time and shard isolation")
		uid := strings.TrimSpace(command("", "get", "pod", "lifecycle", "-n", namespace, "-o", "jsonpath={.metadata.uid}"))
		expectStandalonePodIdentity(uid)
		expectStandalonePodMetadata(uid)

		By("release the work container and wait for Completed")
		waitContainer("lifecycle", "work", false)
		command("", "exec", "-n", namespace, "lifecycle", "-c", "work", "--", "touch", "/gates/finish")
		phase("lifecycle", "Completed")

		By("delete the Pod and wait for its metrics to disappear")
		command("", "delete", "pod", "lifecycle", "-n", namespace, "--wait=true")
		expectDeletedPodMetricsRemoved(uid)
	})
	It("reports Failed for a Pod that exits with an error", func() {
		By("submit a Pod that exits with an error and wait for Failed")
		apply(failedPodManifest)
		phase("failed", "Failed")
	})
	It("updates a zero-Pod Deployment through Karta add, update, failure, repair and removal without restarting", func() {
		By("submit a zero-Pod Deployment and verify unknown-phase metrics")
		apply(emptyDeploymentManifest)
		expectUnknownDeploymentMetrics()

		By("record exporter identity and restarts before adding a Karta")
		before := command("", "get", "pods", "-n", "exporter-e2e-a", "-o", "jsonpath={.items[*].metadata.uid}{.items[*].status.containerStatuses[*].restartCount}")

		By("add a Karta that maps the Deployment to Running")
		apply(deploymentKartaManifest)
		DeferCleanup(func() {
			if !CurrentSpecReport().Failed() {
				command("", "delete", "karta", "exporter-e2e-deployment", "--ignore-not-found")
			}
		})
		phase("empty", "Running")

		By("update the Karta to map the Deployment to Completed")
		command("", "patch", "karta", "exporter-e2e-deployment", "--type=merge", "-p", `{"spec":{"structureDefinition":{"rootComponent":{"statusDefinition":{"statusMappings":{"running":[],"completed":[{"byPhase":"ready"}]}}}}}}`)
		phase("empty", "Completed")

		By("break phase evaluation without discarding the last valid phase")
		command("", "patch", "karta", "exporter-e2e-deployment", "--type=merge", "-p", `{"spec":{"structureDefinition":{"rootComponent":{"statusDefinition":{"phaseDefinition":{"path":"error(\"e2e evaluation failure\")"}}}}}}`)

		By("verify evaluation failure retains Completed with invalid observation")
		expectFailedEvaluationRetainsCompleted()

		By("repair phase evaluation and wait for Completed")
		command("", "patch", "karta", "exporter-e2e-deployment", "--type=merge", "-p", `{"spec":{"structureDefinition":{"rootComponent":{"statusDefinition":{"phaseDefinition":{"path":".metadata.labels.phase"}}}}}}`)
		phase("empty", "Completed")

		By("verify repair clears evaluation health")
		expectEvaluationFailureCleared()

		By("remove the Karta and verify phases clear")
		command("", "delete", "karta", "exporter-e2e-deployment")
		expectRemovedDefinitionClearsPhases()

		By("verify the exporter did not restart during definition changes")
		after := command("", "get", "pods", "-n", "exporter-e2e-a", "-o", "jsonpath={.items[*].metadata.uid}{.items[*].status.containerStatuses[*].restartCount}")
		if before != after {
			Fail("exporter restarted during definition update")
		}

		By("scale the Deployment and verify Pod ownership resolves through its ReplicaSet")
		command("", "scale", "deployment/empty", "-n", namespace, "--replicas=1")
		expectDeploymentPodResolved()
	})

	It("tracks a Job and its Pod through initialization, running and completion", func() {
		By("submit a Job held in initialization by a file gate")
		apply(slowJobManifest)
		phase("slow-job", "Initializing")

		By("release Job initialization and wait for Running")
		pod := waitForJobInit()
		command("", "exec", "-n", namespace, pod, "-c", "initialize", "--", "touch", "/gates/start")
		phase("slow-job", "Running")
		jobUID := strings.TrimSpace(command("", "get", "job", "slow-job", "-n", namespace, "-o", "jsonpath={.metadata.uid}"))
		expectJobPodIdentity(pod, jobUID)

		By("release Job work and wait for Completed")
		waitContainer(pod, "work", false)
		command("", "exec", "-n", namespace, pod, "-c", "work", "--", "touch", "/gates/finish")
		phase("slow-job", "Completed")
	})

	It("isolates Job permission failures and recovers discovery after access is restored", func() {
		By("keep the completed Job namespace authorized during cluster access revocation")
		// Keep the original namespace authorized if its Job watch reconnects.
		command("", "create", "role", "healthy-jobs", "-n", namespace, "--verb=get,list,watch", "--resource=jobs.batch")
		command("", "create", "rolebinding", "healthy-jobs", "-n", namespace, "--role=healthy-jobs", "--serviceaccount=exporter-e2e-a:dsx-workload-exporter")
		jobUID := strings.TrimSpace(command("", "get", "job", "slow-job", "-n", namespace, "-o", "jsonpath={.metadata.uid}"))
		podUID := strings.TrimSpace(command("", "get", "pods", "-l", "job-name=slow-job", "-n", namespace, "-o", "jsonpath={.items[0].metadata.uid}"))
		roleName := "dsx-workload-exporter-exporter-e2e-a"

		By("revoke cluster-wide Job access and create a newly watched namespace")
		restorePatch := revokeClusterJobAccess(roleName)
		// Existing watches survive revocation. A new namespace forces a new LIST.
		apply(deniedNamespaceManifest)
		DeferCleanup(func() {
			if !CurrentSpecReport().Failed() {
				command("", "delete", "namespace", "exporter-workloads-denied", "--ignore-not-found", "--wait=false")
			}
		})

		By("verify the exporter is unready while the authorized Job remains observable")
		eventually("forbidden Job collection remains observable while unready", func() bool {
			_, err := kubectl("", "get", "--raw", "/api/v1/namespaces/exporter-e2e-a/services/dsx-workload-exporter:8080/proxy/readyz")
			return err != nil && strings.Contains(err.Error(), "ServiceUnavailable") && hasJobCollectionHealth(0, podUID, jobUID)
		})

		By("submit a Job in the denied namespace and verify discovery failure without a guessed root")
		apply(recoveredJobManifest)
		recoveredJobUID := strings.TrimSpace(command("", "get", "job", "recovered", "-n", "exporter-workloads-denied", "-o", "jsonpath={.metadata.uid}"))
		recoveredPodUID := waitForUnreadableJobOwner(recoveredJobUID)

		By("restore Job access and verify readiness, ownership and Running metrics recover")
		command("", "patch", "clusterrole", roleName, "--type=merge", "-p", string(restorePatch))
		eventually("Job collection and readiness recovered", func() bool {
			_, err := kubectl("", "get", "--raw", "/api/v1/namespaces/exporter-e2e-a/services/dsx-workload-exporter:8080/proxy/readyz")
			return err == nil && hasJobCollectionHealth(1, podUID, jobUID)
		})
		expectRecoveredJobPublished(recoveredPodUID, recoveredJobUID)
	})
})

func expectStandalonePodIdentity(uid string) {
	GinkgoHelper()
	waitForMetrics("standalone Pod identity", "a", func(fs map[string]*dto.MetricFamily) bool {
		rows := matching(fs, "dsx_workload_pod_info", map[string]string{"pod_uid": uid, "workload_uid": uid, "component": "pod"})
		return hasSingleGauge(rows, 1)
	})
}

func expectStandalonePodMetadata(uid string) {
	GinkgoHelper()
	fs := metrics("a")
	if len(matching(fs, "dsx_workload_status", map[string]string{"workload_uid": uid})) != 9 {
		Fail("expected nine phase gauges")
	}
	created := matching(fs, "dsx_workload_created_timestamp_seconds", map[string]string{"workload_uid": uid})
	stamp := strings.TrimSpace(command("", "get", "pod", "lifecycle", "-n", namespace, "-o", "jsonpath={.metadata.creationTimestamp}"))
	at, err := time.Parse(time.RFC3339, stamp)
	Expect(err).NotTo(HaveOccurred())
	if !hasSingleGauge(created, float64(at.Unix())) {
		Fail("creation timestamp differs from API")
	}
	if len(matching(fs, "dsx_exporter_observation_valid", map[string]string{"scope": "workload", "workload_uid": uid})) != 0 {
		Fail("healthy workload must not emit health row")
	}
	if len(matching(metrics("b"), "dsx_workload_pod_info", map[string]string{"pod_uid": uid})) != 0 {
		Fail("Pod leaked to other shard")
	}
}

func expectDeletedPodMetricsRemoved(uid string) {
	GinkgoHelper()
	waitForMetrics("deleted Pod metrics removed", "a", func(fs map[string]*dto.MetricFamily) bool {
		for _, name := range []string{"dsx_workload_pod_info", "dsx_workload_status", "dsx_workload_created_timestamp_seconds"} {
			if len(matching(fs, name, map[string]string{"workload_uid": uid})) != 0 {
				return false
			}
		}
		return true
	})
}

func expectUnknownDeploymentMetrics() {
	GinkgoHelper()
	waitForMetrics("unknown root has dense zero statuses", "a", func(fs map[string]*dto.MetricFamily) bool {
		if !hasPhases(fs, map[string]string{"namespace": namespace, "workload_name": "empty"}) {
			return false
		}
		return len(matching(fs, "dsx_workload_created_timestamp_seconds", map[string]string{"workload_name": "empty"})) == 1
	})
}

func expectFailedEvaluationRetainsCompleted() {
	GinkgoHelper()
	waitForMetrics("workload evaluation failure retains Completed", "a", func(fs map[string]*dto.MetricFamily) bool {
		rows := matching(fs, "dsx_exporter_observation_valid", map[string]string{"namespace": namespace, "scope": "workload", "workload_name": "empty", "reason": "evaluation_failed"})
		return hasSingleGauge(rows, 0) && hasPhases(fs, map[string]string{"namespace": namespace, "workload_name": "empty", "observation_valid": "false", "karta": "exporter-e2e-deployment", "karta_source": "cluster"}, "Completed")
	})
}

func expectEvaluationFailureCleared() {
	GinkgoHelper()
	waitForMetrics("evaluation failure cleared after repair", "a", func(fs map[string]*dto.MetricFamily) bool {
		return len(matching(fs, "dsx_exporter_observation_valid", map[string]string{"namespace": namespace, "scope": "workload", "workload_name": "empty"})) == 0
	})
}

func expectRemovedDefinitionClearsPhases() {
	GinkgoHelper()
	waitForMetrics("removed definition clears phases", "a", func(fs map[string]*dto.MetricFamily) bool {
		if len(matching(fs, "dsx_exporter_observation_valid", map[string]string{"namespace": namespace, "scope": "workload", "workload_name": "empty"})) != 0 {
			return false
		}
		return hasPhases(fs, map[string]string{"namespace": namespace, "workload_name": "empty"})
	})
}

func expectDeploymentPodResolved() {
	GinkgoHelper()
	waitForMetrics("Deployment Pod resolves through ReplicaSet", "a", func(fs map[string]*dto.MetricFamily) bool {
		return len(matching(fs, "dsx_workload_pod_info", map[string]string{"namespace": namespace, "workload_name": "empty", "workload_kind": "Deployment", "owner_resolution": "resolved"})) == 1
	})
}

func expectJobPodIdentity(pod, jobUID string) {
	GinkgoHelper()
	waitForMetrics("Job Pod identity", "a", func(fs map[string]*dto.MetricFamily) bool {
		rows := matching(fs, "dsx_workload_pod_info", map[string]string{"namespace": namespace, "pod_name": pod, "workload_uid": jobUID, "workload_kind": "Job"})
		return hasSingleGauge(rows, 1)
	})
}

func expectRecoveredJobPublished(recoveredPodUID, recoveredJobUID string) {
	GinkgoHelper()
	discoveryLabels := map[string]string{"namespace": "exporter-workloads-denied", "scope": "discovery"}
	waitForMetrics("recovered namespace Job published", "a", func(fs map[string]*dto.MetricFamily) bool {
		pods := matching(fs, "dsx_workload_pod_info", map[string]string{"namespace": "exporter-workloads-denied", "pod_uid": recoveredPodUID, "workload_uid": recoveredJobUID, "workload_kind": "Job", "component": "job", "owner_resolution": "resolved", "observation_valid": "true"})
		rows := matching(fs, "dsx_workload_status", map[string]string{"namespace": "exporter-workloads-denied", "workload_name": "recovered", "workload_uid": recoveredJobUID, "observation_valid": "true", "phase": "Running"})
		return len(matching(fs, "dsx_exporter_observation_valid", discoveryLabels)) == 0 &&
			hasSingleGauge(pods, 1) && hasSingleGauge(rows, 1)
	})
}

func waitForJobInit() string {
	GinkgoHelper()
	var pod string
	eventually("Job init running", func() bool {
		var pods corev1.PodList
		if !readObject(&pods, "pods", "-l", "job-name=slow-job") || len(pods.Items) != 1 {
			return false
		}
		pod = pods.Items[0].Name
		return containerRunning(pods.Items[0].Status.InitContainerStatuses, "initialize")
	})
	return pod
}

func hasJobCollectionHealth(value float64, podUID, jobUID string) bool {
	fs := pollMetrics("a")
	if fs == nil {
		return false
	}
	rows := matching(fs, "dsx_exporter_observation_valid", map[string]string{"namespace": "exporter-workloads-denied", "scope": "resource_type", "resource_kind": "Job", "resource_group": "batch", "resource_version": "v1"})
	if !hasSingleGauge(rows, value) {
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
	return hasSingleGauge(pods, 1) &&
		hasPhases(fs, map[string]string{"namespace": namespace, "workload_uid": jobUID, "workload_kind": "Job", "observation_valid": "true"}, "Completed")
}

func waitForUnreadableJobOwner(recoveredJobUID string) string {
	GinkgoHelper()
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
		if !hasSingleGauge(rows, 0) {
			return false
		}
		for _, family := range []string{"dsx_workload_pod_info", "dsx_workload_status", "dsx_workload_created_timestamp_seconds"} {
			if len(matching(fs, family, map[string]string{"namespace": "exporter-workloads-denied"})) != 0 {
				return false
			}
		}
		return true
	})
	return recoveredPodUID
}

func revokeClusterJobAccess(roleName string) []byte {
	GinkgoHelper()
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
	return restorePatch
}

const lifecyclePodManifest = `apiVersion: v1
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
`

const failedPodManifest = `apiVersion: v1
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
`

const emptyDeploymentManifest = `apiVersion: apps/v1
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
`

const deploymentKartaManifest = `apiVersion: run.ai/v1alpha1
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
`

const slowJobManifest = `apiVersion: batch/v1
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
`

const deniedNamespaceManifest = `apiVersion: v1
kind: Namespace
metadata:
  name: exporter-workloads-denied
  labels: {exporter-e2e-shard: a}
`

const recoveredJobManifest = `apiVersion: batch/v1
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
`
