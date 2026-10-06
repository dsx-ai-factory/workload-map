// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// KServe returns the built-in Karta for the KServe InferenceService workload
// (serving.kserve.io/v1beta1).
func KServe() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "serving-kserve-io-inferenceservice-v1beta1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "inferenceservice",
					Kind: &v1alpha1.GroupVersionKind{Group: "serving.kserve.io", Version: "v1beta1", Kind: "InferenceService"},
					StatusDefinition: &v1alpha1.StatusDefinition{
						ConditionsDefinition: &v1alpha1.ConditionsDefinition{
							Path:             ".status.conditions",
							TypeFieldName:    "type",
							StatusFieldName:  "status",
							MessageFieldName: ptr.To("message"),
						},
						StatusMappings: v1alpha1.StatusMappings{
							Running: []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "PredictorReady", Status: ptr.To("True")},
								{Type: "RoutesReady", Status: ptr.To("True")},
								{Type: "LatestDeploymentReady", Status: ptr.To("True")},
							}}},
							Failed: []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "PredictorReady", Status: ptr.To("False")},
								{Type: "PredictorConfigurationReady", Status: ptr.To("False")},
								{Type: "RoutesReady", Status: ptr.To("False")},
							}}},
						},
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:     "predictor",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						OwnerRef: ptr.To("inferenceservice"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								SchedulerNamePath:     ptr.To(".spec.predictor.schedulerName"),
								LabelsPath:            ptr.To(".spec.predictor.labels"),
								AnnotationsPath:       ptr.To(".spec.predictor.annotations"),
								PodAffinityPath:       ptr.To(".spec.predictor.affinity.podAffinity"),
								NodeAffinityPath:      ptr.To(".spec.predictor.affinity.nodeAffinity"),
								ContainersPath:        ptr.To(".spec.predictor.containers"),
								ContainerPath:         ptr.To(`.spec.predictor | ( (.[]?  | select(type =="object" and .storageUri )))`),
								PriorityClassNamePath: ptr.To(".spec.predictor.priorityClassName"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							MinReplicasPath: ptr.To(".spec.predictor.minReplicas"),
							MaxReplicasPath: ptr.To(".spec.predictor.maxReplicas"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["component"]`,
								Value:   ptr.To("predictor"),
							},
						},
					},
					{
						Name:     "transformer",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						OwnerRef: ptr.To("inferenceservice"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodSpecPath:  ptr.To(".spec.transformer"),
							MetadataPath: ptr.To(".spec.transformer"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							MinReplicasPath: ptr.To(".spec.transformer.minReplicas"),
							MaxReplicasPath: ptr.To(".spec.transformer.maxReplicas"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["component"]`,
								Value:   ptr.To("transformer"),
							},
						},
					},
				},
			},
			Instructions: v1alpha1.OptimizationInstructions{
				GangScheduling: &v1alpha1.GangSchedulingInstruction{
					PodGroups: []v1alpha1.PodGroupDefinition{{
						Name: "service",
						Members: []v1alpha1.PodGroupMemberDefinition{
							{
								ComponentName:   "predictor",
								GroupByKeyPaths: []string{`.metadata.labels["serving.kserve.io/inferenceservice"]`},
							},
							{
								ComponentName:   "transformer",
								GroupByKeyPaths: []string{`.metadata.labels["serving.kserve.io/inferenceservice"]`},
							},
						},
					}},
				},
			},
		},
	}
}
