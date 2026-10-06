// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// sandboxOwnerUID resolves a pod's owning Sandbox UID, shared by the Sandbox pod
// and any other pod the Sandbox owns.
const sandboxOwnerUID = `[.metadata.ownerReferences[]? | select(.kind == "Sandbox") | .uid][0]`

// Sandbox returns the built-in Karta for the Kubernetes SIG Agent Sandbox
// workload (agents.x-k8s.io/v1beta1). A Sandbox runs one isolated, stateful pod
// created from .spec.podTemplate, and is suspended and resumed through
// .spec.operatingMode.
//
// Integrations built on Agent Sandbox can add their own pods to a Sandbox by
// making the Sandbox their owner (for example a supervisor or proxy pod). Every
// pod a Sandbox owns maps to its single pod component, and the gang keys each pod
// on its owning Sandbox, so such pods join the Sandbox's workload and pod group.
// Their number is integration-specific, so only the Sandbox pod counts toward
// minMember.
func Sandbox() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "agents-x-k8s-io-sandbox-v1beta1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "sandbox",
					Kind: &v1alpha1.GroupVersionKind{Group: "agents.x-k8s.io", Version: "v1beta1", Kind: "Sandbox"},
					SuspendDefinition: &v1alpha1.SuspendDefinition{
						SuspendActions: []v1alpha1.SuspendAction{{Path: ".spec.operatingMode", Value: `"Suspended"`}},
						ResumeActions:  []v1alpha1.SuspendAction{{Path: ".spec.operatingMode", Value: `"Running"`}},
					},
					StatusDefinition: &v1alpha1.StatusDefinition{
						ConditionsDefinition: &v1alpha1.ConditionsDefinition{
							Path:             ".status.conditions",
							TypeFieldName:    "type",
							StatusFieldName:  "status",
							MessageFieldName: ptr.To("message"),
							ReasonFieldName:  ptr.To("reason"),
						},
						// Every status pairs the Sandbox conditions with the requested
						// .spec.operatingMode, so a condition the controller has not yet
						// updated cannot resolve to a second, contradictory status.
						StatusMappings: v1alpha1.StatusMappings{
							Initializing: []v1alpha1.StatusMatcher{{
								ByConditions: []v1alpha1.ExpectedCondition{
									{Type: "Ready", Status: ptr.To("False")},
									{Type: "Suspended", Status: ptr.To("False")},
								},
								ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     `(.spec.operatingMode // "Running") != "Suspended"`,
									ExpectedResult: "true",
								},
							}},
							Running: []v1alpha1.StatusMatcher{{
								ByConditions: []v1alpha1.ExpectedCondition{{Type: "Ready", Status: ptr.To("True")}},
								ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     `(.spec.operatingMode // "Running") != "Suspended"`,
									ExpectedResult: "true",
								},
							}},
							Suspending: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     `.spec.operatingMode == "Suspended" and ([(.status.conditions // [])[] | select(.type == "Suspended" and .status == "True")] | length) == 0`,
								ExpectedResult: "true",
							}}},
							Suspended: []v1alpha1.StatusMatcher{{
								ByConditions: []v1alpha1.ExpectedCondition{{Type: "Suspended", Status: ptr.To("True")}},
								ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     `.spec.operatingMode == "Suspended"`,
									ExpectedResult: "true",
								},
							}},
							// Resumed but the controller still reports the Sandbox as suspended.
							Resuming: []v1alpha1.StatusMatcher{{
								ByConditions: []v1alpha1.ExpectedCondition{{Type: "Suspended", Status: ptr.To("True")}},
								ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     `(.spec.operatingMode // "Running") != "Suspended"`,
									ExpectedResult: "true",
								},
							}},
						},
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:     "agent",
						Kind:     &v1alpha1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
						OwnerRef: ptr.To("sandbox"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodTemplateSpecPath: ptr.To(".spec.podTemplate"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(`if .spec.operatingMode == "Suspended" then 0 else 1 end`),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["agents.x-k8s.io/sandbox-name-hash"]`,
							},
						},
					},
				},
			},
			Instructions: v1alpha1.OptimizationInstructions{
				GangScheduling: &v1alpha1.GangSchedulingInstruction{
					PodGroups: []v1alpha1.PodGroupDefinition{{
						Name: "sandbox",
						Members: []v1alpha1.PodGroupMemberDefinition{
							{
								ComponentName:   "agent",
								GroupByKeyPaths: []string{sandboxOwnerUID},
							},
						},
					}},
				},
			},
		},
	}
}
