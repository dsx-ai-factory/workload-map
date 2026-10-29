// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/json"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

func TestNIMCacheFlowPredicates(t *testing.T) {
	checks := map[v1alpha1.ResourceStatus]recorder.StateCheck{
		v1alpha1.PendingStatus:     nimCachePending,
		v1alpha1.ProgressingStatus: nimCacheProgressing,
		v1alpha1.RunningStatus:     PhaseEq("InProgress", "status", "state"),
		v1alpha1.CompletedStatus:   AllOf(PhaseEq("Ready", "status", "state"), CondTrue("NIM_CACHE_JOB_COMPLETED")),
		v1alpha1.FailedStatus:      PhaseEq("Failed", "status", "state"),
	}
	tests := []struct {
		name   string
		object string
		want   []v1alpha1.ResourceStatus
	}{
		{"absent status", `{}`, nil},
		{"existing empty cache", `{"metadata":{"generation":1}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"deleted empty cache", `{"metadata":{"generation":1,"deletionTimestamp":"2026-10-18T00:00:00Z"}}`, nil},
		{"current reconcile error", `{"status":{"state":"NotReady","conditions":[{"type":"NIM_CACHE_RECONCILE_FAILED","status":"True","reason":"ReconcileFailed"}]}}`, nil},
		{"old reconcile error", `{"metadata":{"generation":2},"status":{"observedGeneration":1,"state":"NotReady","conditions":[{"type":"NIM_CACHE_RECONCILE_FAILED","status":"True","reason":"ReconcileFailed"}]}}`, nil},
		{"historical reconcile error", `{"status":{"state":"InProgress","conditions":[{"type":"NIM_CACHE_RECONCILE_FAILED","status":"True","reason":"ReconcileFailed"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.RunningStatus}},
		{"waiting without created job", `{"status":{"state":"Pending"}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"PVC created", `{"status":{"state":"PVC-Created"}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"job created", `{"status":{"state":"Started"}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"created job waits", `{"status":{"state":"Pending","conditions":[{"type":"NIM_CACHE_JOB_CREATED","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"active job", `{"status":{"state":"InProgress","conditions":[{"type":"NIM_CACHE_JOB_CREATED","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.RunningStatus}},
		{"successful job", `{"status":{"state":"Ready","conditions":[{"type":"NIM_CACHE_JOB_CREATED","status":"True"},{"type":"NIM_CACHE_JOB_COMPLETED","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.CompletedStatus}},
		{"failed job", `{"status":{"state":"Failed","conditions":[{"type":"NIM_CACHE_JOB_CREATED","status":"True"},{"type":"NIM_CACHE_JOB_COMPLETED","status":"False"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.FailedStatus}},
		{"reconcile error", `{"status":{"state":"NotReady","conditions":[{"type":"NIM_CACHE_JOB_CREATED","status":"True"}]}}`, nil},
		{"historical job creation", `{"status":{"conditions":[{"type":"NIM_CACHE_JOB_CREATED","status":"True"}]}}`, nil},
		{"completion supersedes wait", `{"status":{"state":"Pending","conditions":[{"type":"NIM_CACHE_JOB_COMPLETED","status":"True"}]}}`, nil},
		{"old startup observation", `{"metadata":{"generation":2},"status":{"observedGeneration":1,"state":"Started"}}`, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			object := &unstructured.Unstructured{}
			if err := json.Unmarshal([]byte(tc.object), &object.Object); err != nil {
				t.Fatal(err)
			}
			var got []v1alpha1.ResourceStatus
			for status, check := range checks {
				if check(object) {
					got = append(got, status)
				}
			}
			slices.Sort(got)
			want := slices.Clone(tc.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("matched statuses = %v, want %v", got, want)
			}
		})
	}
}
