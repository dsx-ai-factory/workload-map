// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/discovery"
	inventory "github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelevaluator"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
	api "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/instructions"
)

var associationTestGVK = schema.GroupVersionKind{Group: "scheduling.example.org", Version: "v1", Kind: "Workload"}

func TestPodWorkloadAssociationKeepsPhysicalComponentsAndSharesMetricIdentity(t *testing.T) {
	r, podWatch, targetWatch := newAssociationRuntime(t)
	client := fake.NewSimpleDynamicClient(kruntime.NewScheme())
	r.state.reader.clients.Dynamic = client
	r.state.deps.Labels = associationLabelResolver(t, `object.metadata.name`)
	target := rootLifetimeObject(associationTestGVK, "ns", "group", "group-uid")
	for _, name := range []string{"first", "second"} {
		pod := associationPod(name, types.UID(name))
		target.SetOwnerReferences(append(target.GetOwnerReferences(), associationOwner(pod)))
		require.NoError(t, r.state.applyWatchEvent(podWatch, pod, false))
	}
	require.NoError(t, unstructured.SetNestedField(target.Object, "Running", "status", "phase"))
	require.NoError(t, r.state.applyWatchEvent(targetWatch, target, false))
	drainRootLifetimeQueues(t, r)
	for _, uid := range []types.UID{"first", "second"} {
		result := r.state.deps.Publications.Pod(uid)
		require.True(t, result.HasValue)
		require.Empty(t, result.Error)
		require.Equal(t, storedRef(refOf(target)), result.Record.Workload)
		require.Equal(t, "pod", result.Record.Component)
		require.Equal(t, "group", result.Record.Labels["metric_workload"])
		require.Equal(t, uid, r.state.podRoots[uid], "physical ownership must remain unchanged")
	}
	workload := r.state.deps.Publications.Workload(target.GetUID())
	require.True(t, workload.Record.HasStatus)
	require.Equal(t, []string{"Running"}, workload.Record.Phases)
	require.Empty(t, client.Actions(), "association must read the existing cache without API requests")
}

func TestPodWorkloadAssociationLifecycle(t *testing.T) {
	r, podWatch, targetWatch := newAssociationRuntime(t)
	pod := associationPod("member", "pod-1")
	target := rootLifetimeObject(associationTestGVK, "ns", "group", "target-1")
	target.SetOwnerReferences([]metav1.OwnerReference{associationOwner(pod)})
	require.NoError(t, r.state.applyWatchEvent(podWatch, pod, false))
	drainRootLifetimeQueues(t, r)
	assertIdentity := func(want types.UID) {
		t.Helper()
		result := r.state.deps.Publications.Pod(pod.GetUID())
		require.NotNil(t, result)
		require.True(t, result.HasValue)
		if want == pod.GetUID() {
			require.Equal(t, store.ReasonUnresolvedOwner, result.Error)
		} else {
			require.Empty(t, result.Error)
		}
		require.Equal(t, want, result.Record.Workload.UID)
	}
	assertIdentity(pod.GetUID()) // A named target can be created after the Pod.
	require.NoError(t, r.state.applyWatchEvent(targetWatch, target, false))
	drainRootLifetimeQueues(t, r)
	assertIdentity(target.GetUID())

	target = target.DeepCopy()
	target.SetResourceVersion("2")
	target.SetOwnerReferences(nil)
	require.NoError(t, r.state.applyWatchEvent(targetWatch, target, false))
	drainRootLifetimeQueues(t, r)
	assertIdentity(pod.GetUID())
	target = target.DeepCopy()
	target.SetResourceVersion("3")
	target.SetOwnerReferences([]metav1.OwnerReference{associationOwner(pod)})
	require.NoError(t, r.state.applyWatchEvent(targetWatch, target, false))
	drainRootLifetimeQueues(t, r)
	assertIdentity(target.GetUID())

	target = target.DeepCopy()
	target.SetUID("target-2")
	target.SetResourceVersion("4")
	require.NoError(t, r.state.applyWatchEvent(targetWatch, target, false))
	drainRootLifetimeQueues(t, r)
	assertIdentity(target.GetUID())
	require.Nil(t, r.state.deps.Publications.Workload("target-1"))

	pod = pod.DeepCopy()
	pod.SetUID("pod-2")
	pod.SetResourceVersion("2")
	require.NoError(t, r.state.applyWatchEvent(podWatch, pod, false))
	drainRootLifetimeQueues(t, r)
	assertIdentity(pod.GetUID()) // Same name is insufficient when the Pod UID changed.
	target = target.DeepCopy()
	target.SetResourceVersion("5")
	target.SetOwnerReferences([]metav1.OwnerReference{associationOwner(pod)})
	require.NoError(t, r.state.applyWatchEvent(targetWatch, target, false))
	drainRootLifetimeQueues(t, r)
	assertIdentity(target.GetUID())

	pod = pod.DeepCopy()
	pod.SetResourceVersion("3")
	pod.SetLabels(nil)
	require.NoError(t, r.state.applyWatchEvent(podWatch, pod, false))
	drainRootLifetimeQueues(t, r)
	assertIdentity(pod.GetUID())
	pod = pod.DeepCopy()
	pod.SetResourceVersion("4")
	pod.SetLabels(map[string]string{"example.org/group": "other-group"})
	require.NoError(t, r.state.applyWatchEvent(podWatch, pod, false))
	drainRootLifetimeQueues(t, r)
	assertIdentity(pod.GetUID())
	target = target.DeepCopy()
	target.SetName("other-group")
	target.SetUID("target-3")
	target.SetResourceVersion("6")
	require.NoError(t, r.state.applyWatchEvent(targetWatch, target, false))
	drainRootLifetimeQueues(t, r)
	assertIdentity(target.GetUID())
	require.NoError(t, r.state.applyWatchEvent(targetWatch, target, true))
	drainRootLifetimeQueues(t, r)
	assertIdentity(pod.GetUID())
}

