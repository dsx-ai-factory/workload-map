// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// llmStartupReasons are the Ready=False reasons the controller reports while
// it converges, none of which is a failure: the Deployment progressing or not
// yet available, the router waiting on the Gateway API objects, the autoscaler
// objects appearing, and a LeaderWorkerSet rolling (it flips Available to
// False while keeping AllGroupsReady). MinimumReplicasUnavailable is also what
// a Deployment that never becomes available keeps reporting, because the
// controller forwards the Available reason over ProgressDeadlineExceeded, so
// pod-level failures read as Initializing until KServe exposes them.
var llmStartupReasons = []string{
	"Progressing",
	"MinimumReplicasUnavailable",
	"WaitingForGateway",
	"GatewaysNotReady",
	"HTTPRoutesNotReady",
	"InferencePoolNotReady",
	"HPAProgressing",
	"ScaledObjectProgressing",
	"AllGroupsReady",
}

// llmStopReason is the reason every condition carries after the user sets the
// serving.kserve.io/stop annotation: a deliberate pause, not a failure.
const llmStopReason = "Stopped"

// LLMInferenceService returns the built-in Karta for the KServe
// LLMInferenceService workload (serving.kserve.io/v1alpha1).
func LLMInferenceService() *v1alpha1.Karta {
	return llmInferenceService("v1alpha1")
}

// LLMInferenceServiceV1alpha2 returns the built-in Karta for the KServe
// LLMInferenceService workload (serving.kserve.io/v1alpha2).
//
// Both versions are served and their spec schemas are identical for every path
// this definition reads, so the two differ only in the GVK. v1alpha2 is the
// storage version and the version the controller reconciles: it stamps
// serving.kserve.io/v1alpha2 on every child's controller reference whichever
// version created the object, so a consumer walking ownerReferences from a
// child Deployment always lands on v1alpha2.
func LLMInferenceServiceV1alpha2() *v1alpha1.Karta {
	return llmInferenceService("v1alpha2")
}

