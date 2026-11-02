// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/discovery"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

type exclusionFixture struct {
	r                                    *Runtime
	values                               store.PublicationStore
	definitions                          *observedInventory
	definition                           *unstructured.Unstructured
	oldObject, replacement               *unstructured.Unstructured
	previousRoot, excludedRoot           *unstructured.Unstructured
	oldSource, newSource, excludedSource *resourceWatch
}

func TestHealthyOtherNamespaceCannotAuthorizeFailedOwnerCache(t *testing.T) {
	for _, otherNamespace := range []bool{false, true} {
		name := "without_other_namespace"
		if otherNamespace {
			name = "with_healthy_other_namespace"
		}
		t.Run(name, func(t *testing.T) {
			r, values := simpleRuntime(t, nil)
			r.state.config.NamespaceSelector = &metav1.LabelSelector{}
			r.state.scopes = map[string]bool{"a": true, "b": true}
			v1 := schema.GroupVersionKind{Group: "example.test", Version: "v1", Kind: "Task"}
			v2 := v1
			v2.Version = "v2"
			mapper := r.state.reader.clients.Mapper.(*meta.DefaultRESTMapper)
			for _, gvk := range []schema.GroupVersionKind{v1, v2} {
				gvr := gvk.GroupVersion().WithResource("tasks")
				mapper.AddSpecific(gvk, gvr, gvr, meta.RESTScopeNamespace)
			}
			accept := func(gvk schema.GroupVersionKind, namespace string, objects ...*unstructured.Unstructured) *resourceWatch {
				mapping, err := r.state.reader.restMapping(context.Background(), gvk)
				require.NoError(t, err)
				key := watchKey{Resource: mapping.Resource, Namespace: namespace}
				w := &resourceWatch{key: key, gvk: gvk, full: true, objects: map[objectKey]struct{}{}, cancel: func() {}}
				r.state.watches[key] = w
				items := make([]any, len(objects))
				for i, object := range objects {
					items[i] = object
				}
				require.NoError(t, (&reflectorStore{controller: r.state, watch: w}).Replace(items, "1"))
				return w
			}
			stale := simpleObject(v1, "root")
			stale.SetNamespace("a")
			parent := simpleObject(deploymentKind, "former-parent")
			parent.SetNamespace("a")
			simpleOwner(stale, parent)
			failed := accept(v1, "a", stale)
			r.state.setWatchHealth(failed, errors.New("v1 collection in namespace a unavailable"))
			current := stale.DeepCopy()
			current.SetGroupVersionKind(v2)
			current.SetResourceVersion("2")
			current.SetOwnerReferences(nil)
			accept(v2, "a", current)
			if otherNamespace {
				accept(v1, "b")
			}
			pod := simpleObject(podGVK, "pod")
			pod.SetNamespace("a")
			simpleOwner(pod, current)
			accept(podGVK, "a", pod)
			require.NoError(t, r.reconcilePod(context.Background(), objectJobFor(refOf(pod))))
			result := values.Pod(pod.GetUID())
			require.NotNil(t, result)
			require.True(t, result.HasValue)
			require.Equal(t, current.GetUID(), result.Record.Workload.UID)
		})
	}
}

