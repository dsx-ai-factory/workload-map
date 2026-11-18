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
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
)

// fakeWatchSource serves each Watch call from the next watcher in watchers.
func fakeWatchSource(t *testing.T, r *Runtime, resource schema.GroupVersionResource, watchers ...watch.Interface) {
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
	// A routine close follows minutes of traffic; one event stands in for it here.
	go first.Add(rootLifetimeObject(lifetimeDeploymentGVK, "ns", "other", "other"))
	select {
	case <-stream.ResultChan():
	case <-time.After(5 * time.Second):
		t.Fatal("event was not forwarded")
	}
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

func TestAlreadyClosedWatchIsAFailedStart(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{})
	w := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "ns", true)
	// client-go returns this after connection failures such as a dropped proxy.
	fakeWatchSource(t, r, w.key.Resource, watch.NewEmptyWatch())

	_, err := r.state.watchResource(t.Context(), w, metav1.ListOptions{})
	require.ErrorIs(t, err, errShortWatch)
	require.ErrorIs(t, w.err, errShortWatch)
	require.ErrorIs(t, r.state.sources[w.key].err, errShortWatch)
	require.Equal(t, store.ReasonAPIUnavailable, failureReason(w.err))
}

func TestVeryShortWatchMarksSourceUnhealthy(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{})
	w := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "ns", true)
	fake := watch.NewFake()
	fakeWatchSource(t, r, w.key.Resource, fake)

	stream, err := r.state.watchResource(t.Context(), w, metav1.ListOptions{})
	require.NoError(t, err)
	fake.Stop()
	waitClosed(t, stream)
	require.ErrorIs(t, w.err, errShortWatch, "a watch that ends at once without events failed")
	require.ErrorIs(t, r.state.sources[w.key].err, errShortWatch)
}

func TestWatchClosedRightAfterStreamingListIsUnhealthy(t *testing.T) {
	for name, tc := range map[string]struct {
		liveEvent bool
		healthy   bool
	}{
		"closed after the initial-events-end bookmark": {},
		"closed after a live event":                    {liveEvent: true, healthy: true},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRootLifetimeRuntime(t, Config{})
			w := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "ns", true)
			fake := watch.NewFake()
			fakeWatchSource(t, r, w.key.Resource, fake)
			sendInitialEvents := true
			stream, err := r.state.watchResource(t.Context(), w, metav1.ListOptions{SendInitialEvents: &sendInitialEvents, AllowWatchBookmarks: true})
			require.NoError(t, err)

			existing := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "existing", "existing")
			bookmark := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "", "")
			bookmark.SetAnnotations(map[string]string{metav1.InitialEventsAnnotationKey: "true"})
			go func() {
				// The streaming list: the snapshot, its end bookmark, then the live watch.
				fake.Add(existing)
				fake.Action(watch.Bookmark, bookmark)
				if tc.liveEvent {
					fake.Modify(existing)
				}
				fake.Stop()
			}()
			waitClosed(t, stream)
			if tc.healthy {
				require.NoError(t, w.err)
				return
			}
			require.ErrorIs(t, w.err, errShortWatch, "snapshot events are not live watch activity")
			require.ErrorIs(t, r.state.sources[w.key].err, errShortWatch)
		})
	}
}
