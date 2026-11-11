// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas_test

import (
	"encoding/json"
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog/kartas"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

func TestInferenceStatusMappings(t *testing.T) {
	tests := []struct {
		name       string
		definition *v1alpha1.Karta
		object     string
		want       []v1alpha1.ResourceStatus
	}{
		{"NIMService/absent status", kartas.NIMService(), `{}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"NIMCache/absent status", kartas.NIMCache(), `{}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"GrovePodCliqueSet/absent status", kartas.GrovePodCliqueSet(), `{}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"KServe/absent status", kartas.KServe(), `{}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"LLMInferenceService/absent status", kartas.LLMInferenceService(), `{}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"LLMInferenceServiceV1alpha2/absent status", kartas.LLMInferenceServiceV1alpha2(), `{}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"Sandbox/absent status", kartas.Sandbox(), `{}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"NIMService/Pending", kartas.NIMService(), `{"status":{"state":"Pending"}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"NIMService/NotReady", kartas.NIMService(), `{"status":{"state":"NotReady"}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"NIMService/PVC created", kartas.NIMService(), `{"status":{"state":"PVC-Created"}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"NIMService/zero replicas", kartas.NIMService(), `{"spec":{"replicas":0},"status":{"state":"NotReady"}}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"NIMService/stale provisioning", kartas.NIMService(), `{"metadata":{"generation":2},"status":{"observedGeneration":1,"state":"PVC-Created"}}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"NIMService/ready with old PVC condition", kartas.NIMService(), `{"status":{"state":"Ready","conditions":[{"type":"NIM_CACHE_PVC_CREATED","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.RunningStatus}},
		{"NIMService/failed with old PVC condition", kartas.NIMService(), `{"status":{"state":"Failed","conditions":[{"type":"NIM_CACHE_PVC_CREATED","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.FailedStatus}},
		{"NIMCache/Pending", kartas.NIMCache(), `{"status":{"state":"Pending"}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"NIMCache/PVC-Created", kartas.NIMCache(), `{"status":{"state":"PVC-Created"}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"NIMCache/Started", kartas.NIMCache(), `{"status":{"state":"Started"}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"NIMCache/InProgress", kartas.NIMCache(), `{"status":{"state":"InProgress"}}`, []v1alpha1.ResourceStatus{v1alpha1.RunningStatus}},
		{"NIMCache/NotReady", kartas.NIMCache(), `{"status":{"state":"NotReady"}}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"NIMCache/Failed", kartas.NIMCache(), `{"status":{"state":"Failed"}}`, []v1alpha1.ResourceStatus{v1alpha1.FailedStatus}},
		{"NIMCache/created Job waiting", kartas.NIMCache(), `{"status":{"state":"Pending","conditions":[{"type":"NIM_CACHE_JOB_CREATED","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"NIMCache/historical creation alone", kartas.NIMCache(), `{"status":{"conditions":[{"type":"NIM_CACHE_JOB_CREATED","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"NIMCache/completion clears startup", kartas.NIMCache(), `{"status":{"state":"Ready","conditions":[{"type":"NIM_CACHE_JOB_CREATED","status":"True"},{"type":"NIM_CACHE_JOB_COMPLETED","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.CompletedStatus}},
		{"NIMCache/error retains creation", kartas.NIMCache(), `{"status":{"state":"NotReady","conditions":[{"type":"NIM_CACHE_JOB_CREATED","status":"True"},{"type":"NIM_CACHE_RECONCILE_FAILED","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"GrovePodCliqueSet/{\"replicas\": 1}", kartas.GrovePodCliqueSet(), `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{"replicas":1}}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"GrovePodCliqueSet/{\"availableReplicas\": 0}", kartas.GrovePodCliqueSet(), `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{"availableReplicas":0}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"GrovePodCliqueSet/{\"observedGeneration\": 1}", kartas.GrovePodCliqueSet(), `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{"observedGeneration":1}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"GrovePodCliqueSet/{\"availableReplicas\": 1}", kartas.GrovePodCliqueSet(), `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{"availableReplicas":1}}`, []v1alpha1.ResourceStatus{v1alpha1.RunningStatus}},
		{"GrovePodCliqueSet/{\"availableReplicas\": 1, \"conditions\": [{\"type\": \"UpdateInProgress\", \"status\": \"True\"}]}", kartas.GrovePodCliqueSet(), `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{"availableReplicas":1,"conditions":[{"type":"UpdateInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.RunningStatus, v1alpha1.ProgressingStatus}},
		{"GrovePodCliqueSet/{\"availableReplicas\": 0, \"conditions\": [{\"type\": \"UpdateInProgress\", \"status\": \"True\"}]}", kartas.GrovePodCliqueSet(), `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{"availableReplicas":0,"conditions":[{"type":"UpdateInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"GrovePodCliqueSet/{\"availableReplicas\": 0, \"conditions\": [{\"type\": \"TopologyLevelsUnavailable\", \"status\": \"True\"}, {\"type\": \"UpdateInProgress\", \"status\": \"True\"}]}", kartas.GrovePodCliqueSet(), `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{"availableReplicas":0,"conditions":[{"type":"TopologyLevelsUnavailable","status":"True"},{"type":"UpdateInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.FailedStatus}},
		{"GrovePodCliqueSet/stalled unfinished rollout", kartas.GrovePodCliqueSet(), `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{"availableReplicas":0,"conditions":[{"type":"UpdateInProgress","status":"Unknown","reason":"ProgressDeadlineExceeded"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"GrovePodCliqueSet/unrecognized update uncertainty", kartas.GrovePodCliqueSet(), `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{"availableReplicas":0,"conditions":[{"type":"UpdateInProgress","status":"Unknown","reason":"OtherReason"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"GrovePodCliqueSet/convergence watermark lags update", kartas.GrovePodCliqueSet(), `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"availableReplicas":0,"updateProgress":{"updateStartedAt":"2026-10-18T00:00:00Z"},"conditions":[{"type":"UpdateInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"GrovePodCliqueSet/stalled update with old watermark", kartas.GrovePodCliqueSet(), `{"metadata":{"generation":3},"spec":{"replicas":1},"status":{"observedGeneration":1,"availableReplicas":1,"conditions":[{"type":"UpdateInProgress","status":"Unknown","reason":"ProgressDeadlineExceeded"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"GrovePodCliqueSet/completed update retains old condition", kartas.GrovePodCliqueSet(), `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"availableReplicas":0,"updateProgress":{"updateEndedAt":"2026-10-18T00:01:00Z"},"conditions":[{"type":"UpdateInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"GrovePodCliqueSet/completed ready update retains old condition", kartas.GrovePodCliqueSet(), `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":2,"availableReplicas":1,"updateProgress":{"updateEndedAt":"2026-10-18T00:01:00Z"},"conditions":[{"type":"UpdateInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.RunningStatus}},
		{"GrovePodCliqueSet/OnDelete abandons automatic update", kartas.GrovePodCliqueSet(), `{"metadata":{"generation":3},"spec":{"replicas":1,"updateStrategy":{"type":"OnDelete"}},"status":{"observedGeneration":1,"conditions":[{"type":"UpdateInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"GrovePodCliqueSet/deleted update", kartas.GrovePodCliqueSet(), `{"metadata":{"generation":2,"deletionTimestamp":"2026-10-18T00:01:00Z"},"spec":{"replicas":1},"status":{"observedGeneration":1,"conditions":[{"type":"UpdateInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"GrovePodCliqueSet/root timestamp reports unfinished update", kartas.GrovePodCliqueSet(), `{"spec":{"replicas":1},"status":{"availableReplicas":0,"updateProgress":{"updateStartedAt":"2026-10-18T00:00:00Z"}}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"GrovePodCliqueSet/cleared update condition", kartas.GrovePodCliqueSet(), `{"spec":{"replicas":1},"status":{"availableReplicas":1,"conditions":[{"type":"UpdateInProgress","status":"False","reason":"NoActiveUpdate"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.RunningStatus}},
		{"KServe/unknown readiness", kartas.KServe(), `{"status":{"conditions":[{"type":"Ready","status":"Unknown"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"KServe/model loading", kartas.KServe(), `{"status":{"modelStatus":{"states":{"targetModelState":"Loading"}}}}`, []v1alpha1.ResourceStatus{v1alpha1.ProgressingStatus}},
		{"KServe/loading waits for replica", kartas.KServe(), `{"status":{"modelStatus":{"states":{"targetModelState":"Loading"}},"conditions":[{"type":"PredictorReady","status":"False","reason":"MinimumReplicasUnavailable"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"KServe/ready while loading", kartas.KServe(), `{"status":{"modelStatus":{"states":{"targetModelState":"Loading"}},"conditions":[{"type":"PredictorReady","status":"True"},{"type":"RoutesReady","status":"True"},{"type":"LatestDeploymentReady","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.RunningStatus, v1alpha1.ProgressingStatus}},
		{"KServe/failed with retained loading", kartas.KServe(), `{"status":{"modelStatus":{"states":{"targetModelState":"Loading"}},"conditions":[{"type":"PredictorReady","status":"False"},{"type":"PredictorConfigurationReady","status":"False"},{"type":"RoutesReady","status":"False"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.FailedStatus}},
		{"LLMInferenceService/WaitingForGateway", kartas.LLMInferenceService(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"WaitingForGateway"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"LLMInferenceService/GatewaysNotReady", kartas.LLMInferenceService(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"GatewaysNotReady"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"LLMInferenceService/HTTPRoutesNotReady", kartas.LLMInferenceService(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"HTTPRoutesNotReady"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"LLMInferenceService/HPAProgressing", kartas.LLMInferenceService(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"HPAProgressing"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"LLMInferenceService/ScaledObjectProgressing", kartas.LLMInferenceService(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"ScaledObjectProgressing"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"LLMInferenceService/MinimumReplicasUnavailable", kartas.LLMInferenceService(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"MinimumReplicasUnavailable"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"LLMInferenceService/workload progressing", kartas.LLMInferenceService(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"Progressing"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"LLMInferenceService/preset failure", kartas.LLMInferenceService(), `{"status":{"conditions":[{"type":"Ready","status":"Unknown"},{"type":"PresetsCombined","status":"False","reason":"MissingPreset"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.FailedStatus}},
		{"LLMInferenceService/stopped", kartas.LLMInferenceService(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"Stopped"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.SuspendedStatus}},
		{"LLMInferenceService/unknown", kartas.LLMInferenceService(), `{"status":{"conditions":[{"type":"Ready","status":"Unknown"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"LLMInferenceService/healthy", kartas.LLMInferenceService(), `{"status":{"conditions":[{"type":"Ready","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.RunningStatus}},
		{"LLMInferenceService/stale progress", kartas.LLMInferenceService(), `{"metadata":{"generation":2},"status":{"observedGeneration":1,"conditions":[{"type":"Ready","status":"False","reason":"Progressing"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"LLMInferenceServiceV1alpha2/WaitingForGateway", kartas.LLMInferenceServiceV1alpha2(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"WaitingForGateway"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"LLMInferenceServiceV1alpha2/GatewaysNotReady", kartas.LLMInferenceServiceV1alpha2(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"GatewaysNotReady"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"LLMInferenceServiceV1alpha2/HTTPRoutesNotReady", kartas.LLMInferenceServiceV1alpha2(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"HTTPRoutesNotReady"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"LLMInferenceServiceV1alpha2/HPAProgressing", kartas.LLMInferenceServiceV1alpha2(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"HPAProgressing"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"LLMInferenceServiceV1alpha2/ScaledObjectProgressing", kartas.LLMInferenceServiceV1alpha2(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"ScaledObjectProgressing"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"LLMInferenceServiceV1alpha2/MinimumReplicasUnavailable", kartas.LLMInferenceServiceV1alpha2(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"MinimumReplicasUnavailable"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"LLMInferenceServiceV1alpha2/workload progressing", kartas.LLMInferenceServiceV1alpha2(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"Progressing"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"LLMInferenceServiceV1alpha2/preset failure", kartas.LLMInferenceServiceV1alpha2(), `{"status":{"conditions":[{"type":"Ready","status":"Unknown"},{"type":"PresetsCombined","status":"False","reason":"MissingPreset"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.FailedStatus}},
		{"LLMInferenceServiceV1alpha2/stopped", kartas.LLMInferenceServiceV1alpha2(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"Stopped"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.SuspendedStatus}},
		{"LLMInferenceServiceV1alpha2/unknown", kartas.LLMInferenceServiceV1alpha2(), `{"status":{"conditions":[{"type":"Ready","status":"Unknown"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"LLMInferenceServiceV1alpha2/healthy", kartas.LLMInferenceServiceV1alpha2(), `{"status":{"conditions":[{"type":"Ready","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.RunningStatus}},
		{"LLMInferenceServiceV1alpha2/stale progress", kartas.LLMInferenceServiceV1alpha2(), `{"metadata":{"generation":2},"status":{"observedGeneration":1,"conditions":[{"type":"Ready","status":"False","reason":"Progressing"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"Sandbox/not scheduled", kartas.Sandbox(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"DependenciesNotReady"},{"type":"Suspended","status":"False"},{"type":"PodScheduled","status":"False","reason":"Unschedulable"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"Sandbox/scheduled startup", kartas.Sandbox(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"DependenciesNotReady"},{"type":"Suspended","status":"False"},{"type":"PodScheduled","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"Sandbox/finished with retained scheduled", kartas.Sandbox(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"DependenciesNotReady"},{"type":"Suspended","status":"False"},{"type":"PodScheduled","status":"True"},{"type":"Finished","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"Sandbox/reconcile error", kartas.Sandbox(), `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"ReconcilerError"},{"type":"Suspended","status":"False"},{"type":"PodScheduled","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"Sandbox/stale readiness conditions", kartas.Sandbox(), `{"metadata":{"generation":2},"status":{"conditions":[{"type":"Ready","status":"False","reason":"DependenciesNotReady","observedGeneration":1},{"type":"Suspended","status":"False","observedGeneration":1},{"type":"PodScheduled","status":"True","observedGeneration":1}]}}`, []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}},
		{"Sandbox/stale scheduling condition", kartas.Sandbox(), `{"metadata":{"generation":2},"status":{"conditions":[{"type":"Ready","status":"False","reason":"DependenciesNotReady","observedGeneration":2},{"type":"Suspended","status":"False","observedGeneration":2},{"type":"PodScheduled","status":"True","observedGeneration":1}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus}},
		{"Sandbox/current scheduling condition", kartas.Sandbox(), `{"metadata":{"generation":2},"status":{"conditions":[{"type":"Ready","status":"False","reason":"DependenciesNotReady","observedGeneration":2},{"type":"Suspended","status":"False","observedGeneration":2},{"type":"PodScheduled","status":"True","observedGeneration":2}]}}`, []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus}},
		{"Sandbox/suspending with old startup", kartas.Sandbox(), `{"spec":{"operatingMode":"Suspended"},"status":{"conditions":[{"type":"Ready","status":"False","reason":"DependenciesNotReady"},{"type":"Suspended","status":"False"},{"type":"PodScheduled","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.SuspendingStatus}},
		{"Sandbox/resuming", kartas.Sandbox(), `{"spec":{"operatingMode":"Running"},"status":{"conditions":[{"type":"Ready","status":"False","reason":"DependenciesNotReady"},{"type":"Suspended","status":"True"},{"type":"PodScheduled","status":"True"}]}}`, []v1alpha1.ResourceStatus{v1alpha1.ResumingStatus}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			object := &unstructured.Unstructured{}
			if err := json.Unmarshal([]byte(tc.object), &object.Object); err != nil {
				t.Fatal(err)
			}
			component, err := resource.NewComponentFactoryFromObject(tc.definition, object).GetRootComponent()
			if err != nil {
				t.Fatal(err)
			}
			status, err := component.GetStatus(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			got := slices.Clone(status.MatchedStatuses)
			want := slices.Clone(tc.want)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("matched statuses = %v, want %v", got, want)
			}
		})
	}
}
