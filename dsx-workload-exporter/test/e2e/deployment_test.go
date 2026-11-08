//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	. "github.com/onsi/ginkgo/v2"

	dto "github.com/prometheus/client_model/go"
)

var _ = Describe("Deployment", Serial, func() {
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
		waitForWorkloadPhases("empty", "Running")

		By("update the Karta to map the Deployment to Completed")
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
