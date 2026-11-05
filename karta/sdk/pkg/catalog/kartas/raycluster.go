// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

// The legacy ready state and provisioned condition remain true after startup.
// The suspended state also remains until resumed Pods are ready. Current startup
// conditions can supersede it once both suspension conditions have cleared.
const rayClusterStartingExpression = `.spec.suspend != true and
    .status.state != "failed" and
    (.status.state != "suspended" or
     (.spec.suspend == false and .metadata.generation != null and
      .status.observedGeneration == .metadata.generation and
      any(.status.conditions[]?; .type == "RayClusterSuspending" and .status == "False") and
      any(.status.conditions[]?; .type == "RayClusterSuspended" and .status == "False"))) and
    (any(.status.conditions[]?; .status == "True" and
        (.type == "RayClusterSuspending" or .type == "RayClusterSuspended")) | not) and
    any(.status.conditions[]?; .type == "RayClusterProvisioned" and .status == "False") and
    any(.status.conditions[]?; .type == "HeadPodReady" and .status == "False")`

// Suspension intent alone does not establish that the controller has removed the Pods.
const rayClusterSuspendedExpression = `.metadata.deletionTimestamp == null and .spec.suspend == true and
    .status.state == "suspended" and .metadata.generation != null and
    .status.observedGeneration == .metadata.generation`

const rayClusterPendingExpression = `.metadata.deletionTimestamp == null and (` + rayClusterStartingExpression + ` or
    ((.metadata.generation // 0) > 0 and
     ((.status == null or .status == {}) or
      (.spec.suspend == false and .status.state == "suspended"))))`

// Raycluster returns the built-in Karta for the RayCluster workload
// (ray.io/v1).
func Raycluster() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "ray-io-raycluster-v1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "raycluster",
					Kind: &v1alpha1.GroupVersionKind{Group: "ray.io", Version: "v1", Kind: "RayCluster"},
					SuspendDefinition: &v1alpha1.SuspendDefinition{
						SuspendActions: []v1alpha1.SuspendAction{{Path: ".spec.suspend", Value: "true"}},
						ResumeActions:  []v1alpha1.SuspendAction{{Path: ".spec.suspend", Value: "false"}},
					},
					StatusDefinition: &v1alpha1.StatusDefinition{
						PhaseDefinition: &v1alpha1.PhaseDefinition{
							Path: ".status.state",
						},
						StatusMappings: v1alpha1.StatusMappings{
							Pending: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     rayClusterPendingExpression,
								ExpectedResult: "true",
							}}},
							Progressing: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression: `.metadata.deletionTimestamp == null and ` + rayClusterStartingExpression + ` and
                                    any(.status.conditions[]?; .type == "HeadPodReady" and .status == "False" and
                                        (.reason == "ContainerCreating" or .reason == "ContainersNotReady" or
                                         .reason == "ErrImagePull" or .reason == "ImagePullBackOff" or .reason == "CrashLoopBackOff"))`,
								ExpectedResult: "true",
							}}},
							Running: []v1alpha1.StatusMatcher{{ByPhase: "ready"}},
							Failed:  []v1alpha1.StatusMatcher{{ByPhase: "failed"}},
							Suspended: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     rayClusterSuspendedExpression,
								ExpectedResult: "true",
							}}},
						},
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:     "head",
						Kind:     &v1alpha1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
						OwnerRef: ptr.To("raycluster"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodTemplateSpecPath: ptr.To(".spec.headGroupSpec.template"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To("1"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["ray.io/node-type"]`,
								Value:   ptr.To("head"),
							},
						},
					},
					{
						Name:     "worker",
						Kind:     &v1alpha1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
						OwnerRef: ptr.To("raycluster"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodTemplateSpecPath: ptr.To(".spec.workerGroupSpecs[].template"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath:    ptr.To(".spec.workerGroupSpecs[].replicas"),
							MinReplicasPath: ptr.To(".spec.workerGroupSpecs[].minReplicas"),
							MaxReplicasPath: ptr.To(".spec.workerGroupSpecs[].maxReplicas"),
						},
						InstanceIdPath: ptr.To(".spec.workerGroupSpecs[].groupName"),
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["ray.io/node-type"]`,
								Value:   ptr.To("worker"),
							},
							ComponentInstanceSelector: &v1alpha1.ComponentInstanceSelector{
								IdPath: `.metadata.labels["ray.io/group"]`,
							},
						},
					},
				},
			},
			Instructions: v1alpha1.OptimizationInstructions{
				GangScheduling: &v1alpha1.GangSchedulingInstruction{
					PodGroups: []v1alpha1.PodGroupDefinition{{
						Name: "cluster",
						Members: []v1alpha1.PodGroupMemberDefinition{
							{
								ComponentName:   "head",
								GroupByKeyPaths: []string{`.metadata.labels["ray.io/cluster"]`},
							},
							{
								ComponentName:   "worker",
								GroupByKeyPaths: []string{`.metadata.labels["ray.io/cluster"]`},
							},
						},
					}},
				},
			},
		},
	}
}
