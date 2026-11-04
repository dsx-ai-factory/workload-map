//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func testNamespaceHandoff() {
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
	waitContainer("shard-probe", "work", false)
	phase("shard-probe", "Running")
	// A populated namespace must move all result families and its source health.
	check := func(destination, source string) bool {
		dst, src := metrics(destination), metrics(source)
		for _, family := range workloadFamilies {
			if len(matching(src, family, map[string]string{"namespace": namespace})) != 0 {
				return false
			}
		}
		rows := matching(dst, "dsx_workload_pod_info", map[string]string{"namespace": namespace, "pod_name": "shard-probe", "observation_valid": "true"})
		health := matching(dst, "dsx_exporter_observation_valid", map[string]string{"namespace": namespace, "scope": "resource_type", "resource_kind": "Pod", "reason": "none"})
		return len(rows) == 1 && rows[0].GetGauge().GetValue() == 1 &&
			len(health) == 1 && health[0].GetGauge().GetValue() == 1 &&
			len(matching(dst, "dsx_workload_created_timestamp_seconds", map[string]string{"namespace": namespace, "workload_name": "shard-probe"})) == 1 &&
			hasPhases(dst, map[string]string{"namespace": namespace, "workload_name": "shard-probe", "observation_valid": "true"}, "Running")
	}
	eventually("namespace initially collected only by A", func() bool { return check("a", "b") })
	DeferCleanup(func() {
		command("", "label", "namespace", namespace, "exporter-e2e-shard=a", "--overwrite")
	})
	command("", "label", "namespace", namespace, "exporter-e2e-shard=b", "--overwrite")
	eventually("namespace moves from A to B", func() bool { return check("b", "a") })
	Consistently(func() bool { return check("b", "a") }, 3*time.Second, 250*time.Millisecond).
		Should(BeTrue(), "namespace handoff did not remain consistent")
	command("", "label", "namespace", namespace, "exporter-e2e-shard=a", "--overwrite")
	eventually("namespace returns from B to A", func() bool { return check("a", "b") })
}
