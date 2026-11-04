// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/discovery"
)

// fakeWatchSource serves each Watch call from the next FakeWatcher in watchers.
func fakeWatchSource(t *testing.T, r *Runtime, resource schema.GroupVersionResource, watchers ...*watch.FakeWatcher) {
	t.Helper()
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(kruntime.NewScheme(),
		map[schema.GroupVersionResource]string{resource: "DeploymentList"})
	client.PrependWatchReactor("*", func(clienttesting.Action) (bool, watch.Interface, error) {
		require.NotEmpty(t, watchers, "unexpected extra Watch call")
		next := watchers[0]
		watchers = watchers[1:]
		return true, next, nil
	})
	r.state.reader.clients.Dynamic = client
}

func waitClosed(t *testing.T, stream watch.Interface) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-stream.ResultChan():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("watch did not close")
		}
	}
}

func TestWatchCloseKeepsSourceHealthyAndQueuesNothing(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{})
	w := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "ns", true)
	// A known root, registered directly: reconciling one would start real reflectors
	// that share the client this test replaces.
	r.state.roots["root"] = &rootState{ref: discovery.ObjectRef{GVK: lifetimeDeploymentGVK, Namespace: "ns", Name: "root", UID: "root"}}
	first, second := watch.NewFake(), watch.NewFake()
	fakeWatchSource(t, r, w.key.Resource, first, second)

	stream, err := r.state.watchResource(t.Context(), w, metav1.ListOptions{})
	require.NoError(t, err)
	first.Stop()
	waitClosed(t, stream)
	require.NoError(t, w.err)
	require.NoError(t, r.state.sources[w.key].err)

	// The reflector's reconnect is not a recovery, so nothing is queued again.
	_, err = r.state.watchResource(t.Context(), w, metav1.ListOptions{})
	require.NoError(t, err)
	require.Zero(t, r.state.workloadQueue.Len(), "a routine watch close must not requeue workloads")
	require.Zero(t, r.state.podQueue.Len(), "a routine watch close must not rescan Pods")
	second.Stop()
}

func TestWatchErrorEventMarksSourceUnhealthy(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{})
	w := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "ns", true)
	fake := watch.NewFake()
	fakeWatchSource(t, r, w.key.Resource, fake)

	stream, err := r.state.watchResource(t.Context(), w, metav1.ListOptions{})
	require.NoError(t, err)
	go fake.Error(&metav1.Status{Status: metav1.StatusFailure, Code: 410, Reason: metav1.StatusReasonExpired, Message: "too old"})
	select {
	case event := <-stream.ResultChan():
		require.Equal(t, watch.Error, event.Type)
	case <-time.After(5 * time.Second):
		t.Fatal("error event was not forwarded")
	}
	require.Error(t, w.err)
	require.Error(t, r.state.sources[w.key].err)
	fake.Stop()
}

func TestRecoveredNamespacedSourceRequeuesOnlyItsNamespace(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{})
	inA := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "a", true)
	rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "b", true)
	for _, root := range []discovery.ObjectRef{
		{GVK: lifetimeDeploymentGVK, Namespace: "a", Name: "root", UID: "root-a"},
		{GVK: lifetimeDeploymentGVK, Namespace: "b", Name: "root", UID: "root-b"},
	} {
		r.state.roots[root.UID] = &rootState{ref: root}
	}

	r.state.setWatchHealth(inA, errors.New("watch unavailable"))
	r.state.setWatchHealth(inA, nil)

	var queued []types.UID
	for r.state.workloadQueue.Len() > 0 {
		key, _ := r.state.workloadQueue.Get()
		queued = append(queued, key.RootUID)
		r.state.workloadQueue.Done(key)
	}
	require.Equal(t, []types.UID{"root-a"}, queued)
}
