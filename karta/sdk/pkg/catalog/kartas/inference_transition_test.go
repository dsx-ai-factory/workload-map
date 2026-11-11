// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog/kartas"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

func TestInferenceFirstObservation(t *testing.T) {
	definitions := []*v1alpha1.Karta{kartas.NIMService(), kartas.NIMCache(), kartas.KServe(), kartas.LLMInferenceService(), kartas.LLMInferenceServiceV1alpha2()}
	for _, definition := range definitions {
		t.Run(definition.Name, func(t *testing.T) {
			for _, status := range []string{`{}`, `null`} {
				assertInferencePhases(t, definition, `{"metadata":{"generation":1},"status":`+status+`}`, v1alpha1.PendingStatus)
			}
			assertInferencePhases(t, definition, `{"metadata":{"generation":1}}`, v1alpha1.PendingStatus)
			assertInferencePhases(t, definition, `{}`, v1alpha1.UndefinedStatus)
			assertInferencePhases(t, definition, `{"metadata":{"generation":1,"deletionTimestamp":"2026-10-18T00:00:00Z"}}`, v1alpha1.UndefinedStatus)
			assertInferencePhases(t, definition, `{"metadata":{"generation":1},"status":{"state":"Unrecognized"}}`, v1alpha1.UndefinedStatus)
			assertInferencePhases(t, definition, `{"metadata":{"generation":1},"status":{"conditions":[]}}`, v1alpha1.UndefinedStatus)
		})
	}
	for _, definition := range []*v1alpha1.Karta{kartas.NIMService(), kartas.LLMInferenceService(), kartas.LLMInferenceServiceV1alpha2()} {
		assertInferencePhases(t, definition, `{"metadata":{"generation":1},"spec":{"replicas":0}}`, v1alpha1.UndefinedStatus)
	}
	for _, definition := range []*v1alpha1.Karta{kartas.KServe(), kartas.LLMInferenceService(), kartas.LLMInferenceServiceV1alpha2()} {
		assertInferencePhases(t, definition, `{"metadata":{"generation":1,"annotations":{"serving.kserve.io/stop":"True"}}}`, v1alpha1.UndefinedStatus)
	}
	assertInferencePhases(t, kartas.Milvus(), `{"metadata":{"generation":2},"spec":{"mode":"standalone"}}`, v1alpha1.PendingStatus)
	assertInferencePhases(t, kartas.Milvus(), `{"metadata":{"generation":2},"spec":{"mode":"standalone","components":{"standalone":{"replicas":0}}}}`, v1alpha1.UndefinedStatus)
	assertInferencePhases(t, kartas.Milvus(), `{"metadata":{"generation":2},"spec":{"mode":"standalone","components":{"enableManualMode":true}}}`, v1alpha1.UndefinedStatus)
	assertInferencePhases(t, kartas.Milvus(), `{"metadata":{"generation":2},"spec":{"mode":"cluster"}}`, v1alpha1.UndefinedStatus)
}

