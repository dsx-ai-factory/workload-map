// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/discovery"
	inventory "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog"
)

type observedInventory struct {
	mu      sync.Mutex
	results []inventory.SelectionResult
	err     error
	changes chan struct{}
	reads   int
}

type countingLabels struct {
	mu    sync.Mutex
	calls map[types.UID]map[string]int
}

func (i *observedInventory) List() ([]inventory.SelectionResult, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.reads++
	return append([]inventory.SelectionResult{}, i.results...), i.err
}

func (i *observedInventory) Changes() <-chan struct{} { return i.changes }

func (i *observedInventory) readCount() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.reads
}

func (l *countingLabels) Resolve(_ context.Context, input labelresolver.Input) ([]labelresolver.ResolvedMetricLabels, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	uid := input.Workload.GetUID()
	if l.calls[uid] == nil {
		l.calls[uid] = map[string]int{}
	}
	results := make([]labelresolver.ResolvedMetricLabels, 0, len(input.Metrics))
	for _, metric := range input.Metrics {
		l.calls[uid][metric]++
		results = append(results, labelresolver.ResolvedMetricLabels{Metric: metric})
	}
	return results, nil
}

func (l *countingLabels) count(uid types.UID, metric string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls[uid][metric]
}

func (i *observedInventory) setError(err error) {
	i.mu.Lock()
	i.err = err
	i.mu.Unlock()
	select {
	case i.changes <- struct{}{}:
	default:
	}
}

func (i *observedInventory) replace(t *testing.T, objects ...*unstructured.Unstructured) {
	t.Helper()
	results := make([]inventory.SelectionResult, 0, len(objects))
	for _, object := range objects {
		definition := &v1alpha1.Karta{}
		require.NoError(t, kruntime.DefaultUnstructuredConverter.FromUnstructured(object.Object, definition))
		result := inventory.SelectionResult{Selection: inventory.Selection{
			Definition: definition,
			GVK:        catalog.RootKey(definition),
			Origin: inventory.Origin{Source: inventory.SourceCluster, Name: definition.Name,
				UID: definition.UID, ResourceVersion: definition.ResourceVersion},
		}}
		if definition.Spec.StructureDefinition.RootComponent.Name == "" {
			result.Err = inventory.ErrInvalidDefinition
		}
		results = append(results, result)
	}
	i.mu.Lock()
	i.results = results
	i.mu.Unlock()
	i.setError(nil)
}

func setDefinitions(t *testing.T, r *Runtime, objects ...*unstructured.Unstructured) *observedInventory {
	t.Helper()
	observer, ok := r.state.deps.Inventory.(*observedInventory)
	require.True(t, ok)
	observer.replace(t, objects...)
	return observer
}

func TestDefinitionSnapshotFencesInflightValuesAndErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		workload bool
		fail     bool
		changed  bool
	}{
		{"Pod value", false, false, true},
		{"Pod error", false, true, true},
		{"workload value", true, false, true},
		{"workload error", true, true, true},
		{"unchanged snapshot", false, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			labels := &controlledLabels{}
			r, values := simpleRuntime(t, labels)
			enableClusterDefinitions(t, r)
			definition := definitionObject(t, "first")
			observer := setDefinitions(t, r, definition)
			refreshSelection(t, r)
			root, pod := definedPair(t, r)
			labels.entered, labels.resume = make(chan struct{}), make(chan struct{})
			labels.blockWorkload = test.workload
			if test.fail {
				labels.err = errors.New("old evaluation failed")
			}
			done := make(chan error, 1)
			go func() {
				if test.workload {
					done <- r.reconcileWorkload(context.Background(), root.GetUID())
				} else {
					done <- r.reconcilePod(context.Background(), objectJobFor(refOf(pod)))
				}
			}()
			<-labels.entered
			next := definition.DeepCopy()
			if test.changed {
				next = definitionObject(t, "next")
				next.SetResourceVersion("2")
			}
			observer.replace(t, next)
			refreshSelection(t, r)
			close(labels.resume)
			if test.changed {
				require.ErrorIs(t, <-done, discovery.ErrStale)
			} else {
				require.NoError(t, <-done)
			}
			require.Equal(t, "first", values.Pod(pod.GetUID()).Record.Karta)
			require.Empty(t, values.Pod(pod.GetUID()).Error)
			require.Equal(t, "first", values.Workload(root.GetUID()).Record.Karta)
			require.Empty(t, values.Workload(root.GetUID()).StatusError)
			labels.err = nil
			settle(t, r)
			if test.changed {
				require.Equal(t, "next", values.Pod(pod.GetUID()).Record.Karta)
				require.Equal(t, "next", values.Workload(root.GetUID()).Record.Karta)
			}
		})
	}
}