func newExclusionFixture(t *testing.T, handover bool) exclusionFixture {
	t.Helper()
	r, values := simpleRuntime(t, nil)
	enableClusterDefinitions(t, r)
	v1 := schema.GroupVersionKind{Group: "example.test", Version: "v1", Kind: "Task"}
	v2 := v1
	v2.Version = "v2"
	for _, gvk := range []schema.GroupVersionKind{v1, v2} {
		gvr := gvk.GroupVersion().WithResource("tasks")
		r.state.reader.clients.Mapper.(*meta.DefaultRESTMapper).AddSpecific(gvk, gvr, gvr, meta.RESTScopeNamespace)
	}
	definition := definitionObject(t, "task")
	for key, value := range map[string]string{"group": v1.Group, "version": v1.Version, "kind": v1.Kind} {
		require.NoError(t, unstructured.SetNestedField(definition.Object, value, "spec", "structureDefinition", "rootComponent", "kind", key))
	}
	definitions := setDefinitions(t, r, definition)
	refreshSelection(t, r)
	r.state.config.ResourceRules = []ResourceRule{{APIGroups: []string{"apps"}, APIVersions: []string{"v1"}, Resources: []string{"deployments"}}}
	previousRoot := simpleObject(deploymentKind, "previous-root")
	excludedRoot := simpleObject(replicaSetKind, "excluded-root")
	seedSource(t, r, deploymentKind, true, previousRoot)
	excludedSource := seedSource(t, r, replicaSetKind, true, excludedRoot)
	seedSource(t, r, podGVK, true)
	first := simpleObject(v1, "selected-child")
	simpleOwner(first, previousRoot)
	oldSource := seedSource(t, r, v1, true, first)
	second := first.DeepCopy()
	second.SetGroupVersionKind(v2)
	newSource := seedSource(t, r, v2, true, second)
	settle(t, r)
	require.NotNil(t, values.Workload(previousRoot.GetUID()))
	require.Empty(t, values.Capture().Pods)
	if handover {
		definition = definition.DeepCopy()
		definition.SetResourceVersion("2")
		require.NoError(t, unstructured.SetNestedField(definition.Object, "v2", "spec", "structureDefinition", "rootComponent", "kind", "version"))
		definitions.replace(t, definition)
		refreshSelection(t, r)
	}
	first = first.DeepCopy()
	first.SetResourceVersion("2")
	simpleOwner(first, excludedRoot)
	second = first.DeepCopy()
	second.SetGroupVersionKind(v2)
	require.NoError(t, r.state.applyWatchEvent(oldSource, first, false))
	require.NoError(t, r.state.applyWatchEvent(newSource, second, false))
	return exclusionFixture{r: r, values: values, definitions: definitions, definition: definition,
		oldObject: first, replacement: second, previousRoot: previousRoot, excludedRoot: excludedRoot,
		oldSource: oldSource, newSource: newSource, excludedSource: excludedSource}
}

func TestExcludedReplacementCompletesHandoverAndRecovers(t *testing.T) {
	for _, handover := range []bool{false, true} {
		name := "same version"
		if handover {
			name = "version handover"
		}
		t.Run(name, func(t *testing.T) {
			f := newExclusionFixture(t, handover)
			ctx := context.Background()
			if handover {
				require.ErrorIs(t, f.r.resolveSelectedObjectRoot(ctx, objectJobFor(refOf(f.oldObject))), discovery.ErrPending)
				require.NoError(t, f.r.resolveSelectedObjectRoot(ctx, objectJobFor(refOf(f.replacement))))
			}
			require.NoError(t, f.r.resolveSelectedObjectRoot(ctx, objectJobFor(refOf(f.oldObject))))
			require.NoError(t, f.r.reconcileWorkload(ctx, f.previousRoot.GetUID()))
			require.Nil(t, f.values.Workload(f.previousRoot.GetUID()))
			require.Nil(t, f.values.Workload(f.excludedRoot.GetUID()))
			settle(t, f.r)
			require.Empty(t, f.values.Capture().Workloads)

			changed := f.oldObject.DeepCopy()
			changed.SetResourceVersion("3")
			simpleOwner(changed, f.previousRoot)
			require.NoError(t, f.r.state.applyWatchEvent(f.oldSource, changed, false))
			changed = changed.DeepCopy()
			changed.SetGroupVersionKind(f.replacement.GroupVersionKind())
			require.NoError(t, f.r.state.applyWatchEvent(f.newSource, changed, false))
			settle(t, f.r)
			require.NotNil(t, f.values.Workload(f.previousRoot.GetUID()))
			require.Empty(t, f.values.Workload(f.previousRoot.GetUID()).Error)
			require.Nil(t, f.values.Workload(f.excludedRoot.GetUID()))
		})
	}
}

