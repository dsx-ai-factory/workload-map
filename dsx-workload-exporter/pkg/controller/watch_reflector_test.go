// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	clienttesting "k8s.io/client-go/testing"
)

// These tests run the real client-go reflector on the exporter's list and watch
// functions, so they check what client-go does, not only what watchResource does.

var reflectorDeployments = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

// watchErr reads the health error of the watch for key under the state lock.
func watchErr(r *Runtime, key watchKey) error {
	r.state.stateMu.RLock()
	defer r.state.stateMu.RUnlock()
	if w := r.state.watches[key]; w != nil {
		return w.err
	}
	return nil
}

func TestReflectorStreamingListClosedAfterBookmark(t *testing.T) {
	for name, tc := range map[string]struct {
		liveEvent bool
		healthy   bool
	}{
		"closed right after the initial-events-end bookmark": {},
		"closed after one live event":                        {liveEvent: true, healthy: true},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRootLifetimeRuntime(t, Config{})
			r.state.reader.clients.Mapper.(*meta.DefaultRESTMapper).AddSpecific(lifetimeDeploymentGVK, reflectorDeployments, reflectorDeployments, meta.RESTScopeNamespace)
			client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(kruntime.NewScheme(),
				map[schema.GroupVersionResource]string{reflectorDeployments: "DeploymentList"})
			existing := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "existing", "existing")
			bookmark := rootLifetimeObject(lifetimeDeploymentGVK, "", "", "")
			bookmark.SetResourceVersion("10")
			bookmark.SetAnnotations(map[string]string{metav1.InitialEventsAnnotationKey: "true"})
			done := make(chan struct{})
			t.Cleanup(func() { close(done) })
			retried := make(chan struct{})
			var calls atomic.Int32
			client.PrependWatchReactor("*", func(clienttesting.Action) (bool, watch.Interface, error) {
				if calls.Add(1) > 1 {
					// The reflector retried after the close; hold it so health stays as the
					// close left it.
					close(retried)
					<-done
					return true, watch.NewFake(), nil
				}
				fake := watch.NewFake()
				go func() {
					// A streaming list: the snapshot, its end bookmark, then the live watch.
					fake.Add(existing.DeepCopy())
					fake.Action(watch.Bookmark, bookmark.DeepCopy())
					if tc.liveEvent {
						modified := existing.DeepCopy()
						modified.SetResourceVersion("11")
						fake.Modify(modified)
					}
					fake.Stop()
				}()
				return true, fake, nil
			})
			r.state.reader.clients.Dynamic = client

			key, err := r.state.ensureWatch(t.Context(), lifetimeDeploymentGVK, "", false)
			require.NoError(t, err)
			select {
			case <-retried:
			case <-time.After(10 * time.Second):
				t.Fatal("the reflector did not retry the watch")
			}
			r.state.stateMu.RLock()
			_, cached := r.state.objects[keyOf(refOf(existing))]
			r.state.stateMu.RUnlock()
			// The fake tracker holds nothing, so the object can only come from the stream.
			require.True(t, cached, "the reflector must sync through the streaming list")
			if tc.healthy {
				require.NoError(t, watchErr(r, key))
				return
			}
			require.ErrorIs(t, watchErr(r, key), errShortWatch, "a watch that ends with the snapshot failed")
		})
	}
}

func TestReflectorDroppedWatchConnectionMarksSourceUnhealthy(t *testing.T) {
	if testing.Short() {
		t.Skip("client-go retries a dropped watch for about 10s before it returns")
	}
	var watches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("watch") != "true" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"apiVersion":"apps/v1","kind":"DeploymentList","metadata":{"resourceVersion":"5"},"items":[]}`))
			return
		}
		if watches.Add(1) == 1 {
			// The first watch works: a streaming list that ends with its bookmark, then a
			// live watch that the server ends normally after more than shortWatch.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"type":"BOOKMARK","object":{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"resourceVersion":"5","annotations":{"k8s.io/initial-events-end":"true"}}}}` + "\n"))
			w.(http.Flusher).Flush()
			time.Sleep(shortWatch + 500*time.Millisecond)
			return
		}
		// Then a proxy drops every watch: after its retries, client-go returns an
		// already closed watch with no error.
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = connection.Close()
		}
	}))
	t.Cleanup(server.Close)
	r := newRootLifetimeRuntime(t, Config{})
	r.state.reader.clients.Mapper.(*meta.DefaultRESTMapper).AddSpecific(lifetimeDeploymentGVK, reflectorDeployments, reflectorDeployments, meta.RESTScopeNamespace)
	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	r.state.reader.clients.Dynamic = client

	key, err := r.state.ensureWatch(t.Context(), lifetimeDeploymentGVK, "", false)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		r.state.stateMu.RLock()
		defer r.state.stateMu.RUnlock()
		return r.state.sources[key].synced
	}, 5*time.Second, 10*time.Millisecond, "the first streaming list must sync the source")
	require.NoError(t, watchErr(r, key))
	require.Eventually(t, func() bool { return watchErr(r, key) != nil }, 30*time.Second, 10*time.Millisecond,
		"a dropped watch connection must not leave a synced source healthy")
	require.ErrorIs(t, watchErr(r, key), errShortWatch)
	require.GreaterOrEqual(t, watches.Load(), int32(2))
}
