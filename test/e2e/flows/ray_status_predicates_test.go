// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/json"
)

func TestRayJobStartupPredicates(t *testing.T) {
	tests := []struct {
		name        string
		object      string
		pending     bool
		progressing bool
	}{
		{"absent status", `{}`, false, false},
		{"initializing infrastructure", `{"status":{"jobDeploymentStatus":"Initializing"}}`, true, true},
		{"awaiting application status", `{"status":{"jobDeploymentStatus":"Running"}}`, true, false},
		{"empty application status", `{"status":{"jobDeploymentStatus":"Running","jobStatus":""}}`, true, false},
		{"null application status", `{"status":{"jobDeploymentStatus":"Running","jobStatus":null}}`, true, false},
		{"unknown application status", `{"status":{"jobDeploymentStatus":"Running","jobStatus":"UNKNOWN"}}`, false, false},
		{"malformed application status", `{"status":{"jobDeploymentStatus":"Running","jobStatus":1}}`, false, false},
		{"application executing", `{"status":{"jobDeploymentStatus":"Running","jobStatus":"RUNNING"}}`, false, false},
		{"application completed", `{"status":{"jobDeploymentStatus":"Running","jobStatus":"SUCCEEDED"}}`, false, false},
		{"application failed", `{"status":{"jobDeploymentStatus":"Running","jobStatus":"FAILED"}}`, false, false},
		{"application stopped", `{"status":{"jobDeploymentStatus":"Running","jobStatus":"STOPPED"}}`, false, false},
		{"suspension requested", `{"spec":{"suspend":true},"status":{"jobDeploymentStatus":"Running"}}`, false, false},
		{"suspension observed", `{"status":{"jobDeploymentStatus":"Suspended"}}`, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &unstructured.Unstructured{}
			if err := json.Unmarshal([]byte(tt.object), &u.Object); err != nil {
				t.Fatal(err)
			}
			if got := RayJobPending()(u); got != tt.pending {
				t.Errorf("Pending = %v, want %v", got, tt.pending)
			}
			if got := RayJobProgressing()(u); got != tt.progressing {
				t.Errorf("Progressing = %v, want %v", got, tt.progressing)
			}
		})
	}
}

func TestRayClusterResumePredicates(t *testing.T) {
	tests := []struct {
		name        string
		object      string
		pending     bool
		progressing bool
	}{
		{"awaiting head", `{"metadata":{"generation":2},"spec":{"suspend":false},"status":{"observedGeneration":2,"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"False"},{"type":"RayClusterSuspended","status":"False"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"HeadPodNotFound"}]}}`, true, false},
		{"starting head", `{"metadata":{"generation":2},"spec":{"suspend":false},"status":{"observedGeneration":2,"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"False"},{"type":"RayClusterSuspended","status":"False"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, true, true},
		{"stale observation", `{"metadata":{"generation":2},"spec":{"suspend":false},"status":{"observedGeneration":1,"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"False"},{"type":"RayClusterSuspended","status":"False"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, true, false},
		{"missing observed generation", `{"metadata":{"generation":2},"spec":{"suspend":false},"status":{"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"False"},{"type":"RayClusterSuspended","status":"False"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, true, false},
		{"missing desired generation", `{"spec":{"suspend":false},"status":{"observedGeneration":2,"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"False"},{"type":"RayClusterSuspended","status":"False"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, false, false},
		{"missing resume intent", `{"metadata":{"generation":2},"status":{"observedGeneration":2,"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"False"},{"type":"RayClusterSuspended","status":"False"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, false, false},
		{"suspension not cleared", `{"metadata":{"generation":2},"spec":{"suspend":false},"status":{"observedGeneration":2,"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"False"},{"type":"RayClusterSuspended","status":"True"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, true, false},
		{"suspension condition missing", `{"metadata":{"generation":2},"spec":{"suspend":false},"status":{"observedGeneration":2,"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"False"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, true, false},
		{"suspension condition unknown", `{"metadata":{"generation":2},"spec":{"suspend":false},"status":{"observedGeneration":2,"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"False"},{"type":"RayClusterSuspended","status":"Unknown"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &unstructured.Unstructured{}
			if err := json.Unmarshal([]byte(tt.object), &u.Object); err != nil {
				t.Fatal(err)
			}
			if got := RayClusterPending()(u); got != tt.pending {
				t.Errorf("Pending = %v, want %v", got, tt.pending)
			}
			if got := RayClusterProgressing()(u); got != tt.progressing {
				t.Errorf("Progressing = %v, want %v", got, tt.progressing)
			}
		})
	}
}
