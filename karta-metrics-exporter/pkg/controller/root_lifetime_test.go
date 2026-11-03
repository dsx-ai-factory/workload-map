// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/discovery"
	inventory "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelevaluator"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog"
)

var lifetimeDeploymentGVK = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}

func TestRootSurvivesFinalPodRemovalUntilConfirmedDeletion(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{})
	root := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "root", "root")
	pod := rootLifetimeObject(podGVK, "ns", "pod", "pod")
	pod.SetOwnerReferences([]metav1.OwnerReference{*metav1.NewControllerRef(root, root.GroupVersionKind())})
	rootWatch := rootLifetimeWatch(t, r, root.GroupVersionKind(), "", true)
	podWatch := rootLifetimeWatch(t, r, podGVK, "", true)
	// Seed only the Pod discovery path so the root has no object-job membership.
	r.state.objects[keyOf(refOf(root))] = cachedObject{object: root, source: rootWatch.key}
	rootWatch.objects[keyOf(refOf(root))] = struct{}{}
	require.NoError(t, r.state.applyWatchEvent(podWatch, pod, false))
	drainRootLifetimeQueues(t, r)
	require.NotNil(t, r.state.deps.Publications.Workload(root.GetUID()))
	require.NoError(t, r.state.applyWatchEvent(podWatch, pod, true))
	drainRootLifetimeQueues(t, r)
	require.Nil(t, r.state.deps.Publications.Pod(pod.GetUID()))
	require.NotNil(t, r.state.roots[root.GetUID()])
	require.Empty(t, r.state.roots[root.GetUID()].pods)
	require.Empty(t, r.state.roots[root.GetUID()].selectedObjects)
	require.True(t, r.state.deps.Publications.Workload(root.GetUID()).HasValue)
	require.NoError(t, r.state.applyWatchEvent(rootWatch, root, true))
	drainRootLifetimeQueues(t, r)
	require.Nil(t, r.state.deps.Publications.Workload(root.GetUID()))
	require.NotContains(t, r.state.roots, root.GetUID())
}

func TestWatchedWorkloadsWithoutKartaOrPods(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{RequiredKinds: []schema.GroupVersionKind{lifetimeDeploymentGVK}})
	requests, err := r.state.prepareWatchRequests()
	require.NoError(t, err)
	require.Contains(t, requests, discovery.WatchRequest{GVK: lifetimeDeploymentGVK})
	w := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "", true)
	listed := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "listed", "listed")
	require.NoError(t, (&reflectorStore{controller: r.state, watch: w}).Replace([]any{listed}, "1"))
	drainRootLifetimeQueues(t, r)
	added := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "added", "added")
	require.NoError(t, r.state.applyWatchEvent(w, added, false))
	drainRootLifetimeQueues(t, r)
	for _, object := range []*unstructured.Unstructured{listed, added} {
		result := r.state.deps.Publications.Workload(object.GetUID())
		require.NotNil(t, result)
		require.True(t, result.HasValue)
		require.True(t, result.Record.HasStatus)
		require.Empty(t, result.Record.Phases)
		require.Empty(t, result.Record.Karta)
		require.Empty(t, result.Record.KartaSource)
		require.Equal(t, int64(100), result.Record.CreatedTimestamp)
	}
	for _, gvk := range []schema.GroupVersionKind{namespaceGVK, kartaGVK} {
		w := rootLifetimeWatch(t, r, gvk, "", true)
		object := rootLifetimeObject(gvk, "", "internal", types.UID(gvk.Kind))
		require.NoError(t, r.state.applyWatchEvent(w, object, false))
		drainRootLifetimeQueues(t, r)
		require.Nil(t, r.state.deps.Publications.Workload(object.GetUID()))
	}
}

