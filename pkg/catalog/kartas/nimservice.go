// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// NIMService returns the built-in Karta for the NIMService workload
// (apps.nvidia.com/v1alpha1).
func NIMService() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "apps-nvidia-com-nimservice-v1alpha1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "nimservice",
					Kind: &v1alpha1.GroupVersionKind{Group: "apps.nvidia.com", Version: "v1alpha1", Kind: "NIMService"},
					SpecDefinition: &v1alpha1.SpecDefinition{
						FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
							SchedulerNamePath: ptr.To(".spec.schedulerName"),
							LabelsPath:        ptr.To(".spec.labels"),
							AnnotationsPath:   ptr.To(".spec.annotations"),
							ResourcesPath:     ptr.To(".spec.resources"),
							PodAffinityPath:   ptr.To(".spec.affinity.podAffinity"),
							NodeAffinityPath:  ptr.To(".spec.affinity.nodeAffinity"),
						},
					},
					ScaleDefinition: &v1alpha1.ScaleDefinition{
						ReplicasPath: ptr.To(".spec.replicas // 1"),
					},
					StatusDefinition: &v1alpha1.StatusDefinition{
						PhaseDefinition: &v1alpha1.PhaseDefinition{
							Path: ".status.state",
						},
						ConditionsDefinition: &v1alpha1.ConditionsDefinition{
							Path:             ".status.conditions",
							TypeFieldName:    "type",
							StatusFieldName:  "status",
							MessageFieldName: ptr.To("message"),
						},
						StatusMappings: v1alpha1.StatusMappings{
							Initializing: []v1alpha1.StatusMatcher{
								{ByPhase: "NotReady"},
								{ByPhase: "Pending"},
							},
							Running: []v1alpha1.StatusMatcher{
								{ByPhase: "Ready"},
							},
							Failed: []v1alpha1.StatusMatcher{
								{ByPhase: "Failed"},
							},
						},
					},
				},
			},
			Instructions: v1alpha1.OptimizationInstructions{
				GangScheduling: &v1alpha1.GangSchedulingInstruction{
					PodGroups: []v1alpha1.PodGroupDefinition{{
						Name: "service",
						Members: []v1alpha1.PodGroupMemberDefinition{{
							ComponentName:   "nimservice",
							GroupByKeyPaths: []string{`.metadata.labels["app.kubernetes.io/name"]`},
						}},
					}},
				},
			},
		},
	}
}
