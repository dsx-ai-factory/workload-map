// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/discovery"
	inventory "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog"
)

func definitionObject(t *testing.T, name string) *unstructured.Unstructured {
	t.Helper()
	definition, err := catalog.Get(jobKind)
	require.NoError(t, err)
	definition.Name = name
	definition.UID = types.UID(name)
	definition.ResourceVersion = "1"
	definition.CreationTimestamp = metav1.NewTime(time.Unix(1, 0))
	object, err := kruntime.DefaultUnstructuredConverter.ToUnstructured(definition)
	require.NoError(t, err)
	result := &unstructured.Unstructured{Object: object}
	result.SetGroupVersionKind(kartaGVK)
	return result
}
func enableClusterDefinitions(t *testing.T, r *Runtime) {
	t.Helper()
	r.state.config.ClusterDefinitions = true
	r.state.deps.Inventory = &observedInventory{changes: make(chan struct{}, 1)}
}
func refreshSelection(t *testing.T, r *Runtime) {
	t.Helper()
	r.state.stateMu.Lock()
	err := r.state.refreshDefinitionsLocked()
	r.state.stateMu.Unlock()
	require.NoError(t, err)
}
func definedPair(t *testing.T, r *Runtime) (*unstructured.Unstructured, *unstructured.Unstructured) {
	t.Helper()
	root, pod := simpleObject(jobKind, "root"), simpleObject(podGVK, "pod")
	root.Object["spec"] = map[string]any{"parallelism": int64(1)}
	root.Object["status"] = map[string]any{"active": int64(1), "ready": int64(1)}
	simpleOwner(pod, root)
	seedSource(t, r, jobKind, true, root)
	seedSource(t, r, podGVK, true, pod)
	settle(t, r)
	return root, pod
}

func TestDefinitionReadFailurePreservesSelectionAndCreation(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	enableClusterDefinitions(t, r)
	definitions := setDefinitions(t, r, definitionObject(t, "cluster-job"))
	refreshSelection(t, r)
	root, pod := definedPair(t, r)
	beforePod, beforeRoot := values.Pod(pod.GetUID()), values.Workload(root.GetUID())
	require.Equal(t, "cluster-job", beforePod.Record.Karta)
	require.Equal(t, "cluster-job", beforeRoot.Record.Karta)
	r.state.stateMu.Lock()
	definitions.setError(inventory.ErrUnavailable)
	err := r.state.refreshDefinitionsLocked()
	r.state.stateMu.Unlock()
	require.Error(t, err)
	require.Error(t, r.reconcilePod(context.Background(), objectJobFor(refOf(pod))))
	require.NoError(t, r.resolveSelectedObjectRoot(context.Background(), objectJobFor(refOf(root))))
	require.NoError(t, r.reconcileWorkload(context.Background(), root.GetUID()))
	after := values.Workload(root.GetUID())
	require.Equal(t, beforeRoot.Record, after.Record)
	require.Empty(t, after.Error)
	require.Equal(t, store.ReasonAPIUnavailable, after.StatusError)
	valid, _ := values.Capture().Valid(after.CreationSources, after.Error)
	require.True(t, valid)
	require.Equal(t, beforePod.Record, values.Pod(pod.GetUID()).Record)
	r.state.stateMu.Lock()
	definitions.setError(nil)
	r.state.stateMu.Unlock()
	refreshSelection(t, r)
	settle(t, r)
	require.Empty(t, values.Workload(root.GetUID()).StatusError)
	require.Empty(t, values.Pod(pod.GetUID()).Error)
}
func TestInvalidReplacementRetainsOldProvenanceUntilSuccess(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	enableClusterDefinitions(t, r)
	first := definitionObject(t, "first")
	definitions := setDefinitions(t, r, first)
	refreshSelection(t, r)
	root, pod := definedPair(t, r)
	bad := definitionObject(t, "replacement")
	require.NoError(t, unstructured.SetNestedField(bad.Object, "", "spec", "structureDefinition", "rootComponent", "name"))
	definitions.replace(t, bad)
	refreshSelection(t, r)
	selected, err := r.state.lookupDefinition(jobKind)
	require.ErrorIs(t, err, inventory.ErrInvalidDefinition)
	require.Equal(t, "replacement", selected.Origin.Name)
	require.NoError(t, r.reconcilePod(context.Background(), objectJobFor(refOf(pod))))
	require.NoError(t, r.reconcileWorkload(context.Background(), root.GetUID()))
	require.Equal(t, "first", values.Pod(pod.GetUID()).Record.Karta)
	require.Equal(t, "first", values.Workload(root.GetUID()).Record.Karta)
	require.Equal(t, store.ReasonInvalidDefinition, values.Pod(pod.GetUID()).Error)
	good := definitionObject(t, "replacement")
	good.SetResourceVersion("3")
	definitions.replace(t, good)
	refreshSelection(t, r)
	settle(t, r)
	require.Equal(t, "replacement", values.Pod(pod.GetUID()).Record.Karta)
	require.Equal(t, "replacement", values.Workload(root.GetUID()).Record.Karta)
}