func TestExcludedReplacementRejectsStaleInputs(t *testing.T) {
	for _, timing := range []string{"during discovery", "before handover"} {
		for _, change := range []string{"owner", "ancestor", "definition", "source health"} {
			t.Run(timing+"/"+change, func(t *testing.T) {
				f := newExclusionFixture(t, true)
				ctx := context.Background()
				key := objectJobFor(refOf(f.replacement))
				result, err := f.r.state.deps.Discovery.Get(ctx, refOf(f.replacement))
				require.NoError(t, err)
				mutate := func() {
					switch change {
					case "owner":
						changed := f.replacement.DeepCopy()
						changed.SetResourceVersion("3")
						simpleOwner(changed, f.previousRoot)
						require.NoError(t, f.r.state.applyWatchEvent(f.newSource, changed, false))
					case "ancestor":
						changed := f.excludedRoot.DeepCopy()
						changed.SetResourceVersion("3")
						simpleOwner(changed, f.previousRoot)
						require.NoError(t, f.r.state.applyWatchEvent(f.excludedSource, changed, false))
					case "definition":
						changed := f.definition.DeepCopy()
						changed.SetResourceVersion("3")
						f.definitions.replace(t, changed)
						refreshSelection(t, f.r)
					case "source health":
						f.r.state.setWatchHealth(f.excludedSource, errors.New("ancestor unavailable"))
					}
				}
				if timing == "during discovery" {
					discoverer := discovery.NewMockDiscovery(t)
					discoverer.EXPECT().Get(ctx, refOf(f.replacement)).RunAndReturn(func(context.Context, discovery.ObjectRef) (discovery.Result, error) {
						mutate()
						return result, nil
					}).Once()
					f.r.state.deps.Discovery = discoverer
					require.ErrorIs(t, f.r.resolveSelectedObjectRoot(ctx, key), discovery.ErrStale)
					require.ErrorIs(t, f.r.resolveSelectedObjectRoot(ctx, objectJobFor(refOf(f.oldObject))), discovery.ErrPending)
				} else {
					require.NoError(t, f.r.resolveSelectedObjectRoot(ctx, key))
					mutate()
					require.ErrorIs(t, f.r.resolveSelectedObjectRoot(ctx, objectJobFor(refOf(f.oldObject))), discovery.ErrStale)
				}
				require.NoError(t, f.r.reconcileWorkload(ctx, f.previousRoot.GetUID()))
				require.NotNil(t, f.values.Workload(f.previousRoot.GetUID()))
				require.Equal(t, f.previousRoot.GetUID(), f.r.state.selectedObjectRoots[objectJobFor(refOf(f.oldObject))].rootUID)
			})
		}
	}
}

func TestReplacementFailureRetainsPreviousMembership(t *testing.T) {
	for _, failure := range []string{"pending", "unavailable", "unresolved", "failed reevaluation"} {
		t.Run(failure, func(t *testing.T) {
			f := newExclusionFixture(t, true)
			ctx := context.Background()
			key := objectJobFor(refOf(f.replacement))
			var want error
			switch failure {
			case "pending":
				f.r.state.stateMu.Lock()
				source := f.r.state.sources[f.newSource.key]
				source.synced = false
				f.r.state.sources[f.newSource.key] = source
				f.r.state.stateMu.Unlock()
				want = discovery.ErrPending
			case "unavailable":
				want = errors.New("replacement unavailable")
				f.r.state.setWatchHealth(f.newSource, want)
			case "unresolved", "failed reevaluation":
				if failure == "failed reevaluation" {
					require.NoError(t, f.r.resolveSelectedObjectRoot(ctx, key))
				}
				want = discovery.ErrUnresolvedOwner
				discoverer := discovery.NewMockDiscovery(t)
				discoverer.EXPECT().Get(ctx, refOf(f.replacement)).Return(discovery.Result{}, want).Once()
				f.r.state.deps.Discovery = discoverer
			}
			require.ErrorIs(t, f.r.resolveSelectedObjectRoot(ctx, key), want)
			require.Error(t, f.r.resolveSelectedObjectRoot(ctx, objectJobFor(refOf(f.oldObject))))
			require.NoError(t, f.r.reconcileWorkload(ctx, f.previousRoot.GetUID()))
			require.NotNil(t, f.values.Workload(f.previousRoot.GetUID()))
			require.Equal(t, f.previousRoot.GetUID(), f.r.state.selectedObjectRoots[objectJobFor(refOf(f.oldObject))].rootUID)
		})
	}
}

func TestExcludedReplacementTrackingRemovedOnDeletionOrNamespaceRemoval(t *testing.T) {
	for _, deletion := range []bool{false, true} {
		name := "namespace removal"
		if deletion {
			name = "object deletion"
		}
		t.Run(name, func(t *testing.T) {
			f := newExclusionFixture(t, true)
			ctx := context.Background()
			key := objectJobFor(refOf(f.replacement))
			require.NoError(t, f.r.resolveSelectedObjectRoot(ctx, key))
			if deletion {
				require.NoError(t, f.r.state.applyWatchEvent(f.newSource, f.replacement, true))
			} else {
				f.r.state.config.NamespaceSelector = &metav1.LabelSelector{}
				f.r.state.scopes = map[string]bool{}
			}
			require.NoError(t, f.r.resolveSelectedObjectRoot(ctx, key))
			require.NotContains(t, f.r.state.selectedObjectRoots, key)
			require.NoError(t, f.r.resolveSelectedObjectRoot(ctx, objectJobFor(refOf(f.oldObject))))
			require.NoError(t, f.r.reconcileWorkload(ctx, f.previousRoot.GetUID()))
			require.Nil(t, f.values.Workload(f.previousRoot.GetUID()))
		})
	}
}
