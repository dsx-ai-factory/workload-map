// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestObjectSelectorsMatching(t *testing.T) {
	workloadGVK := schema.GroupVersionKind{Group: "example.org", Version: "v1", Kind: "Workload"}
	global := ObjectSelector{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"environment": "production"}}}
	workload := ObjectSelector{APIVersion: "example.org/v1", Kind: "Workload", Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"managed": "true"}}}
	for _, tc := range []struct {
		name    string
		rules   []ObjectSelector
		gvk     schema.GroupVersionKind
		labels  map[string]string
		matches bool
	}{
		{name: "no selectors", gvk: workloadGVK, matches: true},
		{name: "empty list", rules: []ObjectSelector{}, gvk: workloadGVK, matches: true},
		{name: "empty selector", rules: []ObjectSelector{{}}, gvk: workloadGVK, matches: true},
		{name: "global matches Pod", rules: []ObjectSelector{global}, gvk: podGVK, labels: map[string]string{"environment": "production"}, matches: true},
		{name: "global rejects Pod", rules: []ObjectSelector{global}, gvk: podGVK},
		{name: "exact GVK matches", rules: []ObjectSelector{workload}, gvk: workloadGVK, labels: map[string]string{"managed": "true"}, matches: true},
		{name: "exact GVK rejects", rules: []ObjectSelector{workload}, gvk: workloadGVK},
		{name: "other kind is unrestricted", rules: []ObjectSelector{workload}, gvk: podGVK, matches: true},
		{name: "other version is unrestricted", rules: []ObjectSelector{workload}, gvk: schema.GroupVersionKind{Group: "example.org", Version: "v2", Kind: "Workload"}, matches: true},
		{name: "other group is unrestricted", rules: []ObjectSelector{workload}, gvk: schema.GroupVersionKind{Group: "other.example.org", Version: "v1", Kind: "Workload"}, matches: true},
		{name: "both selectors match", rules: []ObjectSelector{global, workload}, gvk: workloadGVK, labels: map[string]string{"environment": "production", "managed": "true"}, matches: true},
		{name: "global rejects despite exact GVK match", rules: []ObjectSelector{global, workload}, gvk: workloadGVK, labels: map[string]string{"managed": "true"}},
		{name: "exact GVK rejects despite global match", rules: []ObjectSelector{global, workload}, gvk: workloadGVK, labels: map[string]string{"environment": "production"}},
		{name: "global applies to other kinds", rules: []ObjectSelector{global, workload}, gvk: podGVK},
		{name: "only global applies to other kinds", rules: []ObjectSelector{global, workload}, gvk: podGVK, labels: map[string]string{"environment": "production"}, matches: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selectors, err := compileObjectSelectors(tc.rules)
			require.NoError(t, err)
			r := controllerState{objectSelectors: selectors}
			object := &unstructured.Unstructured{}
			object.SetGroupVersionKind(tc.gvk)
			object.SetLabels(tc.labels)
			require.Equal(t, tc.matches, r.isObjectSelected(object))
		})
	}
}

func TestObjectSelectorsAssociationLabelChanges(t *testing.T) {
	r, podWatch, targetWatch := newAssociationRuntime(t)
	var err error
	r.state.objectSelectors, err = compileObjectSelectors([]ObjectSelector{{APIVersion: associationTestGVK.GroupVersion().String(), Kind: associationTestGVK.Kind, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"managed": "true"}}}})
	require.NoError(t, err)
	pod := associationPod("member", "pod")
	target := rootLifetimeObject(associationTestGVK, "ns", "group", "target")
	target.SetOwnerReferences([]metav1.OwnerReference{associationOwner(pod)})
	require.True(t, r.state.isObjectSelected(pod), "other kinds remain selected")
	require.NoError(t, r.state.applyWatchEvent(podWatch, pod, false))
	for i, selected := range []bool{false, true, false, true} {
		target = target.DeepCopy()
		target.SetResourceVersion(string(rune('1' + i)))
		target.SetLabels(nil)
		if selected {
			target.SetLabels(map[string]string{"managed": "true"})
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
	invalidSelector := &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "managed", Operator: "Bad"}}}
	for _, tc := range []struct {
		name  string
		rules []ObjectSelector
	}{
		{name: "missing apiVersion", rules: []ObjectSelector{{Kind: "Workload"}}},
		{name: "missing kind", rules: []ObjectSelector{{APIVersion: "v1"}}},
		{name: "invalid apiVersion", rules: []ObjectSelector{{APIVersion: "example.org/", Kind: "Workload"}}},
		{name: "invalid group", rules: []ObjectSelector{{APIVersion: "bad group/v1", Kind: "Workload"}}},
		{name: "invalid kind", rules: []ObjectSelector{{APIVersion: "v1", Kind: "*"}}},
		{name: "duplicate exact GVK", rules: []ObjectSelector{valid, valid}},
		{name: "duplicate global", rules: []ObjectSelector{{}, {}}},
		{name: "invalid exact GVK selector", rules: []ObjectSelector{{APIVersion: "v1", Kind: "Pod", Selector: invalidSelector}}},
		{name: "invalid global selector", rules: []ObjectSelector{{Selector: invalidSelector}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, ValidateObjectSelectors(tc.rules))
		})
	}
	require.NoError(t, ValidateObjectSelectors([]ObjectSelector{valid}))
	require.NoError(t, ValidateObjectSelectors([]ObjectSelector{{}, valid}))
	require.NoError(t, ValidateObjectSelectors([]ObjectSelector{valid, {APIVersion: "example.org/v2", Kind: "Workload"}}))
}
