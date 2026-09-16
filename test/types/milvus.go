// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// +kubebuilder:object:generate=true

package types

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// Milvus is a status-only root with pod-bearing StatefulSet children under
// .spec.components. Mirrors docs/catalog/milvus-io-milvus-v1beta1.yaml.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type Milvus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MilvusSpec   `json:"spec,omitempty"`
	Status            MilvusStatus `json:"status,omitempty"`
}

type MilvusSpec struct {
	Components MilvusComponents `json:"components,omitempty"`
}

type MilvusComponents struct {
	QueryNode MilvusComponentSpec `json:"queryNode,omitempty"`
	DataNode  MilvusComponentSpec `json:"dataNode,omitempty"`

	// Proxy carries no replicas field: a pod-bearing component with no scale.
	Proxy MilvusComponentSpec `json:"proxy,omitempty"`
}

type MilvusComponentSpec struct {
	Replicas *int32                 `json:"replicas,omitempty"`
	Template corev1.PodTemplateSpec `json:"template,omitempty"`
}

type MilvusStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// MilvusKarta returns a Karta for Milvus.
func MilvusKarta() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		ObjectMeta: metav1.ObjectMeta{
			Name: "milvus",
		},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "milvus",
					Kind: &v1alpha1.GroupVersionKind{
						Group:   "milvus.io",
						Version: "v1beta1",
						Kind:    "Milvus",
					},
					StatusDefinition: &v1alpha1.StatusDefinition{
						ConditionsDefinition: &v1alpha1.ConditionsDefinition{
							Path:            ".status.conditions",
							TypeFieldName:   "type",
							StatusFieldName: "status",
						},
						StatusMappings: v1alpha1.StatusMappings{
							Running: []v1alpha1.StatusMatcher{
								{
									ByConditions: []v1alpha1.ExpectedCondition{
										{Type: "EtcdReady", Status: ptr.To("True")},
										{Type: "StorageReady", Status: ptr.To("True")},
										{Type: "MsgStreamReady", Status: ptr.To("True")},
										{Type: "MilvusReady", Status: ptr.To("True")},
									},
								},
							},
						},
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:     "querynode",
						OwnerRef: ptr.To("milvus"),
						Kind: &v1alpha1.GroupVersionKind{
							Group:   "apps",
							Version: "v1",
							Kind:    "StatefulSet",
						},
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodTemplateSpecPath: ptr.To(".spec.components.queryNode.template"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.components.queryNode.replicas"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("querynode"),
							},
						},
					},
					{
						Name:     "datanode",
						OwnerRef: ptr.To("milvus"),
						Kind: &v1alpha1.GroupVersionKind{
							Group:   "apps",
							Version: "v1",
							Kind:    "StatefulSet",
						},
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodTemplateSpecPath: ptr.To(".spec.components.dataNode.template"),
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.components.dataNode.replicas"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("datanode"),
							},
						},
					},
					{
						// Pod-bearing but no ScaleDefinition: defaults to one pod.
						Name:     "proxy",
						OwnerRef: ptr.To("milvus"),
						Kind: &v1alpha1.GroupVersionKind{
							Group:   "apps",
							Version: "v1",
							Kind:    "StatefulSet",
						},
						SpecDefinition: &v1alpha1.SpecDefinition{
							PodTemplateSpecPath: ptr.To(".spec.components.proxy.template"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("proxy"),
							},
						},
					},
				},
			},
		},
	}
}

// NewMilvusObject returns a running Milvus with 2 querynode and 3 datanode replicas.
func NewMilvusObject() *Milvus {
	return &Milvus{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "milvus.io/v1beta1",
			Kind:       "Milvus",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "milvus-example",
			Namespace: "default",
			Labels: map[string]string{
				"app.kubernetes.io/instance": "milvus-example",
			},
		},
		Spec: MilvusSpec{
			Components: MilvusComponents{
				QueryNode: MilvusComponentSpec{
					Replicas: ptr.To(int32(2)),
					Template: milvusPodTemplate("querynode"),
				},
				DataNode: MilvusComponentSpec{
					Replicas: ptr.To(int32(3)),
					Template: milvusPodTemplate("datanode"),
				},
				Proxy: MilvusComponentSpec{
					// No Replicas: exercises a pod-bearing child with no scale in the spec.
					Template: milvusPodTemplate("proxy"),
				},
			},
		},
		Status: MilvusStatus{
			Conditions: []metav1.Condition{
				{Type: "EtcdReady", Status: metav1.ConditionTrue},
				{Type: "StorageReady", Status: metav1.ConditionTrue},
				{Type: "MsgStreamReady", Status: metav1.ConditionTrue},
				{Type: "MilvusReady", Status: metav1.ConditionTrue},
			},
		},
	}
}

func milvusPodTemplate(component string) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"app.kubernetes.io/component": component,
				"app.kubernetes.io/instance":  "milvus-example",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  component,
					Image: "milvusdb/milvus:v2.4.0",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
				},
			},
		},
	}
}
