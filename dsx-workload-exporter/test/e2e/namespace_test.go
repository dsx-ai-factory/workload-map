//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	"testing"
	"time"
)

func testNamespaceHandoff(t *testing.T) {
	apply(t, `apiVersion: v1
kind: Pod
metadata:
  name: shard-probe
  namespace: exporter-workloads-a
spec:
  containers:
    - name: work
      image: busybox:1.37.0
      command: [sh, -c, 'sleep 3600']
`)
	waitContainer(t, "shard-probe", "work", false)
	phase(t, "shard-probe", "Running")
	// A populated namespace must move all result families and its source health.
	check := func(destination, source string) bool {
		dst, src := pollMetrics(t, destination), pollMetrics(t, source)
		if dst == nil || src == nil {
			return false
		}
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
	eventually(t, "namespace initially collected only by A", func() bool { return check("a", "b") })
	t.Cleanup(func() {
		command(t, "", "label", "namespace", namespace, "exporter-e2e-shard=a", "--overwrite")
	})
	command(t, "", "label", "namespace", namespace, "exporter-e2e-shard=b", "--overwrite")
	eventually(t, "namespace moves from A to B", func() bool { return check("b", "a") })
	for until := time.Now().Add(3 * time.Second); time.Now().Before(until); {
		if !check("b", "a") {
			t.Fatal("namespace handoff did not remain consistent")
		}
		time.Sleep(250 * time.Millisecond)
	}
	command(t, "", "label", "namespace", namespace, "exporter-e2e-shard=a", "--overwrite")
	eventually(t, "namespace returns from B to A", func() bool { return check("a", "b") })
}
