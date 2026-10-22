// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

// An active service can remain ready throughout a replacement or rollback.
// Pending cluster names alone do not describe traffic or readiness.
const rayServiceTransitionExpression = `.metadata.deletionTimestamp == null and .spec.suspend != true and
    (.metadata.generation == null or .status.observedGeneration == null or
     .status.observedGeneration >= .metadata.generation) and
    (any(.status.conditions[]?;
        (.status == "True" and (.type == "Suspending" or .type == "Suspended")) or
        (.type == "Ready" and .status == "False" and
         (.reason == "InitializingTimeout" or .reason == "ValidationFailed"))) | not)`

// RayService returns the built-in Karta for the RayService workload
// (ray.io/v1).
func RayService() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "ray-io-rayservice-v1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "rayservice",
					Kind: &v1alpha1.GroupVersionKind{Group: "ray.io", Version: "v1", Kind: "RayService"},
					StatusDefinition: &v1alpha1.StatusDefinition{
						ConditionsDefinition: &v1alpha1.ConditionsDefinition{
							Path:             ".status.conditions",
							TypeFieldName:    "type",
							StatusFieldName:  "status",
							ReasonFieldName:  ptr.To("reason"),
							MessageFieldName: ptr.To("message"),
						},
						StatusMappings: v1alpha1.StatusMappings{
							Running: []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{{Type: "Ready", Status: ptr.To("True")}}}},
							Pending: []v1alpha1.StatusMatcher{{
								ByConditions: []v1alpha1.ExpectedCondition{{Type: "Ready", Status: ptr.To("False"), Reason: ptr.To("Initializing")}},
								ByExpression: &v1alpha1.ExpressionMatcher{Expression: rayServiceTransitionExpression, ExpectedResult: "true"},
							}, {ByExpression: &v1alpha1.ExpressionMatcher{
								Expression: `.metadata.deletionTimestamp == null and (.metadata.generation // 0) > 0 and
                                    .spec.suspend != true and .spec.rayClusterConfig != null and
                                    (.spec.serveConfigV2 // "") != "" and (.status == null or .status == {})`,
								ExpectedResult: "true",
							}}},
							Progressing: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression: rayServiceTransitionExpression + ` and
                                    (any(.status.conditions[]?; .status == "True" and
                                        (.type == "UpgradeInProgress" or .type == "RollbackInProgress")) or
                                     (((.status.pendingServiceStatus.rayClusterName // "") != "" or
                                       (.status.activeServiceStatus.rayClusterName // "") != "") and
                                      any(.status.conditions[]?; .type == "Ready" and .status == "False" and .reason == "Initializing")))`,
								ExpectedResult: "true",
							}}},
							Degraded: []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{{Type: "Ready", Status: ptr.To("False"), Reason: ptr.To("ZeroServeEndpoints")}}}},
							Failed: []v1alpha1.StatusMatcher{
								{ByConditions: []v1alpha1.ExpectedCondition{{Type: "Ready", Status: ptr.To("False"), Reason: ptr.To("InitializingTimeout")}}},
								{ByConditions: []v1alpha1.ExpectedCondition{{Type: "Ready", Status: ptr.To("False"), Reason: ptr.To("ValidationFailed")}}},
							},
						},
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:     "head",
						Kind:     &v1alpha1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
						OwnerRef: ptr.To("rayservice"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodTemplateSpecPath: ptr.To(".spec.rayClusterConfig.headGroupSpec.template"),
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
						OwnerRef: ptr.To("rayservice"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodTemplateSpecPath: ptr.To(".spec.rayClusterConfig.workerGroupSpecs[].template"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.rayClusterConfig.workerGroupSpecs[].replicas // 1"),
						},
						InstanceIdPath: ptr.To(".spec.rayClusterConfig.workerGroupSpecs[].groupName"),
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
						Name: "service",
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
