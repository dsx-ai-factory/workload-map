// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/json"

	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

func TestInferenceObservationFlowPredicates(t *testing.T) {
	tests := []struct {
		name   string
		check  recorder.StateCheck
		object string
		want   bool
	}{
		{"Dynamo initial alpha", DynamoPending(), `{"apiVersion":"nvidia.com/v1alpha1","metadata":{"generation":1},"spec":{"services":{"worker":{"replicas":1}}},"status":{"state":""}}`, true},
		{"Dynamo deleting pending rollout", DynamoPending(), `{"metadata":{"generation":2,"deletionTimestamp":"2026-10-18T00:00:00Z"},"status":{"state":"pending","observedGeneration":2,"rollingUpdate":{"phase":"InProgress"}}}`, false},
		{"Dynamo deleting unfinished rollout", DynamoProgressing(), `{"metadata":{"generation":2,"deletionTimestamp":"2026-10-18T00:00:00Z"},"status":{"state":"pending","observedGeneration":2,"rollingUpdate":{"phase":"InProgress"}}}`, false},
		{"Dynamo initial beta", DynamoPending(), `{"apiVersion":"nvidia.com/v1beta1","metadata":{"generation":1},"spec":{"components":[{"name":"worker"}]},"status":{"state":"initializing"}}`, true},
		{"Dynamo stale success", DynamoPending(), `{"apiVersion":"nvidia.com/v1beta1","metadata":{"generation":2},"spec":{"components":[{"name":"worker","replicas":1}]},"status":{"state":"successful","observedGeneration":1}}`, true},
		{"Dynamo old failure", DynamoPending(), `{"apiVersion":"nvidia.com/v1beta1","metadata":{"generation":2},"spec":{"components":[{"name":"worker","replicas":1}]},"status":{"state":"failed","observedGeneration":1}}`, false},
		{"Dynamo initial zero", DynamoPending(), `{"apiVersion":"nvidia.com/v1beta1","metadata":{"generation":1},"spec":{"components":[{"name":"worker","replicas":0}]},"status":{"state":""}}`, false},
		{"Dynamo unknown status", DynamoPending(), `{"apiVersion":"nvidia.com/v1beta1","metadata":{"generation":1},"spec":{"components":[{"name":"worker"}]},"status":{"state":"","conditions":[{"type":"Ready","status":"False","reason":"Error"}]}}`, false},
		{"Knative config still updating", KnativeProgressing(), `{"status":{"conditions":[{"type":"Ready","status":"Unknown","reason":"RevisionMissing"},{"type":"ConfigurationsReady","status":"Unknown","reason":"OutOfDate"}]}}`, true},
		{"Knative equal revisions route transition", KnativeProgressing(), `{"metadata":{"generation":1},"status":{"observedGeneration":1,"latestCreatedRevisionName":"service-00001","latestReadyRevisionName":"service-00001","conditions":[{"type":"Ready","status":"Unknown","reason":"IngressNotConfigured"},{"type":"RoutesReady","status":"Unknown","reason":"OutOfDate"}]}}`, true},
		{"Knative deleting equal revisions route transition", KnativeProgressing(), `{"metadata":{"generation":1,"deletionTimestamp":"2026-10-19T00:00:00Z"},"status":{"observedGeneration":1,"latestCreatedRevisionName":"service-00001","latestReadyRevisionName":"service-00001","conditions":[{"type":"Ready","status":"Unknown","reason":"IngressNotConfigured"},{"type":"RoutesReady","status":"Unknown","reason":"OutOfDate"}]}}`, false},
		{"Knative deleting route preserves readiness wait", KnativePending(), `{"metadata":{"generation":1,"deletionTimestamp":"2026-10-19T00:00:00Z"},"status":{"observedGeneration":1,"latestCreatedRevisionName":"service-00001","latestReadyRevisionName":"service-00001","conditions":[{"type":"Ready","status":"Unknown","reason":"IngressNotConfigured"},{"type":"RoutesReady","status":"Unknown","reason":"OutOfDate"}]}}`, true},
		{"Knative waits for ingress", KnativeProgressing(), `{"status":{"conditions":[{"type":"Ready","status":"Unknown","reason":"IngressNotConfigured"},{"type":"ConfigurationsReady","status":"True"}]}}`, false},
		{"Milvus initial standalone", MilvusPending(), `{"metadata":{"generation":2},"spec":{"mode":"standalone"}}`, true},
		{"Milvus stopped standalone", MilvusPending(), `{"metadata":{"generation":2},"spec":{"mode":"standalone","components":{"standalone":{"replicas":0}}}}`, false},
		{"Milvus manual standalone", MilvusPending(), `{"metadata":{"generation":2},"spec":{"mode":"standalone","components":{"enableManualMode":true}}}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			object := &unstructured.Unstructured{}
			if err := json.Unmarshal([]byte(tc.object), &object.Object); err != nil {
				t.Fatal(err)
			}
			if got := tc.check(object); got != tc.want {
				t.Errorf("native predicate = %t, want %t", got, tc.want)
			}
		})
	}
}
