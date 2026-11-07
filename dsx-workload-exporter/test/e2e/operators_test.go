//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type operatorWorkload struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Status            struct {
		Replicas             int32              `json:"replicas"`
		ReadyReplicas        int32              `json:"readyReplicas"`
		UpdatedReplicas      int32              `json:"updatedReplicas"`
		Conditions           []metav1.Condition `json:"conditions"`
		ReplicatedJobsStatus []struct {
			Name   string `json:"name"`
			Active int32  `json:"active"`
			Ready  int32  `json:"ready"`
			Failed int32  `json:"failed"`
		} `json:"replicatedJobsStatus"`
	} `json:"status"`
}

var _ = Describe("Operator workloads", Serial, func() {
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

	It("exports LeaderWorkerSet groups and retains only group zero after scale down", func() {
		By("submitting a LeaderWorkerSet with two groups")
		applyFixture("leaderworkerset")
		retained := map[types.UID]bool{}
		removed := map[types.UID]bool{}

		By("waiting for two ready groups and verifying their exact Pod metrics")
		root, pods := waitForLeaderWorkerGroups(operatorWorkload{}, 2, retained, removed)
		for _, pod := range pods.Items {
			if pod.Labels["leaderworkerset.sigs.k8s.io/group-index"] == "0" {
				retained[pod.UID] = true
			} else {
				removed[pod.UID] = true
			}
		}

		By("scaling the LeaderWorkerSet down to one group")
		command("", "patch", "leaderworkerset", root.Name, "-n", namespace, "--type=merge", "-p", `{"spec":{"replicas":1}}`)

		By("verifying retained Pod UIDs and removal of the other group's metrics")
		waitForLeaderWorkerGroups(root, 1, retained, removed)
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
			if owner == nil || owner.Kind != "Job" || !containerRunning(pod.Status.ContainerStatuses, "work") {
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

func waitForLeaderWorkerGroups(root operatorWorkload, replicas int32, retained, removed map[types.UID]bool) (operatorWorkload, corev1.PodList) {
	GinkgoHelper()
	var pods corev1.PodList
	eventually(fmt.Sprintf("LeaderWorkerSet has %d ready groups and exact Pod metrics", replicas), func() bool {
		var current operatorWorkload
		var currentPods corev1.PodList
		if !readObject(&current, "leaderworkerset", "worker-groups") {
			return false
		}
		isReady := current.Status.ReadyReplicas == replicas &&
			current.Status.Replicas == replicas && current.Status.UpdatedReplicas == replicas
		if !isReady {
			return false
		}
		if !readObject(&currentPods, "pods", "-l", "leaderworkerset.sigs.k8s.io/name=worker-groups") {
			return false
		}
		if len(currentPods.Items) != int(replicas)*2 {
			return false
		}
		if root.UID != "" && current.UID != root.UID {
			return false
		}
		conditions := conditionStatuses(current)
		if conditions["Available"] != metav1.ConditionTrue {
			return false
		}
		phases := []string{"Running"}
		if conditions["Progressing"] == metav1.ConditionTrue {
			phases = append(phases, "Initializing")
		}
		fs := pollMetrics("a")
		if fs == nil {
			return false
		}
		if !hasOperatorStatus(fs, current, "leaderworkerset-x-k8s-io-leaderworkerset-v1", phases...) {
			return false
		}
		if len(matching(fs, "dsx_workload_pod_info", map[string]string{"workload_uid": string(current.UID)})) != int(replicas)*2 {
			return false
		}
		groups := map[string]map[string]bool{}
		for _, pod := range currentPods.Items {
			component := "worker"
			if pod.Labels["leaderworkerset.sigs.k8s.io/worker-index"] == "0" {
				component = "leader"
			}
			group := pod.Labels["leaderworkerset.sigs.k8s.io/group-index"]
			if pod.DeletionTimestamp != nil || !containerRunning(pod.Status.ContainerStatuses, "work") {
				return false
			}
			if !hasOperatorPod(fs, current, pod, component, "", group) {
				return false
			}
			if replicas == 1 && (group != "0" || !retained[pod.UID]) {
				return false
			}
			if groups[group] == nil {
				groups[group] = map[string]bool{}
			}
			groups[group][component] = true
		}
		for group := int32(0); group < replicas; group++ {
			members := groups[strconv.Itoa(int(group))]
			if !members["leader"] || !members["worker"] {
				return false
			}
		}
		for uid := range removed {
			if len(matching(fs, "dsx_workload_pod_info", map[string]string{"pod_uid": string(uid)})) != 0 {
				return false
			}
		}
		root, pods = current, currentPods
		return true
	})
	return root, pods
}

func applyFixture(name string) {
	GinkgoHelper()
	body, err := os.ReadFile("testdata/" + name + ".yaml")
	Expect(err).NotTo(HaveOccurred())
	apply(string(body))
}

func conditionStatuses(root operatorWorkload) map[string]metav1.ConditionStatus {
	conditions := map[string]metav1.ConditionStatus{}
	for _, condition := range root.Status.Conditions {
		conditions[condition.Type] = condition.Status
	}
	return conditions
}

func hasOperatorStatus(fs map[string]*dto.MetricFamily, root operatorWorkload, karta string, phases ...string) bool {
	apiVersion := strings.SplitN(root.APIVersion, "/", 2)
	labels := map[string]string{
		"namespace": namespace, "workload_name": root.Name, "workload_uid": string(root.UID),
		"workload_group": apiVersion[0], "workload_version": apiVersion[1], "workload_kind": root.Kind,
		"owner_resolution": "resolved", "observation_valid": "true", "karta_source": "catalog", "karta": karta, "team": "root-team",
	}
	if root.Kind == "LeaderWorkerSet" {
		labels["root_replicas"] = strconv.Itoa(int(root.Status.Replicas))
	}
	if !hasPhases(fs, labels, phases...) {
		return false
	}
	for _, row := range matching(fs, "dsx_workload_status", map[string]string{"workload_uid": string(root.UID)}) {
		if _, exists := labelValue(row, "pod_team"); exists {
			return false
		}
	}
	created := matching(fs, "dsx_workload_created_timestamp_seconds", map[string]string{"workload_uid": string(root.UID)})
	return hasSingleGauge(created, float64(root.CreationTimestamp.Unix())) &&
		len(matching(fs, "dsx_exporter_observation_valid", map[string]string{"scope": "workload", "workload_uid": string(root.UID)})) == 0
}

func hasOperatorPod(fs map[string]*dto.MetricFamily, root operatorWorkload, pod corev1.Pod, component, instance, group string) bool {
	apiVersion := strings.SplitN(root.APIVersion, "/", 2)
	rows := matching(fs, "dsx_workload_pod_info", map[string]string{
		"namespace": namespace, "workload_name": root.Name, "workload_uid": string(root.UID),
		"workload_group": apiVersion[0], "workload_version": apiVersion[1], "workload_kind": root.Kind,
		"pod_name": pod.Name, "pod_uid": string(pod.UID), "component": component, "component_instance": instance, "replica_group_id": group,
		"owner_resolution": "resolved", "observation_valid": "true", "team": "root-team", "pod_team": "pod-team",
	})
	return hasSingleGauge(rows, 1)
}