func TestDefinitionChangesQueueAffectedKinds(t *testing.T) {
	r, _ := simpleRuntime(t, nil)
	enableClusterDefinitions(t, r)
	observer := setDefinitions(t, r)
	refreshSelection(t, r)
	job, deployment := simpleObject(jobKind, "job"), simpleObject(deploymentKind, "deployment")
	jobPod, deploymentPod := simpleObject(podGVK, "job-pod"), simpleObject(podGVK, "deployment-pod")
	simpleOwner(jobPod, job)
	simpleOwner(deploymentPod, deployment)
	seedSource(t, r, jobKind, true, job)
	seedSource(t, r, deploymentKind, true, deployment)
	seedSource(t, r, podGVK, true, jobPod, deploymentPod)
	settle(t, r)
	observer.replace(t, definitionObject(t, "job"))
	refreshSelection(t, r)
	var inputs []objectJobKey
	for r.state.podQueue.Len() > 0 {
		key, _ := r.state.podQueue.Get()
		inputs = append(inputs, key)
		r.state.podQueue.Done(key)
	}
	require.Equal(t, []objectJobKey{objectJobFor(refOf(jobPod))}, inputs)
}

func TestRuntimeDefinitionChangeDoesNotReconcileOtherKinds(t *testing.T) {
	labels := &countingLabels{calls: map[types.UID]map[string]int{}}
	r, values := simpleRuntime(t, labels)
	enableClusterDefinitions(t, r)
	jobDefinition := definitionObject(t, "job-definition")
	deploymentDefinition, err := catalog.Get(deploymentKind)
	require.NoError(t, err)
	deploymentDefinition.Name = "deployment-definition"
	deploymentData, err := kruntime.DefaultUnstructuredConverter.ToUnstructured(deploymentDefinition)
	require.NoError(t, err)
	deploymentObject := &unstructured.Unstructured{Object: deploymentData}
	observer := setDefinitions(t, r, jobDefinition, deploymentObject)
	refreshSelection(t, r)
	job, deployment := simpleObject(jobKind, "job"), simpleObject(deploymentKind, "deployment")
	jobPod, deploymentPod := simpleObject(podGVK, "job-pod"), simpleObject(podGVK, "deployment-pod")
	simpleOwner(jobPod, job)
	simpleOwner(deploymentPod, deployment)
	seedSource(t, r, jobKind, true, job)
	seedSource(t, r, deploymentKind, true, deployment)
	seedSource(t, r, podGVK, true, jobPod, deploymentPod)
	settle(t, r)
	require.Len(t, r.state.selectedObjectRoots, 2)
	jobCalls := labels.count(job.GetUID(), labelresolver.MetricWorkloadStatus)
	deploymentCalls := labels.count(deployment.GetUID(), labelresolver.MetricWorkloadStatus)
	deploymentPodCalls := labels.count(deployment.GetUID(), labelresolver.MetricPodWorkloadInfo)
	require.Positive(t, jobCalls)
	require.Positive(t, deploymentCalls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	next := jobDefinition.DeepCopy()
	next.SetResourceVersion("2")
	require.NoError(t, unstructured.SetNestedField(next.Object, "worker", "spec", "structureDefinition", "rootComponent", "name"))
	unstructured.RemoveNestedField(next.Object, "spec", "optimizationInstructions", "gangScheduling")
	observer.replace(t, next, deploymentObject)
	require.Eventually(t, func() bool {
		return values.Pod(jobPod.GetUID()).Record.Component == "worker" && labels.count(job.GetUID(), labelresolver.MetricWorkloadStatus) > jobCalls
	}, time.Second, time.Millisecond)
	require.Never(t, func() bool {
		return labels.count(deployment.GetUID(), labelresolver.MetricWorkloadStatus) != deploymentCalls || labels.count(deployment.GetUID(), labelresolver.MetricPodWorkloadInfo) != deploymentPodCalls
	}, 100*time.Millisecond, time.Millisecond)
	reads := observer.readCount()
	observer.setError(nil)
	require.Eventually(t, func() bool { return observer.readCount() > reads }, time.Second, time.Millisecond)
	require.Never(t, func() bool {
		return labels.count(deployment.GetUID(), labelresolver.MetricWorkloadStatus) != deploymentCalls || labels.count(deployment.GetUID(), labelresolver.MetricPodWorkloadInfo) != deploymentPodCalls
	}, 100*time.Millisecond, time.Millisecond)
	cancel()
	require.NoError(t, <-done)
}

func TestInventoryWatchDrivesRuntimeWithoutControllerKartaWatch(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	definition := definitionObject(t, "job")
	resource := kartaGVK.GroupVersion().WithResource("kartas")
	api := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(kruntime.NewScheme(), map[schema.GroupVersionResource]string{resource: "KartaList"}, definition)
	definitions, err := inventory.New(api, inventory.Config{Cluster: inventory.SourceConfig{Enabled: true}})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inventoryDone := make(chan error, 1)
	go func() { inventoryDone <- definitions.Run(ctx) }()
	require.NoError(t, definitions.WaitLoaded(ctx))
	r.state.deps.Inventory, r.state.config.ClusterDefinitions = definitions, true
	refreshSelection(t, r)
	root, pod := definedPair(t, r)
	controllerDone := make(chan error, 1)
	go func() { controllerDone <- r.Run(ctx) }()
	next := definition.DeepCopy()
	next.SetResourceVersion("2")
	require.NoError(t, unstructured.SetNestedField(next.Object, "worker", "spec", "structureDefinition", "rootComponent", "name"))
	unstructured.RemoveNestedField(next.Object, "spec", "optimizationInstructions", "gangScheduling")
	_, err = api.Resource(resource).Update(ctx, next, metav1.UpdateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return values.Pod(pod.GetUID()).Record.Component == "worker"
	}, 3*time.Second, time.Millisecond)
	require.NoError(t, api.Resource(resource).Delete(ctx, definition.GetName(), metav1.DeleteOptions{}))
	require.Eventually(t, func() bool {
		return values.Pod(pod.GetUID()).Record.Karta == ""
	}, 3*time.Second, time.Millisecond)
	require.NotNil(t, values.Workload(root.GetUID()))
	for _, action := range r.state.reader.clients.Dynamic.(*dynamicfake.FakeDynamicClient).Actions() {
		require.NotEqual(t, resource, action.GetResource())
	}
	var lists, watches int
	for _, action := range api.Actions() {
		switch action.GetVerb() {
		case "list":
			lists++
		case "watch":
			watches++
		}
	}
	require.Positive(t, lists)
	require.Positive(t, watches)
	cancel()
	require.NoError(t, <-controllerDone)
	require.NoError(t, <-inventoryDone)
}

func TestInventoryNotificationsPropagateHealth(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	enableClusterDefinitions(t, r)
	observer := setDefinitions(t, r, definitionObject(t, "job"))
	refreshSelection(t, r)
	root, pod := definedPair(t, r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	require.Eventually(t, r.Ready, time.Second, time.Millisecond)
	observer.setError(inventory.ErrPermissionDenied)
	require.Eventually(t, func() bool {
		return !r.Ready() && values.Workload(root.GetUID()).StatusError == store.ReasonPermissionDenied && values.Pod(pod.GetUID()).Error == store.ReasonPermissionDenied
	}, time.Second, time.Millisecond)
	row := values.Workload(root.GetUID())
	valid, _ := values.Capture().Valid(row.CreationSources, row.Error)
	require.True(t, valid)
	valid, reason := values.Capture().Valid(row.Sources, row.StatusError)
	require.False(t, valid)
	require.Equal(t, store.ReasonPermissionDenied, reason)
	observer.setError(nil)
	require.Eventually(t, func() bool {
		return r.Ready() && values.Workload(root.GetUID()).StatusError == "" && values.Pod(pod.GetUID()).Error == ""
	}, time.Second, time.Millisecond)
	cancel()
	require.NoError(t, <-done)
}
