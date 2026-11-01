// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/discovery"
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

func TestRootSnapshotCannotPublishAfterMembershipRecreation(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	root, pod, _, _ := seedPair(t, r)
	ref := refOf(root)
	before, ok := r.state.snapshotRoot(ref.UID)
	require.True(t, ok)
	selection, err := r.state.lookupDefinition(ref.GVK)
	require.NoError(t, err)
	previous := *values.Workload(ref.UID)

	// Keep the same cached object and definition. Only the membership lifetime changes.
	r.state.unregisterPod(objectJobFor(refOf(pod)))
	r.state.unregisterSelectedObject(objectJobFor(ref))
	_, ok = r.state.snapshotRoot(ref.UID)
	require.False(t, ok)
	require.Nil(t, values.Workload(ref.UID))
	require.NoError(t, r.reconcilePod(context.Background(), objectJobFor(refOf(pod))))
	after, ok := r.state.snapshotRoot(ref.UID)
	require.True(t, ok)
	require.NotEqual(t, before.generation, after.generation)
	require.ErrorIs(t, r.state.publishWorkload(ref.UID, before.generation, ref, selection, previous), discovery.ErrStale)
	require.Nil(t, values.Workload(ref.UID))
	require.NoError(t, r.state.publishWorkload(ref.UID, after.generation, ref, selection, previous))
	require.NotNil(t, values.Workload(ref.UID))
}
