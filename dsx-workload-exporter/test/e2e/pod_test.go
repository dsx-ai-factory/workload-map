//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	dto "github.com/prometheus/client_model/go"
)

var _ = Describe("Pod", Serial, Ordered, ContinueOnFailure, func() {
	It("tracks a standalone Pod through initialization, running, completion and deletion", func() {
		By("submit a Pod held in initialization by a file gate")
		submitWorkload("pod-lifecycle")
		waitForWorkloadPhases("lifecycle", "Initializing")

		By("release initialization and wait for Running")
		waitForRunningContainer("lifecycle", "initialize", true)
		command("", "exec", "-n", namespace, "lifecycle", "-c", "initialize", "--", "touch", "/gates/start")
		waitForWorkloadPhases("lifecycle", "Running")

		By("verify Pod identity, phase gauges, creation time and shard isolation")
		uid := strings.TrimSpace(command("", "get", "pod", "lifecycle", "-n", namespace, "-o", "jsonpath={.metadata.uid}"))
		expectStandalonePodIdentity(uid)
		expectStandalonePodMetadata(uid)

		By("release the work container and wait for Completed")
		waitForRunningContainer("lifecycle", "work", false)
		command("", "exec", "-n", namespace, "lifecycle", "-c", "work", "--", "touch", "/gates/finish")
		waitForWorkloadPhases("lifecycle", "Completed")

		By("delete the Pod and wait for its metrics to disappear")
		command("", "delete", "pod", "lifecycle", "-n", namespace, "--wait=true")
		expectDeletedPodMetricsRemoved(uid)
	})

	It("reports Failed for a Pod that exits with an error", func() {
		By("submit a Pod that exits with an error and wait for Failed")
		submitWorkload("pod-failed")
		waitForWorkloadPhases("failed", "Failed")
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
