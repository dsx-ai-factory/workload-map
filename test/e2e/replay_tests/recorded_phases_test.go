// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package replay

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog/kartas"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

func TestValidateRecordedPhases(t *testing.T) {
	tests := []struct {
		name      string
		scheduled bool
		phases    []string
		wantError bool
	}{
		{name: "waiting", phases: []string{"Pending"}},
		{name: "waiting and progressing", scheduled: true, phases: []string{"Pending", "Progressing"}},
		{name: "phase order is irrelevant", scheduled: true, phases: []string{"Progressing", "Pending"}},
		{name: "reject missing recorded progress even when checkpoint matches", scheduled: true, phases: []string{"Pending"}, wantError: true},
		{name: "reject progress missing from SDK even when checkpoint matches", phases: []string{"Progressing", "Pending"}, wantError: true},
		{name: "reject duplicate phases", phases: []string{"Pending", "Pending"}, wantError: true},
		{name: "reject missing phases", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheduled := "False"
			if test.scheduled {
				scheduled = "True"
			}
			pod := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata":   map[string]any{"name": "phase-check"},
				"status": map[string]any{
					"phase": "Pending",
					"conditions": []any{
						map[string]any{"type": "PodScheduled", "status": scheduled},
					},
				},
			}}
			root, err := resource.NewComponentFactoryFromObject(kartas.Pod(), pod).GetRootComponent()
			if err != nil {
				t.Fatal(err)
			}
			status, err := root.GetStatus(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := validateRecordedPhases(status.MatchedStatuses, test.phases); (err != nil) != test.wantError {
				t.Fatalf("validateRecordedPhases(%v, %v) = %v; want error %v", status.MatchedStatuses, test.phases, err, test.wantError)
			}
		})
	}
}
