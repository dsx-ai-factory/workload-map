// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/discovery"
)

func TestReadinessRequiresMissingWatches(t *testing.T) {
	for _, kind := range []schema.GroupVersionKind{podGVK, jobKind, namespaceGVK} {
		t.Run(kind.Kind, func(t *testing.T) {
			r, _ := simpleRuntime(t, nil)
			r.state.started = true
			seedSource(t, r, deploymentKind, true)
			if kind != podGVK {
				seedSource(t, r, podGVK, true)
			}
			if kind == namespaceGVK {
				r.state.config.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"include": "yes"}}
				r.state.namespaceSelector, _ = selector(r.state.config.NamespaceSelector)
			} else {
				r.state.config.RequiredKinds = []schema.GroupVersionKind{kind}
			}
			require.False(t, r.Ready(), "an unrelated synchronized watch cannot satisfy a missing required watch")
			seedSource(t, r, kind, true)
			require.True(t, r.Ready())
		})
	}
}

func TestReadinessWaitsForAdmittedNamespaceWatches(t *testing.T) {
	r, _ := simpleRuntime(t, nil)
	r.state.reader.clients.Dynamic.(*dynamicfake.FakeDynamicClient).PrependWatchReactor("*", func(action clienttesting.Action) (bool, watch.Interface, error) {
		options := action.(interface{ GetListOptions() metav1.ListOptions }).GetListOptions()
		if options.SendInitialEvents != nil && *options.SendInitialEvents {
			return true, nil, apierrors.NewBadRequest("watch-list unsupported")
		}
		return true, watch.NewRaceFreeFake(), nil
	})
	r.state.started = true
	r.state.config.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"include": "yes"}}
	r.state.namespaceSelector, _ = selector(r.state.config.NamespaceSelector)
	r.state.config.RequiredKinds = []schema.GroupVersionKind{jobKind}
	r.state.scopes = map[string]bool{}
	namespace := simpleObject(namespaceGVK, "first")
	namespace.SetNamespace("")
	namespace.SetLabels(map[string]string{"include": "yes"})
	namespaces := seedSource(t, r, namespaceGVK, true, namespace)
	require.False(t, r.Ready(), "Namespace synchronization precedes Pod watch creation")
	_, err := r.state.prepareWatchRequests()
	require.NoError(t, err)
	require.False(t, r.Ready(), "admitting a namespace does not create its required watches")
	_, err = r.state.ensureWatch(context.Background(), podGVK, "first", false)
	require.NoError(t, err)
	require.False(t, r.Ready(), "the configured required kind still has no watch")
	_, err = r.state.ensureWatch(context.Background(), jobKind, "first", false)
	require.NoError(t, err)
	require.Eventually(t, r.Ready, time.Second, time.Millisecond)

	namespace = simpleObject(namespaceGVK, "second")
	namespace.SetNamespace("")
	require.NoError(t, r.state.applyWatchEvent(namespaces, namespace, false))
	require.True(t, r.Ready(), "excluded namespaces need no Pod watch")
	namespace = namespace.DeepCopy()
	namespace.SetLabels(map[string]string{"include": "yes"})
	require.NoError(t, r.state.applyWatchEvent(namespaces, namespace, false))
	require.False(t, r.Ready(), "a matching Namespace update must block readiness before source reconciliation")
	_, err = r.state.prepareWatchRequests()
	require.NoError(t, err)
	require.False(t, r.Ready())
	_, err = r.state.ensureWatch(context.Background(), podGVK, "second", false)
	require.NoError(t, err)
	require.False(t, r.Ready())
	_, err = r.state.ensureWatch(context.Background(), jobKind, "second", false)
	require.NoError(t, err)
	require.Eventually(t, r.Ready, time.Second, time.Millisecond)
}

func TestMappingRecoveryClearsOnlyFailuresForSharedSource(t *testing.T) {
	for _, tc := range []struct {
		name              string
		kind              schema.GroupVersionKind
		selectNamespaces  bool
		watchNamespace    string
		remainingFailures int
	}{
		{name: "all namespaces", kind: deploymentKind},
		{name: "cluster scoped resource", kind: namespaceGVK, selectNamespaces: true},
		{name: "separate namespace sources", kind: deploymentKind, selectNamespaces: true, watchNamespace: "first", remainingFailures: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, values := simpleRuntime(t, nil)
			if tc.selectNamespaces {
				r.state.config.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"include": "yes"}}
				r.state.scopes = map[string]bool{"first": true, "second": true}
			}
			source := seedSource(t, r, tc.kind, true)
			if tc.watchNamespace != "" {
				cached := r.state.sources[source.key]
				health := values.Capture().Sources[source.key]
				delete(r.state.watches, source.key)
				delete(r.state.sources, source.key)
				values.RemoveSourceHealth(source.key)
				source.key.Namespace = tc.watchNamespace
				r.state.watches[source.key] = source
				r.state.sources[source.key] = cached
				values.SetSourceHealth(source.key, health)
			}
			mapper := &recordingMapper{RESTMapper: r.state.reader.clients.Mapper, err: errors.New("mapping unavailable")}
			r.state.reader.clients.Mapper = mapper
			for _, namespace := range []string{"first", "second"} {
				_, err := r.state.ensureWatch(context.Background(), tc.kind, namespace, false)
				require.Error(t, err)
			}
			_, err := r.state.ensureWatch(context.Background(), jobKind, "first", false)
			require.Error(t, err)
			require.Len(t, values.Capture().Failures, 3)
			mapper.err = nil
			_, err = r.state.ensureWatch(context.Background(), tc.kind, "first", false)
			require.NoError(t, err)
			require.Len(t, r.state.watchFailures, tc.remainingFailures+1)
			require.Len(t, values.Capture().Failures, tc.remainingFailures+1)
			require.Contains(t, r.state.watchFailures, discovery.WatchRequest{GVK: jobKind, Namespace: "first"}, "another resource's mapping failure must survive")
			if tc.remainingFailures > 0 {
				require.Contains(t, r.state.watchFailures, discovery.WatchRequest{GVK: tc.kind, Namespace: "second"}, "another namespace's source failure must survive")
			}
		})
	}
}
