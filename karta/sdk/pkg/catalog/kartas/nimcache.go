// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

// NIMCache returns the built-in Karta for the NIMCache workload
// (apps.nvidia.com/v1alpha1).
func NIMCache() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "apps-nvidia-com-nimcache-v1alpha1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "nimcache",
					Kind: &v1alpha1.GroupVersionKind{Group: "apps.nvidia.com", Version: "v1alpha1", Kind: "NIMCache"},
					ScaleDefinition: &v1alpha1.ScaleDefinition{
						ReplicasPath: ptr.To("1"),
					},
					SpecDefinition: &v1alpha1.SpecDefinition{
						FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
							ResourcesPath: ptr.To("{requests: .spec.resources}"),
						},
					},
					StatusDefinition: &v1alpha1.StatusDefinition{
						PhaseDefinition: &v1alpha1.PhaseDefinition{
							Path: ".status.state",
						},
						ConditionsDefinition: &v1alpha1.ConditionsDefinition{
							Path:             ".status.conditions",
							TypeFieldName:    "type",
							StatusFieldName:  "status",
							ReasonFieldName:  ptr.To("reason"),
							MessageFieldName: ptr.To("message"),
						},
						StatusMappings: v1alpha1.StatusMappings{
							// NotReady is a reconcile error; retained JobCreated conditions are not current startup evidence.
							Pending: []v1alpha1.StatusMatcher{
								{ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     inferenceEmptyStatusExpression,
									ExpectedResult: "true",
								}},
								{ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     `(.status.observedGeneration == null or .metadata.generation == null or .status.observedGeneration >= .metadata.generation) and (.status.state | IN("Pending", "PVC-Created", "Started")) and (any(.status.conditions[]?; .type == "NIM_CACHE_JOB_COMPLETED" and .status == "True") | not)`,
									ExpectedResult: "true",
								}}},
							Progressing: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     `(.status.observedGeneration == null or .metadata.generation == null or .status.observedGeneration >= .metadata.generation) and (.status.state | IN("Pending", "PVC-Created", "Started")) and (any(.status.conditions[]?; .type == "NIM_CACHE_JOB_COMPLETED" and .status == "True") | not) and ((.status.state | IN("PVC-Created", "Started")) or any(.status.conditions[]?; .type == "NIM_CACHE_JOB_CREATED" and .status == "True"))`,
								ExpectedResult: "true",
							}}},
							Running: []v1alpha1.StatusMatcher{
								{ByPhase: "InProgress"},
							},
							Completed: []v1alpha1.StatusMatcher{
								{ByPhase: "Ready", ByConditions: []v1alpha1.ExpectedCondition{{Type: "NIM_CACHE_JOB_COMPLETED", Status: ptr.To("True")}}},
							},
							Failed: []v1alpha1.StatusMatcher{
								{ByPhase: "Failed"},
							},
						},
					},
				},
			},
		},
	}
}
