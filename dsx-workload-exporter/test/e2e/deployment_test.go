//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	. "github.com/onsi/ginkgo/v2"

	dto "github.com/prometheus/client_model/go"
)

var _ = Describe("Deployment", Serial, func() {
	It("updates Deployment phase metrics when its Karta definition changes, without restarting the exporter", func() {
		By("create a Deployment with zero replicas and verify all phase gauges are zero")
		submitWorkload("deployment")
		expectUnknownDeploymentMetrics()

		By("record exporter identity and restarts before adding a Karta")
		before := command("", "get", "pods", "-n", "exporter-e2e-a", "-o", "jsonpath={.items[*].metadata.uid}{.items[*].status.containerStatuses[*].restartCount}")

		By("add a Karta definition and verify the exported status becomes Running")
		submitWorkload("deployment-karta")
		DeferCleanup(func() {
			if !CurrentSpecReport().Failed() {
				command("", "delete", "karta", "exporter-e2e-deployment", "--ignore-not-found")
			}
		})
		waitForWorkloadPhases("empty", "Running")

		By("change the Karta mapping and verify the exported status becomes Completed")
		command("", "patch", "karta", "exporter-e2e-deployment", "--type=merge", "-p", `{"spec":{"structureDefinition":{"rootComponent":{"statusDefinition":{"statusMappings":{"running":[],"completed":[{"byPhase":"ready"}]}}}}}}`)
		waitForWorkloadPhases("empty", "Completed")

		By("break phase evaluation without discarding the last valid phase")
		command("", "patch", "karta", "exporter-e2e-deployment", "--type=merge", "-p", `{"spec":{"structureDefinition":{"rootComponent":{"statusDefinition":{"phaseDefinition":{"path":"error(\"e2e evaluation failure\")"}}}}}}`)

		By("verify evaluation failure retains Completed with invalid observation")
		expectFailedEvaluationRetainsCompleted()

		By("repair phase evaluation and wait for Completed")
		command("", "patch", "karta", "exporter-e2e-deployment", "--type=merge", "-p", `{"spec":{"structureDefinition":{"rootComponent":{"statusDefinition":{"phaseDefinition":{"path":".metadata.labels.phase"}}}}}}`)
		waitForWorkloadPhases("empty", "Completed")

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
})

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
