// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/discovery"
	inventory "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/instructions"
)

func TestRootHealthIndependentOfChildDiscovery(t *testing.T) {
	for _, childGVK := range []schema.GroupVersionKind{podGVK, {Group: "apps", Version: "v1", Kind: "ReplicaSet"}} {
		t.Run(childGVK.Kind, func(t *testing.T) {
			r := newRootLifetimeRuntime(t, Config{})
			for _, gvk := range []schema.GroupVersionKind{podGVK, lifetimeDeploymentGVK} {
				definition, err := catalog.Get(gvk)
				require.NoError(t, err)
				summary, err := instructions.NewStructureSummary(definition)
				require.NoError(t, err)
				r.state.selectedDefinitions[gvk.GroupKind()] = selectedDefinition{Selection: inventory.Selection{
					Definition: definition, GVK: gvk, Origin: inventory.Origin{Name: definition.Name, Source: inventory.SourceCatalog},
				}, summary: summary}
			}
			root := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "root", "root")
			child := rootLifetimeObject(childGVK, "ns", "child", "child")
			child.SetOwnerReferences([]metav1.OwnerReference{*metav1.NewControllerRef(root, lifetimeDeploymentGVK)})
			rootWatch := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "", true)
			childWatch := rootLifetimeWatch(t, r, childGVK, "", true)
			require.NoError(t, r.state.applyWatchEvent(rootWatch, root, false))
			require.NoError(t, r.state.applyWatchEvent(childWatch, child, false))
			pod := child
			if childGVK != podGVK {
				pod = rootLifetimeObject(podGVK, "ns", "pod", "pod")
				pod.SetOwnerReferences([]metav1.OwnerReference{*metav1.NewControllerRef(child, childGVK)})
				podWatch := rootLifetimeWatch(t, r, podGVK, "", true)
				require.NoError(t, r.state.applyWatchEvent(podWatch, pod, false))
			}
			drainRootLifetimeQueues(t, r)
			result := r.state.deps.Publications.Workload(root.GetUID())
			require.NotNil(t, result)
			require.True(t, result.HasValue)
			require.True(t, result.Record.HasStatus)
			require.Empty(t, result.StatusError)

			denied := apierrors.NewForbidden(childWatch.key.Resource.GroupResource(), child.GetName(), errors.New("denied"))
			r.state.setWatchHealth(childWatch, denied)
			view := r.state.deps.Publications.(store.Capturer).Capture()
			valid, reason := view.Valid(result.Sources, result.Error)
			assert.True(t, valid, "root status after child watch failure: %s", reason)
			valid, reason = view.Valid(result.CreationSources, result.Error)
			assert.True(t, valid, "root creation after child watch failure: %s", reason)

			require.ErrorIs(t, r.resolveSelectedObjectRoot(t.Context(), objectJobFor(refOf(child))), denied)
			require.Error(t, r.reconcilePod(t.Context(), objectJobFor(refOf(pod))))
			require.NoError(t, r.reconcileWorkload(t.Context(), root.GetUID()))
			result = r.state.deps.Publications.Workload(root.GetUID())
			view = r.state.deps.Publications.(store.Capturer).Capture()
			assert.Empty(t, result.Error)
			assert.Empty(t, result.StatusError)
			valid, reason = view.Valid(result.Sources, result.Error)
			assert.True(t, valid, "root status after child discovery failure: %s", reason)
			valid, reason = view.Valid(result.CreationSources, result.Error)
			assert.True(t, valid, "root creation after child discovery failure: %s", reason)
			assert.Equal(t, int64(100), result.Record.CreatedTimestamp)
			assert.Contains(t, view.Failures, store.DiscoveryFailure{Input: storedRef(objectJobFor(refOf(child)).ref()), Error: store.ReasonPermissionDenied})
			podResult := r.state.deps.Publications.Pod(pod.GetUID())
			require.NotNil(t, podResult)
			valid, reason = view.Valid(podResult.Sources, podResult.Error)
			assert.False(t, valid)
			assert.Equal(t, store.ReasonPermissionDenied, reason)

			r.state.setWatchHealth(rootWatch, denied)
			require.ErrorIs(t, r.reconcileWorkload(t.Context(), root.GetUID()), denied)
			result = r.state.deps.Publications.Workload(root.GetUID())
			view = r.state.deps.Publications.(store.Capturer).Capture()
			for _, sources := range [][]store.SourceKey{result.Sources, result.CreationSources} {
				valid, reason = view.Valid(sources, result.Error)
				assert.False(t, valid)
				assert.Equal(t, store.ReasonPermissionDenied, reason)
			}
		})
	}
}

