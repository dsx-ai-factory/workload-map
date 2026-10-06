// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// Milvus returns the built-in Karta for the Milvus workload
// (milvus.io/v1beta1).
func Milvus() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		TypeMeta:   metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"},
		ObjectMeta: metav1.ObjectMeta{Name: "milvus-io-milvus-v1beta1"},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "milvus",
					Kind: &v1alpha1.GroupVersionKind{Group: "milvus.io", Version: "v1beta1", Kind: "Milvus"},
					StatusDefinition: &v1alpha1.StatusDefinition{
						PhaseDefinition: &v1alpha1.PhaseDefinition{
							// .status.status is an enum string: Healthy, Pending, Unhealthy
							Path: ".status.status",
						},
						ConditionsDefinition: &v1alpha1.ConditionsDefinition{
							Path:            ".status.conditions",
							TypeFieldName:   "type",
							StatusFieldName: "status",
						},
						StatusMappings: v1alpha1.StatusMappings{
							Running: []v1alpha1.StatusMatcher{
								{ByPhase: "Healthy"},
							},
							Initializing: []v1alpha1.StatusMatcher{
								{ByPhase: "Pending"},
							},
							Degraded: []v1alpha1.StatusMatcher{
								{ByPhase: "Unhealthy"},
							},
						},
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:     "standalone",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						OwnerRef: ptr.To("milvus"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								SchedulerNamePath:     ptr.To(".spec.components.standalone.schedulerName"),
								LabelsPath:            ptr.To(".spec.components.standalone.podLabels"),
								AnnotationsPath:       ptr.To(".spec.components.standalone.podAnnotations"),
								ResourcesPath:         ptr.To(".spec.components.standalone.resources"),
								PriorityClassNamePath: ptr.To(".spec.components.standalone.priorityClassName"),
								NodeAffinityPath:      ptr.To(".spec.components.standalone.affinity.nodeAffinity"),
								PodAffinityPath:       ptr.To(".spec.components.standalone.affinity.podAffinity"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.components.standalone.replicas"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("standalone"),
							},
						},
					},
					{
						Name:     "proxy",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						OwnerRef: ptr.To("milvus"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								SchedulerNamePath:     ptr.To(".spec.components.proxy.schedulerName"),
								LabelsPath:            ptr.To(".spec.components.proxy.podLabels"),
								AnnotationsPath:       ptr.To(".spec.components.proxy.podAnnotations"),
								ResourcesPath:         ptr.To(".spec.components.proxy.resources"),
								PriorityClassNamePath: ptr.To(".spec.components.proxy.priorityClassName"),
								NodeAffinityPath:      ptr.To(".spec.components.proxy.affinity.nodeAffinity"),
								PodAffinityPath:       ptr.To(".spec.components.proxy.affinity.podAffinity"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.components.proxy.replicas"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("proxy"),
							},
						},
					},
					{
						Name:     "mixcoord",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						OwnerRef: ptr.To("milvus"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								SchedulerNamePath:     ptr.To(".spec.components.mixCoord.schedulerName"),
								LabelsPath:            ptr.To(".spec.components.mixCoord.podLabels"),
								AnnotationsPath:       ptr.To(".spec.components.mixCoord.podAnnotations"),
								ResourcesPath:         ptr.To(".spec.components.mixCoord.resources"),
								PriorityClassNamePath: ptr.To(".spec.components.mixCoord.priorityClassName"),
								NodeAffinityPath:      ptr.To(".spec.components.mixCoord.affinity.nodeAffinity"),
								PodAffinityPath:       ptr.To(".spec.components.mixCoord.affinity.podAffinity"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.components.mixCoord.replicas"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("mixcoord"),
							},
						},
					},
					{
						Name:     "datanode",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						OwnerRef: ptr.To("milvus"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								SchedulerNamePath:     ptr.To(".spec.components.dataNode.schedulerName"),
								LabelsPath:            ptr.To(".spec.components.dataNode.podLabels"),
								AnnotationsPath:       ptr.To(".spec.components.dataNode.podAnnotations"),
								ResourcesPath:         ptr.To(".spec.components.dataNode.resources"),
								PriorityClassNamePath: ptr.To(".spec.components.dataNode.priorityClassName"),
								NodeAffinityPath:      ptr.To(".spec.components.dataNode.affinity.nodeAffinity"),
								PodAffinityPath:       ptr.To(".spec.components.dataNode.affinity.podAffinity"),
							},
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
						Name:     "querynode",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						OwnerRef: ptr.To("milvus"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								SchedulerNamePath:     ptr.To(".spec.components.queryNode.schedulerName"),
								LabelsPath:            ptr.To(".spec.components.queryNode.podLabels"),
								AnnotationsPath:       ptr.To(".spec.components.queryNode.podAnnotations"),
								ResourcesPath:         ptr.To(".spec.components.queryNode.resources"),
								PriorityClassNamePath: ptr.To(".spec.components.queryNode.priorityClassName"),
								NodeAffinityPath:      ptr.To(".spec.components.queryNode.affinity.nodeAffinity"),
								PodAffinityPath:       ptr.To(".spec.components.queryNode.affinity.podAffinity"),
							},
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
						Name:     "streamingnode",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						OwnerRef: ptr.To("milvus"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								SchedulerNamePath:     ptr.To(".spec.components.streamingNode.schedulerName"),
								LabelsPath:            ptr.To(".spec.components.streamingNode.podLabels"),
								AnnotationsPath:       ptr.To(".spec.components.streamingNode.podAnnotations"),
								ResourcesPath:         ptr.To(".spec.components.streamingNode.resources"),
								PriorityClassNamePath: ptr.To(".spec.components.streamingNode.priorityClassName"),
								NodeAffinityPath:      ptr.To(".spec.components.streamingNode.affinity.nodeAffinity"),
								PodAffinityPath:       ptr.To(".spec.components.streamingNode.affinity.podAffinity"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.components.streamingNode.replicas"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("streamingnode"),
							},
						},
					},
					{
						Name:     "indexnode",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						OwnerRef: ptr.To("milvus"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								SchedulerNamePath:     ptr.To(".spec.components.indexNode.schedulerName"),
								LabelsPath:            ptr.To(".spec.components.indexNode.podLabels"),
								AnnotationsPath:       ptr.To(".spec.components.indexNode.podAnnotations"),
								ResourcesPath:         ptr.To(".spec.components.indexNode.resources"),
								PriorityClassNamePath: ptr.To(".spec.components.indexNode.priorityClassName"),
								NodeAffinityPath:      ptr.To(".spec.components.indexNode.affinity.nodeAffinity"),
								PodAffinityPath:       ptr.To(".spec.components.indexNode.affinity.podAffinity"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.components.indexNode.replicas"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("indexnode"),
							},
						},
					},
					{
						Name:     "rootcoord",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						OwnerRef: ptr.To("milvus"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								SchedulerNamePath:     ptr.To(".spec.components.rootCoord.schedulerName"),
								LabelsPath:            ptr.To(".spec.components.rootCoord.podLabels"),
								AnnotationsPath:       ptr.To(".spec.components.rootCoord.podAnnotations"),
								ResourcesPath:         ptr.To(".spec.components.rootCoord.resources"),
								PriorityClassNamePath: ptr.To(".spec.components.rootCoord.priorityClassName"),
								NodeAffinityPath:      ptr.To(".spec.components.rootCoord.affinity.nodeAffinity"),
								PodAffinityPath:       ptr.To(".spec.components.rootCoord.affinity.podAffinity"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.components.rootCoord.replicas"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("rootcoord"),
							},
						},
					},
					{
						Name:     "datacoord",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						OwnerRef: ptr.To("milvus"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								SchedulerNamePath:     ptr.To(".spec.components.dataCoord.schedulerName"),
								LabelsPath:            ptr.To(".spec.components.dataCoord.podLabels"),
								AnnotationsPath:       ptr.To(".spec.components.dataCoord.podAnnotations"),
								ResourcesPath:         ptr.To(".spec.components.dataCoord.resources"),
								PriorityClassNamePath: ptr.To(".spec.components.dataCoord.priorityClassName"),
								NodeAffinityPath:      ptr.To(".spec.components.dataCoord.affinity.nodeAffinity"),
								PodAffinityPath:       ptr.To(".spec.components.dataCoord.affinity.podAffinity"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.components.dataCoord.replicas"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("datacoord"),
							},
						},
					},
					{
						Name:     "querycoord",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						OwnerRef: ptr.To("milvus"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								SchedulerNamePath:     ptr.To(".spec.components.queryCoord.schedulerName"),
								LabelsPath:            ptr.To(".spec.components.queryCoord.podLabels"),
								AnnotationsPath:       ptr.To(".spec.components.queryCoord.podAnnotations"),
								ResourcesPath:         ptr.To(".spec.components.queryCoord.resources"),
								PriorityClassNamePath: ptr.To(".spec.components.queryCoord.priorityClassName"),
								NodeAffinityPath:      ptr.To(".spec.components.queryCoord.affinity.nodeAffinity"),
								PodAffinityPath:       ptr.To(".spec.components.queryCoord.affinity.podAffinity"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.components.queryCoord.replicas"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("querycoord"),
							},
						},
					},
					{
						Name:     "indexcoord",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						OwnerRef: ptr.To("milvus"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								SchedulerNamePath:     ptr.To(".spec.components.indexCoord.schedulerName"),
								LabelsPath:            ptr.To(".spec.components.indexCoord.podLabels"),
								AnnotationsPath:       ptr.To(".spec.components.indexCoord.podAnnotations"),
								ResourcesPath:         ptr.To(".spec.components.indexCoord.resources"),
								PriorityClassNamePath: ptr.To(".spec.components.indexCoord.priorityClassName"),
								NodeAffinityPath:      ptr.To(".spec.components.indexCoord.affinity.nodeAffinity"),
								PodAffinityPath:       ptr.To(".spec.components.indexCoord.affinity.podAffinity"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.components.indexCoord.replicas"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("indexcoord"),
							},
						},
					},
					{
						Name:     "cdc",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						OwnerRef: ptr.To("milvus"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								SchedulerNamePath:     ptr.To(".spec.components.cdc.schedulerName"),
								LabelsPath:            ptr.To(".spec.components.cdc.podLabels"),
								AnnotationsPath:       ptr.To(".spec.components.cdc.podAnnotations"),
								ResourcesPath:         ptr.To(".spec.components.cdc.resources"),
								PriorityClassNamePath: ptr.To(".spec.components.cdc.priorityClassName"),
								NodeAffinityPath:      ptr.To(".spec.components.cdc.affinity.nodeAffinity"),
								PodAffinityPath:       ptr.To(".spec.components.cdc.affinity.podAffinity"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.components.cdc.replicas"),
						},
						PodSelector: &v1alpha1.PodSelector{
							ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
								KeyPath: `.metadata.labels["app.kubernetes.io/component"]`,
								Value:   ptr.To("cdc"),
							},
						},
					},
					{
						Name:     "etcd",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"},
						OwnerRef: ptr.To("milvus"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								ResourcesPath: ptr.To(".spec.dependencies.etcd.inCluster.values.resources"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.dependencies.etcd.inCluster.values.replicaCount"),
						},
					},
					{
						Name:     "minio",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"},
						OwnerRef: ptr.To("milvus"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								ResourcesPath: ptr.To(".spec.dependencies.storage.inCluster.values.resources"),
							},
						},
					},
					{
						Name:     "pulsar-zookeeper",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"},
						OwnerRef: ptr.To("milvus"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								ResourcesPath: ptr.To(".spec.dependencies.pulsar.inCluster.values.zookeeper.resources"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.dependencies.pulsar.inCluster.values.zookeeper.replicaCount"),
						},
					},
					{
						Name:     "pulsar-bookkeeper",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"},
						OwnerRef: ptr.To("milvus"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								ResourcesPath: ptr.To(".spec.dependencies.pulsar.inCluster.values.bookkeeper.resources"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.dependencies.pulsar.inCluster.values.bookkeeper.replicaCount"),
						},
					},
					{
						Name:     "pulsar-broker",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"},
						OwnerRef: ptr.To("milvus"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								ResourcesPath: ptr.To(".spec.dependencies.pulsar.inCluster.values.broker.resources"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.dependencies.pulsar.inCluster.values.broker.replicaCount"),
						},
					},
					{
						Name:     "pulsar-proxy",
						Kind:     &v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"},
						OwnerRef: ptr.To("milvus"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
								ResourcesPath: ptr.To(".spec.dependencies.pulsar.inCluster.values.proxy.resources"),
							},
						},
						ScaleDefinition: &v1alpha1.ScaleDefinition{
							ReplicasPath: ptr.To(".spec.dependencies.pulsar.inCluster.values.proxy.replicaCount"),
						},
					},
				},
			},
			Instructions: v1alpha1.OptimizationInstructions{
				GangScheduling: &v1alpha1.GangSchedulingInstruction{
					PodGroups: []v1alpha1.PodGroupDefinition{{
						Name: "cluster",
						Members: []v1alpha1.PodGroupMemberDefinition{
							{
								ComponentName:   "querynode",
								GroupByKeyPaths: []string{`.metadata.labels["app.kubernetes.io/instance"]`},
							},
							{
								ComponentName:   "datanode",
								GroupByKeyPaths: []string{`.metadata.labels["app.kubernetes.io/instance"]`},
							},
							{
								ComponentName:   "streamingnode",
								GroupByKeyPaths: []string{`.metadata.labels["app.kubernetes.io/instance"]`},
							},
						},
					}},
				},
			},
		},
	}
}
