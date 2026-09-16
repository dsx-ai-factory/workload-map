// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

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
							Initializing: []v1alpha1.StatusMatcher{
								// A just-created Deployment: the controller writes this reason exactly once,
								// before any Available condition exists. Scaling and steady state carry
								// NewReplicaSetAvailable, so this matcher cannot fire outside birth.
								{ByConditions: []v1alpha1.ExpectedCondition{
									{Type: "Progressing", Status: ptr.To("True"), Reason: ptr.To("NewReplicaSetCreated")},
								}},
								{ByConditions: []v1alpha1.ExpectedCondition{
									{Type: "Progressing", Status: ptr.To("True")},
									{Type: "Available", Status: ptr.To("False")},
								}},
							},
							Running: []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Progressing", Status: ptr.To("True"), Reason: ptr.To("NewReplicaSetAvailable")},
							}}},
							Failed: []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Progressing", Status: ptr.To("False"), Reason: ptr.To("ProgressDeadlineExceeded")},
							}}},
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
