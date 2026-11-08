//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	"encoding/json"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type handoffWorkload struct {
	metadata metav1.ObjectMeta
	group    string
	kind     string
	phases   []string
	pod      *corev1.Pod
}

const handoffNamespace = "exporter-workloads-handoff"

var _ = Describe("Namespace sharding", Serial, func() {
	It("moves all namespace metrics from exporter A to B and back", func() {
		By("creating an isolated namespace with a Pod, Job and zero-replica Deployment")
		command("", "delete", "namespace", handoffNamespace, "--ignore-not-found", "--wait=true", "--timeout=60s")
		apply(handoffManifests)
		DeferCleanup(func() {
			command("", "label", "namespace", handoffNamespace, "exporter-e2e-shard=a", "--overwrite")
			if !CurrentSpecReport().Failed() {
				command("", "delete", "namespace", handoffNamespace, "--ignore-not-found", "--wait=false")
			}
		})

		By("waiting for the Pod and Job, then recording all workload and Pod UIDs")
		workloads := waitForHandoffWorkloads()

		By("verifying all three workloads and all four metric families on A only")
		eventually("namespace initially collected only by A", func() bool {
			return hasNamespaceMetricsOnlyOn("a", "b", workloads)
		})

		By("relabeling the namespace for exporter B")
		command("", "label", "namespace", handoffNamespace, "exporter-e2e-shard=b", "--overwrite")
		By("verifying the same workload and Pod UIDs move to B with their expected metrics")
		eventually("namespace moves from A to B", func() bool {
			return hasNamespaceMetricsOnlyOn("b", "a", workloads)
		})
		Consistently(func() bool {
			return hasNamespaceMetricsOnlyOn("b", "a", workloads)
		}, 3*time.Second, 250*time.Millisecond).
			Should(BeTrue(), "namespace handoff did not remain consistent")
		By("relabeling the namespace for exporter A")
		command("", "label", "namespace", handoffNamespace, "exporter-e2e-shard=a", "--overwrite")
		By("verifying the same workload and Pod UIDs return to A with their expected metrics")
		eventually("namespace returns from B to A", func() bool {
			return hasNamespaceMetricsOnlyOn("a", "b", workloads)
		})
	})
})

const handoffManifests = `apiVersion: v1
kind: Namespace
metadata:
  name: exporter-workloads-handoff
  labels: {exporter-e2e-shard: a}
---
apiVersion: v1
kind: Pod
metadata:
  name: shard-probe
  namespace: exporter-workloads-handoff
spec:
  terminationGracePeriodSeconds: 1
  containers:
    - name: work
      image: busybox:1.37.0
      command: [sh, -c, 'sleep 3600']
---
apiVersion: batch/v1
kind: Job
metadata:
  name: shard-job
  namespace: exporter-workloads-handoff
spec:
  template:
    spec:
      restartPolicy: Never
      terminationGracePeriodSeconds: 1
      containers:
        - name: work
          image: busybox:1.37.0
          command: [sh, -c, 'sleep 3600']
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: shard-deployment
  namespace: exporter-workloads-handoff
spec:
  replicas: 0
  selector:
    matchLabels: {app: shard-deployment}
  template:
    metadata:
      labels: {app: shard-deployment}
    spec:
      containers:
        - name: work
          image: busybox:1.37.0
          command: [sh, -c, 'sleep 3600']
`

