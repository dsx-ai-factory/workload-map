// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package instructions

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/resource"
)

var _ = Describe("Gang Scheduling", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("GetPodGroupingEffectiveComponent", func() {
		Context("with single leaf component", func() {
			It("should return correct gang scheduling info", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "simple-job",
								SpecDefinition: &v1alpha1.SpecDefinition{
									PodTemplateSpecPath: ptr.To(".spec.template"),
								},
							},
						},
						Instructions: v1alpha1.OptimizationInstructions{
							GangScheduling: &v1alpha1.GangSchedulingInstruction{
								PodGroups: []v1alpha1.PodGroupDefinition{
									{
										Name: "simple-group",
										Members: []v1alpha1.PodGroupMemberDefinition{
											{
												ComponentName: "simple-job",
											},
										},
									},
								},
							},
						},
					},
				}

				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
					},
				}
				podQuerier := resource.NewPodQuerier(pod)

				result, err := GetPodGroupingEffectiveComponent(ctx, podQuerier, "simple-job", summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.EffectiveComponent).To(Equal("simple-job"))
				Expect(result.PodGroupName).To(Equal("simple-group"))
				Expect(result.MemberDefinition).NotTo(BeNil())
				Expect(result.MemberDefinition.ComponentName).To(Equal("simple-job"))
			})
		})

		Context("with multiple leaf components and selectors", func() {
			It("should infer correct component based on pod labels", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "pytorch-job",
							},
							ChildComponents: []v1alpha1.ComponentDefinition{
								{
									Name:     "worker",
									OwnerRef: ptr.To("pytorch-job"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodTemplateSpecPath: ptr.To(".spec.worker.template"),
									},
									PodSelector: &v1alpha1.PodSelector{
										ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
											KeyPath: ".metadata.labels.component",
											Value:   ptr.To("worker"),
										},
									},
								},
								{
									Name:     "master",
									OwnerRef: ptr.To("pytorch-job"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodTemplateSpecPath: ptr.To(".spec.master.template"),
									},
									PodSelector: &v1alpha1.PodSelector{
										ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
											KeyPath: ".metadata.labels.component",
											Value:   ptr.To("master"),
										},
									},
								},
							},
						},
						Instructions: v1alpha1.OptimizationInstructions{
							GangScheduling: &v1alpha1.GangSchedulingInstruction{
								PodGroups: []v1alpha1.PodGroupDefinition{
									{
										Name: "pytorch-training",
										Members: []v1alpha1.PodGroupMemberDefinition{
											{ComponentName: "worker"},
											{ComponentName: "master"},
										},
									},
								},
							},
						},
					},
				}

				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				// Test worker pod
				workerPod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "worker-pod",
						Namespace: "default",
						Labels: map[string]string{
							"component": "worker",
						},
					},
				}
				workerQuerier := resource.NewPodQuerier(workerPod)

				result, err := GetPodGroupingEffectiveComponent(ctx, workerQuerier, "worker", summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.EffectiveComponent).To(Equal("worker"))
				Expect(result.PodGroupName).To(Equal("pytorch-training"))

				// Test master pod
				masterPod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "master-pod",
						Namespace: "default",
						Labels: map[string]string{
							"component": "master",
						},
					},
				}
				masterQuerier := resource.NewPodQuerier(masterPod)

				result, err = GetPodGroupingEffectiveComponent(ctx, masterQuerier, "master", summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.EffectiveComponent).To(Equal("master"))
				Expect(result.PodGroupName).To(Equal("pytorch-training"))
			})
		})

		Context("with filters on gang scheduling members", func() {
			It("should respect filters when selecting effective component", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "worker-set",
								SpecDefinition: &v1alpha1.SpecDefinition{
									PodTemplateSpecPath: ptr.To(".spec.template"),
								},
							},
						},
						Instructions: v1alpha1.OptimizationInstructions{
							GangScheduling: &v1alpha1.GangSchedulingInstruction{
								PodGroups: []v1alpha1.PodGroupDefinition{
									{
										Name: "gpu-group",
										Members: []v1alpha1.PodGroupMemberDefinition{
											{
												ComponentName: "worker-set",
												Filters: []string{
													`.metadata.labels.tier == "gpu"`,
												},
											},
										},
									},
									{
										Name: "cpu-group",
										Members: []v1alpha1.PodGroupMemberDefinition{
											{
												ComponentName: "worker-set",
												Filters: []string{
													`.metadata.labels.tier == "cpu"`,
												},
											},
										},
									},
								},
							},
						},
					},
				}

				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				// Test GPU pod
				gpuPod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "gpu-worker-pod",
						Namespace: "default",
						Labels: map[string]string{
							"tier": "gpu",
						},
					},
				}
				gpuQuerier := resource.NewPodQuerier(gpuPod)

				result, err := GetPodGroupingEffectiveComponent(ctx, gpuQuerier, "worker-set", summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.EffectiveComponent).To(Equal("worker-set"))
				Expect(result.PodGroupName).To(Equal("gpu-group"))

				// Test CPU pod
				cpuPod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "cpu-worker-pod",
						Namespace: "default",
						Labels: map[string]string{
							"tier": "cpu",
						},
					},
				}
				cpuQuerier := resource.NewPodQuerier(cpuPod)

				result, err = GetPodGroupingEffectiveComponent(ctx, cpuQuerier, "worker-set", summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.EffectiveComponent).To(Equal("worker-set"))
				Expect(result.PodGroupName).To(Equal("cpu-group"))
			})
		})

		Context("with parent component fallback", func() {
			It("should fallback to parent when direct component has no matching filters", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "pytorch-job",
							},
							ChildComponents: []v1alpha1.ComponentDefinition{
								{
									Name:     "worker",
									OwnerRef: ptr.To("pytorch-job"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodTemplateSpecPath: ptr.To(".spec.template"),
									},
									PodSelector: &v1alpha1.PodSelector{
										ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
											KeyPath: ".metadata.labels.component",
											Value:   ptr.To("worker"),
										},
									},
								},
							},
						},
						Instructions: v1alpha1.OptimizationInstructions{
							GangScheduling: &v1alpha1.GangSchedulingInstruction{
								PodGroups: []v1alpha1.PodGroupDefinition{
									{
										Name: "specific-group",
										Members: []v1alpha1.PodGroupMemberDefinition{
											{
												ComponentName: "worker",
												Filters: []string{
													`.metadata.labels.version == "v2"`, // This won't match
												},
											},
										},
									},
									{
										Name: "fallback-group",
										Members: []v1alpha1.PodGroupMemberDefinition{
											{
												ComponentName: "pytorch-job", // Parent fallback, no filters
											},
										},
									},
								},
							},
						},
					},
				}

				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				// Pod that matches worker selector but not the specific filter
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "worker-pod",
						Namespace: "default",
						Labels: map[string]string{
							"component": "worker",
							"version":   "v1", // Doesn't match v2 filter
						},
					},
				}
				podQuerier := resource.NewPodQuerier(pod)

				result, err := GetPodGroupingEffectiveComponent(ctx, podQuerier, "worker", summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.EffectiveComponent).To(Equal("pytorch-job")) // Fallback to parent
				Expect(result.PodGroupName).To(Equal("fallback-group"))
			})
		})

		Context("with no gang scheduling", func() {
			It("should return nil when no gang scheduling instructions exist", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "simple-job",
								SpecDefinition: &v1alpha1.SpecDefinition{
									PodTemplateSpecPath: ptr.To(".spec.template"),
								},
							},
						},
						// No Instructions field
					},
				}

				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
					},
				}
				podQuerier := resource.NewPodQuerier(pod)

				result, err := GetPodGroupingEffectiveComponent(ctx, podQuerier, "simple-job", summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeNil())
			})
		})

	})

	Describe("CalculateSubtreeScale", func() {
		Context("with single component scale", func() {
			It("should return component scale for leaf component", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "worker",
								SpecDefinition: &v1alpha1.SpecDefinition{
									PodTemplateSpecPath: ptr.To(".spec.template"),
								},
								ScaleDefinition: &v1alpha1.ScaleDefinition{
									ReplicasPath:    ptr.To(".spec.replicas"),
									MinReplicasPath: ptr.To(".spec.minReplicas"),
								},
							},
						},
					},
				}

				// Create a simple object with scale values
				obj := map[string]any{
					"spec": map[string]any{
						"replicas":    int32(5),
						"minReplicas": int32(3),
					},
				}

				factory := resource.NewComponentFactoryFromObject(karta, &unstructured.Unstructured{Object: obj})
				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				scale, err := CalculateSubtreeScale(ctx, "worker", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(scale).To(Equal(int32(3))) // Should prefer minReplicas
			})
		})

		Context("with parent-child hierarchy", func() {
			var (
				karta *v1alpha1.Karta
			)
			BeforeEach(func() {
				karta = &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name:           "pytorch-job",
								SpecDefinition: &v1alpha1.SpecDefinition{},
								ScaleDefinition: &v1alpha1.ScaleDefinition{
									ReplicasPath: ptr.To(".spec.replicas"),
								},
							},
							ChildComponents: []v1alpha1.ComponentDefinition{
								{
									Name:     "worker",
									OwnerRef: ptr.To("pytorch-job"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodTemplateSpecPath: ptr.To(".spec.worker.template"),
									},
									ScaleDefinition: &v1alpha1.ScaleDefinition{
										ReplicasPath: ptr.To(".spec.worker.replicas"),
									},
								},
								{
									Name:     "master",
									OwnerRef: ptr.To("pytorch-job"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodTemplateSpecPath: ptr.To(".spec.master.template"),
									},
									ScaleDefinition: &v1alpha1.ScaleDefinition{
										ReplicasPath: ptr.To(".spec.master.replicas"),
									},
								},
							},
						},
					},
				}
			})
			It("when parent has scale, should multiply parent scale by children sum", func() {
				obj := map[string]any{
					"spec": map[string]any{
						"replicas": int32(2), // Parent scale
						"worker": map[string]any{
							"replicas": int32(4),
						},
						"master": map[string]any{
							"replicas": int32(1),
						},
					},
				}

				factory := resource.NewComponentFactoryFromObject(karta, &unstructured.Unstructured{Object: obj})
				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				// Calculate root scale: parent(2) * (worker(4) + master(1)) = 2 * 5 = 10
				scale, err := CalculateSubtreeScale(ctx, "pytorch-job", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(scale).To(Equal(int32(10)))

				// Individual components should return their own scale
				workerScale, err := CalculateSubtreeScale(ctx, "worker", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(workerScale).To(Equal(int32(4)))

				masterScale, err := CalculateSubtreeScale(ctx, "master", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(masterScale).To(Equal(int32(1)))
			})
			It("when parent does not have scale, should only return children sum", func() {
				karta.Spec.StructureDefinition.RootComponent.ScaleDefinition = nil
				obj := map[string]any{
					"spec": map[string]any{
						"worker": map[string]any{
							"replicas": int32(4),
						},
						"master": map[string]any{
							"replicas": int32(1),
						},
					},
				}

				factory := resource.NewComponentFactoryFromObject(karta, &unstructured.Unstructured{Object: obj})
				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				// Calculate root scale: worker(4) + master(1) = 5
				scale, err := CalculateSubtreeScale(ctx, "pytorch-job", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(scale).To(Equal(int32(5)))

				// Individual components should return their own scale
				workerScale, err := CalculateSubtreeScale(ctx, "worker", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(workerScale).To(Equal(int32(4)))

				masterScale, err := CalculateSubtreeScale(ctx, "master", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(masterScale).To(Equal(int32(1)))
			})
		})

		Context("with array/map components", func() {
			It("should sum multiple scales from same component", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name:           "pytorch-job",
								SpecDefinition: &v1alpha1.SpecDefinition{},
								ScaleDefinition: &v1alpha1.ScaleDefinition{
									ReplicasPath: ptr.To(".spec.replicas"),
								},
							},
							ChildComponents: []v1alpha1.ComponentDefinition{
								{
									Name:           "worker-array",
									OwnerRef:       ptr.To("pytorch-job"),
									InstanceIdPath: ptr.To(".spec.workers[].name"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodTemplateSpecPath: ptr.To(".spec.workers[].template"),
									},
									ScaleDefinition: &v1alpha1.ScaleDefinition{
										ReplicasPath: ptr.To(".spec.workers[].replicas"),
									},
								},
							},
						},
					},
				}

				obj := map[string]any{
					"spec": map[string]any{
						"replicas": int32(2), // Parent scale
						"workers": []any{
							map[string]any{
								"name":     "worker-1",
								"replicas": int32(3),
							},
							map[string]any{
								"name":     "worker-2",
								"replicas": int32(2),
							},
						},
					},
				}

				factory := resource.NewComponentFactoryFromObject(karta, &unstructured.Unstructured{Object: obj})
				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				// Should sum all workers scale: 3 + 2 = 5
				scale, err := CalculateSubtreeScale(ctx, "worker-array", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(scale).To(Equal(int32(5)))

				// Calculate root scale: parent(2) * (worker(5)) = 2 * 5 = 10
				scale, err = CalculateSubtreeScale(ctx, "pytorch-job", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(scale).To(Equal(int32(10)))
			})
		})

		Context("with missing scale definitions", func() {
			It("should carry children sum when parent has no scale", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "cluster",
								// No scale definition for parent
							},
							ChildComponents: []v1alpha1.ComponentDefinition{
								{
									Name:     "worker",
									OwnerRef: ptr.To("cluster"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodTemplateSpecPath: ptr.To(".spec.template"),
									},
									ScaleDefinition: &v1alpha1.ScaleDefinition{
										ReplicasPath: ptr.To(".spec.replicas"),
									},
								},
							},
						},
					},
				}

				obj := map[string]any{
					"spec": map[string]any{
						"replicas": int32(4),
						"template": map[string]any{
							"spec": map[string]any{
								"containers": []any{
									map[string]any{
										"name":  "worker",
										"image": "pytorch:latest",
									},
								},
							},
						},
					},
				}

				factory := resource.NewComponentFactoryFromObject(karta, &unstructured.Unstructured{Object: obj})
				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				// Should carry children sum (4) since parent has no scale
				scale, err := CalculateSubtreeScale(ctx, "cluster", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(scale).To(Equal(int32(4)))
			})

			It("should use parent scale when children have no scale", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name:           "job-group",
								SpecDefinition: &v1alpha1.SpecDefinition{},
								ScaleDefinition: &v1alpha1.ScaleDefinition{
									ReplicasPath: ptr.To(".spec.size"),
								},
							},
							ChildComponents: []v1alpha1.ComponentDefinition{
								{
									Name:     "worker",
									OwnerRef: ptr.To("job-group"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodTemplateSpecPath: ptr.To(".spec.template"),
										// No scale definition
									},
								},
							},
						},
					},
				}

				obj := map[string]any{
					"spec": map[string]any{
						"size": int32(3),
						"template": map[string]any{
							"spec": map[string]any{
								"containers": []any{
									map[string]any{
										"name":  "worker",
										"image": "pytorch:latest",
									},
								},
							},
						},
					},
				}

				factory := resource.NewComponentFactoryFromObject(karta, &unstructured.Unstructured{Object: obj})
				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				// Should use parent scale (3) since children have no scale
				scale, err := CalculateSubtreeScale(ctx, "job-group", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(scale).To(Equal(int32(3)))
			})
		})

		Context("with getEffectiveMinReplicas edge cases", func() {
			It("should prefer MinReplicas over Replicas", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "worker",
								SpecDefinition: &v1alpha1.SpecDefinition{
									PodTemplateSpecPath: ptr.To(".spec.template"),
								},
								ScaleDefinition: &v1alpha1.ScaleDefinition{
									ReplicasPath:    ptr.To(".spec.replicas"),
									MinReplicasPath: ptr.To(".spec.minReplicas"),
								},
							},
						},
					},
				}

				obj := map[string]any{
					"spec": map[string]any{
						"replicas":    int32(10),
						"minReplicas": int32(2), // Should prefer this
					},
				}

				factory := resource.NewComponentFactoryFromObject(karta, &unstructured.Unstructured{Object: obj})
				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				scale, err := CalculateSubtreeScale(ctx, "worker", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(scale).To(Equal(int32(2))) // Should use minReplicas, not replicas
			})

			It("should fallback to Replicas when MinReplicas is zero", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "worker",
								SpecDefinition: &v1alpha1.SpecDefinition{
									PodTemplateSpecPath: ptr.To(".spec.template"),
								},
								ScaleDefinition: &v1alpha1.ScaleDefinition{
									ReplicasPath:    ptr.To(".spec.replicas"),
									MinReplicasPath: ptr.To(".spec.minReplicas"),
								},
							},
						},
					},
				}

				obj := map[string]any{
					"spec": map[string]any{
						"replicas":    int32(5),
						"minReplicas": int32(0), // Zero, should fallback to replicas
						"template": map[string]any{
							"spec": map[string]any{
								"containers": []any{
									map[string]any{
										"name":  "worker",
										"image": "pytorch:latest",
									},
								},
							},
						},
					},
				}

				factory := resource.NewComponentFactoryFromObject(karta, &unstructured.Unstructured{Object: obj})
				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				scale, err := CalculateSubtreeScale(ctx, "worker", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(scale).To(Equal(int32(5))) // Should fallback to replicas
			})

			It("should return 0 when both scales are missing", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "worker",
								SpecDefinition: &v1alpha1.SpecDefinition{
									PodTemplateSpecPath: ptr.To(".spec.template"),
								},
								ScaleDefinition: &v1alpha1.ScaleDefinition{
									ReplicasPath:    ptr.To(".spec.replicas"),
									MinReplicasPath: ptr.To(".spec.minReplicas"),
								},
							},
						},
					},
				}

				obj := map[string]any{
					"spec": map[string]any{
						// No replicas or minReplicas fields
						"template": map[string]any{
							"spec": map[string]any{
								"containers": []any{
									map[string]any{
										"name":  "worker",
										"image": "pytorch:latest",
									},
								},
							},
						},
					},
				}

				factory := resource.NewComponentFactoryFromObject(karta, &unstructured.Unstructured{Object: obj})
				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				scale, err := CalculateSubtreeScale(ctx, "worker", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(scale).To(Equal(int32(0))) // Should return 0 when no scale found
			})
		})

		Context("with fallback scale logic (no scale definitions)", func() {
			It("should return leaf component count when no scale definitions exist", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{Name: "pytorch-job"},
							ChildComponents: []v1alpha1.ComponentDefinition{
								{
									Name:     "worker",
									OwnerRef: ptr.To("pytorch-job"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodTemplateSpecPath: ptr.To(".spec.worker.template"),
									},
								},
								{
									Name:     "master",
									OwnerRef: ptr.To("pytorch-job"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodTemplateSpecPath: ptr.To(".spec.master.template"),
									},
								},
							},
						},
					},
				}

				obj := map[string]any{
					"metadata": map[string]any{"name": "test-job"},
				}

				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())
				Expect(summary.hasScaleDefinition).To(BeFalse())

				factory := resource.NewComponentFactoryFromObject(karta, &unstructured.Unstructured{Object: obj})

				// Root component should return total leaf count in its subtree (2)
				scale, err := CalculateSubtreeScale(ctx, "pytorch-job", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(scale).To(Equal(int32(2))) // 2 leaf components: worker, master

				// Leaf components should return 1 (themselves)
				scale, err = CalculateSubtreeScale(ctx, "worker", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(scale).To(Equal(int32(1))) // worker is a leaf

				scale, err = CalculateSubtreeScale(ctx, "master", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(scale).To(Equal(int32(1))) // master is a leaf
			})

			It("should return leaf count for complex hierarchy without scale definitions", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{Name: "cluster"},
							ChildComponents: []v1alpha1.ComponentDefinition{
								{
									Name:     "job-group",
									OwnerRef: ptr.To("cluster"),
								},
								{
									Name:     "worker",
									OwnerRef: ptr.To("job-group"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodTemplateSpecPath: ptr.To(".spec.master.template"),
									},
								},
								{
									Name:     "master",
									OwnerRef: ptr.To("job-group"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodTemplateSpecPath: ptr.To(".spec.master.template"),
									},
								},
								{
									Name:     "storage",
									OwnerRef: ptr.To("cluster"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodTemplateSpecPath: ptr.To(".spec.storage.template"),
									},
								},
							},
						},
					},
				}

				obj := map[string]any{
					"metadata": map[string]any{"name": "test-cluster"},
				}

				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())
				Expect(summary.hasScaleDefinition).To(BeFalse())
				Expect(summary.leafComponents).To(ContainElements("worker", "master", "storage"))

				factory := resource.NewComponentFactoryFromObject(karta, &unstructured.Unstructured{Object: obj})

				// Should return 3 (worker, master, storage are leaf components in cluster subtree)
				scale, err := CalculateSubtreeScale(ctx, "cluster", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(scale).To(Equal(int32(3)))

				// job-group subtree should have 2 leaves (worker, master)
				scale, err = CalculateSubtreeScale(ctx, "job-group", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(scale).To(Equal(int32(2)))

				// Individual leaf components should return 1
				scale, err = CalculateSubtreeScale(ctx, "worker", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(scale).To(Equal(int32(1)))

				scale, err = CalculateSubtreeScale(ctx, "storage", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(scale).To(Equal(int32(1)))
			})
		})

		Context("with instance IDs", func() {
			It("should calculate scale for specific instance using byScale method - array", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "job-group",
							},
							ChildComponents: []v1alpha1.ComponentDefinition{
								{
									Name:           "job",
									InstanceIdPath: ptr.To(".spec.replicatedJobs[].name"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodSpecPath: ptr.To(".spec.replicatedJobs[].spec"),
									},
									ScaleDefinition: &v1alpha1.ScaleDefinition{
										ReplicasPath:    ptr.To(".spec.replicatedJobs[].replicas"),
										MinReplicasPath: ptr.To(".spec.replicatedJobs[].minReplicas"),
									},
								},
							},
						},
					},
				}

				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				jobgroupObject := &unstructured.Unstructured{
					Object: map[string]any{
						"spec": map[string]any{
							"replicatedJobs": []any{
								map[string]any{
									"name":     "indexer",
									"replicas": 3,
									"spec": map[string]any{
										"containers": []any{
											map[string]any{"name": "indexer"},
										},
									},
								},
								map[string]any{
									"name":        "processor",
									"replicas":    3,
									"minReplicas": 2,
									"spec": map[string]any{
										"containers": []any{
											map[string]any{"name": "processor"},
										},
									},
								},
							},
						},
					},
				}

				factory := resource.NewComponentFactoryFromObject(karta, jobgroupObject)

				// Test scale for all instances (nil instanceId)
				allScale, err := CalculateSubtreeScale(ctx, "job", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(allScale).To(Equal(int32(5)))

				// Test scale for specific instance "indexer"
				indexerScale, err := CalculateSubtreeScale(ctx, "job", ptr.To("indexer"), factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(indexerScale).To(Equal(int32(3)))

				// Test scale for specific instance "processor"
				processorScale, err := CalculateSubtreeScale(ctx, "job", ptr.To("processor"), factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(processorScale).To(Equal(int32(2)))
			})

			It("should calculate scale for specific instance using byScale method - map", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "job-group",
							},
							ChildComponents: []v1alpha1.ComponentDefinition{
								{
									Name:           "job",
									InstanceIdPath: ptr.To(".spec.replicatedJobs | to_entries[] | .key"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodSpecPath: ptr.To(".spec.replicatedJobs | .[] | .spec"),
									},
									ScaleDefinition: &v1alpha1.ScaleDefinition{
										ReplicasPath:    ptr.To(".spec.replicatedJobs | .[] | .replicas"),
										MinReplicasPath: ptr.To(".spec.replicatedJobs | .[] | .minReplicas"),
									},
								},
							},
						},
					},
				}

				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				jobgroupObject := &unstructured.Unstructured{
					Object: map[string]any{
						"spec": map[string]any{
							"replicatedJobs": map[string]any{
								"indexer": map[string]any{
									"replicas": 3,
									"spec": map[string]any{
										"containers": []any{
											map[string]any{"name": "indexer"},
										},
									},
								},
								"processor": map[string]any{
									"replicas":    3,
									"minReplicas": 2,
									"spec": map[string]any{
										"containers": []any{
											map[string]any{"name": "processor"},
										},
									},
								},
							},
						},
					},
				}

				factory := resource.NewComponentFactoryFromObject(karta, jobgroupObject)

				// Test scale for all instances (nil instanceId)
				allScale, err := CalculateSubtreeScale(ctx, "job", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(allScale).To(Equal(int32(5)))

				// Test scale for specific instance "indexer"
				indexerScale, err := CalculateSubtreeScale(ctx, "job", ptr.To("indexer"), factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(indexerScale).To(Equal(int32(3)))

				// Test scale for specific instance "processor"
				processorScale, err := CalculateSubtreeScale(ctx, "job", ptr.To("processor"), factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(processorScale).To(Equal(int32(2)))
			})

			It("should calculate scale for specific instance using byLeaves method", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "job-group",
							},
							ChildComponents: []v1alpha1.ComponentDefinition{
								{
									Name:           "job",
									InstanceIdPath: ptr.To(".spec.replicatedJobs[].name"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodSpecPath: ptr.To(".spec.replicatedJobs[].spec"),
									},
									// No ScaleDefinition - will use byLeaves method
								},
							},
						},
					},
				}

				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				jobgroupObject := &unstructured.Unstructured{
					Object: map[string]any{
						"spec": map[string]any{
							"replicatedJobs": []any{
								map[string]any{
									"name": "indexer",
									"spec": map[string]any{
										"containers": []any{
											map[string]any{"name": "indexer"},
										},
									},
								},
								map[string]any{
									"name": "processor",
									"spec": map[string]any{
										"containers": []any{
											map[string]any{"name": "processor"},
										},
									},
								},
							},
						},
					},
				}

				factory := resource.NewComponentFactoryFromObject(karta, jobgroupObject)

				// Test scale for all instances (nil instanceId) - should count all instances
				allScale, err := CalculateSubtreeScale(ctx, "job", nil, factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(allScale).To(Equal(int32(2))) // 2 instances

				// Test scale for specific instance "indexer"
				indexerScale, err := CalculateSubtreeScale(ctx, "job", ptr.To("indexer"), factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(indexerScale).To(Equal(int32(1))) // Single instance

				// Test scale for specific instance "processor"
				processorScale, err := CalculateSubtreeScale(ctx, "job", ptr.To("processor"), factory, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(processorScale).To(Equal(int32(1))) // Single instance
			})

			It("should return error for non-existent instance ID", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "job-group",
							},
							ChildComponents: []v1alpha1.ComponentDefinition{
								{
									Name:           "job",
									InstanceIdPath: ptr.To(".spec.replicatedJobs[].name"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodSpecPath: ptr.To(".spec.replicatedJobs[].spec"),
									},
								},
							},
						},
					},
				}

				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				jobgroupObject := &unstructured.Unstructured{
					Object: map[string]any{
						"spec": map[string]any{
							"replicatedJobs": []any{
								map[string]any{
									"name": "indexer",
									"spec": map[string]any{
										"containers": []any{
											map[string]any{"name": "indexer"},
										},
									},
								},
							},
						},
					},
				}

				factory := resource.NewComponentFactoryFromObject(karta, jobgroupObject)

				// Test scale for non-existent instance
				nonExistentId := "non-existent"
				scale, err := CalculateSubtreeScale(ctx, "job", &nonExistentId, factory, summary)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("instance id non-existent not found"))
				Expect(scale).To(Equal(int32(0)))
			})

			It("should return error for empty instance ID", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{Name: "job-group"},
						},
					},
				}

				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				scale, err := CalculateSubtreeScale(ctx, "job", ptr.To(""), nil, summary)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("instance id is empty"))
				Expect(scale).To(Equal(int32(0)))
			})
		})
	})
})
