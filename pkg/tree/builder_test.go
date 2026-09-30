// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package tree

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/resource"
	"github.com/dsx-ai-factory/workload-map/test/types"
)

var _ = Describe("Build", func() {
	ctx := context.Background()

	Describe("flat two-component workload (PyFlow)", func() {
		var (
			karta   *v1alpha1.Karta
			factory *resource.ComponentFactory
		)

		BeforeEach(func() {
			karta = types.PyFlowKarta()
			factory = resource.NewComponentFactoryFromObject(karta, types.NewPyFlowObject())
		})

		It("extracts root status into Phases", func() {
			tree, err := Build(ctx, factory)
			Expect(err).NotTo(HaveOccurred())
			Expect(tree.Status.Phases).To(ConsistOf("Running"))
		})

		It("produces one ComponentNode per child component", func() {
			tree, err := Build(ctx, factory)
			Expect(err).NotTo(HaveOccurred())
			Expect(componentNames(tree.Children)).To(ConsistOf("master", "worker"))
		})

		It("produces a single instance for the master component", func() {
			tree, err := Build(ctx, factory)
			Expect(err).NotTo(HaveOccurred())
			master := findComponent(tree.Children, "master")
			Expect(master).NotTo(BeNil())
			Expect(master.Instances).To(HaveLen(1))
			Expect(master.Instances[0].InstanceKey).To(BeNil())
		})

		It("populates Scale from the extracted instance", func() {
			tree, err := Build(ctx, factory)
			Expect(err).NotTo(HaveOccurred())
			worker := findComponent(tree.Children, "worker")
			Expect(worker.Instances[0].Scale).NotTo(BeNil())
			Expect(*worker.Instances[0].Scale.MinReplicas).To(Equal(int32(1)))
			Expect(*worker.Instances[0].Scale.MaxReplicas).To(Equal(int32(5)))
		})

		It("preserves an inverted scale where the worker's max is lower than its min", func() {
			obj := types.NewPyFlowObject()
			obj.Spec.Worker.MinReplicas = ptr.To(int32(5))
			obj.Spec.Worker.MaxReplicas = ptr.To(int32(2))
			tree, err := Build(ctx, resource.NewComponentFactoryFromObject(karta, obj))
			Expect(err).NotTo(HaveOccurred())
			worker := findComponent(tree.Children, "worker")
			Expect(worker).NotTo(BeNil())
			Expect(*worker.Instances[0].Scale.MinReplicas).To(Equal(int32(5)))
			Expect(*worker.Instances[0].Scale.MaxReplicas).To(Equal(int32(2)))
		})
	})

	Describe("multi-instance component", func() {
		It("creates one InstanceNode per spec-defined instance", func() {
			// Reactor (Dynamo-like) enumerates its services from the spec via
			// InstanceIdPath: api, worker, cache.
			karta := types.ReactorKarta()
			factory := resource.NewComponentFactoryFromObject(karta, types.NewReactorObject())
			tree, err := Build(ctx, factory)
			Expect(err).NotTo(HaveOccurred())
			svc := findComponent(tree.Children, "service")
			Expect(svc).NotTo(BeNil())
			Expect(svc.Instances).To(HaveLen(3))
			Expect(findInstance(svc.Instances, "api")).NotTo(BeNil())
			Expect(findInstance(svc.Instances, "worker")).NotTo(BeNil())
			Expect(findInstance(svc.Instances, "cache")).NotTo(BeNil())
		})
	})

	Describe("replica-selector component", func() {
		It("collapses to a single instance with no replica key", func() {
			// The group component defines a ReplicaSelector, but the builder works
			// from the spec only: it does not enumerate replica groups. The replica
			// dimension is left to live consumers that observe pods.
			karta := replicaGroupKarta()
			obj := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "example.com/v1",
				"kind":       "LeaderWorkerSet",
				"metadata":   map[string]any{"name": "lws-sample"},
				"spec":       map[string]any{"replicas": int64(3)},
			}}
			factory := resource.NewComponentFactoryFromObject(karta, obj)
			tree, err := Build(ctx, factory)
			Expect(err).NotTo(HaveOccurred())
			group := findComponent(tree.Children, "group")
			Expect(group).NotTo(BeNil())
			Expect(group.Instances).To(HaveLen(1))
			Expect(group.Instances[0].ReplicaKey).To(BeNil())
		})
	})

	Describe("multi-instance component with per-instance replicas (JobGroup)", func() {
		var factory *resource.ComponentFactory

		BeforeEach(func() {
			factory = resource.NewComponentFactoryFromObject(types.JobGroupKarta(), types.NewJobGroupObject())
		})

		It("creates one InstanceNode per replicated job", func() {
			tree, err := Build(ctx, factory)
			Expect(err).NotTo(HaveOccurred())
			job := findComponent(tree.Children, "job")
			Expect(job).NotTo(BeNil())
			Expect(instanceKeys(job.Instances)).To(ConsistOf("indexer", "processor"))
		})

		It("populates per-instance Scale from each job's replicas", func() {
			tree, err := Build(ctx, factory)
			Expect(err).NotTo(HaveOccurred())
			job := findComponent(tree.Children, "job")
			indexer := findInstance(job.Instances, "indexer")
			processor := findInstance(job.Instances, "processor")
			Expect(*indexer.Scale.Replicas).To(Equal(int32(2)))
			Expect(*processor.Scale.Replicas).To(Equal(int32(3)))
		})

		It("extracts the Running phase from conditions", func() {
			tree, err := Build(ctx, factory)
			Expect(err).NotTo(HaveOccurred())
			Expect(tree.Status.Phases).To(ConsistOf("Running"))
		})
	})

	Describe("multi-StatefulSet workload (Milvus)", func() {
		var factory *resource.ComponentFactory

		BeforeEach(func() {
			factory = resource.NewComponentFactoryFromObject(types.MilvusKarta(), types.NewMilvusObject())
		})

		It("extracts root status into Phases", func() {
			tree, err := Build(ctx, factory)
			Expect(err).NotTo(HaveOccurred())
			Expect(tree.Status.Phases).To(ConsistOf("Running"))
		})

		It("produces one single-instance ComponentNode per StatefulSet child", func() {
			tree, err := Build(ctx, factory)
			Expect(err).NotTo(HaveOccurred())
			Expect(componentNames(tree.Children)).To(ConsistOf("querynode", "datanode", "proxy"))
			for _, node := range tree.Children {
				Expect(node.Instances).To(HaveLen(1))
				Expect(node.Instances[0].InstanceKey).To(BeNil())
				Expect(node.Instances[0].ExtractedInstance).NotTo(BeNil())
			}
		})

		It("populates per-component Scale from each StatefulSet's replicas", func() {
			tree, err := Build(ctx, factory)
			Expect(err).NotTo(HaveOccurred())
			querynode := findComponent(tree.Children, "querynode")
			datanode := findComponent(tree.Children, "datanode")
			Expect(*querynode.Instances[0].Scale.Replicas).To(Equal(int32(2)))
			Expect(*datanode.Instances[0].Scale.Replicas).To(Equal(int32(3)))
		})

		It("leaves Scale nil for a pod-bearing child that has no scale in the spec", func() {
			tree, err := Build(ctx, factory)
			Expect(err).NotTo(HaveOccurred())
			proxy := findComponent(tree.Children, "proxy")
			Expect(proxy).NotTo(BeNil())
			// No ScaleDefinition, so no scale is extracted.
			Expect(proxy.Instances[0].Scale).To(BeNil())
		})
	})

	Describe("desired structure without pods", func() {
		It("produces ComponentNodes with populated ExtractedInstance", func() {
			karta := types.PyFlowKarta()
			factory := resource.NewComponentFactoryFromObject(karta, types.NewPyFlowObject())
			tree, err := Build(ctx, factory)
			Expect(err).NotTo(HaveOccurred())
			Expect(tree.Children).To(HaveLen(2))
			for _, node := range tree.Children {
				Expect(node.Instances).NotTo(BeEmpty())
				Expect(node.Instances[0].ExtractedInstance).NotTo(BeNil())
			}
		})
	})

	Describe("invalid karta", func() {
		It("returns an error instead of building a tree", func() {
			karta := types.PyFlowKarta()
			karta.Spec.StructureDefinition.RootComponent.Kind = nil
			factory := resource.NewComponentFactoryFromObject(karta, types.NewPyFlowObject())
			tree, err := Build(ctx, factory)
			Expect(err).To(HaveOccurred())
			Expect(tree).To(BeNil())
		})
	})

	Describe("pod-bearing root without children (bare Job)", func() {
		It("builds a tree with no children", func() {
			obj := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "batch/v1",
				"kind":       "Job",
				"metadata":   map[string]any{"name": "pi"},
				"spec": map[string]any{
					"parallelism": int64(5),
					"template": map[string]any{
						"spec": map[string]any{
							"containers": []any{map[string]any{"name": "pi", "image": "perl:5.34"}},
						},
					},
				},
			}}
			factory := resource.NewComponentFactoryFromObject(bareJobKarta(), obj)
			tree, err := Build(ctx, factory)
			Expect(err).NotTo(HaveOccurred())
			Expect(tree.Children).To(BeEmpty())
		})
	})
})

