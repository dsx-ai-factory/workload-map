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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	kubediscovery "k8s.io/client-go/discovery"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/discovery"
	inventory "github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog"
)

type availabilityInventory struct {
	inventory.Observer
	selection inventory.Selection
}

func (i availabilityInventory) List() ([]inventory.SelectionResult, error) {
	return []inventory.SelectionResult{{Selection: i.selection}}, nil
}

type availabilityDiscovery struct {
	resources []*metav1.APIResourceList
	err       error
}

func (d availabilityDiscovery) ServerGroupsAndResourcesWithContext(context.Context) ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	return nil, d.resources, d.err
}

type availabilityMapper struct {
	meta.RESTMapper
	gvk    schema.GroupVersionKind
	err    error
	resets int
}

func (m *availabilityMapper) RESTMapping(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
	if gk == m.gvk.GroupKind() && m.err != nil {
		return nil, m.err
	}
	return m.RESTMapper.RESTMapping(gk, versions...)
}

func (m *availabilityMapper) Reset() { m.resets++ }

func TestMissingOptionalAPIsAreSkippedBeforeNamespaceExpansion(t *testing.T) {
	missing := &meta.NoKindMatchError{GroupKind: lifetimeDeploymentGVK.GroupKind(), SearchedVersions: []string{"v1"}}
	for _, tc := range []struct {
		name               string
		explicit, required bool
		err                error
		reason             string
	}{
		{name: "implicit missing API", err: missing},
		{name: "included missing API", explicit: true, err: missing, reason: store.ReasonAPIUnavailable},
		{name: "required missing API", required: true, err: missing, reason: store.ReasonAPIUnavailable},
		{name: "permission failure", err: apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "", errors.New("denied")), reason: store.ReasonPermissionDenied},
		{name: "discovery failure", err: errors.New("discovery unavailable"), reason: store.ReasonAPIUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"selected": "true"}}}
			if tc.required {
				cfg.RequiredKinds = []schema.GroupVersionKind{lifetimeDeploymentGVK}
			}
			r := newRootLifetimeRuntime(t, cfg)
			r.state.reader.clients.Discovery = availabilityDiscovery{}
			definition, err := catalog.Get(lifetimeDeploymentGVK)
			require.NoError(t, err)
			r.state.deps.Inventory = availabilityInventory{selection: inventory.Selection{Definition: definition, GVK: lifetimeDeploymentGVK, Explicit: tc.explicit}}
			namespaces := rootLifetimeWatch(t, r, namespaceGVK, "", true)
			for _, name := range []string{"first", "second"} {
				ns := rootLifetimeObject(namespaceGVK, "", name, types.UID("namespace-"+name))
				ns.SetLabels(map[string]string{"selected": "true"})
				require.NoError(t, r.state.applyWatchEvent(namespaces, ns, false))
				rootLifetimeWatch(t, r, podGVK, name, true)
			}
			r.state.reader.clients.Mapper = &availabilityMapper{RESTMapper: r.state.reader.clients.Mapper, gvk: lifetimeDeploymentGVK, err: tc.err}
			r.state.start(t.Context())
			require.ErrorIs(t, r.state.syncWatches(t.Context()), tc.err, "missing APIs must remain queued for retry")
			view := r.state.deps.Publications.(store.Capturer).Capture()
			if tc.reason == "" {
				require.Empty(t, view.Failures)
				require.Empty(t, r.state.watchFailures)
				require.True(t, r.Ready())
			} else {
				require.Len(t, view.Failures, 2)
				for _, failure := range view.Failures {
					require.Equal(t, tc.reason, failure.Error)
				}
			}
			for _, source := range view.Sources {
				require.NotEqual(t, lifetimeDeploymentGVK, source.GVK)
			}
			if tc.required {
				require.False(t, r.Ready())
			}
		})
	}
}