func TestZeroPodRootKeepsGenericValueAfterDefinitionRemoval(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{})
	root := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "idle", "idle")
	definition, err := catalog.Get(lifetimeDeploymentGVK)
	require.NoError(t, err)
	r.state.selectedDefinitions[lifetimeDeploymentGVK.GroupKind()] = selectedDefinition{Selection: inventory.Selection{
		Definition: definition, GVK: lifetimeDeploymentGVK,
		Origin: inventory.Origin{Name: definition.Name, Source: inventory.SourceCatalog},
	}}
	w := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "", true)
	require.NoError(t, r.state.applyWatchEvent(w, root, false))
	drainRootLifetimeQueues(t, r)
	require.True(t, r.state.deps.Publications.Workload(root.GetUID()).Record.HasStatus)
	r.state.stateMu.Lock()
	err = r.state.refreshDefinitions(map[schema.GroupKind]selectedDefinition{}, nil)
	r.state.stateMu.Unlock()
	require.NoError(t, err)
	drainRootLifetimeQueues(t, r)
	result := r.state.deps.Publications.Workload(root.GetUID())
	require.NotNil(t, result)
	require.True(t, result.HasValue)
	require.True(t, result.Record.HasStatus)
	require.Empty(t, result.Record.Phases)
	require.Empty(t, result.Record.Karta)
	require.Empty(t, result.Record.KartaSource)
	require.Empty(t, result.StatusError)
	require.Equal(t, int64(100), result.Record.CreatedTimestamp)
	require.Contains(t, r.state.watches, w.key)
}

func TestRootLifetimeHandlesUIDReplacement(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{})
	w := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "", true)
	old := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "root", "old")
	require.NoError(t, r.state.applyWatchEvent(w, old, false))
	drainRootLifetimeQueues(t, r)
	replacement := old.DeepCopy()
	replacement.SetUID("new")
	replacement.SetResourceVersion("2")
	require.NoError(t, r.state.applyWatchEvent(w, replacement, false))
	drainRootLifetimeQueues(t, r)
	require.Nil(t, r.state.deps.Publications.Workload(old.GetUID()))
	require.NotContains(t, r.state.roots, old.GetUID())
	require.NotNil(t, r.state.deps.Publications.Workload(replacement.GetUID()))
	require.NoError(t, r.state.applyWatchEvent(w, old, true))
	drainRootLifetimeQueues(t, r)
	require.NotNil(t, r.state.deps.Publications.Workload(replacement.GetUID()))
}

func TestRootLifetimeHandlesAdoptionAndIgnoredOwners(t *testing.T) {
	for _, ignored := range []bool{false, true} {
		t.Run(map[bool]string{false: "controller owner", true: "ignored owner"}[ignored], func(t *testing.T) {
			ownerGVK := schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
			cfg := Config{}
			if ignored {
				cfg.IgnoredOwnerKinds = []schema.GroupVersionKind{ownerGVK}
			}
			r := newRootLifetimeRuntime(t, cfg)
			root := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "root", "root")
			owner := rootLifetimeObject(ownerGVK, "ns", "owner", "owner")
			rootWatch := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "", true)
			ownerWatch := rootLifetimeWatch(t, r, ownerGVK, "", true)
			require.NoError(t, r.state.applyWatchEvent(rootWatch, root, false))
			require.NoError(t, r.state.applyWatchEvent(ownerWatch, owner, false))
			drainRootLifetimeQueues(t, r)
			adopted := root.DeepCopy()
			adopted.SetResourceVersion("2")
			adopted.SetOwnerReferences([]metav1.OwnerReference{*metav1.NewControllerRef(owner, ownerGVK)})
			require.NoError(t, r.state.applyWatchEvent(rootWatch, adopted, false))
			drainRootLifetimeQueues(t, r)
			if ignored {
				require.Equal(t, store.IgnoredOwner, r.state.deps.Publications.Workload(root.GetUID()).Record.OwnerResolution)
			} else {
				require.Nil(t, r.state.deps.Publications.Workload(root.GetUID()))
				require.NotContains(t, r.state.roots, root.GetUID())
				require.NotNil(t, r.state.deps.Publications.Workload(owner.GetUID()))
			}
			detached := root.DeepCopy()
			detached.SetResourceVersion("3")
			require.NoError(t, r.state.applyWatchEvent(rootWatch, detached, false))
			drainRootLifetimeQueues(t, r)
			require.Equal(t, store.Resolved, r.state.deps.Publications.Workload(root.GetUID()).Record.OwnerResolution)
		})
	}
}

