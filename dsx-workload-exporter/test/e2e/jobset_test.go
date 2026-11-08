//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("JobSet", Serial, func() {
	It("exports JobSet components through completion and removes deleted metrics", func() {
		By("submitting a JobSet with two workers and one evaluator")
		applyFixture("jobset")

		By("waiting for running Pods with live Job-to-JobSet owner UID chains")
		root, pods := waitForJobSetPods()

		By("verifying running JobSet status and component metrics")
		expectJobSetStatus(root, pods, false)

		By("allowing every JobSet Pod to finish")
		for _, pod := range pods.Items {
			command("", "exec", "-n", namespace, pod.Name, "-c", "work", "--", "touch", "/tmp/finish")
		}
		By("verifying completed JobSet status and component metrics")
		expectJobSetStatus(root, pods, true)

		By("deleting the JobSet and verifying descendant metrics disappear")
		command("", "delete", "jobset", root.Name, "-n", namespace, "--cascade=foreground", "--wait=true", "--timeout=30s")
		expectJobSetDeleted(root, pods)
	})
})

func waitForJobSetPods() (operatorWorkload, corev1.PodList) {
	GinkgoHelper()
	var root operatorWorkload
	var pods corev1.PodList
	var jobs batchv1.JobList
	eventually("JobSet has three running Pods owned through three Jobs", func() bool {
		if !readObject(&root, "jobset", "component-jobs") {
			return false
		}
		if !readObject(&pods, "pods", "-l", "exporter-e2e-case=jobset") {
			return false
		}
		if len(pods.Items) != 3 {
			return false
		}
		if !readObject(&jobs, "jobs", "-l", "jobset.sigs.k8s.io/jobset-name=component-jobs") {
			return false
		}
		if len(jobs.Items) != 3 {
			return false
		}
		for _, pod := range pods.Items {
			owner := metav1.GetControllerOf(&pod)
			if owner == nil || owner.Kind != "Job" || !isContainerRunning(pod.Status.ContainerStatuses, "work") {
				return false
			}
			found := false
			for _, job := range jobs.Items {
				jobOwner := metav1.GetControllerOf(&job)
				if job.UID == owner.UID && jobOwner != nil && jobOwner.Kind == "JobSet" && jobOwner.UID == root.UID {
					found = true
				}
			}
			if !found {
				return false
			}
		}
		return true
	})
	return root, pods
}

func expectJobSetStatus(root operatorWorkload, pods corev1.PodList, completed bool) {
	GinkgoHelper()
	eventually(fmt.Sprintf("JobSet controller status and metrics, completed=%t", completed), func() bool {
		var current operatorWorkload
		if !readObject(&current, "jobset", root.Name) {
			return false
		}
		if len(current.Status.ReplicatedJobsStatus) != 2 {
			return false
		}
		conditions := conditionStatuses(current)
		if (conditions["Completed"] == metav1.ConditionTrue) != completed || conditions["Failed"] == metav1.ConditionTrue || conditions["Suspended"] == metav1.ConditionTrue {
			return false
		}
		var phases []string
		initializing, running := false, false
		for _, status := range current.Status.ReplicatedJobsStatus {
			want := int32(0)
			if !completed {
				switch status.Name {
				case "workers":
					want = 2
				case "evaluator":
					want = 1
				default:
					return false
				}
			}
			if status.Active != want || status.Failed != 0 {
				return false
			}
			initializing = initializing || status.Active > 0 && status.Ready == 0
			running = running || status.Active > 0 || status.Ready > 0
		}
		if completed {
			phases = append(phases, "Completed")
		}
		if running {
			phases = append(phases, "Running")
		}
		if initializing {
			phases = append(phases, "Initializing")
		}
		fs := pollMetrics("a")
		if fs == nil {
			return false
		}
		if !hasOperatorStatus(fs, current, "jobset-x-k8s-io-jobset-v1alpha2", phases...) {
			return false
		}
		if len(matching(fs, "dsx_workload_pod_info", map[string]string{"workload_uid": string(root.UID)})) != 3 {
			return false
		}
		instances := map[string]int{}
		for _, pod := range pods.Items {
			instance := pod.Labels["jobset.sigs.k8s.io/replicatedjob-name"]
			instances[instance]++
			if !hasOperatorPod(fs, root, pod, "replicatedjob", instance, "") {
				return false
			}
		}
		return instances["workers"] == 2 && instances["evaluator"] == 1
	})
}

func expectJobSetDeleted(root operatorWorkload, pods corev1.PodList) {
	GinkgoHelper()
	eventually("deleted JobSet and descendant metrics removed", func() bool {
		var remaining corev1.PodList
		if !readObject(&remaining, "pods", "-l", "exporter-e2e-case=jobset") {
			return false
		}
		if len(remaining.Items) != 0 {
			return false
		}
		fs := pollMetrics("a")
		if fs == nil {
			return false
		}
		for _, family := range workloadFamilies {
			if len(matching(fs, family, map[string]string{"workload_uid": string(root.UID)})) != 0 {
				return false
			}
		}
		for _, pod := range pods.Items {
			if len(matching(fs, "dsx_workload_pod_info", map[string]string{"pod_uid": string(pod.UID)})) != 0 {
				return false
			}
		}
		return true
	})
}