// llmInferenceService builds the definition for one served version. The paths
// are version-independent; only the GVK and the object name change.
//
// Recorded on a router-less single-node service: Initializing, Running,
// Failed from a reconcile error, Failed from a missing preset, and Suspended
// from the stop annotation fired while Running. Unproven: the router reasons
// (no Gateway implementation on kind), the autoscaler reasons, a rolling
// LeaderWorkerSet, and the multi-node and disaggregated prefill components,
// whose structure and gang grouping follow the controller source rather than
// a recording.
func llmInferenceService(version string) *v1alpha1.Karta {
	condition := func(conditionType, status string) string {
		return fmt.Sprintf(`any(.status.conditions[]?; .type == %q and .status == %q`, conditionType, status)
	}
	ready := func(status string) string { return condition("Ready", status) }
	quoted := make([]string, len(llmStartupReasons))
	for i, reason := range llmStartupReasons {
		quoted[i] = fmt.Sprintf("%q", reason)
	}
	startup := strings.Join(quoted, ", ")

	lwsGroupKeys := []string{
		`.metadata.labels["app.kubernetes.io/name"]`,
		`.metadata.labels["leaderworkerset.sigs.k8s.io/group-index"] // "0"`,
	}

	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "serving-kserve-io-llminferenceservice-" + version},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "llminferenceservice",
					Kind: &v1alpha1.GroupVersionKind{Group: "serving.kserve.io", Version: version, Kind: "LLMInferenceService"},
					// The controller never writes a phase string: it uses a
					// Knative living condition set whose top-level condition
					// "Ready" is derived from WorkloadsReady and RouterReady.
					// PresetsCombined sits outside that rollup and halts
					// reconciliation when False, leaving Ready at Unknown, so
					// it is read explicitly.
					StatusDefinition: &v1alpha1.StatusDefinition{
						ConditionsDefinition: &v1alpha1.ConditionsDefinition{
							Path:             ".status.conditions",
							TypeFieldName:    "type",
							StatusFieldName:  "status",
							MessageFieldName: ptr.To("message"),
							ReasonFieldName:  ptr.To("reason"),
						},
						StatusMappings: v1alpha1.StatusMappings{
							Initializing: []v1alpha1.StatusMatcher{
								// A fresh object whose preset merge failed keeps
								// Ready at Unknown forever; that one is Failed below.
								{ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     ready("Unknown") + `) and (` + condition("PresetsCombined", "False") + `) | not)`,
									ExpectedResult: "true",
								}},
								{ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     ready("False") + ` and (.reason | IN(` + startup + `)))`,
									ExpectedResult: "true",
								}},
							},
							Running: []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Ready", Status: ptr.To("True")},
							}}},
							Suspended: []v1alpha1.StatusMatcher{
								{ByConditions: []v1alpha1.ExpectedCondition{
									{Type: "Ready", Status: ptr.To("False"), Reason: ptr.To(llmStopReason)},
								}},
								{ByConditions: []v1alpha1.ExpectedCondition{
									{Type: "PresetsCombined", Status: ptr.To("False"), Reason: ptr.To(llmStopReason)},
								}},
							},
							// Every other False reason is a failure, so reasons
							// the controller adds later still map to Failed.
							Failed: []v1alpha1.StatusMatcher{
								{ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     ready("False") + ` and (.reason | IN(` + startup + `, ` + fmt.Sprintf("%q", llmStopReason) + `) | not))`,
									ExpectedResult: "true",
								}},
								{ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     ready("Unknown") + `) and (` + condition("PresetsCombined", "False") + fmt.Sprintf(` and .reason != %q)`, llmStopReason) + `)`,
									ExpectedResult: "true",
								}},
							},
						},
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						// The decode pods, or every pod when .spec.prefill is
						// unset and the topology is aggregated.
						Name:     "main",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						OwnerRef: ptr.To("llminferenceservice"),
						// .spec.template is a bare PodSpec - the CRD declares no
						// .spec.template.metadata - so this is PodSpecPath, not
						// PodTemplateSpecPath. Taking the whole pod spec also
						// keeps nodeSelector, which a fragmented definition
						// cannot express and which carries the GPU SKU.
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodSpecPath: ptr.To("(select(.spec.worker == null) | .spec.template)"),
						},
						// Single-node only: with .spec.worker set the controller
						// runs .spec.template as the LeaderWorkerSet leader
						// instead (the leader component), so the paths select
						// on worker presence to count each pod spec once.
						// .spec.replicas and .spec.scaling are mutually
						// exclusive (a CRD validation rule). An autoscaled
						// service has no static count: the live one belongs to
						// the Deployment and its autoscaler, so replicas reads
						// null there and the bounds come from .spec.scaling.
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath:    ptr.To("(select(.spec.worker == null) | .spec.replicas)"),
							MinReplicasPath: ptr.To("(select(.spec.worker == null) | .spec.scaling.minReplicas)"),
							MaxReplicasPath: ptr.To("(select(.spec.worker == null) | .spec.scaling.maxReplicas)"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("llminferenceservice-workload"),
							},
						},
					},
					{
						// Disaggregated serving only: exists when .spec.prefill is set.
						Name:     "prefill",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						OwnerRef: ptr.To("llminferenceservice"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodSpecPath: ptr.To("(select(.spec.prefill.worker == null) | .spec.prefill.template)"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath:    ptr.To("(select(.spec.prefill.worker == null) | .spec.prefill.replicas)"),
							MinReplicasPath: ptr.To("(select(.spec.prefill.worker == null) | .spec.prefill.scaling.minReplicas)"),
							MaxReplicasPath: ptr.To("(select(.spec.prefill.worker == null) | .spec.prefill.scaling.maxReplicas)"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("llminferenceservice-workload-prefill"),
							},
						},
					},
					{
						// Multi-node serving: the controller emits a
						// LeaderWorkerSet whose worker pods run .spec.worker;
						// .spec.replicas is the group count and the autoscaler
						// bounds target the LeaderWorkerSet.
						Name:     "worker",
						Kind:     &v1alpha1.GroupVersionKind{Group: "leaderworkerset.x-k8s.io", Version: "v1", Kind: "LeaderWorkerSet"},
						OwnerRef: ptr.To("llminferenceservice"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodSpecPath: ptr.To(".spec.worker"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath:    ptr.To("(select(.spec.worker != null) | .spec.replicas)"),
							MinReplicasPath: ptr.To("(select(.spec.worker != null) | .spec.scaling.minReplicas)"),
							MaxReplicasPath: ptr.To("(select(.spec.worker != null) | .spec.scaling.maxReplicas)"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("llminferenceservice-workload-worker"),
							},
						},
					},
					{
						// Multi-node serving: the LeaderWorkerSet's leader pods
						// run .spec.template, the same spec the single-node
						// Deployment runs, under their own component label.
						Name:     "leader",
						Kind:     &v1alpha1.GroupVersionKind{Group: "leaderworkerset.x-k8s.io", Version: "v1", Kind: "LeaderWorkerSet"},
						OwnerRef: ptr.To("llminferenceservice"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodSpecPath: ptr.To("(select(.spec.worker != null) | .spec.template)"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath:    ptr.To("(select(.spec.worker != null) | .spec.replicas)"),
							MinReplicasPath: ptr.To("(select(.spec.worker != null) | .spec.scaling.minReplicas)"),
							MaxReplicasPath: ptr.To("(select(.spec.worker != null) | .spec.scaling.maxReplicas)"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("llminferenceservice-workload-leader"),
							},
						},
					},
					{
						// Disaggregated multi-node prefill: exists when
						// .spec.prefill.worker is set; leaders run
						// .spec.prefill.template.
						Name:     "prefill-leader",
						Kind:     &v1alpha1.GroupVersionKind{Group: "leaderworkerset.x-k8s.io", Version: "v1", Kind: "LeaderWorkerSet"},
						OwnerRef: ptr.To("llminferenceservice"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodSpecPath: ptr.To("(select(.spec.prefill.worker != null) | .spec.prefill.template)"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath:    ptr.To("(select(.spec.prefill.worker != null) | .spec.prefill.replicas)"),
							MinReplicasPath: ptr.To("(select(.spec.prefill.worker != null) | .spec.prefill.scaling.minReplicas)"),
							MaxReplicasPath: ptr.To("(select(.spec.prefill.worker != null) | .spec.prefill.scaling.maxReplicas)"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("llminferenceservice-workload-leader-prefill"),
							},
						},
					},
					{
						Name:     "prefill-worker",
						Kind:     &v1alpha1.GroupVersionKind{Group: "leaderworkerset.x-k8s.io", Version: "v1", Kind: "LeaderWorkerSet"},
						OwnerRef: ptr.To("llminferenceservice"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodSpecPath: ptr.To(".spec.prefill.worker"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath:    ptr.To("(select(.spec.prefill.worker != null) | .spec.prefill.replicas)"),
							MinReplicasPath: ptr.To("(select(.spec.prefill.worker != null) | .spec.prefill.scaling.minReplicas)"),
							MaxReplicasPath: ptr.To("(select(.spec.prefill.worker != null) | .spec.prefill.scaling.maxReplicas)"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("llminferenceservice-workload-worker-prefill"),
							},
						},
					},
					{
						// The llm-d inference scheduler: exists when
						// .spec.router.scheduler.template is set and
						// .spec.router.scheduler.pool.ref is not (a pool ref
						// means a user-managed scheduler, no Deployment).
						Name:     "router-scheduler",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						OwnerRef: ptr.To("llminferenceservice"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodSpecPath: ptr.To(".spec.router.scheduler.template"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.router.scheduler.replicas"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("llminferenceservice-router-scheduler"),
							},
						},
					},
				},
				// Kinds the controller creates that are not modelled as
				// components; listed so a consumer knows what it will meet.
				AdditionalChildKinds: []v1alpha1.GroupVersionKind{
					{Group: "leaderworkerset.x-k8s.io", Version: "v1", Kind: "LeaderWorkerSet"},
					{Group: "inference.networking.k8s.io", Version: "v1", Kind: "InferencePool"},
					{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRoute"},
					{Group: "", Version: "v1", Kind: "Service"},
				},
			},
			Instructions: v1alpha1.OptimizationInstructions{
				GangScheduling: &v1alpha1.GangSchedulingInstruction{
					// LeaderWorkerSet members group per group index as well:
					// each group is an independent model replica, so scaling
					// the group count must not grow a running gang.
					PodGroups: []v1alpha1.PodGroupDefinition{{
						Name: "service",
						Members: []v1alpha1.PodGroupMemberDefinition{
							{ComponentName: "main", GroupByKeyPaths: []string{`.metadata.labels["app.kubernetes.io/name"]`}},
							{ComponentName: "prefill", GroupByKeyPaths: []string{`.metadata.labels["app.kubernetes.io/name"]`}},
							{ComponentName: "worker", GroupByKeyPaths: lwsGroupKeys},
							{ComponentName: "leader", GroupByKeyPaths: lwsGroupKeys},
							{ComponentName: "prefill-leader", GroupByKeyPaths: lwsGroupKeys},
							{ComponentName: "prefill-worker", GroupByKeyPaths: lwsGroupKeys},
							{ComponentName: "router-scheduler", GroupByKeyPaths: []string{`.metadata.labels["app.kubernetes.io/name"]`}},
						},
					}},
				},
			},
		},
	}
}