func TestDefinitionStructureChangeUpdatesExistingPodMapping(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	enableClusterDefinitions(t, r)
	definition := definitionObject(t, "job")
	definitions := setDefinitions(t, r, definition)
	refreshSelection(t, r)
	_, pod := definedPair(t, r)
	require.Equal(t, "job", values.Pod(pod.GetUID()).Record.Component)
	previous, err := r.state.lookupDefinition(jobKind)
	require.NoError(t, err)

	changed := definition.DeepCopy()
	changed.SetResourceVersion("2")
	require.NoError(t, unstructured.SetNestedField(changed.Object, "worker", "spec", "structureDefinition", "rootComponent", "name"))
	unstructured.RemoveNestedField(changed.Object, "spec", "optimizationInstructions", "gangScheduling")
	definitions.replace(t, changed)
	refreshSelection(t, r)
	_, err = r.state.lookupDefinition(jobKind)
	require.NoError(t, err)
	settle(t, r)
	require.Equal(t, "worker", values.Pod(pod.GetUID()).Record.Component)
	require.Empty(t, values.Pod(pod.GetUID()).Error)
	require.Equal(t, "job", previous.summary.GetKarta().Spec.StructureDefinition.RootComponent.Name)
}
func TestDefinitionRemovalReconcilesExistingPodAndZeroPodCandidate(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	enableClusterDefinitions(t, r)
	definitions := setDefinitions(t, r, definitionObject(t, "job"))
	refreshSelection(t, r)
	root, pod := definedPair(t, r)
	empty := simpleObject(jobKind, "empty")
	mapping, err := r.state.reader.restMapping(context.Background(), jobKind)
	require.NoError(t, err)
	require.NoError(t, r.state.applyWatchEvent(r.state.watches[watchKey{Resource: mapping.Resource}], empty, false))
	settle(t, r)
	require.NotNil(t, values.Workload(empty.GetUID()))
	definitions.replace(t)
	refreshSelection(t, r)
	settle(t, r)
	require.Nil(t, values.Workload(empty.GetUID()))
	require.NotNil(t, values.Workload(root.GetUID()))
	require.Empty(t, values.Pod(pod.GetUID()).Record.Karta)
	require.Equal(t, "<unknown>", values.Pod(pod.GetUID()).Record.Component)
}
func TestRootSelectorRestorationRediscoversRemovedMembership(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	root, pod, source, _ := seedPair(t, r)
	r.state.objectSelector, _ = selector(&metav1.LabelSelector{MatchLabels: map[string]string{"selected": "true"}})
	require.NoError(t, r.reconcilePod(context.Background(), objectJobFor(refOf(pod))))
	require.NoError(t, r.reconcileWorkload(context.Background(), root.GetUID()))
	require.Nil(t, values.Pod(pod.GetUID()))
	changed := root.DeepCopy()
	changed.SetResourceVersion("2")
	changed.SetLabels(map[string]string{"selected": "true"})
	require.NoError(t, r.state.applyWatchEvent(source, changed, false))
	settle(t, r)
	require.NotNil(t, values.Pod(pod.GetUID()))
	require.NotNil(t, values.Workload(root.GetUID()))
}

