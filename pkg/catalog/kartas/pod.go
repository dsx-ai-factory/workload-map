// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// Pod returns the built-in Karta for the core/v1 Pod workload.
func Pod() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "core-pod-v1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "pod",
					Kind: &v1alpha1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					ScaleDefinition: &v1alpha1.ScaleDefinition{
						ReplicasPath: ptr.To("1"),
					},
					SpecDefinition: &v1alpha1.SpecDefinition{
						PodTemplateSpecPath: ptr.To("."),
					},
					StatusDefinition: &v1alpha1.StatusDefinition{
						PhaseDefinition: &v1alpha1.PhaseDefinition{
							Path: ".status.phase",
						},
						ConditionsDefinition: &v1alpha1.ConditionsDefinition{
							Path:             ".status.conditions",
							TypeFieldName:    "type",
							StatusFieldName:  "status",
							MessageFieldName: ptr.To("message"),
							ReasonFieldName:  ptr.To("reason"),
						},
						StatusMappings: v1alpha1.StatusMappings{
							Initializing: []v1alpha1.StatusMatcher{{ByPhase: "Pending"}},
							Running:      []v1alpha1.StatusMatcher{{ByPhase: "Running"}},
							Completed:    []v1alpha1.StatusMatcher{{ByPhase: "Succeeded"}},
							Failed:       []v1alpha1.StatusMatcher{{ByPhase: "Failed"}},
						},
					},
				},
			},
		},
	}
}
