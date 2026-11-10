// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

// VcJob returns the built-in Karta for the Volcano Job workload
// (batch.volcano.sh/v1alpha1).
//
// Volcano runs batch jobs with named task groups. Each task carries its own
// pod template, replica count, and optional minAvailable gang minimum. The
// controller creates pods directly (no intermediate Job object) and labels
// each pod with volcano.sh/task-spec set to the task name.
//
// Status is a phase string at .status.state.phase. The controller does not
// write observedGeneration or standard Kubernetes conditions. Mapped phases:
//   - Pending to Initializing.
//   - Restarting to Initializing when retries remain
//     (retryCount < maxRetry), to Failed when exhausted.
//   - Running to Running (minAvailable tasks are scheduled).
//   - Completing to Completed (in-flight, always ends at Completed).
//   - Completed to Completed.
//   - Terminating to Failed (in-flight, ending at Terminated).
//   - Terminated and Failed to Failed.
//   - Aborting to Suspending (in-flight abort that Volcano can resume).
//   - Aborted to Suspended (resumable terminal state).
//
// Degraded is not mapped. The controller writes no fault condition or error
// reason to status; a consumer wanting pod faults reads them from pods.
//
// Unproven by recordings: Restarting (both retry-count branches),
// Completing, Aborting, and Aborted. These mappings follow the Volcano
// controller source but no e2e flow exercises them. Writes are unproven.
// Advanced lifecycle policies beyond retry-count gating are excluded.
func VcJob() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "batch-volcano-sh-job-v1alpha1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "vcjob",
					Kind: &v1alpha1.GroupVersionKind{Group: "batch.volcano.sh", Version: "v1alpha1", Kind: "Job"},
					StatusDefinition: &v1alpha1.StatusDefinition{
						PhaseDefinition: &v1alpha1.PhaseDefinition{
							Path: ".status.state.phase",
						},
						StatusMappings: v1alpha1.StatusMappings{
							Initializing: []v1alpha1.StatusMatcher{
								{ByPhase: "Pending"},
								{ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     `.status.state.phase == "Restarting" and (.spec.maxRetry == null or (.status.retryCount // 0) < .spec.maxRetry)`,
									ExpectedResult: "true",
								}},
							},
							Running: []v1alpha1.StatusMatcher{
								{ByPhase: "Running"},
							},
							Completed: []v1alpha1.StatusMatcher{
								// Completing is in-flight: the controller always drives it to Completed.
								{ByPhase: "Completing"},
								{ByPhase: "Completed"},
							},
							Failed: []v1alpha1.StatusMatcher{
								{ByPhase: "Terminating"},
								{ByPhase: "Terminated"},
								{ByPhase: "Failed"},
								{ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     `.status.state.phase == "Restarting" and .spec.maxRetry != null and (.status.retryCount // 0) >= .spec.maxRetry`,
									ExpectedResult: "true",
								}},
							},
							Suspending: []v1alpha1.StatusMatcher{
								{ByPhase: "Aborting"},
							},
							Suspended: []v1alpha1.StatusMatcher{
								{ByPhase: "Aborted"},
							},
						},
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:     "task",
						Kind:     &v1alpha1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
						OwnerRef: ptr.To("vcjob"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodTemplateSpecPath: ptr.To(".spec.tasks[].template"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath:    ptr.To(".spec.tasks[].replicas"),
							MinReplicasPath: ptr.To(".spec.tasks[].minAvailable"),
						},
						InstanceIdPath: ptr.To(".spec.tasks[].name"),
						PodSelector: &v1alpha1.PodSelector{
							ComponentInstanceSelector: &v1alpha1.ComponentInstanceSelector{
								IdPath: `.metadata.labels["volcano.sh/task-spec"]`,
							},
						},
					},
				},
			},
			Instructions: v1alpha1.OptimizationInstructions{
				GangScheduling: &v1alpha1.GangSchedulingInstruction{
					PodGroups: []v1alpha1.PodGroupDefinition{{
						Name: "job",
						Members: []v1alpha1.PodGroupMemberDefinition{{
							ComponentName:   "task",
							GroupByKeyPaths: []string{`.metadata.labels["volcano.sh/job-name"]`},
						}},
					}},
				},
			},
		},
	}
}