func TestInitialDefinitionFailureStillPublishesConfirmedRootCreation(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprint(invalid), func(t *testing.T) {
			r, values := simpleRuntime(t, nil)
			enableClusterDefinitions(t, r)
			definition := definitionObject(t, "job")
			if invalid {
				require.NoError(t, unstructured.SetNestedField(definition.Object, "", "spec", "structureDefinition", "rootComponent", "name"))
			}
			definitions := setDefinitions(t, r, definition)
			if invalid {
				refreshSelection(t, r)
			} else {
				r.state.stateMu.Lock()
				definitions.setError(inventory.ErrPermissionDenied)
				require.Error(t, r.state.refreshDefinitionsLocked())
				r.state.stateMu.Unlock()
			}
			root, pod := simpleObject(jobKind, "root"), simpleObject(podGVK, "pod")
			simpleOwner(pod, root)
			seedSource(t, r, jobKind, true, root)
			seedSource(t, r, podGVK, true, pod)
			err := r.reconcilePod(context.Background(), objectJobFor(refOf(pod)))
			if invalid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.NoError(t, r.reconcileWorkload(context.Background(), root.GetUID()))
			row := values.Workload(root.GetUID())
			require.NotNil(t, row)
			require.True(t, row.HasValue)
			require.Empty(t, row.Error)
			require.NotEmpty(t, row.StatusError)
			valid, _ := values.Capture().Valid(row.CreationSources, row.Error)
			require.True(t, valid)
			require.False(t, values.Pod(pod.GetUID()).HasValue)
			require.Equal(t, root.GetUID(), values.Pod(pod.GetUID()).Record.Workload.UID)
		})
	}
}

func TestGenericPodVersionsAndSelectedVersionScope(t *testing.T) {
	for _, test := range []struct {
		name            string
		onlyV2, defined bool
	}{{"both API versions", false, false}, {"v2-only resource rule", true, false}, {"selected Karta supplies one version", false, true}} {
		t.Run(test.name, func(t *testing.T) {
			r, values := simpleRuntime(t, nil)
			v2 := jobKind
			v2.Version = "v2"
			gvr := v2.GroupVersion().WithResource("jobs")
			r.state.reader.clients.Mapper.(*meta.DefaultRESTMapper).AddSpecific(v2, gvr, gvr, meta.RESTScopeNamespace)
			if test.defined {
				selectCatalog(t, r, "batch-job-v1")
			}
			first := simpleObject(jobKind, "root")
			second := first.DeepCopy()
			second.SetGroupVersionKind(v2)
			one, two := simpleObject(podGVK, "one"), simpleObject(podGVK, "two")
			simpleOwner(one, first)
			simpleOwner(two, second)
			seedSource(t, r, jobKind, true, first)
			seedSource(t, r, v2, true, second)
			seedSource(t, r, podGVK, true, one, two)
			if test.onlyV2 {
				r.state.config.ResourceRules = []ResourceRule{{APIGroups: []string{"batch"}, APIVersions: []string{"v2"}, Resources: []string{"jobs"}}}
			}
			for _, pod := range []*unstructured.Unstructured{one, two, one, two} {
				require.NoError(t, r.reconcilePod(context.Background(), objectJobFor(refOf(pod))))
			}
			require.NoError(t, r.reconcileWorkload(context.Background(), first.GetUID()))
			require.NotNil(t, values.Workload(first.GetUID()))
			require.Len(t, values.Capture().Workloads, 1)
			expectedSecond := v2
			if test.defined {
				expectedSecond = jobKind
			}
			require.Equal(t, expectedSecond, values.Pod(two.GetUID()).Record.Workload.GVK)
			if test.defined {
				require.Contains(t, values.Pod(two.GetUID()).Sources, watchKey{Resource: jobKind.GroupVersion().WithResource("jobs")})
				require.Contains(t, values.Pod(two.GetUID()).Sources, watchKey{Resource: v2.GroupVersion().WithResource("jobs")})
			}
			if test.onlyV2 {
				require.Nil(t, values.Pod(one.GetUID()))
				require.Equal(t, v2, values.Workload(first.GetUID()).Record.Ref.GVK)
			} else {
				require.Equal(t, jobKind, values.Pod(one.GetUID()).Record.Workload.GVK)
				require.Equal(t, jobKind, values.Workload(first.GetUID()).Record.Ref.GVK)
			}
		})
	}
}

