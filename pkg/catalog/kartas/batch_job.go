// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// BatchJob returns the built-in Karta for the batch/v1 Job workload.
func BatchJob() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "batch-job-v1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "job",
					Kind: &v1alpha1.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"},
					ScaleDefinition: &v1alpha1.ScaleDefinition{
						ReplicasPath: ptr.To(".spec.parallelism // 1"),
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
							Initializing: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     "(.status.active // 0) > 0 and (.status.ready // 0) == 0",
								ExpectedResult: "true",
							}}},
							Running: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     "(.status.active // 0) > 0 and (.status.ready // 0) > 0",
								ExpectedResult: "true",
							}}},
							Completed: []v1alpha1.StatusMatcher{
								{ByConditions: []v1alpha1.ExpectedCondition{{Type: "Complete", Status: ptr.To("True")}}},
								{ByConditions: []v1alpha1.ExpectedCondition{{Type: "SuccessCriteriaMet", Status: ptr.To("True")}}},
							},
							Failed: []v1alpha1.StatusMatcher{
								{ByConditions: []v1alpha1.ExpectedCondition{{Type: "Failed", Status: ptr.To("True")}}},
								{ByConditions: []v1alpha1.ExpectedCondition{{Type: "FailureTarget", Status: ptr.To("True")}}},
							},
							Degraded: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     ".spec.parallelism > 1 and (.status.ready // 0) < .spec.parallelism and ((.status.succeeded // 0) > 0 or (.status.failed // 0) > 0)",
								ExpectedResult: "true",
							}}},
							Suspended: []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{{Type: "Suspended", Status: ptr.To("True")}}}},
						},
					},
					SuspendDefinition: &v1alpha1.SuspendDefinition{
						SuspendActions: []v1alpha1.SuspendAction{{Path: ".spec.suspend", Value: "true"}},
						ResumeActions:  []v1alpha1.SuspendAction{{Path: ".spec.suspend", Value: "false"}},
					},
				},
			},
			Instructions: v1alpha1.OptimizationInstructions{
				GangScheduling: &v1alpha1.GangSchedulingInstruction{
					PodGroups: []v1alpha1.PodGroupDefinition{{
						Name: "job",
						Members: []v1alpha1.PodGroupMemberDefinition{{
							ComponentName:   "job",
							GroupByKeyPaths: []string{`.metadata.labels["batch.kubernetes.io/job-name"]`},
						}},
					}},
				},
			},
		},
	}
}
