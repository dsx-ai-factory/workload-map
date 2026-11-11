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
//
//	Running ← "Healthy" AND observedGeneration == generation AND spec.paused
//	  false: all replicas updated and the stable RS matches the pod template
//	  hash. The generation guard prevents reporting Running on stale status
//	  after a spec update. observedGeneration is string-typed in this API
//	  (strconv of generation).
//	Degraded ← "Degraded" AND observedGeneration == generation: covers
//	  InvalidSpec, RolloutAborted, and ProgressDeadlineExceeded. Not Failed
//	  because the controller clears the state when the cause resolves. The
//	  controller checks Degraded before spec.paused, so a degraded Rollout
//	  stays Degraded even if paused. The generation guard clears stale
//	  Degraded immediately after a fix.
//	Suspended ← spec.paused true AND phase is not Degraded: the user
//	  suspended the Rollout via spec.paused (same field kubectl argo rollouts
//	  pause sets). Pausing does not delete pods, so no capacity is freed.
//	Initializing ← fallback: not Running, not Degraded, not Suspended.
//	  Covers Progressing, canary/blue-green Paused waits (spec.paused false),
//	  the brief window before the first status write, and stale-generation
//	  Healthy or Degraded right after a spec change.
//
// Exclusions: workloadRef, analysis resources, pauseConditions (only
// spec.paused is exposed as suspend; canary/blue-green pause steps are
// mapped as Initializing).
//
// Recorded: deadline recovery (ProgressDeadlineExceeded → image fix →
// Healthy), canary timed pause step, suspend/resume via spec.paused.
// Unrecorded: InvalidSpec and RolloutAborted degraded causes, abort
// (status.abort is a status patch the recorder cannot apply), promote
// (also a status patch); live writes through the SDK (only direct patches
// demonstrated).
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
					SuspendDefinition: &v1alpha1.SuspendDefinition{
						SuspendActions: []v1alpha1.SuspendAction{{Path: ".spec.paused", Value: "true"}},
						ResumeActions:  []v1alpha1.SuspendAction{{Path: ".spec.paused", Value: "false"}},
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
							Running: []v1alpha1.StatusMatcher{{
								ByPhase: "Healthy",
								ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     `.status.observedGeneration == (.metadata.generation | tostring) and ((.spec.paused // false) | not)`,
									ExpectedResult: "true",
								},
							}},
							Degraded: []v1alpha1.StatusMatcher{{
								ByPhase: "Degraded",
								ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     `.status.observedGeneration == (.metadata.generation | tostring)`,
									ExpectedResult: "true",
								},
							}},
							Suspended: []v1alpha1.StatusMatcher{{
								ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     `(.spec.paused // false) and (.status.phase // "") != "Degraded"`,
									ExpectedResult: "true",
								},
							}},
							Initializing: []v1alpha1.StatusMatcher{{
								ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     `((.status.phase // "") != "Healthy" or .status.observedGeneration != (.metadata.generation | tostring) or (.spec.paused // false)) and ((.status.phase // "") != "Degraded" or .status.observedGeneration != (.metadata.generation | tostring)) and (((.spec.paused // false) | not) or (.status.phase // "") == "Degraded")`,
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
						OwnerRef: ptr.To("rollout"),
					},
				},
			},
		},
	}
}