func TestGenericDiscoveryDoesNotPromoteUnresolvedOwner(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{})
	w := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "", true)
	child := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "child", "child")
	missing := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "missing", "missing")
	child.SetOwnerReferences([]metav1.OwnerReference{*metav1.NewControllerRef(missing, lifetimeDeploymentGVK)})
	require.NoError(t, r.state.applyWatchEvent(w, child, false))
	require.ErrorIs(t, r.resolveSelectedObjectRoot(t.Context(), objectJobFor(refOf(child))), discovery.ErrUnresolvedOwner)
	require.Nil(t, r.state.deps.Publications.Workload(child.GetUID()))
	require.Empty(t, r.state.roots)
}

func TestGenericDiscoveryAppliesRootResourceExclusions(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{ExcludeResourceRules: []ResourceRule{{APIGroups: []string{"batch"}, APIVersions: []string{"v1"}, Resources: []string{"jobs"}}}})
	ownerGVK := schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
	child := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "child", "child")
	owner := rootLifetimeObject(ownerGVK, "ns", "owner", "owner")
	child.SetOwnerReferences([]metav1.OwnerReference{*metav1.NewControllerRef(owner, ownerGVK)})
	childWatch := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "", true)
	ownerWatch := rootLifetimeWatch(t, r, ownerGVK, "", true)
	require.NoError(t, r.state.applyWatchEvent(ownerWatch, owner, false))
	require.NoError(t, r.state.applyWatchEvent(childWatch, child, false))
	drainRootLifetimeQueues(t, r)
	require.Nil(t, r.state.deps.Publications.Workload(child.GetUID()))
	require.Nil(t, r.state.deps.Publications.Workload(owner.GetUID()))
	require.NotNil(t, r.state.selectedObjectRoots[objectJobFor(refOf(child))].exclusion)
}

func TestRootLifetimeRetainsValuesDuringSourceFailure(t *testing.T) {
	for name, failure := range map[string]error{
		"pending":     discovery.ErrPending,
		"unavailable": errors.New("watch unavailable"),
		"forbidden":   apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "root", errors.New("denied")),
	} {
		t.Run(name, func(t *testing.T) {
			r := newRootLifetimeRuntime(t, Config{})
			w := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "", true)
			root := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "root", "root")
			require.NoError(t, r.state.applyWatchEvent(w, root, false))
			drainRootLifetimeQueues(t, r)
			r.state.setWatchHealth(w, failure)
			require.ErrorIs(t, r.reconcileWorkload(t.Context(), root.GetUID()), failure)
			result := r.state.deps.Publications.Workload(root.GetUID())
			require.True(t, result.HasValue)
			require.Equal(t, int64(100), result.Record.CreatedTimestamp)
			require.Equal(t, failureReason(failure), result.Error)
			r.state.setWatchHealth(w, nil)
			drainRootLifetimeQueues(t, r)
			require.Empty(t, r.state.deps.Publications.Workload(root.GetUID()).Error)
		})
	}
}

