// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// DynamoV1beta1 returns the built-in Karta for the DynamoGraphDeployment
// workload (nvidia.com/v1beta1).
func DynamoV1beta1() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "nvidia-com-dynamographdeployment-v1beta1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "dynamographdeployment",
					Kind: &v1alpha1.GroupVersionKind{Group: "nvidia.com", Version: "v1beta1", Kind: "DynamoGraphDeployment"},
					StatusDefinition: &v1alpha1.StatusDefinition{
						PhaseDefinition: &v1alpha1.PhaseDefinition{Path: ".status.state"},
						StatusMappings: v1alpha1.StatusMappings{
							Initializing: []v1alpha1.StatusMatcher{
								{ByPhase: "initializing"},
								{ByPhase: "pending"},
							},
							Running: []v1alpha1.StatusMatcher{{ByPhase: "successful"}},
							Failed:  []v1alpha1.StatusMatcher{{ByPhase: "failed"}},
						},
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:     "component",
						OwnerRef: ptr.To("dynamographdeployment"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								SchedulerNamePath:     ptr.To(".spec.components[] | .podTemplate.spec.schedulerName"),
								LabelsPath:            ptr.To(".spec.components[] | .podTemplate.metadata.labels"),
								AnnotationsPath:       ptr.To(".spec.components[] | .podTemplate.metadata.annotations"),
								ResourceClaimsPath:    ptr.To(".spec.components[] | .podTemplate.spec.resourceClaims"),
								PodAffinityPath:       ptr.To(".spec.components[] | .podTemplate.spec.affinity.podAffinity"),
								NodeAffinityPath:      ptr.To(".spec.components[] | .podTemplate.spec.affinity.nodeAffinity"),
								ContainerPath:         ptr.To(`.spec.components[] | .podTemplate.spec.containers[] | select(.name == "main")`),
								ImagePath:             ptr.To(`.spec.components[] | .podTemplate.spec.containers[] | select(.name == "main") | .image`),
								PriorityClassNamePath: ptr.To(".spec.components[] | .podTemplate.spec.priorityClassName"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.components[] | (.replicas // 1) * (.multinode.nodeCount // 1)"),
						},
						InstanceIdPath: ptr.To(".spec.components[] | .name"),
						PodSelector: &v1alpha1.PodSelector{
							ComponentInstanceSelector: &v1alpha1.ComponentInstanceSelector{
								IdPath: `.metadata.labels["nvidia.com/dynamo-component"]`,
							},
							ReplicaSelector: &v1alpha1.ReplicaSelector{
								KeyPath: `.metadata.labels["grove.io/podcliquescalinggroup-replica-index"] // .metadata.labels["leaderworkerset.sigs.k8s.io/group-index"]`,
							},
						},
					},
				},
				AdditionalChildKinds: []v1alpha1.GroupVersionKind{
					{Group: "nvidia.com", Version: "v1beta1", Kind: "DynamoComponentDeployment"},
					{Group: "leaderworkerset.x-k8s.io", Version: "v1", Kind: "LeaderWorkerSet"},
					{Group: "scheduler.grove.io", Version: "v1alpha1", Kind: "PodGang"},
					{Group: "grove.io", Version: "v1alpha1", Kind: "PodClique"},
					{Group: "grove.io", Version: "v1alpha1", Kind: "PodCliqueSet"},
					{Group: "grove.io", Version: "v1alpha1", Kind: "PodCliqueScalingGroup"},
				},
			},
			Instructions: v1alpha1.OptimizationInstructions{
				GangScheduling: &v1alpha1.GangSchedulingInstruction{
					PodGroups: []v1alpha1.PodGroupDefinition{{
						Name: "component",
						Members: []v1alpha1.PodGroupMemberDefinition{{
							ComponentName:   "component",
							GroupByKeyPaths: []string{`.metadata.labels["nvidia.com/dynamo-component"] | ascii_downcase`},
						}},
					}},
				},
			},
		},
	}
}
