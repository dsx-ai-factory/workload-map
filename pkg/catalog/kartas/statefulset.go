// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// StatefulSet returns the built-in Karta for the apps/v1 StatefulSet workload.
func StatefulSet() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "apps-statefulset-v1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "statefulset",
					Kind: &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"},
					ScaleDefinition: &v1alpha1.ScaleDefinition{
						ReplicasPath: ptr.To(".spec.replicas // 1"),
					},
					SpecDefinition: &v1alpha1.SpecDefinition{
						PodTemplateSpecPath: ptr.To(".spec.template"),
					},
					StatusDefinition: &v1alpha1.StatusDefinition{
						StatusMappings: v1alpha1.StatusMappings{
							Running: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     "(.status.observedGeneration // 0) == (.metadata.generation // 0) and (.status.readyReplicas // 0) == (.spec.replicas // 1) and (.status.updatedReplicas // 0) == (.spec.replicas // 1) and (.status.currentRevision == .status.updateRevision)",
								ExpectedResult: "true",
							}}},
							Degraded: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     "(.status.readyReplicas // 0) > 0 and (.status.readyReplicas // 0) < (.spec.replicas // 1) and (.status.updatedReplicas // 0) == (.spec.replicas // 1) and (.status.currentRevision == .status.updateRevision) and (.status.observedGeneration // 0) == (.metadata.generation // 0)",
								ExpectedResult: "true",
							}}},
							Initializing: []v1alpha1.StatusMatcher{
								// Scale-down: the controller has acknowledged the new spec (generation
								// caught up) while more pods are ready than desired - the extras are
								// still draining.
								{ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     ".status.observedGeneration == .metadata.generation and .status.readyReplicas > (.spec.replicas // 1)",
									ExpectedResult: "true",
								}},
								{ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     "(.spec.replicas // 1) > 0 and ((.status.observedGeneration // 0) != (.metadata.generation // 0) or (.status.readyReplicas // 0) == 0 or (.status.updatedReplicas // 0) != (.spec.replicas // 1) or (.status.currentRevision != .status.updateRevision))",
									ExpectedResult: "true",
								}},
							},
						},
					},
				},
			},
		},
	}
}