func TestPodWorkloadAssociationRequiresSelectedTargetAndBarePod(t *testing.T) {
	for _, scenario := range []string{"disabled", "no target definition", "excluded resource", "target selector", "Pod selector", "wrong namespace", "wrong Pod name", "target controller owner", "Pod controller owner", "ignored Pod owner"} {
		t.Run(scenario, func(t *testing.T) {
			r, podWatch, targetWatch := newAssociationRuntime(t)
			pod := associationPod("member", "pod")
			target := rootLifetimeObject(associationTestGVK, "ns", "group", "target")
			target.SetOwnerReferences([]metav1.OwnerReference{associationOwner(pod)})
			want := pod.GetUID()
			switch scenario {
			case "disabled":
				r.state.config.PodWorkloadAssociation = nil
			case "no target definition":
				delete(r.state.selectedDefinitions, associationTestGVK.GroupKind())
			case "excluded resource":
				r.state.config.ExcludeResourceRules = []ResourceRule{{APIGroups: []string{associationTestGVK.Group}, APIVersions: []string{"v1"}, Resources: []string{"workloads"}}}
			case "target selector":
				var err error
				r.state.objectSelectors, err = compileObjectSelectors([]ObjectSelector{{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"example.org/group": "group"}}}})
				require.NoError(t, err)
			case "Pod selector":
				r.state.associationSelector, _ = selector(&metav1.LabelSelector{MatchLabels: map[string]string{"example.org/managed": "true"}})
			case "wrong namespace":
				target.SetNamespace("other")
			case "wrong Pod name":
				owner := associationOwner(pod)
				owner.Name = "different"
				target.SetOwnerReferences([]metav1.OwnerReference{owner})
			default:
				jobGVK := schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
				job := rootLifetimeObject(jobGVK, "ns", "job", "job")
				jobWatch := rootLifetimeWatch(t, r, jobGVK, "", true)
				require.NoError(t, r.state.applyWatchEvent(jobWatch, job, false))
				owner := *metav1.NewControllerRef(job, jobGVK)
				if scenario == "target controller owner" {
					target.SetOwnerReferences(append(target.GetOwnerReferences(), owner))
				} else {
					pod.SetOwnerReferences([]metav1.OwnerReference{owner})
					want = job.GetUID()
					if scenario == "ignored Pod owner" {
						r.state.config.IgnoredOwnerKinds = []schema.GroupVersionKind{jobGVK}
						var err error
						r.state.deps.Discovery, err = discovery.New(r.state.reader, discovery.Config{IgnoredTopOwners: []schema.GroupVersionKind{jobGVK}})
						require.NoError(t, err)
						want = pod.GetUID()
					}
				}
			}
			require.NoError(t, r.state.applyWatchEvent(podWatch, pod, false))
			require.NoError(t, r.state.applyWatchEvent(targetWatch, target, false))
			drainRootLifetimeQueues(t, r)
			require.Equal(t, want, r.state.deps.Publications.Pod(pod.GetUID()).Record.Workload.UID)
		})
	}
}

