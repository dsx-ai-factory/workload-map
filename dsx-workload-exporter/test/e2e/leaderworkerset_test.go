//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	"fmt"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("LeaderWorkerSet", Serial, func() {
	It("exports LeaderWorkerSet groups and retains only group zero after scale down", func() {
		By("submitting a LeaderWorkerSet with two groups")
		submitWorkload("leaderworkerset")
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
			if pod.DeletionTimestamp != nil || !isContainerRunning(pod.Status.ContainerStatuses, "work") {
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
