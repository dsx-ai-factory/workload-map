// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

// KueueWorkload returns the admission, readiness and activity view of a Kueue Workload.
// Running requires Kueue to report PodsReady, through waitForPodsReady or its
// workload-level readiness feature. Admission alone does not mean that Pods
// have started. Missing readiness remains Undefined.
//
// Completed follows Kueue's Finished/Succeeded condition. For Pod groups this
// means the group finished, not that every Pod or application task succeeded.
// Failed maps only explicit execution or permanent start failures.
// Workload podSets describe admission requests. They are not mutable templates
// for the Pods, and the Workload does not own those Pods.
// Suspend deactivates and evicts the Workload. Resume allows admission again;
// recreating deleted Pods requires their controller. Finished work is not restarted.
func KueueWorkload() *v1alpha1.Karta {
	const unfinished = `((.status.conditions // []) | any(.type == "Finished" and .status == "True") | not)`
	const reserved = `((.status.conditions // []) | any(.type == "QuotaReserved" and .status == "True"))`
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "kueue-x-k8s-io-workload-v1beta2"},
		Spec: v1alpha1.KartaSpec{
			Experimental: true,
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "workload",
					Kind: &v1alpha1.GroupVersionKind{Group: "kueue.x-k8s.io", Version: "v1beta2", Kind: "Workload"},
					SuspendDefinition: &v1alpha1.SuspendDefinition{
						SuspendActions: []v1alpha1.SuspendAction{{Path: ".spec.active", Value: "false"}},
						ResumeActions:  []v1alpha1.SuspendAction{{Path: ".spec.active", Value: "true"}},
					},
					StatusDefinition: &v1alpha1.StatusDefinition{
						ConditionsDefinition: &v1alpha1.ConditionsDefinition{
							Path:             ".status.conditions // []",
							TypeFieldName:    "type",
							StatusFieldName:  "status",
							ReasonFieldName:  ptr.To("reason"),
							MessageFieldName: ptr.To("message"),
						},
						StatusMappings: v1alpha1.StatusMappings{
							Initializing: []v1alpha1.StatusMatcher{{
								ByConditions: []v1alpha1.ExpectedCondition{{Type: "Admitted", Status: ptr.To("False")}},
								ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     `.spec.active != false and ` + unfinished,
									ExpectedResult: "true",
								},
							}},
							Running: []v1alpha1.StatusMatcher{{
								ByConditions: []v1alpha1.ExpectedCondition{
									{Type: "Admitted", Status: ptr.To("True")},
									{Type: "PodsReady", Status: ptr.To("True")},
								},
								ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     `.spec.active != false and ((.status.conditions // []) | any((.type == "Finished" or .type == "Evicted" or .type == "WaitingForReplacementPods") and .status == "True") | not)`,
									ExpectedResult: "true",
								},
							}},
							Suspended: []v1alpha1.StatusMatcher{{
								ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     `.spec.active == false and ` + unfinished + ` and (` + reserved + ` | not)`,
									ExpectedResult: "true",
								},
							}},
							Completed: []v1alpha1.StatusMatcher{
								{ByConditions: []v1alpha1.ExpectedCondition{{Type: "Finished", Status: ptr.To("True"), Reason: ptr.To("Succeeded")}}},
							},
							Failed: []v1alpha1.StatusMatcher{
								{ByConditions: []v1alpha1.ExpectedCondition{{Type: "Finished", Status: ptr.To("True"), Reason: ptr.To("Failed")}}},
								{ByConditions: []v1alpha1.ExpectedCondition{{Type: "Finished", Status: ptr.To("True"), Reason: ptr.To("FailedToStart")}}},
							},
						},
					},
				},
			},
		},
	}
}
