// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

// Deployment status tracks infrastructure and submission, independently of jobStatus.
// Cleanup, retry and suspension can retain the previous application's status.
const rayJobStartingExpression = `.metadata.deletionTimestamp == null and .spec.suspend != true and
    .status.jobStatus != "RUNNING" and .status.jobStatus != "SUCCEEDED" and
    .status.jobStatus != "FAILED" and .status.jobStatus != "STOPPED" and
    (.status.jobDeploymentStatus == null or .status.jobDeploymentStatus == "" or
     .status.jobDeploymentStatus == "Initializing" or .status.jobDeploymentStatus == "Running" or
     .status.jobDeploymentStatus == "Waiting")`

// Empty deployment and application status are the native New states, including a resume reset.
const rayJobAwaitingObservationExpression = `.metadata.deletionTimestamp == null and (.metadata.generation // 0) > 0 and
    (.status.jobDeploymentStatus == null or .status.jobDeploymentStatus == "") and
    (.status.jobStatus == null or .status.jobStatus == "") and
    (.status.reason == null or .status.reason == "") and
    (.status.message == null or .status.message == "")`

// Initializing can be observed before the controller acknowledges a suspension request.
const rayJobAwaitingSuspensionExpression = `.metadata.deletionTimestamp == null and (.metadata.generation // 0) > 0 and
    .spec.suspend == true and .status.jobDeploymentStatus == "Initializing" and
    (.status.jobStatus == null or .status.jobStatus == "" or .status.jobStatus == "PENDING") and
    (.status.reason == null or .status.reason == "") and
    (.status.message == null or .status.message == "")`

// Rayjob returns the built-in Karta for the RayJob workload
// (ray.io/v1).
func Rayjob() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "ray-io-rayjob-v1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "rayjob",
					Kind: &v1alpha1.GroupVersionKind{Group: "ray.io", Version: "v1", Kind: "RayJob"},
					SuspendDefinition: &v1alpha1.SuspendDefinition{
						SuspendActions: []v1alpha1.SuspendAction{{Path: ".spec.suspend", Value: "true"}},
						ResumeActions:  []v1alpha1.SuspendAction{{Path: ".spec.suspend", Value: "false"}},
					},
					StatusDefinition: &v1alpha1.StatusDefinition{
						PhaseDefinition: &v1alpha1.PhaseDefinition{
							Path: ".status.jobStatus",
						},
						ConditionsDefinition: &v1alpha1.ConditionsDefinition{
							Path:             ".status.conditions",
							TypeFieldName:    "type",
							StatusFieldName:  "status",
							MessageFieldName: ptr.To("message"),
						},
						StatusMappings: v1alpha1.StatusMappings{
							Pending: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression: `(` + rayJobStartingExpression + ` and
                                    (.status.jobStatus == "PENDING" or .status.jobDeploymentStatus == "Initializing" or
                                     .status.jobDeploymentStatus == "Waiting" or
                                     (.status.jobDeploymentStatus == "Running" and
                                      (.status.jobStatus == null or .status.jobStatus == "")))) or (` + rayJobAwaitingObservationExpression + `) or (` + rayJobAwaitingSuspensionExpression + `)`,
								ExpectedResult: "true",
							}}},
							Progressing: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     rayJobStartingExpression + ` and .status.jobDeploymentStatus == "Initializing"`,
								ExpectedResult: "true",
							}}},
							Running:   []v1alpha1.StatusMatcher{{ByPhase: "RUNNING"}},
							Completed: []v1alpha1.StatusMatcher{{ByPhase: "SUCCEEDED"}},
							Failed:    []v1alpha1.StatusMatcher{{ByPhase: "FAILED"}},
							Suspending: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression: `.metadata.deletionTimestamp == null and .status.jobDeploymentStatus == "Suspending" and
                                    (.status.jobStatus | IN("SUCCEEDED", "FAILED", "STOPPED") | not)`,
								ExpectedResult: "true",
							}}},
							Suspended: []v1alpha1.StatusMatcher{{ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     `.status.jobDeploymentStatus == "Suspended"`,
								ExpectedResult: "true",
							}}},
						},
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:     "head",
						Kind:     &v1alpha1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
						OwnerRef: ptr.To("rayjob"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodTemplateSpecPath: ptr.To(".spec.rayClusterSpec.headGroupSpec.template"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To("1"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["ray.io/node-type"]`,
								Value:   ptr.To("head"),
							},
						},
					},
					{
						Name:     "worker",
						Kind:     &v1alpha1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
						OwnerRef: ptr.To("rayjob"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodTemplateSpecPath: ptr.To(".spec.rayClusterSpec.workerGroupSpecs[].template"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath:    ptr.To(".spec.rayClusterSpec.workerGroupSpecs[].replicas // 1"),
							MinReplicasPath: ptr.To(".spec.rayClusterSpec.workerGroupSpecs[].minReplicas"),
							MaxReplicasPath: ptr.To(".spec.rayClusterSpec.workerGroupSpecs[].maxReplicas"),
						},
						InstanceIdPath: ptr.To(".spec.rayClusterSpec.workerGroupSpecs[].groupName"),
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["ray.io/node-type"]`,
								Value:   ptr.To("worker"),
							},
							ComponentInstanceSelector: &v1alpha1.ComponentInstanceSelector{
								IdPath: `.metadata.labels["ray.io/group"]`,
							},
						},
					},
				},
			},
			Instructions: v1alpha1.OptimizationInstructions{
				GangScheduling: &v1alpha1.GangSchedulingInstruction{
					PodGroups: []v1alpha1.PodGroupDefinition{{
						Name: "job",
						Members: []v1alpha1.PodGroupMemberDefinition{
							{
								ComponentName:   "head",
								GroupByKeyPaths: []string{`.metadata.labels["ray.io/cluster"]`},
							},
							{
								ComponentName:   "worker",
								GroupByKeyPaths: []string{`.metadata.labels["ray.io/cluster"]`},
							},
						},
					}},
				},
			},
		},
	}
}
