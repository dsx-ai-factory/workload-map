// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// KnativeServing returns the built-in Karta for the Knative Serving Service
// workload (serving.knative.dev/v1).
func KnativeServing() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "serving-knative-dev-service-v1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "knativeservice",
					Kind: &v1alpha1.GroupVersionKind{Group: "serving.knative.dev", Version: "v1", Kind: "Service"},
					StatusDefinition: &v1alpha1.StatusDefinition{
						ConditionsDefinition: &v1alpha1.ConditionsDefinition{
							Path:             ".status.conditions",
							TypeFieldName:    "type",
							StatusFieldName:  "status",
							MessageFieldName: ptr.To("message"),
						},
						StatusMappings: v1alpha1.StatusMappings{
							Running: []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{{Type: "Ready", Status: ptr.To("True")}}}},
							// Knative reports the whole deploy through Ready=Unknown (reasons OutOfDate,
							// RevisionMissing, IngressNotConfigured, Uninitialized) and a broken Service
							// through Ready=False - both deliberate signals, matched as written.
							Initializing: []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{{Type: "Ready", Status: ptr.To("Unknown")}}}},
							Failed:       []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{{Type: "Ready", Status: ptr.To("False")}}}},
						},
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:     "revision",
						Kind:     &v1alpha1.GroupVersionKind{Group: "serving.knative.dev", Version: "v1", Kind: "Revision"},
						OwnerRef: ptr.To("knativeservice"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodTemplateSpecPath: ptr.To(".spec.template"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							MinReplicasPath: ptr.To(`.spec.template.metadata.annotations["autoscaling.knative.dev/min-scale"] // 1 | tonumber`),
							MaxReplicasPath: ptr.To(`.spec.template.metadata.annotations["autoscaling.knative.dev/max-scale"] | tonumber?`),
						},
					},
				},
				AdditionalChildKinds: []v1alpha1.GroupVersionKind{
					{Group: "apps", Version: "v1", Kind: "Deployment"},
				},
			},
			Instructions: v1alpha1.OptimizationInstructions{
				GangScheduling: &v1alpha1.GangSchedulingInstruction{
					PodGroups: []v1alpha1.PodGroupDefinition{{
						Name: "revision",
						Members: []v1alpha1.PodGroupMemberDefinition{{
							ComponentName:   "revision",
							GroupByKeyPaths: []string{`.metadata.labels["serving.knative.dev/revision"]`},
						}},
					}},
				},
			},
		},
	}
}
