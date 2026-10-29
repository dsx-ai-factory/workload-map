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

func TestInferenceFlowPredicates(t *testing.T) {
	checks := map[string]map[v1alpha1.ResourceStatus]recorder.StateCheck{
		"NIM": {
			v1alpha1.PendingStatus:     nimPending,
			v1alpha1.ProgressingStatus: AllOf(nimPending, PhaseEq("PVC-Created", "status", "state")),
		},
		"Grove": {
			v1alpha1.PendingStatus:     grovePending,
			v1alpha1.ProgressingStatus: groveProgressing,
			v1alpha1.RunningStatus:     groveRunning,
		},
		"Sandbox": {
			v1alpha1.PendingStatus:     sandboxPending,
			v1alpha1.ProgressingStatus: sandboxProgressing,
		},
		"KServe": {
			v1alpha1.PendingStatus:     inferenceServicePending,
			v1alpha1.ProgressingStatus: inferenceServiceProgressing,
		},
	}
	tests := []struct {
		name   string
		kind   string
		object string
		want   []v1alpha1.ResourceStatus
	}{
		{"unobserved service", "NIM", `{}`, nil},
		{"existing empty service", "NIM", `{"metadata":{"generation":1}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"existing empty service zero replicas", "NIM", `{"metadata":{"generation":1},"spec":{"replicas":0}}`, nil},
		{"existing service unknown state", "NIM", `{"metadata":{"generation":1},"status":{"state":"Other"}}`, nil},
		{"cache dependency wait", "NIM", `{"status":{"state":"NotReady"}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"PVC created", "NIM", `{"status":{"state":"PVC-Created"}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"zero replicas", "NIM", `{"spec":{"replicas":0},"status":{"state":"NotReady"}}`, nil},
		{"old provisioning observation", "NIM", `{"metadata":{"generation":2},"status":{"observedGeneration":1,"state":"PVC-Created"}}`, nil},
		{"ready clears waiting", "NIM", `{"status":{"state":"Ready"}}`, nil},
		{"failed clears waiting", "NIM", `{"status":{"state":"Failed"}}`, nil},
		{"unobserved set", "Grove", `{"spec":{"replicas":1}}`, nil},
		{"copied desired count alone", "Grove", `{"spec":{"replicas":1},"status":{"replicas":1}}`, nil},
		{"omitted zero availability after observation", "Grove", `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{"observedGeneration":1}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"waiting rollout", "Grove", `{"spec":{"replicas":1},"status":{"availableReplicas":0,"conditions":[{"type":"UpdateInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"stalled unfinished rollout", "Grove", `{"spec":{"replicas":1},"status":{"availableReplicas":0,"conditions":[{"type":"UpdateInProgress","status":"Unknown","reason":"ProgressDeadlineExceeded"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"unrecognized update uncertainty", "Grove", `{"spec":{"replicas":1},"status":{"availableReplicas":0,"conditions":[{"type":"UpdateInProgress","status":"Unknown","reason":"OtherReason"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"available rollout", "Grove", `{"spec":{"replicas":1},"status":{"availableReplicas":1,"conditions":[{"type":"UpdateInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.RunningStatus, v1alpha1.ProgressingStatus}},
		{"topology failure", "Grove", `{"spec":{"replicas":1},"status":{"availableReplicas":0,"conditions":[{"type":"TopologyLevelsUnavailable","status":"True"},{"type":"UpdateInProgress","status":"True"}]}}`, nil},
		{"convergence watermark lags update", "Grove", `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"availableReplicas":0,"updateProgress":{"updateStartedAt":"2026-10-18T00:00:00Z"},"conditions":[{"type":"UpdateInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"stalled update with old watermark", "Grove", `{"metadata":{"generation":3},"spec":{"replicas":1},"status":{"observedGeneration":1,"availableReplicas":1,"conditions":[{"type":"UpdateInProgress","status":"Unknown","reason":"ProgressDeadlineExceeded"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"completed update retains old condition", "Grove", `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"availableReplicas":0,"updateProgress":{"updateEndedAt":"2026-10-18T00:01:00Z"},"conditions":[{"type":"UpdateInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"completed ready update retains old condition", "Grove", `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":2,"availableReplicas":1,"updateProgress":{"updateEndedAt":"2026-10-18T00:01:00Z"},"conditions":[{"type":"UpdateInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.RunningStatus}},
		{"OnDelete abandons automatic update", "Grove", `{"metadata":{"generation":3},"spec":{"replicas":1,"updateStrategy":{"type":"OnDelete"}},"status":{"observedGeneration":1,"conditions":[{"type":"UpdateInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"deleted update", "Grove", `{"metadata":{"generation":2,"deletionTimestamp":"2026-10-18T00:01:00Z"},"spec":{"replicas":1},"status":{"observedGeneration":1,"conditions":[{"type":"UpdateInProgress","status":"True"}]}}`, nil},
		{"root timestamp reports unfinished update", "Grove", `{"spec":{"replicas":1},"status":{"availableReplicas":0,"updateProgress":{"updateStartedAt":"2026-10-18T00:00:00Z"}}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"cleared update condition", "Grove", `{"spec":{"replicas":1},"status":{"availableReplicas":1,"conditions":[{"type":"UpdateInProgress","status":"False","reason":"NoActiveUpdate"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.RunningStatus}},
		{"zero target satisfied", "Grove", `{"spec":{"replicas":0},"status":{"observedGeneration":1}}`, []v1alpha1.ResourceStatus{v1alpha1.RunningStatus}},
		{"unobserved sandbox", "Sandbox", `{}`, nil},
		{"scheduling wait", "Sandbox", `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"DependenciesNotReady"},{"type":"Suspended","status":"False"},{"type":"PodScheduled","status":"False"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"scheduled readiness wait", "Sandbox", `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"DependenciesNotReady"},{"type":"Suspended","status":"False"},{"type":"PodScheduled","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"finished pod remains scheduled", "Sandbox", `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"DependenciesNotReady"},{"type":"Suspended","status":"False"},{"type":"PodScheduled","status":"True"},{"type":"Finished","status":"True"}]}}`, nil},
		{"reconcile error", "Sandbox", `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"ReconcilerError"},{"type":"Suspended","status":"False"},{"type":"PodScheduled","status":"True"}]}}`, nil},
		{"suspended intent", "Sandbox", `{"spec":{"operatingMode":"Suspended"},"status":{"conditions":[{"type":"Ready","status":"False","reason":"DependenciesNotReady"},{"type":"Suspended","status":"False"},{"type":"PodScheduled","status":"True"}]}}`, nil},
		{"old readiness conditions", "Sandbox", `{"metadata":{"generation":2},"status":{"conditions":[{"type":"Ready","status":"False","reason":"DependenciesNotReady","observedGeneration":1},{"type":"Suspended","status":"False","observedGeneration":1},{"type":"PodScheduled","status":"True","observedGeneration":1}]}}`, nil},
		{"old scheduling condition", "Sandbox", `{"metadata":{"generation":2},"status":{"conditions":[{"type":"Ready","status":"False","reason":"DependenciesNotReady","observedGeneration":2},{"type":"Suspended","status":"False","observedGeneration":2},{"type":"PodScheduled","status":"True","observedGeneration":1}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"current scheduling condition", "Sandbox", `{"metadata":{"generation":2},"status":{"conditions":[{"type":"Ready","status":"False","reason":"DependenciesNotReady","observedGeneration":2},{"type":"Suspended","status":"False","observedGeneration":2},{"type":"PodScheduled","status":"True","observedGeneration":2}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"unobserved inference service", "KServe", `{}`, nil},
		{"existing empty inference service", "KServe", `{"metadata":{"generation":1},"status":{}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"stopped empty inference service", "KServe", `{"metadata":{"generation":1,"annotations":{"serving.kserve.io/stop":"True"}}}`, nil},
		{"deleted empty inference service", "KServe", `{"metadata":{"generation":1,"deletionTimestamp":"2026-10-18T00:00:00Z"}}`, nil},
		{"unknown populated inference service", "KServe", `{"metadata":{"generation":1},"status":{"conditions":[]}}`, nil},
		{"unknown readiness", "KServe", `{"status":{"conditions":[{"type":"Ready","status":"Unknown"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"loading alone", "KServe", `{"status":{"modelStatus":{"states":{"targetModelState":"Loading"}}}}`, []v1alpha1.ResourceStatus{v1alpha1.ProgressingStatus}},
		{"loading waits for replicas", "KServe", `{"status":{"modelStatus":{"states":{"targetModelState":"Loading"}},"conditions":[{"type":"PredictorReady","status":"False","reason":"MinimumReplicasUnavailable"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"ready while loading", "KServe", `{"status":{"modelStatus":{"states":{"targetModelState":"Loading"}},"conditions":[{"type":"Ready","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.ProgressingStatus}},
		{"old loading observation", "KServe", `{"metadata":{"generation":2},"status":{"observedGeneration":1,"modelStatus":{"states":{"targetModelState":"Loading"}}}}`, nil},
		{"failure with retained loading", "KServe", `{"status":{"modelStatus":{"states":{"targetModelState":"Loading"}},"conditions":[{"type":"PredictorReady","status":"False"},{"type":"PredictorConfigurationReady","status":"False"},{"type":"RoutesReady","status":"False"}]}}`, nil},
	}
	for _, tc := range tests {
		t.Run(tc.kind+"/"+tc.name, func(t *testing.T) {
			object := &unstructured.Unstructured{}
			if err := json.Unmarshal([]byte(tc.object), &object.Object); err != nil {
				t.Fatal(err)
			}
			var got []v1alpha1.ResourceStatus
			for status, check := range checks[tc.kind] {
				if check(object) {
					got = append(got, status)
				}
			}
			slices.Sort(got)
			want := slices.Clone(tc.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("native predicate matches = %v, want %v", got, want)
			}
		})
	}
}
