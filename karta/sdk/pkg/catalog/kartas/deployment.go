// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

const deploymentCurrentStatus = `.metadata.deletionTimestamp == null
    and .status.observedGeneration != null
    and .status.observedGeneration == .metadata.generation`

// Deployment returns the built-in Karta for the apps/v1 Deployment workload.
func Deployment() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "apps-deployment-v1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "deployment",
					Kind: &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
					ScaleDefinition: &v1alpha1.ScaleDefinition{
						ReplicasPath: ptr.To(".spec.replicas // 1"),
					},
					SpecDefinition: &v1alpha1.SpecDefinition{
						PodTemplateSpecPath: ptr.To(".spec.template"),
					},
					StatusDefinition: &v1alpha1.StatusDefinition{
						ConditionsDefinition: &v1alpha1.ConditionsDefinition{
							Path:             ".status.conditions",
							TypeFieldName:    "type",
							StatusFieldName:  "status",
							MessageFieldName: ptr.To("message"),
							ReasonFieldName:  ptr.To("reason"),
						},
						StatusMappings: v1alpha1.StatusMappings{
							Pending: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression: `(` + deploymentCurrentStatus + ` and (.spec.replicas // 1) > 0
    and (.status.availableReplicas // 0) < (.spec.replicas // 1)) or (
    .metadata.deletionTimestamp == null and (.spec.replicas // 1) > 0
    and (.metadata.generation // 0) > 0
    and (.status.observedGeneration == null or .status.observedGeneration < .metadata.generation)
    and (any(.status.conditions[]?;
        (.type == "ReplicaFailure" and .status == "True") or
        (.type == "Progressing" and .status == "False" and .reason == "ProgressDeadlineExceeded")) | not))`,
								ExpectedResult: "true",
							}}},
							Progressing: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								// Pausing prevents template rollouts, but scaling still reconciles.
								// NewReplicaSetAvailable remains true after success. A readiness loss alone
								// does not establish a new rollout while that historical reason remains.
								Expression: deploymentCurrentStatus + ` and (
    (.status.replicas // 0) != (.spec.replicas // 1)
    or (.spec.paused != true and (
        (.status.updatedReplicas // 0) < (.spec.replicas // 1)
        or ((.status.availableReplicas // 0) < (.spec.replicas // 1)
            and any(.status.conditions[]?; .type == "Progressing"
                and ((.status == "True" and .reason != "NewReplicaSetAvailable")
                    or (.status == "False" and .reason == "ProgressDeadlineExceeded")))))))`,
								ExpectedResult: "true",
							}}},
							Running: []v1alpha1.StatusMatcher{{
								ByConditions: []v1alpha1.ExpectedCondition{{Type: "Available", Status: ptr.To("True")}},
								ByExpression: &v1alpha1.ExpressionMatcher{
									Expression: deploymentCurrentStatus + ` and (.spec.replicas // 1) > 0
    and (.status.availableReplicas // 0) > 0`,
									ExpectedResult: "true",
								},
							}},
							Failed: []v1alpha1.StatusMatcher{{
								ByConditions: []v1alpha1.ExpectedCondition{{Type: "Progressing", Status: ptr.To("False"), Reason: ptr.To("ProgressDeadlineExceeded")}},
								ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     deploymentCurrentStatus + " and .spec.paused != true",
									ExpectedResult: "true",
								},
							}},
						},
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:     "replicaset",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"},
						OwnerRef: ptr.To("deployment"),
					},
				},
			},
		},
	}
}