func TestPodWorkloadAssociationSourceFailureAndDefinitionChanges(t *testing.T) {
	r, podWatch, targetWatch := newAssociationRuntime(t)
	pod := associationPod("member", "pod")
	target := rootLifetimeObject(associationTestGVK, "ns", "group", "target")
	target.SetOwnerReferences([]metav1.OwnerReference{associationOwner(pod)})
	require.NoError(t, r.state.applyWatchEvent(podWatch, pod, false))
	require.NoError(t, r.state.applyWatchEvent(targetWatch, target, false))
	drainRootLifetimeQueues(t, r)
	r.state.setWatchHealth(targetWatch, apierrors.NewForbidden(targetWatch.key.Resource.GroupResource(), "", errors.New("denied")))
	publications := r.state.deps.Publications.(store.PublicationStore)
	view := publications.Capture()
	valid, reason := view.Valid(publications.Pod(pod.GetUID()).Sources, "")
	require.False(t, valid, "saved identity must become invalid before a Pod worker runs")
	require.Equal(t, store.ReasonPermissionDenied, reason)
	require.NoError(t, r.reconcilePod(t.Context(), objectJobFor(refOf(pod))))
	result := publications.Pod(pod.GetUID())
	require.True(t, result.HasValue)
	require.Equal(t, pod.GetUID(), result.Record.Workload.UID)
	require.Equal(t, store.ReasonPermissionDenied, result.Error)
	r.state.setWatchHealth(targetWatch, nil)
	drainRootLifetimeQueues(t, r)
	require.Equal(t, target.GetUID(), publications.Pod(pod.GetUID()).Record.Workload.UID)
	require.Empty(t, publications.Pod(pod.GetUID()).Error)

	targetDefinition := r.state.selectedDefinitions[associationTestGVK.GroupKind()]
	next := map[schema.GroupKind]selectedDefinition{podGVK.GroupKind(): r.state.selectedDefinitions[podGVK.GroupKind()]}
	r.state.stateMu.Lock()
	err := r.state.refreshDefinitions(next, nil)
	r.state.stateMu.Unlock()
	require.NoError(t, err)
	drainRootLifetimeQueues(t, r)
	require.Equal(t, pod.GetUID(), publications.Pod(pod.GetUID()).Record.Workload.UID)
	next = map[schema.GroupKind]selectedDefinition{podGVK.GroupKind(): next[podGVK.GroupKind()], associationTestGVK.GroupKind(): targetDefinition}
	r.state.stateMu.Lock()
	err = r.state.refreshDefinitions(next, nil)
	r.state.stateMu.Unlock()
	require.NoError(t, err)
	drainRootLifetimeQueues(t, r)
	require.Equal(t, target.GetUID(), publications.Pod(pod.GetUID()).Record.Workload.UID)
}

