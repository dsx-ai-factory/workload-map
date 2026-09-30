// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// +kubebuilder:object:generate=true

package types

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"

	"k8s.io/utils/ptr"
)

// Reactor represents a Dynamo-like job with map of service components
// Multiple components via map key discovery, fragmented pod spec extraction
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type Reactor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ReactorSpec   `json:"spec,omitempty"`
	Status            ReactorStatus `json:"status,omitempty"`
}

type ReactorSpec struct {
	// Services defines a map of service specifications keyed by service name
	Services map[string]ServiceSpec `json:"services,omitempty"`
}

type ServiceSpec struct {
	// Labels for pods (fragmented field)
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations for pods (fragmented field)
	Annotations map[string]string `json:"annotations,omitempty"`

	// Containers for pods (fragmented field)
	Containers []corev1.Container `json:"containers,omitempty"`

	// mainContainer (fragmented field)
	MainContainer corev1.Container `json:"mainContainer,omitempty"`

	// Resources for pods (fragmented field)
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Scale configuration
	Replicas    int32 `json:"replicas"`
	MinReplicas int32 `json:"minReplicas"`
	MaxReplicas int32 `json:"maxReplicas"`
}

type ReactorStatus struct {
	// Phase represents the current phase
	Phase string `json:"phase,omitempty"`

	// Conditions represent the latest available observations
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ReactorKarta returns a Karta for Reactor
// Models DynamO-like structure: map components, fragmented pod spec extraction
func ReactorKarta() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reactor",
		},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "reactor",
					Kind: &v1alpha1.GroupVersionKind{
						Group:   "jobs.example.com",
						Version: "v1",
						Kind:    "Reactor",
					},
					StatusDefinition: &v1alpha1.StatusDefinition{
						PhaseDefinition: &v1alpha1.PhaseDefinition{
							Path: ".status.phase",
						},
						ConditionsDefinition: &v1alpha1.ConditionsDefinition{
							Path:             ".status.conditions",
							TypeFieldName:    "type",
							StatusFieldName:  "status",
							ReasonFieldName:  ptr.To("reason"),
							MessageFieldName: ptr.To("message"),
						},
						StatusMappings: v1alpha1.StatusMappings{
							Initializing: []v1alpha1.StatusMatcher{
								{
									ByPhase: "pending",
								},
							},
							Running: []v1alpha1.StatusMatcher{
								{
									ByPhase: "running",
								},
							},
							Failed: []v1alpha1.StatusMatcher{
								{
									ByPhase: "failed",
								},
							},
						},
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:           "service",
						OwnerRef:       ptr.To("reactor"),
						InstanceIdPath: ptr.To(".spec.services | to_entries[] | .key"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								LabelsPath:      ptr.To(".spec.services | .[] | .labels"),
								AnnotationsPath: ptr.To(".spec.services | .[] | .annotations"),
								ContainersPath:  ptr.To(".spec.services | .[] | .containers"),
								ContainerPath:   ptr.To(".spec.services | .[] | .mainContainer"),
								ResourcesPath:   ptr.To(".spec.services | .[] | .resources"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath:    ptr.To(".spec.services | .[] | .replicas"),
							MinReplicasPath: ptr.To(".spec.services | .[] | .minReplicas"),
							MaxReplicasPath: ptr.To(".spec.services | .[] | .maxReplicas"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: ".metadata.labels.service-name",
							},
							ComponentInstanceSelector: &v1alpha1.ComponentInstanceSelector{
								IdPath: ".metadata.labels.service-name",
							},
						},
					},
				},
			},
		},
	}
}

// NewReactorObject creates a test instance of Reactor
// Map job structure with multiple discovered components via map key discovery
func NewReactorObject() *Reactor {
	return &Reactor{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "jobs.example.com/v1",
			Kind:       "Reactor",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "reactor-example",
			Namespace: "default",
			Labels: map[string]string{
				"app":  "reactor",
				"type": "distributed-service",
			},
		},
		Spec: ReactorSpec{
			Services: map[string]ServiceSpec{
				"api": {
					Labels: map[string]string{
						"app":          "reactor",
						"service-name": "api",
						"tier":         "frontend",
					},
					Annotations: map[string]string{
						"service.beta.kubernetes.io/aws-load-balancer-type": "nlb",
					},
					Containers: []corev1.Container{
						{
							Name:  "api-server",
							Image: "api:latest",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 8080,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Env: []corev1.EnvVar{
								{
									Name:  "PORT",
									Value: "8080",
								},
							},
						},
					},
					MainContainer: corev1.Container{
						Name:  "api-server-main",
						Image: "api:latest",
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
					Replicas:    3,
					MinReplicas: 2,
					MaxReplicas: 10,
				},
				"worker": {
					Labels: map[string]string{
						"app":          "reactor",
						"service-name": "worker",
						"tier":         "backend",
					},
					Annotations: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/port":   "9090",
					},
					Containers: []corev1.Container{
						{
							Name:  "worker",
							Image: "worker:latest",
							Env: []corev1.EnvVar{
								{
									Name:  "WORKER_TYPE",
									Value: "processor",
								},
							},
						},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
							"nvidia.com/gpu":      resource.MustParse("1"),
						},
					},
					Replicas:    5,
					MinReplicas: 3,
					MaxReplicas: 20,
				},
				"cache": {
					Labels: map[string]string{
						"app":          "reactor",
						"service-name": "cache",
						"tier":         "middleware",
					},
					Containers: []corev1.Container{
						{
							Name:  "redis",
							Image: "redis:7-alpine",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 6379,
									Protocol:      corev1.ProtocolTCP,
								},
							},
						},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("250m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
					Replicas:    1,
					MinReplicas: 1,
					MaxReplicas: 3,
				},
			},
		},
		Status: ReactorStatus{
			Phase: "running",
			Conditions: []metav1.Condition{
				{
					Type:   "Ready",
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
}