func waitForHandoffWorkloads() []handoffWorkload {
	GinkgoHelper()
	command("", "wait", "pod/shard-probe", "-n", handoffNamespace, "--for=condition=Ready", "--timeout=90s")
	command("", "wait", "job/shard-job", "-n", handoffNamespace, "--for=jsonpath={.status.ready}=1", "--timeout=90s")
	var pod corev1.Pod
	var job batchv1.Job
	var deployment appsv1.Deployment
	var jobPods corev1.PodList
	readHandoffObject(&pod, "pod", "shard-probe")
	readHandoffObject(&job, "job", "shard-job")
	readHandoffObject(&deployment, "deployment", "shard-deployment")
	readHandoffObject(&jobPods, "pods", "-l", "job-name=shard-job")
	Expect(jobPods.Items).To(HaveLen(1))
	owner := metav1.GetControllerOf(&jobPods.Items[0])
	Expect(owner).NotTo(BeNil())
	Expect(owner.APIVersion).To(Equal("batch/v1"))
	Expect(owner.Kind).To(Equal("Job"))
	Expect(owner.Name).To(Equal(job.Name))
	Expect(owner.UID).To(Equal(job.UID))
	return []handoffWorkload{
		{metadata: pod.ObjectMeta, kind: "Pod", phases: []string{"Running"}, pod: &pod},
		{metadata: job.ObjectMeta, group: "batch", kind: "Job", phases: []string{"Running"}, pod: &jobPods.Items[0]},
		{metadata: deployment.ObjectMeta, group: "apps", kind: "Deployment"},
	}
}

func readHandoffObject(object any, args ...string) {
	GinkgoHelper()
	body := command("", append([]string{"get", "-n", handoffNamespace, "-o", "json"}, args...)...)
	Expect(json.Unmarshal([]byte(body), object)).To(Succeed())
}

func hasNamespaceMetricsOnlyOn(destination, source string, workloads []handoffWorkload) bool {
	dst, src := pollMetrics(destination), pollMetrics(source)
	if dst == nil || src == nil {
		return false
	}
	namespaceLabels := map[string]string{"namespace": handoffNamespace}
	for _, family := range workloadFamilies {
		if len(matching(src, family, namespaceLabels)) != 0 {
			return false
		}
	}
	podCount := 0
	for _, workload := range workloads {
		uid := string(workload.metadata.UID)
		identity := map[string]string{"namespace": handoffNamespace, "workload_name": workload.metadata.Name, "workload_uid": uid}
		labels := map[string]string{
			"namespace": handoffNamespace, "workload_name": workload.metadata.Name, "workload_uid": uid,
			"workload_group": workload.group, "workload_version": "v1", "workload_kind": workload.kind,
			"owner_resolution": "resolved", "observation_valid": "true",
		}
		if !hasPhases(dst, labels, workload.phases...) {
			return false
		}
		created := matching(dst, "dsx_workload_created_timestamp_seconds", identity)
		if !hasSingleGauge(created, float64(workload.metadata.CreationTimestamp.Unix())) {
			return false
		}
		if workload.pod != nil {
			podCount++
			labels["pod_name"] = workload.pod.Name
			labels["pod_uid"] = string(workload.pod.UID)
			labels["component"] = strings.ToLower(workload.kind)
			labels["component_instance"] = ""
			labels["replica_group_id"] = ""
			if !hasSingleGauge(matching(dst, "dsx_workload_pod_info", labels), 1) {
				return false
			}
		} else if len(matching(dst, "dsx_workload_pod_info", identity)) != 0 {
			return false
		}
		health := matching(dst, "dsx_exporter_observation_valid", map[string]string{
			"namespace": handoffNamespace, "scope": "resource_type",
			"resource_group": workload.group, "resource_version": "v1", "resource_kind": workload.kind,
			"workload_name": "", "workload_uid": "", "reason": "none",
		})
		if !hasSingleGauge(health, 1) {
			return false
		}
	}
	for _, health := range matching(dst, "dsx_exporter_observation_valid", namespaceLabels) {
		if health.GetGauge().GetValue() != 1 {
			return false
		}
	}
	return len(matching(dst, "dsx_workload_pod_info", namespaceLabels)) == podCount &&
		len(matching(dst, "dsx_workload_status", namespaceLabels)) == 9*len(workloads) &&
		len(matching(dst, "dsx_workload_created_timestamp_seconds", namespaceLabels)) == len(workloads)
}
