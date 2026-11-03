// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"errors"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/discovery"
	inventory "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

func TestDefinitionRefreshUsesEffectiveChanges(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	definition := selectedDefinition{Selection: inventory.Selection{
		GVK:    gvk,
		Origin: inventory.Origin{Source: inventory.SourceCluster, Name: "deployment", UID: "karta-1", ResourceVersion: "1"},
		Definition: &v1alpha1.Karta{
			ObjectMeta: metav1.ObjectMeta{Name: "deployment", UID: "karta-1", ResourceVersion: "1"},
			Spec: v1alpha1.KartaSpec{StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{Name: "root"},
			}},
		},
	}}
	tests := []struct {
		name        string
		change      func(*selectedDefinition)
		wantRefresh bool
	}{
		{name: "identical relist"},
		{name: "metadata update", change: func(next *selectedDefinition) {
			next.Origin.ResourceVersion = "2"
			next.Definition.ResourceVersion = "2"
			next.Definition.Annotations = map[string]string{"example.com/note": "updated"}
			next.Definition.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}}
		}},
		{name: "spec update", wantRefresh: true, change: func(next *selectedDefinition) {
			next.Definition.Spec.StructureDefinition.RootComponent.Name = "renamed"
		}},
		{name: "replacement UID", wantRefresh: true, change: func(next *selectedDefinition) { next.Origin.UID = "karta-2" }},
		{name: "winner name", wantRefresh: true, change: func(next *selectedDefinition) { next.Origin.Name = "other" }},
		{name: "winner source", wantRefresh: true, change: func(next *selectedDefinition) { next.Origin.Source = inventory.SourceCatalog }},
		{name: "selected version", wantRefresh: true, change: func(next *selectedDefinition) { next.GVK.Version = "v2" }},
		{name: "invalid definition", wantRefresh: true, change: func(next *selectedDefinition) { next.err = inventory.ErrInvalidDefinition }},
		{name: "removed definition", wantRefresh: true, change: func(next *selectedDefinition) { next.Definition = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r, ref := definitionRefreshState(t, definition)
			previous, _ := r.lookupDefinition(gvk)
			next := definition
			next.Definition = definition.Definition.DeepCopy()
			if test.change != nil {
				test.change(&next)
			}
			if err := r.refreshDefinitions(map[schema.GroupKind]selectedDefinition{gvk.GroupKind(): next}, nil); err != nil {
				t.Fatal(err)
			}
			if got := r.definitionVersion != previous.version; got != test.wantRefresh {
				t.Fatalf("version changed = %v, want %v", got, test.wantRefresh)
			}
			if got := r.workloadQueue.Len() > 0 && r.podQueue.Len() > 0; got != test.wantRefresh {
				t.Fatalf("root and Pod queued = %v, want %v", got, test.wantRefresh)
			}
			if !test.wantRefresh {
				if r.workloadQueue.Len() != 0 || r.podQueue.Len() != 0 {
					t.Fatal("unchanged definition queued work")
				}
				if !r.isDefinitionCurrent(ref, previous) {
					t.Fatal("metadata refresh invalidated in-flight evaluation")
				}
				if r.selectedDefinitions[gvk.GroupKind()].Definition != previous.Definition {
					t.Fatal("equivalent refresh replaced the evaluation snapshot")
				}
			}
		})
	}
}

func TestDefinitionRefreshKeepsHealthAndMembershipChanges(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	definition := selectedDefinition{Selection: inventory.Selection{GVK: gvk, Definition: &v1alpha1.Karta{}}}
	r, ref := definitionRefreshState(t, definition)
	for _, sourceErr := range []error{inventory.ErrUnavailable, nil} {
		before := r.definitionVersion
		if err := r.refreshDefinitions(map[schema.GroupKind]selectedDefinition{gvk.GroupKind(): definition}, sourceErr); !errors.Is(err, sourceErr) {
			t.Fatalf("source error = %v, want %v", err, sourceErr)
		}
		if r.definitionVersion != before+1 || r.workloadQueue.Len() == 0 || r.podQueue.Len() == 0 {
			t.Fatal("health transition did not requeue affected work")
		}
		for r.podQueue.Len() > 0 {
			key, _ := r.podQueue.Get()
			r.podQueue.Done(key)
		}
		for r.workloadQueue.Len() > 0 {
			key, _ := r.workloadQueue.Get()
			r.workloadQueue.Done(key)
		}
	}
	before := r.definitionVersion
	if err := r.refreshDefinitions(map[schema.GroupKind]selectedDefinition{}, nil); err != nil {
		t.Fatal(err)
	}
	if r.definitionVersion != before+1 || r.workloadQueue.Len() == 0 || r.podQueue.Len() == 0 {
		t.Fatal("definition removal did not requeue affected work")
	}
	before = r.definitionVersion
	if err := r.refreshDefinitions(map[schema.GroupKind]selectedDefinition{gvk.GroupKind(): definition}, nil); err != nil {
		t.Fatal(err)
	}
	if r.definitionVersion != before+1 {
		t.Fatal("definition addition did not advance version")
	}
	current, _ := r.lookupDefinition(gvk)
	r.selectedDefinitions[gvk.GroupKind()] = selectedDefinition{Selection: definition.Selection, err: fmt.Errorf("first: %w", inventory.ErrInvalidDefinition)}
	before = r.definitionVersion
	next := selectedDefinition{Selection: definition.Selection, err: fmt.Errorf("second: %w", inventory.ErrInvalidDefinition)}
	if err := r.refreshDefinitions(map[schema.GroupKind]selectedDefinition{gvk.GroupKind(): next}, nil); err != nil {
		t.Fatal(err)
	}
	if r.definitionVersion != before || !r.isDefinitionCurrent(ref, current) {
		t.Fatal("equivalent validation outcome invalidated evaluation")
	}
}

func definitionRefreshState(t *testing.T, definition selectedDefinition) (*controllerState, discovery.ObjectRef) {
	t.Helper()
	r := newControllerState(&objectCache{objects: map[objectKey]cachedObject{}})
	r.deps.Publications = store.New()
	r.podSelector = labels.Everything()
	r.podQueue = newQueue[objectJobKey]("definition-change-pods")
	r.workloadQueue = newQueue[workloadJobKey]("definition-change-workloads")
	t.Cleanup(r.podQueue.ShutDown)
	t.Cleanup(r.workloadQueue.ShutDown)
	r.definitionVersion = 1
	r.selectedDefinitions[definition.GVK.GroupKind()] = definition
	ref := discovery.ObjectRef{GVK: definition.GVK, Namespace: "test", Name: "api", UID: "root"}
	pod := &unstructured.Unstructured{}
	pod.SetGroupVersionKind(podGVK)
	pod.SetNamespace("test")
	pod.SetName("api-pod")
	pod.SetUID("pod")
	key := objectJobFor(refOf(pod))
	r.objects[key.Object] = cachedObject{object: pod}
	r.podRoots[key.UID] = ref.UID
	r.roots[ref.UID] = &rootState{ref: ref, pods: map[types.UID]objectJobKey{key.UID: key}, selectedObjects: map[objectJobKey]struct{}{}}
	return r, ref
}