// bareJobKarta returns a single-component Karta whose pod-bearing root has no children.
func bareJobKarta() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "job",
					Kind: &v1alpha1.GroupVersionKind{
						Group:   "batch",
						Version: "v1",
						Kind:    "Job",
					},
					StatusDefinition: &v1alpha1.StatusDefinition{
						StatusMappings: v1alpha1.StatusMappings{},
					},
					SpecDefinition: &v1alpha1.SpecDefinition{
						PodTemplateSpecPath: ptr.To(".spec.template"),
					},
					ScaleDefinition: &v1alpha1.ScaleDefinition{
						ReplicasPath: ptr.To(".spec.parallelism // 1"),
					},
				},
			},
		},
	}
}

// replicaGroupKarta returns a minimal two-level Karta (lws -> group) whose group
// component defines a ReplicaSelector, used to verify the builder collapses such a
// component to a single instance rather than enumerating replica groups.
func replicaGroupKarta() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "lws",
					Kind: &v1alpha1.GroupVersionKind{
						Group:   "example.com",
						Version: "v1",
						Kind:    "LeaderWorkerSet",
					},
					StatusDefinition: &v1alpha1.StatusDefinition{
						StatusMappings: v1alpha1.StatusMappings{},
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:     "group",
						OwnerRef: ptr.To("lws"),
						PodSelector: &v1alpha1.PodSelector{
							ReplicaSelector: &v1alpha1.ReplicaSelector{
								KeyPath: ".metadata.labels.group",
							},
						},
					},
				},
			},
		},
	}
}

