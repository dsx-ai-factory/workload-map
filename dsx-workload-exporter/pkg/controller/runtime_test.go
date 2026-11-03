// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	inventory "github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
)

type runtimeTestInventory struct {
	changes chan struct{}
	entered chan struct{}
	release chan struct{}
}

func (i *runtimeTestInventory) List() ([]inventory.SelectionResult, error) {
	close(i.entered)
	<-i.release
	return nil, nil
}

func (i *runtimeTestInventory) Changes() <-chan struct{} { return i.changes }

func TestRuntimeStopsWhenInventoryChangesCloses(t *testing.T) {
	for _, tc := range []struct {
		name         string
		cancelFirst  bool
		closeChanges bool
		wantErr      error
	}{
		{name: "unexpected closure", closeChanges: true, wantErr: errInventoryChangesClosed},
		{name: "closure after cancellation", cancelFirst: true, closeChanges: true},
		{name: "cancellation with open channel", cancelFirst: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definitions := &runtimeTestInventory{
				changes: make(chan struct{}),
				entered: make(chan struct{}),
				release: make(chan struct{}),
			}
			state := newControllerState(&objectCache{objects: map[objectKey]cachedObject{}, sources: map[watchKey]cacheSource{}})
			state.config = Config{LeafWorkers: 1, RootWorkers: 1}
			state.deps = Dependencies{Inventory: definitions, Publications: store.New()}
			state.reader = &Reader{objectCache: state.objectCache, options: ReaderOptions{RequestTimeout: time.Second}}
			state.podQueue = newQueue[objectJobKey]("runtime-lifecycle-pods")
			state.workloadQueue = newQueue[workloadJobKey]("runtime-lifecycle-workloads")
			state.watchSyncQueue = newQueue[string]("runtime-lifecycle-sources")
			state.scopes[""] = true
			podSource := watchKey{Resource: podGVK.GroupVersion().WithResource("pods")}
			state.watches[podSource] = &resourceWatch{key: podSource, gvk: podGVK, full: true, ready: true, cancel: func() {}}
			runtime := &Runtime{state: state}
			if runtime.Ready() {
				t.Fatal("runtime is ready before it starts")
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			var runErr error
			var release sync.Once
			go func() {
				runErr = runtime.Run(ctx)
				close(done)
			}()
			t.Cleanup(func() {
				cancel()
				release.Do(func() { close(definitions.release) })
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("runtime did not join its workers")
				}
			})
			select {
			case <-definitions.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("definition refresh did not start")
			}
			if !runtime.Ready() {
				t.Fatal("running runtime with a synchronized required watch is not ready")
			}

			duplicateCtx, cancelDuplicate := context.WithCancel(context.Background())
			cancelDuplicate()
			duplicateDone := make(chan error, 1)
			go func() { duplicateDone <- runtime.Run(duplicateCtx) }()
			state.stateMu.RLock()
			runCtx := state.ctx
			state.stateMu.RUnlock()
			if tc.cancelFirst {
				cancel()
			}
			if tc.closeChanges {
				close(definitions.changes)
			}
			select {
			case <-runCtx.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("runtime did not cancel in-flight work")
			}
			if runtime.Ready() {
				t.Error("runtime remained ready during shutdown")
			}
			select {
			case <-done:
				t.Fatal("runtime returned before joining the blocked refresh")
			default:
			}
			release.Do(func() { close(definitions.release) })
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("runtime did not stop after the refresh completed")
			}
			if !errors.Is(runErr, tc.wantErr) {
				t.Errorf("Run error = %v, want %v", runErr, tc.wantErr)
			}
			select {
			case err := <-duplicateDone:
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("duplicate Run error = %v, want %v", err, tc.wantErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("duplicate Run did not finish")
			}
			if runtime.Ready() {
				t.Error("runtime remained ready after stopping")
			}
			if !tc.cancelFirst && ctx.Err() != nil {
				t.Error("runtime canceled its caller's context")
			}
		})
	}
}
