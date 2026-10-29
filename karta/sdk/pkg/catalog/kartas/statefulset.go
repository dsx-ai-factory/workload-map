// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

const statefulSetCurrentStatus = `.metadata.deletionTimestamp == null
    and .status.observedGeneration != null
    and .status.observedGeneration == .metadata.generation`

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
							Pending: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression: `(` + statefulSetCurrentStatus + ` and (.spec.replicas // 1) > 0
    and (.status.readyReplicas // 0) < (.spec.replicas // 1)) or (
    .metadata.deletionTimestamp == null and (.spec.replicas // 1) > 0
    and (.metadata.generation // 0) > 0
    and (.status.observedGeneration == null or .status.observedGeneration < .metadata.generation)
    and ((.status.conditions // []) | length == 0))`,
								ExpectedResult: "true",
							}}},
							Progressing: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								// OnDelete and partitioned updates can intentionally retain an old revision.
								Expression: statefulSetCurrentStatus + ` and (
    (.status.replicas // 0) != (.spec.replicas // 1)
    or ((.spec.updateStrategy.type // "RollingUpdate") == "RollingUpdate"
        and .status.currentRevision != null and .status.updateRevision != null
        and .status.currentRevision != .status.updateRevision
        and (.spec.replicas // 1) > (.spec.updateStrategy.rollingUpdate.partition // 0)
        and ((.status.updatedReplicas // 0) < ((.spec.replicas // 1) - (.spec.updateStrategy.rollingUpdate.partition // 0))
            or (.status.readyReplicas // 0) < (.spec.replicas // 1)))
    or ((.spec.updateStrategy.type // "RollingUpdate") == "Recreate"
        and any(.status.conditions[]?; .type == "Progressing" and .status == "True")
        and ((.status.updatedReplicas // 0) < (.spec.replicas // 1)
            or (.status.readyReplicas // 0) < (.spec.replicas // 1))))`,
								ExpectedResult: "true",
							}}},
							Running: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression: statefulSetCurrentStatus + ` and (.spec.replicas // 1) > 0
    and (.status.replicas // 0) >= (.spec.replicas // 1)
    and (.status.readyReplicas // 0) >= (.spec.replicas // 1)`,
								ExpectedResult: "true",
							}}},
							Degraded: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression: statefulSetCurrentStatus + ` and (.status.readyReplicas // 0) > 0
    and (.status.readyReplicas // 0) < (.spec.replicas // 1)
    and (.status.updatedReplicas // 0) == (.spec.replicas // 1)
    and .status.currentRevision == .status.updateRevision`,
								ExpectedResult: "true",
							}}},
						},
					},
				},
			},
		},
	}
}
