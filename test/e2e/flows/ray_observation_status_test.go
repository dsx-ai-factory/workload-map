// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/json"

	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog/kartas"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

func TestRayObservationAndSuspensionPhases(t *testing.T) {
	tests := []struct {
		name, kind, object string
		want               []string
	}{
		{"new application without generation is unknown", "job", `{"metadata":{"uid":"fixture"},"status":{}}`, []string{"Undefined"}},
		{"new application with zero generation is unknown", "job", `{"metadata":{"generation":0},"status":{}}`, []string{"Undefined"}},
		{"new cluster without generation is unknown", "cluster", `{"metadata":{"uid":"fixture"},"status":{}}`, []string{"Undefined"}},
		{"new cluster with zero generation is unknown", "cluster", `{"metadata":{"generation":0},"status":{}}`, []string{"Undefined"}},
		{"new application awaiting observation", "job", `{"metadata":{"generation":1},"spec":{},"status":{}}`, []string{"Pending"}},
		{"resume reset awaiting observation", "job", `{"metadata":{"generation":1},"spec":{"suspend":false},"status":{"startTime":"2026-10-19T00:00:00Z"}}`, []string{"Pending"}},
		{"new suspension awaiting observation", "job", `{"metadata":{"generation":1},"spec":{"suspend":true}}`, []string{"Pending"}},
		{"new state with error stays unknown", "job", `{"metadata":{"generation":1},"status":{"reason":"UnexpectedFailure"}}`, []string{"Undefined"}},
		{"unknown deployment stays unknown", "job", `{"metadata":{"generation":1},"status":{"jobDeploymentStatus":"Unknown"}}`, []string{"Undefined"}},
		{"initialization awaits suspension acknowledgment", "job", `{"metadata":{"generation":1},"spec":{"suspend":true},"status":{"jobDeploymentStatus":"Initializing"}}`, []string{"Pending"}},
		{"pending application awaits suspension acknowledgment", "job", `{"metadata":{"generation":1},"spec":{"suspend":true},"status":{"jobDeploymentStatus":"Initializing","jobStatus":"PENDING"}}`, []string{"Pending"}},
		{"suspension wait with error remains unknown", "job", `{"metadata":{"generation":1},"spec":{"suspend":true},"status":{"jobDeploymentStatus":"Initializing","reason":"UnexpectedFailure"}}`, []string{"Undefined"}},
		{"unknown application is not suspension wait", "job", `{"metadata":{"generation":1},"spec":{"suspend":true},"status":{"jobDeploymentStatus":"Initializing","jobStatus":"UNKNOWN"}}`, []string{"Undefined"}},
		{"terminal application is not suspension wait", "job", `{"metadata":{"generation":1},"spec":{"suspend":true},"status":{"jobDeploymentStatus":"Initializing","jobStatus":"FAILED"}}`, []string{"Failed"}},
		{"deleting initialization is not suspension wait", "job", `{"metadata":{"generation":1,"deletionTimestamp":"2026-10-19T00:00:00Z"},"spec":{"suspend":true},"status":{"jobDeploymentStatus":"Initializing"}}`, []string{"Undefined"}},
		{"observed suspension cleanup", "job", `{"status":{"jobDeploymentStatus":"Suspending"}}`, []string{"Suspending"}},
		{"terminal status excludes suspension cleanup", "job", `{"status":{"jobDeploymentStatus":"Suspending","jobStatus":"SUCCEEDED"}}`, []string{"Completed"}},
		{"deleting job initialization is not progressing", "job", `{"metadata":{"generation":1,"deletionTimestamp":"2026-10-19T00:00:00Z"},"status":{"jobDeploymentStatus":"Initializing","jobStatus":"PENDING"}}`, []string{"Undefined"}},
		{"deleting new job is not waiting", "job", `{"metadata":{"generation":1,"deletionTimestamp":"2026-10-19T00:00:00Z"}}`, []string{"Undefined"}},
		{"cluster suspension intent awaits acknowledgment", "cluster", `{"metadata":{"generation":1},"spec":{"suspend":true}}`, []string{"Pending"}},
		{"cluster acknowledged suspended", "cluster", `{"metadata":{"generation":1},"spec":{"suspend":true},"status":{"observedGeneration":1,"state":"suspended"}}`, []string{"Suspended"}},
		{"cluster stale suspended is not acknowledgment", "cluster", `{"metadata":{"generation":2},"spec":{"suspend":true},"status":{"observedGeneration":1,"state":"suspended"}}`, []string{"Undefined"}},
		{"cluster explicit resume awaits acknowledgment", "cluster", `{"metadata":{"generation":2},"spec":{"suspend":false},"status":{"observedGeneration":1,"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"True"}]}}`, []string{"Pending"}},
		{"cluster deleting resume is not waiting", "cluster", `{"metadata":{"generation":2,"deletionTimestamp":"2026-10-19T00:00:00Z"},"spec":{"suspend":false},"status":{"observedGeneration":1,"state":"suspended"}}`, []string{"Undefined"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &unstructured.Unstructured{}
			if err := json.Unmarshal([]byte(tt.object), &u.Object); err != nil {
				t.Fatal(err)
			}
			definition := kartas.Rayjob()
			pending, progressing, suspending, suspended := RayJobPending()(u), RayJobProgressing()(u), RayJobSuspending()(u), PhaseEq("Suspended", "status", "jobDeploymentStatus")(u)
			running, completed, failed := PhaseEq("RUNNING", "status", "jobStatus")(u), PhaseEq("SUCCEEDED", "status", "jobStatus")(u), PhaseEq("FAILED", "status", "jobStatus")(u)
			if tt.kind == "cluster" {
				definition = kartas.Raycluster()
				pending, progressing, suspending, suspended = RayClusterPending()(u), RayClusterProgressing()(u), false, RaySuspended()(u)
				running, completed, failed = PhaseEq("ready", "status", "state")(u), false, PhaseEq("failed", "status", "state")(u)
			}
			var native []string
			for _, phase := range []struct {
				name    string
				matches bool
			}{{"Pending", pending}, {"Progressing", progressing}, {"Suspending", suspending}, {"Suspended", suspended}, {"Running", running}, {"Completed", completed}, {"Failed", failed}} {
				if phase.matches {
					native = append(native, phase.name)
				}
			}
			if len(native) == 0 {
				native = []string{"Undefined"}
			}
			slices.Sort(native)
			if !slices.Equal(native, tt.want) {
				t.Fatalf("native=%v,want %v", native, tt.want)
			}
			root, err := resource.NewComponentFactoryFromObject(definition, u).GetRootComponent()
			if err != nil {
				t.Fatal(err)
			}
			status, err := root.GetStatus(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var actual []string
			for _, phase := range status.MatchedStatuses {
				actual = append(actual, string(phase))
			}
			slices.Sort(actual)
			if !slices.Equal(actual, tt.want) {
				t.Fatalf("SDK=%v,want %v", actual, tt.want)
			}
		})
	}
}

func TestRayClusterMalformedStatusIsNotWaiting(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"generation": int64(1)}, "status": "unexpected",
	}}
	if RayClusterPending()(u) {
		t.Fatal("malformed status must not be classified as waiting")
	}
	root, err := resource.NewComponentFactoryFromObject(kartas.Raycluster(), u).GetRootComponent()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.GetStatus(t.Context()); err == nil {
		t.Fatal("malformed native status must retain the SDK extraction error")
	}
}
