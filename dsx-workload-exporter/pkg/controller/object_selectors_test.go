// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestObjectSelectorsAssociationLabelChanges(t *testing.T) {
	r, podWatch, targetWatch := newAssociationRuntime(t)
	var err error
	r.state.objectSelectors, err = compileObjectSelectors([]ObjectSelector{{APIVersion: associationTestGVK.GroupVersion().String(), Kind: associationTestGVK.Kind, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"platform": "sky"}}}})
	require.NoError(t, err)
	pod := associationPod("member", "pod")
	target := rootLifetimeObject(associationTestGVK, "ns", "group", "target")
	target.SetOwnerReferences([]metav1.OwnerReference{associationOwner(pod)})
	require.True(t, r.state.matchesObjectSelectors(pod), "other kinds remain selected")
	require.NoError(t, r.state.applyWatchEvent(podWatch, pod, false))
	for i, selected := range []bool{false, true, false, true} {
		target = target.DeepCopy()
		target.SetResourceVersion(string(rune('1' + i)))
		target.SetLabels(nil)
		if selected {
			target.SetLabels(map[string]string{"platform": "sky"})
		}
		require.NoError(t, r.state.applyWatchEvent(targetWatch, target, false))
		drainRootLifetimeQueues(t, r)
		result := r.state.deps.Publications.Pod(pod.GetUID())
		require.NotNil(t, result)
		if selected {
			require.Equal(t, target.GetUID(), result.Record.Workload.UID)
			require.Empty(t, result.Error)
		} else {
			require.Equal(t, pod.GetUID(), result.Record.Workload.UID)
			require.Equal(t, "unresolved_owner", result.Error)
			require.Nil(t, r.state.deps.Publications.Workload(target.GetUID()))
		}
	}
}

func TestObjectSelectorsValidation(t *testing.T) {
	valid := ObjectSelector{APIVersion: "example.org/v1", Kind: "Workload"}
	for _, rules := range [][]ObjectSelector{
		{{Kind: "Workload"}}, {{APIVersion: "v1"}}, {valid, valid},
		{{APIVersion: "v1", Kind: "Pod", Selector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "platform", Operator: "Bad"}}}}},
	} {
		require.Error(t, ValidateObjectSelectors(rules))
	}
	require.NoError(t, ValidateObjectSelectors([]ObjectSelector{valid}))
}