func TestRootLifetimeNamespaceExclusionAndReentry(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{
		NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"observe": "yes"}},
		RequiredKinds:     []schema.GroupVersionKind{lifetimeDeploymentGVK},
	})
	namespace := rootLifetimeObject(namespaceGVK, "", "ns", "namespace")
	namespace.SetLabels(map[string]string{"observe": "yes"})
	namespaceWatch := rootLifetimeWatch(t, r, namespaceGVK, "", true)
	require.NoError(t, r.state.applyWatchEvent(namespaceWatch, namespace, false))
	_, err := r.state.prepareWatchRequests()
	require.NoError(t, err)
	w := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "ns", true)
	root := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "root", "root")
	require.NoError(t, r.state.applyWatchEvent(w, root, false))
	drainRootLifetimeQueues(t, r)
	require.NotNil(t, r.state.deps.Publications.Workload(root.GetUID()))
	excluded := namespace.DeepCopy()
	excluded.SetLabels(nil)
	excluded.SetResourceVersion("2")
	require.NoError(t, r.state.applyWatchEvent(namespaceWatch, excluded, false))
	_, err = r.state.prepareWatchRequests()
	require.NoError(t, err)
	require.Nil(t, r.state.deps.Publications.Workload(root.GetUID()))
	require.NotContains(t, r.state.roots, root.GetUID())
	require.NotContains(t, r.state.watches, w.key)
	path := &discovery.GenericWorkload{Root: refOf(root), Path: []discovery.ObjectRef{refOf(root)}, Resolution: discovery.Resolved}
	require.ErrorIs(t, r.state.registerPodRoot(objectJobFor(refOf(root)), path), discovery.ErrStale)
	require.ErrorIs(t, r.state.registerSelectedObjectRoot(objectJobFor(refOf(root)), path, selectedDefinition{}, selectedDefinition{}), discovery.ErrStale)
	drainRootLifetimeQueues(t, r)
	require.NotContains(t, r.state.watches, w.key)
	namespace.SetResourceVersion("3")
	require.NoError(t, r.state.applyWatchEvent(namespaceWatch, namespace, false))
	requests, err := r.state.prepareWatchRequests()
	require.NoError(t, err)
	require.Contains(t, requests, discovery.WatchRequest{GVK: lifetimeDeploymentGVK, Namespace: "ns"})
	w = rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "ns", true)
	require.NoError(t, (&reflectorStore{controller: r.state, watch: w}).Replace([]any{root}, "3"))
	drainRootLifetimeQueues(t, r)
	require.NotNil(t, r.state.deps.Publications.Workload(root.GetUID()))
}

func TestGenericRootSelectorExclusionAndReentry(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{ObjectSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"observe": "yes"}}})
	w := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "", true)
	root := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "root", "root")
	for i, selected := range []bool{true, false, true} {
		current := root.DeepCopy()
		current.SetResourceVersion(string(rune('1' + i)))
		if selected {
			current.SetLabels(map[string]string{"observe": "yes"})
		}
		require.NoError(t, r.state.applyWatchEvent(w, current, false))
		drainRootLifetimeQueues(t, r)
		if selected {
			require.NotNil(t, r.state.deps.Publications.Workload(root.GetUID()))
		} else {
			require.Nil(t, r.state.deps.Publications.Workload(root.GetUID()))
		}
	}
}

func TestGenericMetadataDiscoveryWaitsForFullReplacement(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{})
	root := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "root", "root")
	metadataWatch := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "", false)
	require.NoError(t, (&reflectorStore{controller: r.state, watch: metadataWatch}).Replace([]any{root}, "1"))
	require.NoError(t, r.resolveSelectedObjectRoot(t.Context(), objectJobFor(refOf(root))))
	fullWatch := &resourceWatch{key: metadataWatch.key, gvk: lifetimeDeploymentGVK, full: true, objects: map[objectKey]struct{}{}, cancel: func() {}}
	r.state.replacements[fullWatch.key] = fullWatch
	require.ErrorIs(t, r.reconcileWorkload(t.Context(), root.GetUID()), discovery.ErrPending)
	require.NoError(t, (&reflectorStore{controller: r.state, watch: fullWatch}).Replace([]any{root}, "1"))
	drainRootLifetimeQueues(t, r)
	require.True(t, r.state.deps.Publications.Workload(root.GetUID()).HasValue)
	require.Len(t, r.state.roots, 1)
	require.NoError(t, r.state.applyWatchEvent(metadataWatch, root, true))
	drainRootLifetimeQueues(t, r)
	require.True(t, r.state.deps.Publications.Workload(root.GetUID()).HasValue)
}