var _ = Describe("cloneComponentNodes", func() {
	It("copies nodes so per-instance mutations do not alias siblings", func() {
		base := []ComponentNode{
			{
				Name: "leader",
				Instances: []InstanceNode{
					{InstanceKey: ptr.To("original")},
				},
			},
		}

		clone := cloneComponentNodes(base)

		// Mutating the clone must not affect the original (independent backing arrays).
		clone[0].Name = "mutated"
		clone[0].Instances[0].InstanceKey = ptr.To("mutated")
		Expect(base[0].Name).To(Equal("leader"))
		Expect(base[0].Instances[0].InstanceKey).To(Equal(ptr.To("original")))
	})

	It("returns nil for nil input", func() {
		Expect(cloneComponentNodes(nil)).To(BeNil())
	})
})

func componentNames(nodes []ComponentNode) []string {
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	return names
}

func findComponent(nodes []ComponentNode, name string) *ComponentNode {
	for i := range nodes {
		if nodes[i].Name == name {
			return &nodes[i]
		}
	}
	return nil
}

func instanceKeys(instances []InstanceNode) []string {
	keys := make([]string, 0, len(instances))
	for i := range instances {
		if instances[i].InstanceKey != nil {
			keys = append(keys, *instances[i].InstanceKey)
		}
	}
	return keys
}

func findInstance(instances []InstanceNode, key string) *InstanceNode {
	for i := range instances {
		if instances[i].InstanceKey != nil && *instances[i].InstanceKey == key {
			return &instances[i]
		}
	}
	return nil
}