func TestOptionalAPIDisappearanceRemovesStaleRecordsAndRetries(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{})
	r.state.reader.clients.Discovery = availabilityDiscovery{}
	definition, err := catalog.Get(lifetimeDeploymentGVK)
	require.NoError(t, err)
	r.state.deps.Inventory = availabilityInventory{selection: inventory.Selection{Definition: definition, GVK: lifetimeDeploymentGVK}}
	rootLifetimeWatch(t, r, podGVK, "", true)
	w := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "", true)
	root := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "root", "root")
	require.NoError(t, r.state.syncWatches(t.Context()))
	require.NoError(t, r.state.applyWatchEvent(w, root, false))
	drainRootLifetimeQueues(t, r)
	require.NotNil(t, r.state.deps.Publications.Workload(root.GetUID()))

	mapper := &availabilityMapper{RESTMapper: r.state.reader.clients.Mapper, gvk: lifetimeDeploymentGVK,
		err: &meta.NoKindMatchError{GroupKind: lifetimeDeploymentGVK.GroupKind(), SearchedVersions: []string{"v1"}}}
	r.state.reader.clients.Mapper = mapper
	r.state.setWatchHealth(w, apierrors.NewNotFound(w.key.Resource.GroupResource(), ""))
	require.True(t, meta.IsNoMatchError(r.state.syncWatches(t.Context())))
	drainRootLifetimeQueues(t, r)
	r.state.failSelectedObjectDiscovery(objectJobFor(refOf(root)), discovery.ErrPending)
	view := r.state.deps.Publications.(store.Capturer).Capture()
	require.Empty(t, view.Workloads)
	require.Empty(t, view.Failures)
	require.NotContains(t, view.Sources, w.key)
	require.NotContains(t, r.state.watches, w.key)
	require.Empty(t, r.state.selectedObjectRoots)

	mapper.err = nil
	r.state.reader.lastMapperReset = time.Now().Add(-mapperResetInterval)
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{w.key.Resource: "DeploymentList"})
	firstWatch := true
	client.PrependWatchReactor("deployments", func(clienttesting.Action) (bool, watch.Interface, error) {
		if firstWatch {
			firstWatch = false
			return true, nil, apierrors.NewBadRequest("streaming lists are unsupported by the fake client")
		}
		return false, nil, nil
	})
	r.state.reader.clients.Dynamic = client
	require.NoError(t, r.state.syncWatches(t.Context()))
	require.Equal(t, 2, mapper.resets)
	require.Eventually(t, func() bool {
		health := r.state.deps.Publications.(store.Capturer).Capture().Sources[w.key]
		return health.Synced && health.Error == ""
	}, time.Second, time.Millisecond)
	require.Empty(t, r.state.deps.Publications.(store.Capturer).Capture().Failures)
}

func TestAbsentOptionalAPIStillReportsAnActualOwnerDependency(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{})
	r.state.reader.clients.Discovery = availabilityDiscovery{}
	r.state.absentKinds[lifetimeDeploymentGVK] = true
	_, err := r.state.reader.GetMetadata(t.Context(), discovery.ObjectRef{GVK: lifetimeDeploymentGVK, Namespace: "ns", Name: "owner"})
	require.True(t, meta.IsNoMatchError(err))
	view := r.state.deps.Publications.(store.Capturer).Capture()
	require.Len(t, view.Failures, 1)
	require.Equal(t, store.ReasonAPIUnavailable, view.Failures[0].Error)
}