func newRootLifetimeRuntime(t *testing.T, cfg Config) *Runtime {
	t.Helper()
	reader := &Reader{objectCache: &objectCache{objects: map[objectKey]cachedObject{}, sources: map[watchKey]cacheSource{}},
		clients: Clients{Mapper: meta.NewDefaultRESTMapper(nil)}, options: ReaderOptions{RequestTimeout: time.Second}}
	ownership, err := discovery.New(reader, discovery.Config{IgnoredTopOwners: cfg.IgnoredOwnerKinds})
	require.NoError(t, err)
	definitions, err := inventory.New(nil, inventory.Config{})
	require.NoError(t, err)
	resolver, err := labelresolver.New(labelresolver.Config{}, labelresolver.Dependencies{Compiler: labelevaluator.NewCompiler()})
	require.NoError(t, err)
	r, err := New(cfg, Dependencies{Discovery: ownership, Inventory: definitions, Labels: resolver, Publications: store.New()}, reader)
	require.NoError(t, err)
	t.Cleanup(func() {
		r.state.podQueue.ShutDown()
		r.state.workloadQueue.ShutDown()
		r.state.watchSyncQueue.ShutDown()
		r.state.stopWatches()
		r.state.wg.Wait()
	})
	return r
}

func rootLifetimeObject(gvk schema.GroupVersionKind, namespace, name string, uid types.UID) *unstructured.Unstructured {
	object := &unstructured.Unstructured{Object: map[string]any{
		"spec":   map[string]any{"replicas": int64(1)},
		"status": map[string]any{"replicas": int64(1), "readyReplicas": int64(1), "availableReplicas": int64(1), "updatedReplicas": int64(1)},
	}}
	object.SetGroupVersionKind(gvk)
	object.SetNamespace(namespace)
	object.SetName(name)
	object.SetUID(uid)
	object.SetResourceVersion("1")
	object.SetCreationTimestamp(metav1.NewTime(time.Unix(100, 0)))
	return object
}

func rootLifetimeWatch(t *testing.T, r *Runtime, gvk schema.GroupVersionKind, namespace string, full bool) *resourceWatch {
	t.Helper()
	resource := gvk.GroupVersion().WithResource(strings.ToLower(gvk.Kind) + "s")
	scope := meta.RESTScopeNamespace
	if gvk == namespaceGVK || gvk == kartaGVK {
		scope = meta.RESTScopeRoot
	}
	r.state.reader.clients.Mapper.(*meta.DefaultRESTMapper).AddSpecific(gvk, resource, resource, scope)
	key := watchKey{Resource: resource, Namespace: namespace}
	w := &resourceWatch{key: key, gvk: gvk, full: full, ready: true, objects: map[objectKey]struct{}{}, cancel: func() {}}
	r.state.watches[key] = w
	r.state.sources[key] = cacheSource{gvk: gvk, hasFullObject: full, synced: true}
	r.state.deps.Publications.SetSourceHealth(key, store.SourceHealth{GVK: gvk, Synced: true})
	return w
}

func drainRootLifetimeQueues(t *testing.T, r *Runtime) {
	t.Helper()
	for attempts := 0; r.state.podQueue.Len()+r.state.workloadQueue.Len() > 0; attempts++ {
		require.Less(t, attempts, 100, "queues did not settle")
		if r.state.podQueue.Len() > 0 {
			key, _ := r.state.podQueue.Get()
			err := r.handlePodInput(context.Background(), key)
			r.state.podQueue.Done(key)
			require.NoError(t, err)
		}
		if r.state.workloadQueue.Len() > 0 {
			key, _ := r.state.workloadQueue.Get()
			err := r.handleWorkloadInput(context.Background(), key)
			r.state.workloadQueue.Done(key)
			require.NoError(t, err)
		}
	}
}