func TestDynamoObservationTransitions(t *testing.T) {
	for _, definition := range []*v1alpha1.Karta{kartas.Dynamo(), kartas.DynamoV1beta1()} {
		t.Run(definition.Name, func(t *testing.T) {
			desired := `"services":{"worker":{"replicas":1}}`
			if definition.Spec.StructureDefinition.RootComponent.Kind.Version == "v1beta1" {
				desired = `"components":[{"name":"worker","replicas":1}]`
			}
			prefix := `{"metadata":{"generation":2},"spec":{` + desired + `},"status":`
			for _, status := range []string{`{}`, `{"state":""}`, `{"state":"initializing"}`, `{"state":"successful","observedGeneration":1}`, `{"state":"pending","observedGeneration":1}`} {
				assertInferencePhases(t, definition, prefix+status+`}`, v1alpha1.PendingStatus)
			}
			assertInferencePhases(t, definition, prefix+`{"state":"successful","observedGeneration":2}}`, v1alpha1.RunningStatus)
			assertInferencePhases(t, definition, prefix+`{"state":"pending","observedGeneration":2,"rollingUpdate":{"phase":"InProgress"}}}`, v1alpha1.PendingStatus, v1alpha1.ProgressingStatus)
			assertInferencePhases(t, definition, `{"metadata":{"generation":2,"deletionTimestamp":"2026-10-18T00:00:00Z"},"spec":{`+desired+`},"status":{"state":"pending","observedGeneration":2,"rollingUpdate":{"phase":"InProgress"}}}`, v1alpha1.UndefinedStatus)
			assertInferencePhases(t, definition, prefix+`{"state":"failed","observedGeneration":1}}`, v1alpha1.FailedStatus)
			assertInferencePhases(t, definition, prefix+`{"state":"Unrecognized","observedGeneration":1}}`, v1alpha1.UndefinedStatus)
			assertInferencePhases(t, definition, prefix+`{"state":"","conditions":[{"type":"Ready","status":"False","reason":"Error"}]}}`, v1alpha1.UndefinedStatus)
			assertInferencePhases(t, definition, `{"metadata":{"generation":2,"deletionTimestamp":"2026-10-18T00:00:00Z"},"spec":{`+desired+`},"status":{"state":"successful","observedGeneration":1}}`, v1alpha1.UndefinedStatus)
			assertInferencePhases(t, definition, `{"metadata":{"generation":1},"spec":{`+strings.ReplaceAll(desired, `"replicas":1`, `"replicas":0`)+`},"status":{"state":""}}`, v1alpha1.UndefinedStatus)
		})
	}
}

func TestInferenceTransitionEvidence(t *testing.T) {
	for _, condition := range []string{"ConfigurationsReady", "RoutesReady"} {
		assertInferencePhases(t, kartas.KnativeServing(), `{"status":{"conditions":[{"type":"Ready","status":"Unknown","reason":"RevisionMissing"},{"type":"`+condition+`","status":"Unknown","reason":"OutOfDate"}]}}`, v1alpha1.PendingStatus, v1alpha1.ProgressingStatus)
	}
	assertInferencePhases(t, kartas.KnativeServing(), `{"status":{"conditions":[{"type":"Ready","status":"Unknown","reason":"IngressNotConfigured"},{"type":"ConfigurationsReady","status":"True"}]}}`, v1alpha1.PendingStatus)
	assertInferencePhases(t, kartas.KnativeServing(), `{"status":{"conditions":[{"type":"Ready","status":"True"},{"type":"ConfigurationsReady","status":"Unknown","reason":"OutOfDate"}]}}`, v1alpha1.RunningStatus)
	errorCondition := `"conditions":[{"type":"NIM_CACHE_RECONCILE_FAILED","status":"True","reason":"ReconcileFailed"}]`
	assertInferencePhases(t, kartas.NIMCache(), `{"status":{"state":"NotReady",`+errorCondition+`}}`, v1alpha1.UndefinedStatus)
	assertInferencePhases(t, kartas.NIMCache(), `{"metadata":{"generation":2},"status":{"state":"NotReady","observedGeneration":1,`+errorCondition+`}}`, v1alpha1.UndefinedStatus)
	assertInferencePhases(t, kartas.NIMCache(), `{"status":{"state":"InProgress",`+errorCondition+`}}`, v1alpha1.RunningStatus)
	assertInferencePhases(t, kartas.NIMCache(), `{"status":{"state":"NotReady","conditions":[{"type":"NIM_CACHE_RECONCILE_FAILED","status":"False","reason":"Reconciled"}]}}`, v1alpha1.UndefinedStatus)
}

func assertInferencePhases(t *testing.T, definition *v1alpha1.Karta, input string, want ...v1alpha1.ResourceStatus) {
	t.Helper()
	object := &unstructured.Unstructured{}
	if err := json.Unmarshal([]byte(input), &object.Object); err != nil {
		t.Fatal(err)
	}
	component, err := resource.NewComponentFactoryFromObject(definition, object).GetRootComponent()
	if err != nil {
		t.Fatal(err)
	}
	status, err := component.GetStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := slices.Clone(status.MatchedStatuses)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s: %s matched %v, want %v", definition.Name, input, got, want)
	}
}
