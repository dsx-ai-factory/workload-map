// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"fmt"
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog/kartas"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

// The recorder labels each frame from these predicates and the replay suite then requires Karta to read
// the same state, so every case is checked against the Karta definition itself, not only an expected value.

func TestPredicatesCloneSetMirrorsKarta(t *testing.T) {
	const (
		running      = kartav1alpha1.RunningStatus
		initializing = kartav1alpha1.InitializingStatus
		undefined    = kartav1alpha1.UndefinedStatus
	)
	cases := []struct {
		name       string
		generation int64
		spec       string
		status     string
		want       kartav1alpha1.ResourceStatus
	}{
		{"settled", 1, `{replicas: 3}`,
			`{observedGeneration: 1, replicas: 3, readyReplicas: 3, availableReplicas: 3, updatedReplicas: 3, expectedUpdatedReplicas: 3}`, running},
		{"availableReplicas missing", 1, `{replicas: 3}`,
			`{observedGeneration: 1, replicas: 3, readyReplicas: 3, updatedReplicas: 3, expectedUpdatedReplicas: 3}`, initializing},
		{"availableReplicas lower", 1, `{replicas: 3}`,
			`{observedGeneration: 1, replicas: 3, readyReplicas: 3, availableReplicas: 2, updatedReplicas: 3, expectedUpdatedReplicas: 3}`, initializing},
		{"no status yet", 1, `{replicas: 3}`, `{}`, undefined},
		{"generation not observed", 2, `{replicas: 3}`,
			`{observedGeneration: 1, replicas: 3, readyReplicas: 3, availableReplicas: 3, updatedReplicas: 3, expectedUpdatedReplicas: 3}`, initializing},
		{"scale-down draining", 2, `{replicas: 2}`,
			`{observedGeneration: 2, replicas: 3, readyReplicas: 3, availableReplicas: 3, updatedReplicas: 3, expectedUpdatedReplicas: 2}`, initializing},
		{"rollout in progress", 2, `{replicas: 3}`,
			`{observedGeneration: 2, replicas: 3, readyReplicas: 3, availableReplicas: 3, updatedReplicas: 1, expectedUpdatedReplicas: 3}`, initializing},
		{"pods not ready", 1, `{replicas: 3}`,
			`{observedGeneration: 1, replicas: 3, readyReplicas: 0, availableReplicas: 0, updatedReplicas: 3, expectedUpdatedReplicas: 3}`, initializing},
		{"partition canary settled", 2, `{replicas: 3, updateStrategy: {partition: 2}}`,
			`{observedGeneration: 2, replicas: 3, readyReplicas: 3, availableReplicas: 3, updatedReplicas: 1, expectedUpdatedReplicas: 1}`, running},
		{"paused rollout", 2, `{replicas: 1, updateStrategy: {paused: true}}`,
			`{observedGeneration: 2, replicas: 1, readyReplicas: 1, availableReplicas: 1, updatedReplicas: 0, expectedUpdatedReplicas: 1}`, running},
		{"scaled to zero", 3, `{replicas: 0}`,
			`{observedGeneration: 3, replicas: 0, readyReplicas: 0, availableReplicas: 0, updatedReplicas: 0, expectedUpdatedReplicas: 0}`, running},
		{"scale to zero not yet observed", 3, `{replicas: 0}`,
			`{observedGeneration: 2, replicas: 1, readyReplicas: 1, availableReplicas: 1, updatedReplicas: 1, expectedUpdatedReplicas: 1}`, initializing},
		{"spec.replicas defaulted", 1, `{}`,
			`{observedGeneration: 1, replicas: 1, readyReplicas: 1, availableReplicas: 1, updatedReplicas: 1, expectedUpdatedReplicas: 1}`, running},
		{"expectedUpdatedReplicas missing", 1, `{replicas: 3}`,
			`{observedGeneration: 1, replicas: 3, readyReplicas: 3, availableReplicas: 3, updatedReplicas: 3}`, running},
		{"revisions differ after the update", 2, `{replicas: 1}`,
			`{observedGeneration: 2, replicas: 1, readyReplicas: 1, availableReplicas: 1, updatedReplicas: 1, expectedUpdatedReplicas: 1, currentRevision: a, updateRevision: b}`, running},
	}
	karta := kartas.CloneSet()
	isRunning, isInitializing := CloneSetRunning(), CloneSetInitializing()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := decodeObject(t, "apps.kruise.io/v1alpha1", "CloneSet", tc.generation, tc.spec, tc.status)

			got := undefined
			switch r, i := isRunning(u), isInitializing(u); {
			case r && i:
				t.Fatal("CloneSetRunning and CloneSetInitializing both matched")
			case r:
				got = running
			case i:
				got = initializing
			}
			if got != tc.want {
				t.Errorf("recorder predicates read %q, want %q", got, tc.want)
			}

			matched := kartaStatuses(t, karta, u)
			for _, s := range []kartav1alpha1.ResourceStatus{running, initializing} {
				if slices.Contains(matched, s) != (s == tc.want) {
					t.Errorf("Karta matched %v, want %q", matched, tc.want)
				}
			}
		})
	}
}

func TestPredicatesStatefulSetIgnoresAvailability(t *testing.T) {
	cases := []struct {
		name   string
		status string
	}{
		{"availableReplicas lower",
			`{observedGeneration: 1, replicas: 3, readyReplicas: 3, updatedReplicas: 3, availableReplicas: 1, currentRevision: r, updateRevision: r}`},
		{"availableReplicas missing",
			`{observedGeneration: 1, replicas: 3, readyReplicas: 3, updatedReplicas: 3, currentRevision: r, updateRevision: r}`},
	}
	karta := kartas.StatefulSet()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := decodeObject(t, "apps/v1", "StatefulSet", 1, `{replicas: 3}`, tc.status)
			if !FullyAvailable()(u) {
				t.Error("FullyAvailable rejected a StatefulSet whose Running fields all match")
			}
			if matched := kartaStatuses(t, karta, u); !slices.Contains(matched, kartav1alpha1.RunningStatus) {
				t.Errorf("Karta matched %v, want %q", matched, kartav1alpha1.RunningStatus)
			}
		})
	}
}

// decodeObject builds the object the way a recording is read back, so integers stay int64 for the
// unstructured accessors.
func decodeObject(t *testing.T, apiVersion, kind string, generation int64, spec, status string) *unstructured.Unstructured {
	t.Helper()
	doc := fmt.Sprintf("apiVersion: %s\nkind: %s\nmetadata: {name: test, namespace: default, generation: %d}\nspec: %s\nstatus: %s\n",
		apiVersion, kind, generation, spec, status)
	u := &unstructured.Unstructured{}
	if err := yaml.Unmarshal([]byte(doc), u); err != nil {
		t.Fatalf("decode %s: %v", kind, err)
	}
	return u
}

func kartaStatuses(t *testing.T, karta *kartav1alpha1.Karta, u *unstructured.Unstructured) []kartav1alpha1.ResourceStatus {
	t.Helper()
	root, err := resource.NewComponentFactoryFromObject(karta, u).GetRootComponent()
	if err != nil {
		t.Fatalf("Karta could not parse the %s: %v", u.GetKind(), err)
	}
	status, err := root.GetStatus(t.Context())
	if err != nil || status == nil {
		t.Fatalf("Karta could not read the %s status: %v", u.GetKind(), err)
	}
	return status.MatchedStatuses
}
