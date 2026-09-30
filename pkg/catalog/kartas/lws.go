// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// LWS returns the built-in Karta for the LeaderWorkerSet workload
// (leaderworkerset.x-k8s.io/v1).
func LWS() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "leaderworkerset-x-k8s-io-leaderworkerset-v1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "leaderworkerset",
					Kind: &v1alpha1.GroupVersionKind{Group: "leaderworkerset.x-k8s.io", Version: "v1", Kind: "LeaderWorkerSet"},
					StatusDefinition: &v1alpha1.StatusDefinition{
						ConditionsDefinition: &v1alpha1.ConditionsDefinition{
							Path:             ".status.conditions",
							TypeFieldName:    "type",
							StatusFieldName:  "status",
							MessageFieldName: ptr.To("message"),
						},
						StatusMappings: v1alpha1.StatusMappings{
							// The operator toggles Progressing: True while reconciling toward the desired
							// groups (startup and scale-up), False once settled. Available may not exist yet
							// at startup, so the matcher reads only the condition the operator guarantees.
							Initializing: []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Progressing", Status: ptr.To("True")},
							}}},
							// Available=True is the operator's authoritative all-groups-ready signal and it
							// holds through a scale-down while the replica counters lag. The definition does
							// not extract reason, so the old reason-constrained matcher could never fire.
							Running: []v1alpha1.StatusMatcher{
								{ByConditions: []v1alpha1.ExpectedCondition{
									{Type: "Available", Status: ptr.To("True")},
								}},
								{ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     "(.status.replicas // 0) > 0 and .status.readyReplicas == .status.replicas and .status.updatedReplicas == .status.replicas",
									ExpectedResult: "true",
								}},
							},
							Failed: []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Available", Status: ptr.To("False")},
								{Type: "Progressing", Status: ptr.To("False")},
								{Type: "UpdateInProgress", Status: ptr.To("False")},
							}}},
						},
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:     "group",
						OwnerRef: ptr.To("leaderworkerset"),
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.leaderWorkerTemplate.size"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ReplicaSelector: &v1alpha1.ReplicaSelector{
								KeyPath: `.metadata.labels["leaderworkerset.sigs.k8s.io/group-index"]`,
							},
						},
					},
					{
						Name:     "leader",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"},
						OwnerRef: ptr.To("group"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodTemplateSpecPath: ptr.To(".spec.leaderWorkerTemplate.leaderTemplate"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.replicas // 1"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["leaderworkerset.sigs.k8s.io/worker-index"]`,
								Value:   ptr.To("0"),
							},
						},
					},
					{
						Name:     "worker",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"},
						OwnerRef: ptr.To("group"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodTemplateSpecPath: ptr.To(".spec.leaderWorkerTemplate.workerTemplate"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To("(.spec.replicas // 1) * ((.spec.leaderWorkerTemplate.size // 1) - 1)"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.annotations["leaderworkerset.sigs.k8s.io/leader-name"]`,
							},
						},
					},
				},
			},
			Instructions: v1alpha1.OptimizationInstructions{
				GangScheduling: &v1alpha1.GangSchedulingInstruction{
					PodGroups: []v1alpha1.PodGroupDefinition{{
						Name: "group",
						Members: []v1alpha1.PodGroupMemberDefinition{{
							ComponentName: "group",
							GroupByKeyPaths: []string{
								`.metadata.labels["leaderworkerset.sigs.k8s.io/name"]`,
								`.metadata.labels["leaderworkerset.sigs.k8s.io/group-index"] // "0"`,
							},
						}},
					}},
				},
			},
		},
	}
}