func TestSelectedVersionHandoverWaitsForReplacementAndUsesItsBoundary(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	enableClusterDefinitions(t, r)
	definition := definitionObject(t, "job")
	definitions := setDefinitions(t, r, definition)
	refreshSelection(t, r)
	root := simpleObject(jobKind, "root")
	old := seedSource(t, r, jobKind, true, root)
	seedSource(t, r, podGVK, true)
	settle(t, r)
	require.NotNil(t, values.Workload(root.GetUID()))
	v2 := jobKind
	v2.Version = "v2"
	gvr := v2.GroupVersion().WithResource("jobs")
	r.state.reader.clients.Mapper.(*meta.DefaultRESTMapper).AddSpecific(v2, gvr, gvr, meta.RESTScopeNamespace)
	next := seedSource(t, r, v2, true)
	next.ready = false
	r.state.reader.sources[next.key] = cacheSource{gvk: v2, hasFullObject: true}
	updated := definition.DeepCopy()
	updated.SetResourceVersion("2")
	require.NoError(t, unstructured.SetNestedField(updated.Object, "v2", "spec", "structureDefinition", "rootComponent", "kind", "version"))
	definitions.replace(t, updated)
	refreshSelection(t, r)
	require.ErrorIs(t, r.resolveSelectedObjectRoot(context.Background(), objectJobFor(refOf(root))), discovery.ErrPending)
	require.NotNil(t, values.Workload(root.GetUID()))
	require.NotEmpty(t, r.state.selectedObjectRoots[objectJobFor(refOf(root))])
	oldOwned := root.DeepCopy()
	oldOwned.SetResourceVersion("2")
	simpleOwner(oldOwned, simpleObject(jobKind, "missing"))
	require.NoError(t, r.state.applyWatchEvent(old, oldOwned, false))
	r.state.stateMu.Lock()
	r.state.setWatchHealthLocked(old, errors.New("version retired"))
	r.state.stateMu.Unlock()
	replacement := root.DeepCopy()
	replacement.SetGroupVersionKind(v2)
	require.NoError(t, (&reflectorStore{controller: r.state, watch: next}).Replace([]any{replacement}, "3"))
	require.NoError(t, r.resolveSelectedObjectRoot(context.Background(), objectJobFor(refOf(replacement))))
	require.NoError(t, r.resolveSelectedObjectRoot(context.Background(), objectJobFor(refOf(root))))
	require.NoError(t, r.reconcileWorkload(context.Background(), root.GetUID()))
	require.Equal(t, v2, values.Workload(root.GetUID()).Record.Ref.GVK)
	require.Empty(t, values.Workload(root.GetUID()).Error)
}