func TestOptionalAPIDisappearanceKeepsDependentPodErrors(t *testing.T) {
	r := newRootLifetimeRuntime(t, Config{})
	r.state.reader.clients.Discovery = availabilityDiscovery{}
	definition, err := catalog.Get(lifetimeDeploymentGVK)
	require.NoError(t, err)
	r.state.deps.Inventory = availabilityInventory{selection: inventory.Selection{Definition: definition, GVK: lifetimeDeploymentGVK}}
	rootWatch := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "", true)
	podWatch := rootLifetimeWatch(t, r, podGVK, "", true)
	root := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "root", "root")
	pod := rootLifetimeObject(podGVK, "ns", "pod", "pod")
	pod.SetOwnerReferences([]metav1.OwnerReference{*metav1.NewControllerRef(root, root.GroupVersionKind())})
	require.NoError(t, r.state.syncWatches(t.Context()))
	require.NoError(t, r.state.applyWatchEvent(rootWatch, root, false))
	require.NoError(t, r.state.applyWatchEvent(podWatch, pod, false))
	drainRootLifetimeQueues(t, r)
	require.Empty(t, r.state.deps.Publications.Pod(pod.GetUID()).Error)

	r.state.reader.clients.Mapper = &availabilityMapper{RESTMapper: r.state.reader.clients.Mapper, gvk: lifetimeDeploymentGVK,
		err: &meta.NoKindMatchError{GroupKind: lifetimeDeploymentGVK.GroupKind(), SearchedVersions: []string{"v1"}}}
	require.True(t, meta.IsNoMatchError(r.state.syncWatches(t.Context())))
	require.True(t, meta.IsNoMatchError(r.handlePodInput(t.Context(), objectJobFor(refOf(pod)))))
	// A later optional-source refresh must not clear the live Pod's failure.
	require.True(t, meta.IsNoMatchError(r.state.syncWatches(t.Context())))
	result := r.state.deps.Publications.Pod(pod.GetUID())
	require.True(t, result.HasValue)
	require.Equal(t, root.GetUID(), result.Record.Workload.UID)
	require.Equal(t, store.ReasonAPIUnavailable, result.Error)
	require.Nil(t, r.state.deps.Publications.Workload(root.GetUID()))
}

func TestUnconfirmedAPIAbsencePreservesAcceptedState(t *testing.T) {
	for _, tc := range []struct {
		name      string
		discovery APIResourceDiscoverer
	}{
		{name: "discovery unavailable"},
		{name: "partial discovery", discovery: availabilityDiscovery{err: &kubediscovery.ErrGroupDiscoveryFailed{Groups: map[schema.GroupVersion]error{{Group: "apps", Version: "v1"}: errors.New("discovery unavailable")}}}},
		{name: "another served version", discovery: availabilityDiscovery{resources: []*metav1.APIResourceList{{GroupVersion: "apps/v1beta1", APIResources: []metav1.APIResource{{Name: "deployments", Kind: "Deployment"}}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRootLifetimeRuntime(t, Config{})
			definition, err := catalog.Get(lifetimeDeploymentGVK)
			require.NoError(t, err)
			r.state.deps.Inventory = availabilityInventory{selection: inventory.Selection{Definition: definition, GVK: lifetimeDeploymentGVK}}
			rootLifetimeWatch(t, r, podGVK, "", true)
			w := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "", true)
			root := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "root", "root")
			require.NoError(t, r.state.applyWatchEvent(w, root, false))
			drainRootLifetimeQueues(t, r)
			require.NotNil(t, r.state.deps.Publications.Workload(root.GetUID()))
			r.state.reader.clients.Mapper = &availabilityMapper{RESTMapper: r.state.reader.clients.Mapper, gvk: lifetimeDeploymentGVK, err: &meta.NoKindMatchError{GroupKind: lifetimeDeploymentGVK.GroupKind(), SearchedVersions: []string{"v1"}}}
			r.state.reader.clients.Discovery = tc.discovery
			require.Error(t, r.state.syncWatches(t.Context()))
			require.Empty(t, r.state.absentKinds)
			require.Contains(t, r.state.watches, w.key)
			require.NotNil(t, r.state.deps.Publications.Workload(root.GetUID()))
			require.NotEmpty(t, r.state.selectedObjectRoots)
			view := r.state.deps.Publications.(store.Capturer).Capture()
			require.NotEmpty(t, view.Failures)
			require.Equal(t, store.ReasonAPIUnavailable, view.Failures[0].Error)
		})
	}
}
