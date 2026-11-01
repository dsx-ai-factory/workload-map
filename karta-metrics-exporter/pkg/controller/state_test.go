// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/discovery"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

func TestRootSnapshotOwnsSources(t *testing.T) {
	r, _ := simpleRuntime(t, nil)
	root, _, _, _ := seedPair(t, r)
	ref := refOf(root)
	key := objectJobFor(ref)
	selection, err := r.state.lookupDefinition(ref.GVK)
	require.NoError(t, err)
	sources := []store.SourceKey{{Namespace: "original"}}
	workload := &discovery.GenericWorkload{Root: ref, Path: []discovery.ObjectRef{ref}}
	require.NoError(t, r.state.registerSelectedObjectRoot(key, workload, selection, selection, sources))
	sources[0].Namespace = "changed input"
	snapshot, ok := r.state.snapshotRoot(ref.UID)
	require.True(t, ok)
	require.Equal(t, "original", snapshot.sources[0].Namespace)
	snapshot.sources[0].Namespace = "changed snapshot"
	next, ok := r.state.snapshotRoot(ref.UID)
	require.True(t, ok)
	require.Equal(t, "original", next.sources[0].Namespace)
}

type blockedRootLabels struct {
	root            types.UID
	calls           atomic.Int32
	entered, resume chan struct{}
}

func (l *blockedRootLabels) Resolve(ctx context.Context, input labelresolver.Input) ([]labelresolver.ResolvedMetricLabels, error) {
	var call int32
	if input.Metrics[0] == labelresolver.MetricWorkloadStatus && input.Workload.GetUID() == l.root {
		call = l.calls.Add(1)
		if call == 1 {
			close(l.entered)
			select {
			case <-l.resume:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return []labelresolver.ResolvedMetricLabels{{Labels: map[string]string{"evaluation": fmt.Sprint(call)}}}, nil
}

func TestWorkloadQueueSerializesRootPublication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, values := simpleRuntime(t, nil)
		root, pod, roots, pods := seedPair(t, r)
		otherRoot, otherPod := simpleObject(deploymentKind, "other-root"), simpleObject(podGVK, "other-pod")
		simpleOwner(otherPod, otherRoot)
		require.NoError(t, r.state.applyWatchEvent(roots, otherRoot, false))
		require.NoError(t, r.state.applyWatchEvent(pods, otherPod, false))
		settle(t, r)
		previousRoot := values.Workload(root.GetUID())
		previousOther := values.Workload(otherRoot.GetUID())
		entry := r.state.roots[root.GetUID()]
		resolver := &blockedRootLabels{root: root.GetUID(), entered: make(chan struct{}), resume: make(chan struct{})}
		r.state.deps.Labels = resolver
		for range 2 {
			r.state.wg.Go(func() { runWorker(r.state.ctx, r.state.workloadQueue, r.handleWorkloadInput) })
		}
		key := workloadJobKey{RootUID: root.GetUID()}
		r.state.workloadQueue.Add(key)
		<-resolver.entered

		// An empty membership queues cleanup, but cannot remove an in-flight root entry.
		r.state.unregisterPod(objectJobFor(refOf(pod)))
		r.state.unregisterSelectedObject(objectJobFor(refOf(root)))
		for range 5 {
			r.state.workloadQueue.Add(key)
		}
		r.state.workloadQueue.Add(workloadJobKey{RootUID: otherRoot.GetUID()})
		synctest.Wait()
		require.Equal(t, int32(1), resolver.calls.Load(), "same-root work must wait for publication")
		require.Same(t, entry, r.state.roots[root.GetUID()])
		require.Empty(t, entry.pods)
		require.Empty(t, entry.selectedObjects)
		require.Same(t, previousRoot, values.Workload(root.GetUID()), "blocked evaluation has not published")
		otherResult := values.Workload(otherRoot.GetUID())
		require.NotNil(t, otherResult)
		require.NotSame(t, previousOther, otherResult, "another root must progress")

		// Restore membership while the first evaluation still owns the queue key.
		require.NoError(t, r.reconcilePod(context.Background(), objectJobFor(refOf(pod))))
		synctest.Wait()
		require.Same(t, entry, r.state.roots[root.GetUID()])
		require.Contains(t, entry.pods, pod.GetUID())
		require.Equal(t, int32(1), resolver.calls.Load())
		close(resolver.resume)
		synctest.Wait()
		require.Equal(t, int32(2), resolver.calls.Load(), "requeues coalesce until the first worker calls Done")
		require.Equal(t, "2", values.Workload(root.GetUID()).Record.StatusLabels["evaluation"])

		// With no evaluation in flight, the next root-UID job removes the empty entry.
		r.state.unregisterPod(objectJobFor(refOf(pod)))
		synctest.Wait()
		require.NotContains(t, r.state.roots, root.GetUID())
		require.Nil(t, values.Workload(root.GetUID()))
		r.state.workloadQueue.ShutDown()
		r.state.wg.Wait()
	})
}

func TestStartPreservesOriginalContext(t *testing.T) {
	r, _ := simpleRuntime(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, r.state.start(ctx))
	require.EqualError(t, r.state.start(context.Background()), "runtime already started")
	require.Same(t, ctx, r.state.ctx)
	r.state.stopWatches()
}

func TestNamespaceAdmissionUsesCurrentScopes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		selector  *metav1.LabelSelector
		namespace string
		admitted  bool
	}{
		{name: "no selector", namespace: "ns", admitted: true},
		{name: "empty selector", selector: &metav1.LabelSelector{}, namespace: "ns", admitted: true},
		{name: "cluster scoped", selector: &metav1.LabelSelector{MatchLabels: map[string]string{"include": "yes"}}, admitted: true},
		{name: "namespaced", selector: &metav1.LabelSelector{MatchLabels: map[string]string{"include": "yes"}}, namespace: "ns"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := simpleRuntime(t, nil)
			runtime, err := New(Config{NamespaceSelector: tc.selector}, r.state.deps, r.state.reader)
			require.NoError(t, err)
			require.Equal(t, tc.admitted, runtime.state.namespaceAdmitted(tc.namespace))
			runtime.state.scopes[tc.namespace] = true
			require.True(t, runtime.state.namespaceAdmitted(tc.namespace))
			delete(runtime.state.scopes, tc.namespace)
			require.Equal(t, tc.admitted, runtime.state.namespaceAdmitted(tc.namespace))
		})
	}
}
