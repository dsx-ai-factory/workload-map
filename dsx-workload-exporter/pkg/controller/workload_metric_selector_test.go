// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/discovery"
)

func TestWorkloadMetricSelectorKeepsPodInfoAndRejectsStalePublication(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{WorkloadMetricSelector: &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "example.org/group", Operator: metav1.LabelSelectorOpDoesNotExist}},
	}})
	podWatch := rootLifetimeWatch(t, r, podGVK, "", true)
	pod := rootLifetimeObject(podGVK, "ns", "pod", "pod")
	require.NoError(t, r.state.applyWatchEvent(podWatch, pod, false))
	drainRootLifetimeQueues(t, r)
	previous := *r.state.deps.Publications.Workload(pod.GetUID())
	selection, err := r.state.lookupDefinition(podGVK)
	require.NoError(t, err)
	oldRef := refOf(pod)
	pod = pod.DeepCopy()
	pod.SetResourceVersion("2")
	pod.SetLabels(map[string]string{"example.org/group": "missing-target"})
	require.NoError(t, r.state.applyWatchEvent(podWatch, pod, false))
	require.ErrorIs(t, r.state.publishWorkload(pod.GetUID(), oldRef, selection, previous), discovery.ErrStale)
	require.ErrorIs(t, r.state.failWorkload(pod.GetUID(), refOf(pod), discovery.ErrPermissionDenied), discovery.ErrPermissionDenied)
	require.Nil(t, r.state.deps.Publications.Workload(pod.GetUID()), "source errors must not restore suppressed metrics")
	drainRootLifetimeQueues(t, r)
	require.Nil(t, r.state.deps.Publications.Workload(pod.GetUID()))
	result := r.state.deps.Publications.Pod(pod.GetUID())
	require.True(t, result.HasValue)
	require.Empty(t, result.Error)
	require.Equal(t, pod.GetUID(), result.Record.Workload.UID)

	pod = pod.DeepCopy()
	pod.SetResourceVersion("3")
	pod.SetLabels(nil)
	require.NoError(t, r.state.applyWatchEvent(podWatch, pod, false))
	drainRootLifetimeQueues(t, r)
	require.True(t, r.state.deps.Publications.Workload(pod.GetUID()).HasValue)
	require.True(t, r.state.deps.Publications.Pod(pod.GetUID()).HasValue)
}
