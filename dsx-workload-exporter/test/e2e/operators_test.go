//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

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

func testJobSet(t *testing.T) {
	applyFixture(t, "jobset")
	var root operatorWorkload
	var pods corev1.PodList
	var jobs batchv1.JobList
	eventually(t, "JobSet has three running Pods owned through three Jobs", func() bool {
		if !readObject(t, &root, "jobset", "component-jobs") ||
			!readObject(t, &pods, "pods", "-l", "exporter-e2e-case=jobset") || len(pods.Items) != 3 ||
			!readObject(t, &jobs, "jobs", "-l", "jobset.sigs.k8s.io/jobset-name=component-jobs") || len(jobs.Items) != 3 {
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
	waitStatus := func(completed bool) {
		t.Helper()
		eventually(t, fmt.Sprintf("JobSet controller status and metrics, completed=%t", completed), func() bool {
			var current operatorWorkload
			if !readObject(t, &current, "jobset", root.Name) || len(current.Status.ReplicatedJobsStatus) != 2 {
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
			fs := pollMetrics(t, "a")
			if fs == nil || !hasOperatorStatus(fs, current, "jobset-x-k8s-io-jobset-v1alpha2", phases...) || len(matching(fs, "dsx_workload_pod_info", map[string]string{"workload_uid": string(root.UID)})) != 3 {
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
	waitStatus(false)
	for _, pod := range pods.Items {
		command(t, "", "exec", "-n", namespace, pod.Name, "-c", "work", "--", "touch", "/tmp/finish")
	}
	waitStatus(true)
	command(t, "", "delete", "jobset", root.Name, "-n", namespace, "--cascade=foreground", "--wait=true", "--timeout=30s")
	eventually(t, "deleted JobSet and descendant metrics removed", func() bool {
		var remaining corev1.PodList
		if !readObject(t, &remaining, "pods", "-l", "exporter-e2e-case=jobset") || len(remaining.Items) != 0 {
			return false
		}
		fs := pollMetrics(t, "a")
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

func testLeaderWorkerSet(t *testing.T) {
	applyFixture(t, "leaderworkerset")
	var root operatorWorkload
	var pods corev1.PodList
	retained := map[types.UID]bool{}
	removed := map[types.UID]bool{}
	waitReady := func(replicas int32) {
		t.Helper()
		eventually(t, fmt.Sprintf("LeaderWorkerSet has %d ready groups and exact Pod metrics", replicas), func() bool {
			var current operatorWorkload
			var currentPods corev1.PodList
			if !readObject(t, &current, "leaderworkerset", "worker-groups") || current.Status.ReadyReplicas != replicas || current.Status.Replicas != replicas || current.Status.UpdatedReplicas != replicas ||
				!readObject(t, &currentPods, "pods", "-l", "leaderworkerset.sigs.k8s.io/name=worker-groups") || len(currentPods.Items) != int(replicas)*2 {
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
			fs := pollMetrics(t, "a")
			if fs == nil || !hasOperatorStatus(fs, current, "leaderworkerset-x-k8s-io-leaderworkerset-v1", phases...) || len(matching(fs, "dsx_workload_pod_info", map[string]string{"workload_uid": string(current.UID)})) != int(replicas)*2 {
				return false
			}
			groups := map[string]map[string]bool{}
			for _, pod := range currentPods.Items {
				component := "worker"
				if pod.Labels["leaderworkerset.sigs.k8s.io/worker-index"] == "0" {
					component = "leader"
				}
				group := pod.Labels["leaderworkerset.sigs.k8s.io/group-index"]
				if pod.DeletionTimestamp != nil || !containerRunning(pod.Status.ContainerStatuses, "work") || !hasOperatorPod(fs, current, pod, component, "", group) {
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
	}
	waitReady(2)
	for _, pod := range pods.Items {
		if pod.Labels["leaderworkerset.sigs.k8s.io/group-index"] == "0" {
			retained[pod.UID] = true
		} else {
			removed[pod.UID] = true
		}
	}
	command(t, "", "patch", "leaderworkerset", root.Name, "-n", namespace, "--type=merge", "-p", `{"spec":{"replicas":1}}`)
	waitReady(1)
}

func applyFixture(t *testing.T, name string) {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name + ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	apply(t, string(body))
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
	return len(created) == 1 && created[0].GetGauge().GetValue() == float64(root.CreationTimestamp.Unix()) &&
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
	return len(rows) == 1 && rows[0].GetGauge().GetValue() == 1
}
