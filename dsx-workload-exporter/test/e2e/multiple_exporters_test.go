//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Namespace sharding", Serial, func() {
	It("moves all namespace metrics from exporter A to B and back", func() {
		By("submitting a Pod in exporter A's namespace")
		apply(`apiVersion: v1
kind: Pod
metadata:
  name: shard-probe
  namespace: exporter-workloads-a
spec:
  terminationGracePeriodSeconds: 1
  containers:
    - name: work
      image: busybox:1.37.0
      command: [sh, -c, 'sleep 3600']
`)
		By("waiting for the Pod to run and report Running")
		waitContainer("shard-probe", "work", false)
		phase("shard-probe", "Running")

		By("verifying that only exporter A collects the namespace")
		eventually("namespace initially collected only by A", func() bool { return namespaceMetricsCollectedOnlyBy("a", "b") })
		DeferCleanup(func() {
			command("", "label", "namespace", namespace, "exporter-e2e-shard=a", "--overwrite")
		})

		By("relabeling the namespace for exporter B")
		command("", "label", "namespace", namespace, "exporter-e2e-shard=b", "--overwrite")
		By("verifying all four metric families move to B and remain stable")
		eventually("namespace moves from A to B", func() bool { return namespaceMetricsCollectedOnlyBy("b", "a") })
		Consistently(func() bool { return namespaceMetricsCollectedOnlyBy("b", "a") }, 3*time.Second, 250*time.Millisecond).
			Should(BeTrue(), "namespace handoff did not remain consistent")
		By("relabeling the namespace for exporter A")
		command("", "label", "namespace", namespace, "exporter-e2e-shard=a", "--overwrite")
		By("verifying all four metric families return to A")
		eventually("namespace returns from B to A", func() bool { return namespaceMetricsCollectedOnlyBy("a", "b") })
	})
})

func namespaceMetricsCollectedOnlyBy(destination, source string) bool {
	dst, src := metrics(destination), metrics(source)
	for _, family := range workloadFamilies {
		if len(matching(src, family, map[string]string{"namespace": namespace})) != 0 {
			return false
		}
	}
	rows := matching(dst, "dsx_workload_pod_info", map[string]string{"namespace": namespace, "pod_name": "shard-probe", "observation_valid": "true"})
	health := matching(dst, "dsx_exporter_observation_valid", map[string]string{"namespace": namespace, "scope": "resource_type", "resource_kind": "Pod", "reason": "none"})
	return hasSingleGauge(rows, 1) &&
		hasSingleGauge(health, 1) &&
		len(matching(dst, "dsx_workload_created_timestamp_seconds", map[string]string{"namespace": namespace, "workload_name": "shard-probe"})) == 1 &&
		hasPhases(dst, map[string]string{"namespace": namespace, "workload_name": "shard-probe", "observation_valid": "true"}, "Running")
}
