// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// Dynamo returns the built-in Karta for the DynamoGraphDeployment workload
// (nvidia.com/v1alpha1).
func Dynamo() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "nvidia-com-dynamographdeployment-v1alpha1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "dynamographdeployment",
					Kind: &v1alpha1.GroupVersionKind{Group: "nvidia.com", Version: "v1alpha1", Kind: "DynamoGraphDeployment"},
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
						Name:     "service",
						OwnerRef: ptr.To("dynamographdeployment"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								SchedulerNamePath:     ptr.To(".spec.services | .[] | .extraPodSpec.schedulerName"),
								LabelsPath:            ptr.To(".spec.services | .[] | .labels"),
								AnnotationsPath:       ptr.To(".spec.services | .[] | .annotations"),
								ResourcesPath:         ptr.To(".spec.services | .[] | .resources"),
								ResourceClaimsPath:    ptr.To(".spec.services | .[] | .extraPodSpec.resourceClaims"),
								PodAffinityPath:       ptr.To(".spec.services | .[] | .extraPodSpec.affinity.podAffinity"),
								NodeAffinityPath:      ptr.To(".spec.services | .[] | .extraPodSpec.affinity.nodeAffinity"),
								ContainerPath:         ptr.To(".spec.services | .[] | .extraPodSpec.mainContainer"),
								PriorityClassNamePath: ptr.To(".spec.services | .[] | .extraPodSpec.priorityClassName"),
								ImagePath:             ptr.To(".spec.services | .[] | .extraPodSpec.mainContainer.image"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath:    ptr.To(".spec.services[] | (.replicas // 1) * (.multinode.nodeCount // 1)"),
							MinReplicasPath: ptr.To(".spec.services | .[] | .autoscaling.minReplicas"),
							MaxReplicasPath: ptr.To(".spec.services | .[] | .autoscaling.maxReplicas"),
						},
						InstanceIdPath: ptr.To(".spec.services | to_entries[] | .key"),
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
					{Group: "nvidia.com", Version: "v1alpha1", Kind: "DynamoComponentDeployment"},
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
						Name: "service",
						Members: []v1alpha1.PodGroupMemberDefinition{{
							ComponentName:   "service",
							GroupByKeyPaths: []string{`.metadata.labels["nvidia.com/dynamo-component"]`},
						}},
					}},
				},
			},
		},
	}
}
