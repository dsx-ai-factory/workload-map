// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package instructions

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/resource"
)

var _ = Describe("Pod Utils", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("InferPodComponent", func() {
		Context("with single leaf component", func() {
			It("should infer the only leaf component", func() {
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

				componentName, err := InferPodComponent(ctx, podQuerier, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(componentName).To(Equal("simple-job"))
			})

			It("should ignore non-matching selector", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "simple-job",
								SpecDefinition: &v1alpha1.SpecDefinition{
									PodTemplateSpecPath: ptr.To(".spec.template"),
								},
								PodSelector: &v1alpha1.PodSelector{
									ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
										KeyPath: ".non-existing",
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

				componentName, err := InferPodComponent(ctx, podQuerier, summary)
				Expect(err).NotTo(HaveOccurred())
				Expect(componentName).To(Equal("simple-job"))
			})
		})

		Context("with multiple leaf components", func() {
			Context("should infer component based on pod selector match", func() {
				var (
					summary *StructureSummary
				)

				BeforeEach(func() {
					karta := &v1alpha1.Karta{
						Spec: v1alpha1.KartaSpec{
							StructureDefinition: v1alpha1.StructureDefinition{
								RootComponent: v1alpha1.ComponentDefinition{
									Name: "pytorch-job",
								},
								ChildComponents: []v1alpha1.ComponentDefinition{
									{
										Name: "worker",
										SpecDefinition: &v1alpha1.SpecDefinition{
											PodTemplateSpecPath: ptr.To(".spec.replicaSpecs.Worker.template"),
										},
										PodSelector: &v1alpha1.PodSelector{
											ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
												KeyPath: ".metadata.labels.component",
												Value:   ptr.To("worker"),
											},
										},
									},
									{
										Name: "master",
										SpecDefinition: &v1alpha1.SpecDefinition{
											PodTemplateSpecPath: ptr.To(".spec.replicaSpecs.Master.template"),
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
						},
					}

					var err error
					summary, err = NewStructureSummary(karta)
					Expect(err).NotTo(HaveOccurred())
				})

				It("worker pod", func() {
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

					componentName, err := InferPodComponent(ctx, workerQuerier, summary)
					Expect(err).NotTo(HaveOccurred())
					Expect(componentName).To(Equal("worker"))
				})

				It("master pod", func() {
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

					componentName, err := InferPodComponent(ctx, masterQuerier, summary)
					Expect(err).NotTo(HaveOccurred())
					Expect(componentName).To(Equal("master"))
				})
			})

			It("should return error when pod doesn't match any component", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "pytorch-job",
							},
							ChildComponents: []v1alpha1.ComponentDefinition{
								{
									Name: "worker",
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodTemplateSpecPath: ptr.To(".spec.replicaSpecs.Worker.template"),
									},
									PodSelector: &v1alpha1.PodSelector{
										ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
											KeyPath: ".metadata.labels.component",
											Value:   ptr.To("worker"),
										},
									},
								},
								{
									Name: "master",
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodTemplateSpecPath: ptr.To(".spec.replicaSpecs.Master.template"),
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
					},
				}

				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				// Pod with no labels - won't match any component
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "unmatched-pod",
						Namespace: "default",
					},
				}
				podQuerier := resource.NewPodQuerier(pod)

				componentName, err := InferPodComponent(ctx, podQuerier, summary)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("no component found for pod"))
				Expect(componentName).To(Equal(""))
			})
		})
	})

	Describe("InferPodComponentInstance", func() {
		var (
			ctrl         *gomock.Controller
			mockAccessor *resource.MockComponentAccessor
			factory      *resource.ComponentFactory
			podQuerier   *resource.PodQuerier
		)

		BeforeEach(func() {
			ctrl = gomock.NewController(GinkgoT())
			mockAccessor = resource.NewMockComponentAccessor(ctrl)

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			}
			podQuerier = resource.NewPodQuerier(pod)
		})

		AfterEach(func() {
			ctrl.Finish()
		})

		Context("when component doesn't exist", func() {
			It("should return error", func() {
				// Create minimal Karta with no components
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "root",
							},
						},
					},
				}
				factory = resource.NewComponentFactory(karta, mockAccessor)

				instancePtr, err := InferPodComponentInstance(ctx, podQuerier, "non-existent", factory)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("component non-existent not found"))
				Expect(instancePtr).To(BeNil())
			})
		})

		Context("when component has no instance IDs", func() {
			It("should return nil", func() {
				// Create Karta with single component, no instance path
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "simple-job",
								PodSelector: &v1alpha1.PodSelector{
									ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
										KeyPath: ".metadata.labels.app",
										Value:   ptr.To("simple-job"),
									},
								},
							},
						},
					},
				}
				factory = resource.NewComponentFactory(karta, mockAccessor)

				// Mock GetInstanceIds to return definition not found error (no instanceIdPath)
				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, karta.Spec.StructureDefinition.RootComponent).
					Return(nil, resource.DefinitionNotFoundError("no instanceIdPath"))

				instancePtr, err := InferPodComponentInstance(ctx, podQuerier, "simple-job", factory)
				Expect(err).NotTo(HaveOccurred())
				Expect(instancePtr).To(BeNil())
			})
		})

		Context("when component has instance IDs", func() {
			var (
				karta *v1alpha1.Karta
			)

			BeforeEach(func() {
				// Karta with component that has instance IDs
				karta = &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name:           "worker",
								InstanceIdPath: ptr.To(".spec.groups[].name"),
								PodSelector: &v1alpha1.PodSelector{
									ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
										KeyPath: ".metadata.labels.component",
										Value:   ptr.To("worker"),
									},
									ComponentInstanceSelector: &v1alpha1.ComponentInstanceSelector{
										IdPath: ".metadata.labels.group",
									},
								},
							},
						},
					},
				}
			})

			It("should return matching instance ID", func() {
				factory = resource.NewComponentFactory(karta, mockAccessor)

				// Mock GetInstanceIds to return multiple instance IDs
				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, karta.Spec.StructureDefinition.RootComponent).
					Return([]string{"gpu-workers", "cpu-workers"}, nil)

				// Create pod with matching label
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						Labels: map[string]string{
							"group": "gpu-workers",
						},
					},
				}
				podQuerier := resource.NewPodQuerier(pod)

				instancePtr, err := InferPodComponentInstance(ctx, podQuerier, "worker", factory)
				Expect(err).NotTo(HaveOccurred())
				Expect(instancePtr).NotTo(BeNil())
				Expect(*instancePtr).To(Equal("gpu-workers"))
			})

			It("should return nil when matching returns empty string", func() {
				factory = resource.NewComponentFactory(karta, mockAccessor)

				// Mock GetInstanceIds to return single empty instance ID (single instance case)
				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, karta.Spec.StructureDefinition.RootComponent).
					Return([]string{""}, nil)

				instancePtr, err := InferPodComponentInstance(ctx, podQuerier, "worker", factory)
				Expect(err).NotTo(HaveOccurred())
				Expect(instancePtr).To(BeNil())
			})

			It("should return error when GetMatchingInstanceId fails", func() {
				// Create Karta with component that has instance IDs
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name:           "worker",
								InstanceIdPath: ptr.To(".spec.groups[].name"),
								PodSelector: &v1alpha1.PodSelector{
									ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
										KeyPath: ".metadata.labels.component",
										Value:   ptr.To("worker"),
									},
									ComponentInstanceSelector: &v1alpha1.ComponentInstanceSelector{
										IdPath: ".metadata.labels.group",
									},
								},
							},
						},
					},
				}
				factory = resource.NewComponentFactory(karta, mockAccessor)

				// Mock GetInstanceIds to return multiple instance IDs
				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, karta.Spec.StructureDefinition.RootComponent).
					Return([]string{"gpu-workers", "cpu-workers"}, nil)

				// Create pod with non-matching label
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						Labels: map[string]string{
							"group": "invalid-group",
						},
					},
				}
				podQuerier := resource.NewPodQuerier(pod)

				instancePtr, err := InferPodComponentInstance(ctx, podQuerier, "worker", factory)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("could not match instance id"))
				Expect(instancePtr).To(BeNil())
			})

			It("should return error when GetInstanceIds fails", func() {
				// Create Karta with component that has instance IDs
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name:           "worker",
								InstanceIdPath: ptr.To(".spec.groups[].name"),
							},
						},
					},
				}
				factory = resource.NewComponentFactory(karta, mockAccessor)

				// Mock GetInstanceIds to return error
				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, karta.Spec.StructureDefinition.RootComponent).
					Return(nil, errors.New("extraction failed"))

				instancePtr, err := InferPodComponentInstance(ctx, podQuerier, "worker", factory)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("extraction failed"))
				Expect(instancePtr).To(BeNil())
			})
		})

		Context("when component has instance IDs but no instance selector", func() {
			It("should return error when no instance selector but has instance IDs", func() {
				// Create Karta with component that has instance IDs but no instance selector
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name:           "worker",
								InstanceIdPath: ptr.To(".spec.groups[].name"),
								PodSelector: &v1alpha1.PodSelector{
									ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
										KeyPath: ".metadata.labels.component",
										Value:   ptr.To("worker"),
									},
									// No ComponentInstanceSelector
								},
							},
						},
					},
				}
				factory = resource.NewComponentFactory(karta, mockAccessor)

				// Mock GetInstanceIds to return instance IDs
				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, karta.Spec.StructureDefinition.RootComponent).
					Return([]string{"worker1", "worker2"}, nil)

				instancePtr, err := InferPodComponentInstance(ctx, podQuerier, "worker", factory)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("no instance selector provided but instance ids are not empty"))
				Expect(instancePtr).To(BeNil())
			})
		})

		Context("when component has no pod selector", func() {
			It("should return nil", func() {
				// Create Karta with component that has instance IDs but no pod selector
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name:           "worker",
								InstanceIdPath: ptr.To(".spec.groups[].name"),
								// No PodSelector
							},
						},
					},
				}
				factory = resource.NewComponentFactory(karta, mockAccessor)

				// Mock GetInstanceIds to return instance IDs
				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, karta.Spec.StructureDefinition.RootComponent).
					Return([]string{"worker1", "worker2"}, nil)

				instancePtr, err := InferPodComponentInstance(ctx, podQuerier, "worker", factory)
				Expect(err).NotTo(HaveOccurred())
				Expect(instancePtr).To(BeNil())
			})
		})
	})
})
