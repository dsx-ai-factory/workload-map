// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

// CloneSet returns the built-in Karta for the OpenKruise apps.kruise.io/v1alpha1
// CloneSet workload.
//
// CloneSet manages pods directly (no intermediate ReplicaSet) and reports
// counter-based status: replicas, readyReplicas, updatedReplicas,
// currentRevision, and updateRevision. The controller reconciles in
// clonesetcontroller.syncCloneSet; it updates counters after every pod
// change and bumps observedGeneration on each reconcile.
//
// Status mapping (every rule guarded by observedGeneration > 0):
//
//	Running ← generation current, all counters match spec.replicas,
//	  currentRevision == updateRevision. A zero-replica CloneSet reads
//	  Running: all counters are 0 == desired 0.
//	Initializing ← (1) draining: generation current, status.replicas >
//	  spec.replicas. (2) not-converged: any counter or revision mismatch,
//	  covering scale-up, rollouts, and partial readiness.
//	Degraded ← not mapped. FailedScale and FailedUpdate conditions
//	  (clonesetutils) are set once and never cleared by the controller;
//	  a consumer wanting the fault reads the condition directly.
//	  ProgressDeadlineExceeded fires during healthy progress; unmapped.
//
// spec.replicas defaults to 1 (API defaulting webhook). Writes through
// the exposed paths are unproven (no write round-trip check exists yet).
//
// Unrecorded branches: InPlaceIfPossible and InPlaceOnly update strategies,
// partition-based canary (spec.updateStrategy.partition), lifecycle hooks,
// and PVC templates. OnDelete update strategy is rejected by the webhook.
func CloneSet() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "apps-kruise-io-cloneset-v1alpha1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "cloneset",
					Kind: &v1alpha1.GroupVersionKind{Group: "apps.kruise.io", Version: "v1alpha1", Kind: "CloneSet"},
					ScaleDefinition: &v1alpha1.ScaleDefinition{
						ReplicasPath: ptr.To(".spec.replicas"),
					},
					SpecDefinition: &v1alpha1.SpecDefinition{
						PodTemplateSpecPath: ptr.To(".spec.template"),
					},
					StatusDefinition: &v1alpha1.StatusDefinition{
						StatusMappings: v1alpha1.StatusMappings{
							Running: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     `(.status.observedGeneration // 0) > 0 and (.status.observedGeneration // 0) == (.metadata.generation // 0) and (.status.replicas // 0) == (.spec.replicas // 1) and (.status.readyReplicas // 0) == (.spec.replicas // 1) and (.status.updatedReplicas // 0) == (.spec.replicas // 1) and (.status.currentRevision == .status.updateRevision)`,
								ExpectedResult: "true",
							}}},
							Initializing: []v1alpha1.StatusMatcher{
								{ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     `(.status.observedGeneration // 0) > 0 and .status.observedGeneration == .metadata.generation and (.status.replicas // 0) > (.spec.replicas // 1)`,
									ExpectedResult: "true",
								}},
								{ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     `(.status.observedGeneration // 0) > 0 and (.spec.replicas // 1) > 0 and ((.status.observedGeneration // 0) != (.metadata.generation // 0) or (.status.readyReplicas // 0) != (.spec.replicas // 1) or (.status.updatedReplicas // 0) != (.spec.replicas // 1) or (.status.currentRevision != .status.updateRevision))`,
									ExpectedResult: "true",
								}},
							},
						},
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:     "pod",
						Kind:     &v1alpha1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
						OwnerRef: ptr.To("cloneset"),
					},
				},
			},
		},
	}
}
