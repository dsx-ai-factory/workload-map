// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// Jobset returns the built-in Karta for the JobSet workload
// (jobset.x-k8s.io/v1alpha2).
func Jobset() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "jobset-x-k8s-io-jobset-v1alpha2"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "jobset",
					Kind: &v1alpha1.GroupVersionKind{Group: "jobset.x-k8s.io", Version: "v1alpha2", Kind: "JobSet"},
					SuspendDefinition: &v1alpha1.SuspendDefinition{
						SuspendActions: []v1alpha1.SuspendAction{{Path: ".spec.suspend", Value: "true"}},
						ResumeActions:  []v1alpha1.SuspendAction{{Path: ".spec.suspend", Value: "false"}},
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
							Initializing: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								// Some replicatedJobs are active but none are ready yet.
								// Guard against a null replicatedJobsStatus before the JobSet
								// controller initializes status.
								Expression:     "(.status.replicatedJobsStatus // []) | any(.active > 0 and (.ready // 0) == 0) and all(.failed == 0)",
								ExpectedResult: "true",
							}}},
							Running: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								// Working: at least one replicatedJob has active or ready pods and none
								// have failed. Reading either count (not both) keeps the state stable
								// while the controller briefly flaps ready to 0 mid-run.
								Expression:     "(.status.replicatedJobsStatus // []) | any(.active > 0 or .ready > 0) and all((.failed // 0) == 0)",
								ExpectedResult: "true",
							}}},
							Completed: []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{{Type: "Completed", Status: ptr.To("True")}}}},
							Failed:    []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{{Type: "Failed", Status: ptr.To("True")}}}},
							Suspended: []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{{Type: "Suspended", Status: ptr.To("True")}}}},
						},
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					// ReplicatedJob - represents the actual Job resources created by JobSet.
					{
						Name:     "replicatedjob",
						Kind:     &v1alpha1.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"},
						OwnerRef: ptr.To("jobset"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodTemplateSpecPath: ptr.To(".spec.replicatedJobs[].template.spec.template"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							// Total pods per replicatedJob = replicas (Job instances) * parallelism (pods per Job).
							ReplicasPath: ptr.To(".spec.replicatedJobs[] | .replicas * .template.spec.parallelism"),
						},
						InstanceIdPath: ptr.To(".spec.replicatedJobs[].name"),
						PodSelector: &v1alpha1.PodSelector{
							ComponentInstanceSelector: &v1alpha1.ComponentInstanceSelector{
								IdPath: `.metadata.labels["jobset.sigs.k8s.io/replicatedjob-name"]`,
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
							ComponentName:   "replicatedjob",
							GroupByKeyPaths: []string{`.metadata.labels["jobset.sigs.k8s.io/replicatedjob-name"]`},
						}},
					}},
				},
			},
		},
	}
}
