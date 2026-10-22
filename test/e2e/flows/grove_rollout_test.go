// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	utiljson "k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/yaml"

	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

func TestGroveSupportsUpdateCondition(t *testing.T) {
	for _, test := range []struct {
		version string
		want    bool
	}{
		{"v0.1.0-alpha.10-rc1", false},
		{"v0.1.0-alpha.13", false},
		{"v0.1.0-alpha.14-rc1", false},
		{"v0.1.0-alpha.14", true},
		{"v0.1.0-alpha.14+build.1", true},
		{"v0.1.0-alpha.15-rc1", true},
		{"v0.1.0-alpha.15", true},
		{"v0.1.0", true},
	} {
		t.Run(test.version, func(t *testing.T) {
			got, err := groveSupportsUpdateCondition(test.version)
			if err != nil || got != test.want {
				t.Fatalf("groveSupportsUpdateCondition(%q) = %t, %v; want %t", test.version, got, err, test.want)
			}
		})
	}
	if _, err := groveSupportsUpdateCondition("unknown"); err == nil {
		t.Fatal("an unknown version must not silently enable or skip the flow")
	}
}

func TestGroveWorkerUpdatePatch(t *testing.T) {
	manifest := "testdata/grove/updated.yaml"
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var before map[string]any
	if err := yaml.Unmarshal(data, &before); err != nil {
		t.Fatal(err)
	}
	data, err = groveWorkerUpdatePatch(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var patch map[string]any
	if err := json.Unmarshal(data, &patch); err != nil {
		t.Fatal(err)
	}
	if len(patch) != 1 || patch["spec"] == nil {
		t.Fatalf("update must patch only spec: %s", data)
	}
	cliques, _, _ := unstructured.NestedSlice(patch, "spec", "template", "cliques")
	if len(cliques) != 1 {
		t.Fatalf("worker clique missing: %s", data)
	}
	worker := cliques[0].(map[string]any)
	if annotation, _, _ := unstructured.NestedString(worker, "annotations", "karta-e2e-update"); annotation != "replacement" {
		t.Fatalf("worker annotation update missing: %s", data)
	}
	delete(worker, "annotations")
	originalCliques, _, _ := unstructured.NestedSlice(before, "spec", "template", "cliques")
	if !reflect.DeepEqual(cliques, originalCliques) {
		t.Fatal("update changed fields other than the worker annotation")
	}
}

func TestGroveRolloutComplete(t *testing.T) {
	for _, test := range []struct {
		name   string
		object string
		want   bool
	}{
		{"completed", `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":2,"availableReplicas":1,"updatedReplicas":1,"updateProgress":{"updateEndedAt":"2026-10-18T00:01:00Z"},"conditions":[{"type":"UpdateInProgress","status":"False","reason":"NoActiveUpdate"}]}}`, true},
		{"initial ready", `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{"observedGeneration":1,"availableReplicas":1,"updatedReplicas":1,"conditions":[{"type":"UpdateInProgress","status":"False","reason":"NoActiveUpdate"}]}}`, false},
		{"old watermark", `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"availableReplicas":1,"updatedReplicas":1,"updateProgress":{"updateEndedAt":"2026-10-18T00:01:00Z"},"conditions":[{"type":"UpdateInProgress","status":"False","reason":"NoActiveUpdate"}]}}`, false},
		{"not ready", `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":2,"availableReplicas":0,"updatedReplicas":1,"updateProgress":{"updateEndedAt":"2026-10-18T00:01:00Z"},"conditions":[{"type":"UpdateInProgress","status":"False","reason":"NoActiveUpdate"}]}}`, false},
		{"not all updated", `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":2,"availableReplicas":1,"updatedReplicas":0,"updateProgress":{"updateEndedAt":"2026-10-18T00:01:00Z"},"conditions":[{"type":"UpdateInProgress","status":"False","reason":"NoActiveUpdate"}]}}`, false},
		{"condition not yet cleared", `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":2,"availableReplicas":1,"updatedReplicas":1,"updateProgress":{"updateEndedAt":"2026-10-18T00:01:00Z"},"conditions":[{"type":"UpdateInProgress","status":"True","reason":"Progressing"}]}}`, false},
		{"missing condition", `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":2,"availableReplicas":1,"updatedReplicas":1,"updateProgress":{"updateEndedAt":"2026-10-18T00:01:00Z"}}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var object unstructured.Unstructured
			if err := utiljson.Unmarshal([]byte(test.object), &object.Object); err != nil {
				t.Fatal(err)
			}
			if got := groveRolloutComplete(&object); got != test.want {
				t.Fatalf("groveRolloutComplete() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestGroveRecordedUpdateProgress(t *testing.T) {
	var object map[string]any
	if err := utiljson.Unmarshal([]byte(`{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"conditions":[{"type":"UpdateInProgress","status":"True"}]}}`), &object); err != nil {
		t.Fatal(err)
	}
	action := recorder.Event{Kind: recorder.EventAction, Action: &recorder.RecordedAction{Name: "Update"}}
	progress := recorder.Event{Kind: recorder.EventState, Phases: []string{"Progressing"}, Object: object, StaleObservedGeneration: true}
	for _, test := range []struct {
		name   string
		events []recorder.Event
		want   bool
	}{
		{"native progress after action", []recorder.Event{action, progress}, true},
		{"no observations", nil, false},
		{"before action", []recorder.Event{progress, action}, false},
		{"missing action", []recorder.Event{progress}, false},
		{"label alone", []recorder.Event{action, {Kind: recorder.EventState, Phases: []string{"Progressing"}, Object: map[string]any{}}}, false},
		{"native condition but missing phase", []recorder.Event{action, {Kind: recorder.EventState, Phases: []string{"Undefined"}, Object: object}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := groveRecordedUpdateProgress(&recorder.Recording{Events: test.events}); got != test.want {
				t.Fatalf("groveRecordedUpdateProgress() = %t, want %t", got, test.want)
			}
		})
	}
	if groveRecordedUpdateProgress(nil) {
		t.Fatal("nil recording cannot prove an update")
	}
}

func TestGroveReplacementPodsReady(t *testing.T) {
	original := []types.UID{"old-1", "old-2"}
	ready := []corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{UID: "new-1", Annotations: map[string]string{"karta-e2e-update": "replacement"}}, Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}},
		{ObjectMeta: metav1.ObjectMeta{UID: "new-2", Annotations: map[string]string{"karta-e2e-update": "replacement"}}, Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}},
	}
	for _, test := range []struct {
		name   string
		mutate func([]corev1.Pod)
		want   bool
	}{
		{"new ready pods", func([]corev1.Pod) {}, true},
		{"one original pod", func(p []corev1.Pod) { p[0].UID = original[0] }, false},
		{"missing uid", func(p []corev1.Pod) { p[0].UID = "" }, false},
		{"missing update annotation", func(p []corev1.Pod) { p[0].Annotations = nil }, false},
		{"unready replacement", func(p []corev1.Pod) { p[0].Status.Conditions[0].Status = corev1.ConditionFalse }, false},
		{"deleting replacement", func(p []corev1.Pod) { now := metav1.Now(); p[0].DeletionTimestamp = &now }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			pods := (&corev1.PodList{Items: ready}).DeepCopy().Items
			test.mutate(pods)
			if got := groveReplacementPodsReady(pods, original); got != test.want {
				t.Fatalf("groveReplacementPodsReady() = %t, want %t", got, test.want)
			}
		})
	}
	if groveReplacementPodsReady(ready[:1], original) || groveReplacementPodsReady(ready, nil) {
		t.Fatal("partial replicas or missing baseline cannot prove replacement")
	}
}
