// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"errors"
	"reflect"
	"testing"
	"time"

	inventory "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

type blockedDefinitionInventory struct {
	inventory.Observer
	entered chan struct{}
	release chan struct{}
}

func (i *blockedDefinitionInventory) List() ([]inventory.SelectionResult, error) {
	close(i.entered)
	<-i.release
	return nil, inventory.ErrUnavailable
}

func TestDefinitionPreparationDoesNotBlockStateAndRetainsSnapshotOnFailure(t *testing.T) {
	catalog, err := inventory.New(nil, inventory.Config{Catalog: inventory.SourceConfig{Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	values := store.New()
	r := newControllerState(&objectCache{objects: map[objectKey]cachedObject{}})
	r.config.ClusterDefinitions = true
	r.deps = Dependencies{Inventory: catalog, Publications: values}
	r.podQueue = newQueue[objectJobKey]("definition-preparation-test")
	t.Cleanup(r.podQueue.ShutDown)
	if _, err := r.prepareWatchRequests(); err != nil {
		t.Fatal(err)
	}
	before, version := r.selectedDefinitions, r.definitionVersion
	if len(before) == 0 {
		t.Fatal("expected catalog definitions")
	}
	blocked := &blockedDefinitionInventory{entered: make(chan struct{}), release: make(chan struct{})}
	r.deps.Inventory = blocked
	done := make(chan error, 1)
	go func() {
		_, err := r.prepareWatchRequests()
		done <- err
	}()
	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		close(blocked.release)
		t.Fatal("inventory read did not start")
	}
	available := func() bool {
		if !r.stateMu.TryLock() {
			return false
		}
		defer r.stateMu.Unlock()
		return true
	}()
	close(blocked.release)
	select {
	case err := <-done:
		if !errors.Is(err, inventory.ErrUnavailable) {
			t.Fatalf("refresh error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("definition refresh did not finish")
	}
	if !available {
		t.Error("inventory read held the controller state lock")
	}
	if !reflect.DeepEqual(before, r.selectedDefinitions) {
		t.Error("inventory failure discarded the last accepted definitions")
	}
	if !errors.Is(r.definitionInventoryErr, inventory.ErrUnavailable) || r.definitionVersion != version+1 {
		t.Error("inventory failure did not update health and definition version")
	}
	if health := values.Capture().Sources[definitionSource]; health.Synced || health.Error != store.ReasonAPIUnavailable {
		t.Fatalf("inventory source health = %+v", health)
	}
}