func TestRootOwnerResolutionUsesCurrentRootWhileChildUnavailable(t *testing.T) {
	ownerGVK := schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
	r := newRootLifetimeRuntime(t, Config{IgnoredOwnerKinds: []schema.GroupVersionKind{ownerGVK}})
	root := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "root", "root")
	pod := rootLifetimeObject(podGVK, "ns", "pod", "pod")
	pod.SetOwnerReferences([]metav1.OwnerReference{*metav1.NewControllerRef(root, lifetimeDeploymentGVK)})
	rootWatch := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "", true)
	podWatch := rootLifetimeWatch(t, r, podGVK, "", true)
	// Discover the root only through the Pod before checking it independently.
	r.state.objects[keyOf(refOf(root))] = cachedObject{object: root, source: rootWatch.key}
	rootWatch.objects[keyOf(refOf(root))] = struct{}{}
	require.NoError(t, r.state.applyWatchEvent(podWatch, pod, false))
	drainRootLifetimeQueues(t, r)
	require.Equal(t, store.Resolved, r.state.deps.Publications.Workload(root.GetUID()).Record.OwnerResolution)
	r.state.setWatchHealth(podWatch, discovery.ErrPermissionDenied)
	owner := rootLifetimeObject(ownerGVK, "ns", "owner", "owner")
	for i, expected := range []store.OwnerResolution{store.IgnoredOwner, store.Resolved} {
		current := root.DeepCopy()
		current.SetResourceVersion(strconv.Itoa(i + 2))
		if expected == store.IgnoredOwner {
			current.SetOwnerReferences([]metav1.OwnerReference{*metav1.NewControllerRef(owner, ownerGVK)})
		}
		require.NoError(t, r.state.applyWatchEvent(rootWatch, current, false))
		require.ErrorIs(t, r.reconcilePod(t.Context(), objectJobFor(refOf(pod))), discovery.ErrPermissionDenied)
		require.NoError(t, r.reconcileWorkload(t.Context(), root.GetUID()))
		result := r.state.deps.Publications.Workload(root.GetUID())
		require.NotNil(t, result)
		assert.Equal(t, expected, result.Record.OwnerResolution)
		view := r.state.deps.Publications.(store.Capturer).Capture()
		valid, reason := view.Valid(result.Sources, result.Error)
		assert.True(t, valid, reason)
	}
}

func TestRootPodStillDependsOnPodSource(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{})
	pod := rootLifetimeObject(podGVK, "ns", "pod", "pod")
	w := rootLifetimeWatch(t, r, podGVK, "", true)
	require.NoError(t, r.state.applyWatchEvent(w, pod, false))
	drainRootLifetimeQueues(t, r)
	r.state.setWatchHealth(w, discovery.ErrPermissionDenied)
	require.ErrorIs(t, r.reconcileWorkload(t.Context(), pod.GetUID()), discovery.ErrPermissionDenied)
	result := r.state.deps.Publications.Workload(pod.GetUID())
	require.NotNil(t, result)
	require.True(t, result.HasValue)
	view := r.state.deps.Publications.(store.Capturer).Capture()
	for _, sources := range [][]store.SourceKey{result.Sources, result.CreationSources} {
		valid, reason := view.Valid(sources, result.Error)
		require.False(t, valid)
		require.Equal(t, store.ReasonPermissionDenied, reason)
	}
}

func TestFailedInitialChildDiscoveryDoesNotPublishRoot(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{})
	root := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "root", "root")
	pod := rootLifetimeObject(podGVK, "ns", "pod", "pod")
	pod.SetOwnerReferences([]metav1.OwnerReference{*metav1.NewControllerRef(root, lifetimeDeploymentGVK)})
	rootWatch := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "", true)
	podWatch := rootLifetimeWatch(t, r, podGVK, "", true)
	r.state.objects[keyOf(refOf(root))] = cachedObject{object: root, source: rootWatch.key}
	rootWatch.objects[keyOf(refOf(root))] = struct{}{}
	require.NoError(t, r.state.applyWatchEvent(podWatch, pod, false))
	r.state.setWatchHealth(rootWatch, discovery.ErrPermissionDenied)
	require.ErrorIs(t, r.reconcilePod(t.Context(), objectJobFor(refOf(pod))), discovery.ErrPermissionDenied)
	require.ErrorIs(t, r.resolveSelectedObjectRoot(t.Context(), objectJobFor(refOf(pod))), discovery.ErrPermissionDenied)
	require.NoError(t, r.reconcileWorkload(t.Context(), root.GetUID()))
	require.Empty(t, r.state.roots)
	require.Nil(t, r.state.deps.Publications.Workload(root.GetUID()))
	view := r.state.deps.Publications.(store.Capturer).Capture()
	require.Contains(t, view.Failures, store.DiscoveryFailure{Input: storedRef(objectJobFor(refOf(pod)).ref()), Error: store.ReasonPermissionDenied})
}