func TestPodWorkloadAssociationRejectsStalePublicationAndOldIdentityRetention(t *testing.T) {
	r, podWatch, targetWatch := newAssociationRuntime(t)
	pod := associationPod("member", "pod")
	target := rootLifetimeObject(associationTestGVK, "ns", "group", "target")
	target.SetOwnerReferences([]metav1.OwnerReference{associationOwner(pod)})
	require.NoError(t, r.state.applyWatchEvent(podWatch, pod, false))
	drainRootLifetimeQueues(t, r)
	selection, err := r.state.lookupDefinition(podGVK)
	require.NoError(t, err)
	path := []discovery.ObjectRef{refOf(pod)}
	workload := &discovery.GenericWorkload{Root: refOf(pod), Path: path, Resolution: discovery.Resolved}
	_, missing, _, err := r.resolvePodAssociation(t.Context(), pod, pod, workload)
	require.ErrorIs(t, err, ErrInvalidPodAssociation)
	fallback := *r.state.deps.Publications.Pod(pod.GetUID())
	require.NoError(t, r.state.applyWatchEvent(targetWatch, target, false))
	require.ErrorIs(t, r.state.publishPod(path, refOf(pod), selection, fallback, false, missing), discovery.ErrStale)
	drainRootLifetimeQueues(t, r)
	_, snapshot, _, err := r.resolvePodAssociation(t.Context(), pod, pod, workload)
	require.NoError(t, err)
	previous := *r.state.deps.Publications.Pod(pod.GetUID())
	target = target.DeepCopy()
	target.SetResourceVersion("2")
	target.SetOwnerReferences(nil)
	require.NoError(t, r.state.applyWatchEvent(targetWatch, target, false))
	require.ErrorIs(t, r.state.publishPod(path, refOf(pod), selection, previous, false, snapshot), discovery.ErrStale)
	r.state.deps.Labels = associationLabelResolver(t, `object.missing`)
	drainRootLifetimeQueues(t, r)
	result := r.state.deps.Publications.Pod(pod.GetUID())
	require.True(t, result.HasValue)
	require.Equal(t, pod.GetUID(), result.Record.Workload.UID, "label failure must not restore a removed association")
	require.Equal(t, store.ReasonUnresolvedOwner, result.Error)
}

func newAssociationRuntime(t *testing.T) (*Runtime, *resourceWatch, *resourceWatch) {
	t.Helper()
	r := newRootLifetimeRuntime(t, Config{PodWorkloadAssociation: &PodWorkloadAssociation{
		Target: AssociationTarget{APIVersion: associationTestGVK.GroupVersion().String(), Kind: associationTestGVK.Kind, NameFromLabel: "example.org/group"},
	}})
	podDefinition, err := catalog.Get(podGVK)
	require.NoError(t, err)
	targetDefinition := podDefinition.DeepCopy()
	targetDefinition.Name = "group-workload"
	targetDefinition.Spec.StructureDefinition.RootComponent.Name = "group"
	targetDefinition.Spec.StructureDefinition.RootComponent.Kind = &api.GroupVersionKind{Group: associationTestGVK.Group, Version: associationTestGVK.Version, Kind: associationTestGVK.Kind}
	targetDefinition.Spec.StructureDefinition.RootComponent.SpecDefinition = nil
	for gvk, definition := range map[schema.GroupVersionKind]*api.Karta{podGVK: podDefinition, associationTestGVK: targetDefinition} {
		summary, err := instructions.NewStructureSummary(definition)
		require.NoError(t, err)
		r.state.selectedDefinitions[gvk.GroupKind()] = selectedDefinition{Selection: inventory.Selection{
			Definition: definition, GVK: gvk, Origin: inventory.Origin{Name: definition.Name, Source: inventory.SourceCatalog},
		}, summary: summary}
	}
	return r, rootLifetimeWatch(t, r, podGVK, "", true), rootLifetimeWatch(t, r, associationTestGVK, "", true)
}

func associationPod(name string, uid types.UID) *unstructured.Unstructured {
	pod := rootLifetimeObject(podGVK, "ns", name, uid)
	pod.SetLabels(map[string]string{"example.org/group": "group"})
	return pod
}

func associationOwner(pod *unstructured.Unstructured) metav1.OwnerReference {
	return metav1.OwnerReference{APIVersion: "v1", Kind: "Pod", Name: pod.GetName(), UID: pod.GetUID()}
}

func associationLabelResolver(t *testing.T, expression string) labelresolver.Resolver {
	t.Helper()
	resolver, err := labelresolver.New(labelresolver.Config{Rules: []labelresolver.LabelRule{{
		Name: "metric_workload", Metrics: []string{labelresolver.MetricPodWorkloadInfo}, Sources: []labelresolver.Source{labelresolver.SourceWorkload}, Expression: expression,
	}}}, labelresolver.Dependencies{Compiler: labelevaluator.NewCompiler()})
	require.NoError(t, err)
	return resolver
}
