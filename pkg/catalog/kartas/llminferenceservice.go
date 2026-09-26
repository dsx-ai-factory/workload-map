// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

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
// storage version, which is the one that matters to a consumer walking
// ownerReferences: the apiserver normalises them to the storage version, so a
// child Deployment of an LLMInferenceService carries
// apiVersion serving.kserve.io/v1alpha2 whichever version created the object.
func LLMInferenceServiceV1alpha2() *v1alpha1.Karta {
	return llmInferenceService("v1alpha2")
}

// llmInferenceService builds the definition for one served version. The paths
// are version-independent; only the GVK and the object name change.
func llmInferenceService(version string) *v1alpha1.Karta {
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
					StatusDefinition: &v1alpha1.StatusDefinition{
						ConditionsDefinition: &v1alpha1.ConditionsDefinition{
							Path:             ".status.conditions",
							TypeFieldName:    "type",
							StatusFieldName:  "status",
							MessageFieldName: ptr.To("message"),
							ReasonFieldName:  ptr.To("reason"),
						},
						StatusMappings: v1alpha1.StatusMappings{
							Initializing: []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Ready", Status: ptr.To("Unknown")},
							}}},
							Running: []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Ready", Status: ptr.To("True")},
							}}},
							Failed: []v1alpha1.StatusMatcher{{ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Ready", Status: ptr.To("False")},
							}}},
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
						// .spec.template is a bare PodSpec — the CRD declares no
						// .spec.template.metadata — so this is PodSpecPath, not
						// PodTemplateSpecPath. Taking the whole pod spec also
						// keeps nodeSelector, which a fragmented definition
						// cannot express and which carries the GPU SKU.
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodSpecPath: ptr.To(".spec.template"),
						},
						// .spec.replicas and .spec.scaling are mutually
						// exclusive (enforced by the admission webhook), so one
						// of the two is always absent. `//` covers both and is
						// still an assignable path.
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath:    ptr.To("(.spec.replicas // .spec.scaling.minReplicas)"),
							MinReplicasPath: ptr.To(".spec.scaling.minReplicas"),
							MaxReplicasPath: ptr.To(".spec.scaling.maxReplicas"),
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
							PodSpecPath: ptr.To(".spec.prefill.template"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath:    ptr.To("(.spec.prefill.replicas // .spec.prefill.scaling.minReplicas)"),
							MinReplicasPath: ptr.To(".spec.prefill.scaling.minReplicas"),
							MaxReplicasPath: ptr.To(".spec.prefill.scaling.maxReplicas"),
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
						// LeaderWorkerSet and .spec.replicas is the group count.
						Name:     "worker",
						Kind:     &v1alpha1.GroupVersionKind{Group: "leaderworkerset.x-k8s.io", Version: "v1", Kind: "LeaderWorkerSet"},
						OwnerRef: ptr.To("llminferenceservice"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodSpecPath: ptr.To(".spec.worker"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.replicas"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("llminferenceservice-workload-worker"),
							},
						},
					},
					{
						// The llm-d inference scheduler: exists when
						// .spec.router.scheduler is set.
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
					PodGroups: []v1alpha1.PodGroupDefinition{{
						Name: "service",
						Members: []v1alpha1.PodGroupMemberDefinition{
							{ComponentName: "main", GroupByKeyPaths: []string{`.metadata.labels["app.kubernetes.io/name"]`}},
							{ComponentName: "prefill", GroupByKeyPaths: []string{`.metadata.labels["app.kubernetes.io/name"]`}},
							{ComponentName: "worker", GroupByKeyPaths: []string{`.metadata.labels["app.kubernetes.io/name"]`}},
							{ComponentName: "router-scheduler", GroupByKeyPaths: []string{`.metadata.labels["app.kubernetes.io/name"]`}},
						},
					}},
				},
			},
		},
	}
}
