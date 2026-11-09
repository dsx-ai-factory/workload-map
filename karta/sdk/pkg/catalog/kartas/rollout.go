// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

// Rollout maps the Argo Rollouts progressive delivery controller
// (argoproj.io/v1alpha1).
//
// Controller check order (CalculateRolloutPhase): InvalidSpec condition,
// RolloutAborted reason on Progressing condition, ProgressDeadlineExceeded
// reason on Progressing condition, spec.paused, pauseConditions, then
// replica and strategy convergence. Final fallback is "Healthy".
//
// Status mapping:
//   Initializing ← "Progressing": scaling, stepping canary/blue-green, or
//     waiting for pods.
//   Running ← "Healthy" AND observedGeneration == generation: all replicas
//     updated and the stable RS matches the pod template hash. The generation
//     guard prevents reporting Running on stale status after a spec update.
//     observedGeneration is string-typed in this API (strconv of generation).
//   Degraded ← "Degraded": covers InvalidSpec, RolloutAborted, and
//     ProgressDeadlineExceeded. Not Failed because the controller clears the
//     state when the cause resolves.
//
// Exclusions (initial scope): "Paused" phase unmapped, suspension
// (spec.paused, pauseConditions), workloadRef, analysis resources.
//
// Recorded: deadline recovery (ProgressDeadlineExceeded → image fix → Healthy)
// via direct Kubernetes patches to spec.template and spec.replicas.
// Unrecorded: InvalidSpec and RolloutAborted degraded causes; live writes
// through the SDK (only direct patches demonstrated).

func Rollout() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "argoproj-io-rollout-v1alpha1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "rollout",
					Kind: &v1alpha1.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Rollout"},
					ScaleDefinition: &v1alpha1.ScaleDefinition{
						ReplicasPath: ptr.To(".spec.replicas"),
					},
					SpecDefinition: &v1alpha1.SpecDefinition{
						PodTemplateSpecPath: ptr.To(".spec.template"),
					},
					StatusDefinition: &v1alpha1.StatusDefinition{
						PhaseDefinition: &v1alpha1.PhaseDefinition{
							Path: ".status.phase",
						},
						ConditionsDefinition: &v1alpha1.ConditionsDefinition{
							Path:             ".status.conditions",
							TypeFieldName:    "type",
							StatusFieldName:  "status",
							MessageFieldName: ptr.To("message"),
							ReasonFieldName:  ptr.To("reason"),
						},
						StatusMappings: v1alpha1.StatusMappings{
							Initializing: []v1alpha1.StatusMatcher{{ByPhase: "Progressing"}},
							Running: []v1alpha1.StatusMatcher{{
								ByPhase: "Healthy",
								ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     `.status.observedGeneration == (.metadata.generation | tostring)`,
									ExpectedResult: "true",
								},
							}},
							Degraded: []v1alpha1.StatusMatcher{{ByPhase: "Degraded"}},
						},
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:     "replicaset",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"},
						OwnerRef: ptr.To("rollout"),
					},
				},
			},
		},
	}
}