func TestResolvedOwnerReplacesUnknownIdentityBeforeFirstMappingSucceeds(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	selectCatalog(t, r, "batch-job-v1")
	root, pod := simpleObject(jobKind, "root"), simpleObject(podGVK, "pod")
	simpleOwner(pod, root)
	pod.Object["spec"] = map[string]any{"containers": "invalid container list"}
	roots := seedSource(t, r, jobKind, true)
	roots.ready = false
	r.state.reader.sources[roots.key] = cacheSource{gvk: jobKind, hasFullObject: true}
	seedSource(t, r, podGVK, true, pod)
	require.ErrorIs(t, r.reconcilePod(context.Background(), objectJobFor(refOf(pod))), discovery.ErrPending)
	require.False(t, values.Pod(pod.GetUID()).HasValue)
	require.Empty(t, values.Pod(pod.GetUID()).Record.Workload.UID)
	require.NoError(t, (&reflectorStore{controller: r.state, watch: roots}).Replace([]any{root}, "2"))
	require.NoError(t, r.reconcilePod(context.Background(), objectJobFor(refOf(pod))))
	failure := values.Pod(pod.GetUID())
	require.False(t, failure.HasValue)
	require.Equal(t, store.ReasonEvaluationFailed, failure.Error)
	require.Equal(t, root.GetUID(), failure.Record.Workload.UID)
	require.Equal(t, root.GetName(), failure.Record.Workload.Name)
}

func TestGenericCanonicalVersionRecoversAfterElectionRemoval(t *testing.T) {
	for _, condition := range []string{"healthy", "failed", "retired"} {
		t.Run(condition, func(t *testing.T) {
			r, values := simpleRuntime(t, nil)
			enableClusterDefinitions(t, r)
			v2 := jobKind
			v2.Version = "v2"
			gvr := v2.GroupVersion().WithResource("jobs")
			r.state.reader.clients.Mapper.(*meta.DefaultRESTMapper).AddSpecific(v2, gvr, gvr, meta.RESTScopeNamespace)
			definition := definitionObject(t, "job-v2")
			require.NoError(t, unstructured.SetNestedField(definition.Object, "v2", "spec", "structureDefinition", "rootComponent", "kind", "version"))
			definitions := setDefinitions(t, r, definition)
			refreshSelection(t, r)
			first := simpleObject(jobKind, "root")
			second := first.DeepCopy()
			second.SetGroupVersionKind(v2)
			pod := simpleObject(podGVK, "pod")
			simpleOwner(pod, first)
			seedSource(t, r, jobKind, true, first)
			alternate := seedSource(t, r, v2, true, second)
			seedSource(t, r, podGVK, true, pod)
			settle(t, r)
			require.Equal(t, v2, values.Workload(first.GetUID()).Record.Ref.GVK)
			definitions.replace(t)
			refreshSelection(t, r)
			require.NoError(t, r.resolveSelectedObjectRoot(context.Background(), objectJobFor(refOf(second))))
			r.state.stateMu.Lock()
			switch condition {
			case "failed":
				r.state.setWatchHealthLocked(alternate, errors.New("old API version unavailable"))
			case "retired":
				r.state.retireWatchLocked(alternate)
			}
			r.state.stateMu.Unlock()
			emptyQueue(r.state.workloadQueue)
			require.NoError(t, r.reconcilePod(context.Background(), objectJobFor(refOf(pod))))
			expected := jobKind
			if condition == "healthy" {
				expected = v2
			} else {
				require.NotZero(t, r.state.workloadQueue.Len(), "changing the canonical version must enqueue its unchanged UID")
			}
			require.NoError(t, r.reconcileWorkload(context.Background(), first.GetUID()))
			result := values.Workload(first.GetUID())
			require.Equal(t, expected, result.Record.Ref.GVK)
			require.Empty(t, result.Record.Karta)
			valid, reason := values.Capture().Valid(result.CreationSources, result.Error)
			require.True(t, valid, reason)
			require.Len(t, values.Capture().Workloads, 1)
			require.Equal(t, jobKind, values.Pod(pod.GetUID()).Record.Workload.GVK)
		})
	}
}
