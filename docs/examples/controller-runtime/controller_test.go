// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

func TestFormatMatchedStatuses(t *testing.T) {
	tests := []struct {
		name     string
		statuses []v1alpha1.ResourceStatus
		want     string
	}{
		{name: "waiting transition", statuses: []v1alpha1.ResourceStatus{v1alpha1.ProgressingStatus, v1alpha1.PendingStatus}, want: "Pending, Progressing"},
		{name: "serving transition", statuses: []v1alpha1.ResourceStatus{v1alpha1.RunningStatus, v1alpha1.ProgressingStatus}, want: "Progressing, Running"},
		{name: "blocked transition retains failure", statuses: []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.FailedStatus, v1alpha1.ProgressingStatus}, want: "Failed, Pending, Progressing"},
		{name: "terminal", statuses: []v1alpha1.ResourceStatus{v1alpha1.CompletedStatus}, want: "Completed"},
		{name: "legacy remains distinct", statuses: []v1alpha1.ResourceStatus{"Initializing", v1alpha1.PendingStatus}, want: "Initializing, Pending"},
		{name: "resuming retained", statuses: []v1alpha1.ResourceStatus{v1alpha1.ResumingStatus}, want: "Resuming"},
		{name: "deduplicated", statuses: []v1alpha1.ResourceStatus{v1alpha1.RunningStatus, v1alpha1.RunningStatus}, want: "Running"},
		{name: "undefined", statuses: []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{name: "no matches"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := slices.Clone(tt.statuses)
			if got := formatMatchedStatuses(tt.statuses); got != tt.want {
				t.Errorf("formatMatchedStatuses() = %q, want %q", got, tt.want)
			}
			if !slices.Equal(tt.statuses, before) {
				t.Fatal("formatMatchedStatuses mutated its input")
			}
		})
	}
}

func TestStatusAnnotationPreservesEvidenceGaps(t *testing.T) {
	object := &unstructured.Unstructured{}
	object.SetAnnotations(map[string]string{annotationStatus: "Pending, Progressing"})
	observations := []struct {
		statuses []v1alpha1.ResourceStatus
		want     string
		changed  bool
	}{
		{statuses: []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}, want: "Pending, Progressing"},
		{statuses: []v1alpha1.ResourceStatus{v1alpha1.ProgressingStatus, v1alpha1.PendingStatus}, want: "Pending, Progressing"},
		{statuses: []v1alpha1.ResourceStatus{v1alpha1.RunningStatus, v1alpha1.ProgressingStatus}, want: "Progressing, Running", changed: true},
		{want: "Progressing, Running"},
		{statuses: []v1alpha1.ResourceStatus{v1alpha1.RunningStatus}, want: "Running", changed: true},
	}
	for i, observation := range observations {
		summary := map[string]string{}
		if matched := formatMatchedStatuses(observation.statuses); matched != "" {
			summary[annotationStatus] = matched
		}
		if changed := applyAnnotations(object, summary); changed != observation.changed {
			t.Errorf("observation %d changed = %v, want %v", i, changed, observation.changed)
		}
		if got := object.GetAnnotations()[annotationStatus]; got != observation.want {
			t.Errorf("observation %d annotation = %q, want %q", i, got, observation.want)
		}
	}
}
