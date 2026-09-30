// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package resource

import (
	"context"
	"errors"

	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

var _ = Describe("Component", func() {
	var (
		ctrl         *gomock.Controller
		mockAccessor *MockComponentAccessor
		ctx          context.Context
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockAccessor = NewMockComponentAccessor(ctrl)
		ctx = context.Background()
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	Context("Basic Properties", func() {
		var component *Component

		BeforeEach(func() {
			definition := v1alpha1.ComponentDefinition{
				Name: "test-component",
				Kind: &v1alpha1.GroupVersionKind{
					Group:   "apps",
					Version: "v1",
					Kind:    "Deployment",
				},
				PodSelector: &v1alpha1.PodSelector{
					ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{
						KeyPath: ".metadata.labels.role",
						Value:   stringPtr("worker"),
					},
				},
			}

			component = &Component{
				name:       "test-component",
				definition: definition,
				accessor:   mockAccessor,
			}
		})

		It("should return component name", func() {
			Expect(component.Name()).To(Equal("test-component"))
		})

		It("should return component definition", func() {
			Expect(component.Definition()).To(Equal(component.definition))
		})

		It("should return GroupVersionKind when defined", func() {
			gvk := component.Kind()
			Expect(gvk).NotTo(BeNil())
			Expect(gvk.Group).To(Equal("apps"))
			Expect(gvk.Version).To(Equal("v1"))
			Expect(gvk.Kind).To(Equal("Deployment"))
		})

		It("should return nil Kind when not defined", func() {
			componentWithoutKind := &Component{
				definition: v1alpha1.ComponentDefinition{Name: "no-kind"},
			}
			Expect(componentWithoutKind.Kind()).To(BeNil())
		})

		It("should return pod selector", func() {
			selector := component.GetPodSelector()
			Expect(selector).NotTo(BeNil())
			Expect(selector.ComponentTypeSelector.KeyPath).To(Equal(".metadata.labels.role"))
			Expect(*selector.ComponentTypeSelector.Value).To(Equal("worker"))
		})

		It("should return nil pod selector when not defined", func() {
			componentWithoutSelector := &Component{
				definition: v1alpha1.ComponentDefinition{Name: "no-selector"},
			}
			Expect(componentWithoutSelector.GetPodSelector()).To(BeNil())
		})
	})

	Context("Pod Definition Detection", func() {
		It("should detect pod definition with PodTemplateSpecPath", func() {
			component := &Component{
				definition: v1alpha1.ComponentDefinition{
					SpecDefinition: &v1alpha1.SpecDefinition{
						PodTemplateSpecPath: stringPtr(".spec.template"),
					},
				},
			}
			Expect(component.HasPodDefinition()).To(BeTrue())
		})

		It("should detect pod definition with PodSpecPath", func() {
			component := &Component{
				definition: v1alpha1.ComponentDefinition{
					SpecDefinition: &v1alpha1.SpecDefinition{
						PodSpecPath: stringPtr(".spec.podSpec"),
					},
				},
			}
			Expect(component.HasPodDefinition()).To(BeTrue())
		})

		It("should detect pod definition with FragmentedPodSpecDefinition", func() {
			component := &Component{
				definition: v1alpha1.ComponentDefinition{
					SpecDefinition: &v1alpha1.SpecDefinition{
						FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{},
					},
				},
			}
			Expect(component.HasPodDefinition()).To(BeTrue())
		})

		It("should return false when no pod definition exists", func() {
			component := &Component{
				definition: v1alpha1.ComponentDefinition{
					SpecDefinition: &v1alpha1.SpecDefinition{
						// No pod-related paths
					},
				},
			}
			Expect(component.HasPodDefinition()).To(BeFalse())
		})

		It("should return false when SpecDefinition is nil", func() {
			component := &Component{
				definition: v1alpha1.ComponentDefinition{
					// No SpecDefinition
				},
			}
			Expect(component.HasPodDefinition()).To(BeFalse())
		})
	})

	Context("Instance ID Logic", func() {
		It("should return true for HasInstanceIdDefinition when InstanceIdPath is defined", func() {
			component := &Component{
				definition: v1alpha1.ComponentDefinition{
					InstanceIdPath: stringPtr(".spec.jobs[].name"),
				},
			}
			Expect(component.HasInstanceIdDefinition()).To(BeTrue())
		})

		It("should return false for HasInstanceIdDefinition when InstanceIdPath is not defined", func() {
			component := &Component{
				definition: v1alpha1.ComponentDefinition{
					// No InstanceIdPath
				},
			}
			Expect(component.HasInstanceIdDefinition()).To(BeFalse())
		})

		Context("GetInstanceIds", func() {
			var component *Component

			BeforeEach(func() {
				component = &Component{
					name:       "test-component",
					definition: v1alpha1.ComponentDefinition{Name: "test-component"},
					accessor:   mockAccessor,
				}
			})

			It("should return extracted instance IDs for multi-instance component", func() {
				expectedIds := []string{"job-1", "job-2", "job-3"}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(expectedIds, nil).
					Times(1)

				result, err := component.GetInstanceIds(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(expectedIds))
			})

			It("should return empty string for single-instance component", func() {
				expectedError := DefinitionNotFoundError("no instance id path defined")

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(nil, expectedError).
					Times(1)

				result, err := component.GetInstanceIds(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal([]string{""}))
			})

			It("should propagate non-DefinitionNotFoundError errors", func() {
				expectedError := errors.New("extraction failed")

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(nil, expectedError).
					Times(1)

				result, err := component.GetInstanceIds(ctx)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to extract instance ids"))
				Expect(err.Error()).To(ContainSubstring(expectedError.Error()))
				Expect(result).To(BeNil())
			})
		})
	})

	Context("Extraction Methods", func() {
		var component *Component

		BeforeEach(func() {
			component = &Component{
				name:       "test-component",
				definition: v1alpha1.ComponentDefinition{Name: "test-component"},
				accessor:   mockAccessor,
			}
		})

		Context("GetPodTemplateSpec", func() {
			It("should zip instance IDs with pod template specs for multi-instance", func() {
				instanceIds := []string{"job-1", "job-2"}
				templates := []corev1.PodTemplateSpec{
					{ObjectMeta: metav1.ObjectMeta{Name: "template-1"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "template-2"}},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				mockAccessor.EXPECT().
					ExtractPodTemplateSpec(ctx, component.definition).
					Return(templates, nil)

				result, err := component.GetPodTemplateSpec(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(2))
				Expect(result["job-1"]).To(Equal(templates[0]))
				Expect(result["job-2"]).To(Equal(templates[1]))
			})

			It("should handle single instance with empty string ID", func() {
				instanceIds := []string{""}
				templates := []corev1.PodTemplateSpec{
					{ObjectMeta: metav1.ObjectMeta{Name: "single-template"}},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				mockAccessor.EXPECT().
					ExtractPodTemplateSpec(ctx, component.definition).
					Return(templates, nil)

				result, err := component.GetPodTemplateSpec(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(1))
				Expect(result[""]).To(Equal(templates[0]))
			})

			It("should propagate instance ID extraction errors", func() {
				templates := []corev1.PodTemplateSpec{
					{ObjectMeta: metav1.ObjectMeta{Name: "template-1"}},
				}
				expectedError := errors.New("instance extraction failed")

				mockAccessor.EXPECT().
					ExtractPodTemplateSpec(ctx, component.definition).
					Return(templates, nil)

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(nil, expectedError)

				result, err := component.GetPodTemplateSpec(ctx)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to get instance ids"))
				Expect(err.Error()).To(ContainSubstring(expectedError.Error()))
				Expect(result).To(BeNil())
			})

			It("should propagate template extraction errors", func() {
				expectedError := errors.New("template extraction failed")

				mockAccessor.EXPECT().
					ExtractPodTemplateSpec(ctx, component.definition).
					Return(nil, expectedError)

				result, err := component.GetPodTemplateSpec(ctx)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to extract pod template specs"))
				Expect(err.Error()).To(ContainSubstring(expectedError.Error()))
				Expect(result).To(BeNil())
			})

			It("should return nil when definition not found", func() {
				expectedError := DefinitionNotFoundError("component test-component does not have pod template spec definition")

				mockAccessor.EXPECT().
					ExtractPodTemplateSpec(ctx, component.definition).
					Return(nil, expectedError)

				result, err := component.GetPodTemplateSpec(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeNil())
			})

			It("should return error when counts mismatch", func() {
				instanceIds := []string{"job-1", "job-2"}
				templates := []corev1.PodTemplateSpec{
					{ObjectMeta: metav1.ObjectMeta{Name: "only-one"}},
				}

				mockAccessor.EXPECT().
					ExtractPodTemplateSpec(ctx, component.definition).
					Return(templates, nil)

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				result, err := component.GetPodTemplateSpec(ctx)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("instance ids count (2) does not match results count (1)"))
				Expect(result).To(BeNil())
			})
		})

		Context("GetScale", func() {
			It("should zip instance IDs with scales", func() {
				instanceIds := []string{"worker-1", "worker-2"}
				scales := []Scale{
					{Replicas: int32Ptr(3)},
					{Replicas: int32Ptr(5)},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				mockAccessor.EXPECT().
					ExtractScale(ctx, component.definition).
					Return(scales, nil)

				result, err := component.GetScale(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(2))
				Expect(result["worker-1"]).To(Equal(scales[0]))
				Expect(result["worker-2"]).To(Equal(scales[1]))
			})
		})

		Context("GetPodSpec", func() {
			It("should zip instance IDs with pod specs", func() {
				instanceIds := []string{"spec-1"}
				specs := []corev1.PodSpec{
					{Containers: []corev1.Container{{Name: "test-container"}}},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				mockAccessor.EXPECT().
					ExtractPodSpec(ctx, component.definition).
					Return(specs, nil)

				result, err := component.GetPodSpec(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(1))
				Expect(result["spec-1"]).To(Equal(specs[0]))
			})
		})

		Context("GetPodMetadata", func() {
			It("should zip instance IDs with pod metadata", func() {
				instanceIds := []string{"meta-1"}
				metadata := []metav1.ObjectMeta{
					{Name: "test-pod", Labels: map[string]string{"app": "test"}},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				mockAccessor.EXPECT().
					ExtractPodMetadata(ctx, component.definition).
					Return(metadata, nil)

				result, err := component.GetPodMetadata(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(1))
				Expect(result["meta-1"]).To(Equal(metadata[0]))
			})
		})

		Context("GetFragmentedPodSpec", func() {
			It("should zip instance IDs with fragmented pod specs", func() {
				instanceIds := []string{"frag-1"}
				fragSpecs := []FragmentedPodSpec{
					{SchedulerName: "test-scheduler"},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				mockAccessor.EXPECT().
					ExtractFragmentedPodSpec(ctx, component.definition).
					Return(fragSpecs, nil)

				result, err := component.GetFragmentedPodSpec(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(1))
				Expect(result["frag-1"]).To(Equal(fragSpecs[0]))
			})
		})

		Context("GetStatus", func() {
			It("should extract and return status", func() {
				expectedStatus := Status{
					Phase: stringPtr("running"),
					Conditions: []Condition{
						{Type: "Ready", Status: ptr.To("True"), Message: "All pods are ready"},
					},
					MatchedStatuses: []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
				}

				mockAccessor.EXPECT().
					ExtractStatus(ctx, component.definition).
					Return(&expectedStatus, nil)

				result, err := component.GetStatus(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Phase).To(Equal(expectedStatus.Phase))
				Expect(result.Conditions).To(HaveLen(1))
				Expect(result.Conditions[0].Type).To(Equal(expectedStatus.Conditions[0].Type))
				Expect(result.Conditions[0].Status).To(Equal(expectedStatus.Conditions[0].Status))
				Expect(result.Conditions[0].Message).To(Equal(expectedStatus.Conditions[0].Message))
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.RunningStatus))
			})

			It("should return nil when StatusDefinition not found", func() {
				expectedError := DefinitionNotFoundError("component test-component does not have status definition")

				mockAccessor.EXPECT().
					ExtractStatus(ctx, component.definition).
					Return(nil, expectedError)

				result, err := component.GetStatus(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeNil())
			})

			It("should propagate extraction errors", func() {
				expectedError := errors.New("failed to evaluate status query")

				mockAccessor.EXPECT().
					ExtractStatus(ctx, component.definition).
					Return(nil, expectedError)

				result, err := component.GetStatus(ctx)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expectedError.Error()))
				Expect(result).To(BeNil())
			})

			It("should return empty status when no status extracted", func() {
				mockAccessor.EXPECT().
					ExtractStatus(ctx, component.definition).
					Return(&Status{
						MatchedStatuses: []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
					}, nil)

				result, err := component.GetStatus(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.UndefinedStatus))
			})
		})

		Context("GetExtractedInstances", func() {
			It("should aggregate all fields for multi-instance component", func() {
				instanceIds := []string{"job-1", "job-2"}
				podTemplateSpecs := []corev1.PodTemplateSpec{
					{ObjectMeta: metav1.ObjectMeta{Name: "template-1"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "template-2"}},
				}
				podSpecs := []corev1.PodSpec{
					{Containers: []corev1.Container{{Name: "container-1"}}},
					{Containers: []corev1.Container{{Name: "container-2"}}},
				}
				fragmentedSpecs := []FragmentedPodSpec{
					{SchedulerName: "scheduler-1"},
					{SchedulerName: "scheduler-2"},
				}
				metadata := []metav1.ObjectMeta{
					{Name: "pod-1", Labels: map[string]string{"app": "test"}},
					{Name: "pod-2", Labels: map[string]string{"app": "test"}},
				}
				scales := []Scale{
					{Replicas: int32Ptr(3)},
					{Replicas: int32Ptr(5)},
				}

				mockAccessor.EXPECT().ExtractInstanceIds(ctx, component.definition).Return(instanceIds, nil).AnyTimes()
				mockAccessor.EXPECT().ExtractPodTemplateSpec(ctx, component.definition).Return(podTemplateSpecs, nil)
				mockAccessor.EXPECT().ExtractPodSpec(ctx, component.definition).Return(podSpecs, nil)
				mockAccessor.EXPECT().ExtractFragmentedPodSpec(ctx, component.definition).Return(fragmentedSpecs, nil)
				mockAccessor.EXPECT().ExtractPodMetadata(ctx, component.definition).Return(metadata, nil)
				mockAccessor.EXPECT().ExtractScale(ctx, component.definition).Return(scales, nil)

				result, err := component.GetExtractedInstances(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(2))

				Expect(result["job-1"].PodTemplateSpec).NotTo(BeNil())
				Expect(result["job-1"].PodTemplateSpec.Name).To(Equal("template-1"))
				Expect(result["job-1"].PodSpec).NotTo(BeNil())
				Expect(result["job-1"].PodSpec.Containers[0].Name).To(Equal("container-1"))
				Expect(result["job-1"].FragmentedPodSpec).NotTo(BeNil())
				Expect(result["job-1"].FragmentedPodSpec.SchedulerName).To(Equal("scheduler-1"))
				Expect(result["job-1"].Metadata).NotTo(BeNil())
				Expect(result["job-1"].Metadata.Name).To(Equal("pod-1"))
				Expect(result["job-1"].Scale).NotTo(BeNil())
				Expect(*result["job-1"].Scale.Replicas).To(Equal(int32(3)))

				Expect(result["job-2"].PodTemplateSpec).NotTo(BeNil())
				Expect(result["job-2"].PodTemplateSpec.Name).To(Equal("template-2"))
				Expect(result["job-2"].Scale).NotTo(BeNil())
				Expect(*result["job-2"].Scale.Replicas).To(Equal(int32(5)))
			})

			It("should handle partial data (only PodTemplateSpec and Scale)", func() {
				instanceIds := []string{"worker-1"}
				podTemplateSpecs := []corev1.PodTemplateSpec{
					{ObjectMeta: metav1.ObjectMeta{Name: "template-1"}},
				}
				scales := []Scale{
					{Replicas: int32Ptr(3)},
				}

				mockAccessor.EXPECT().ExtractInstanceIds(ctx, component.definition).Return(instanceIds, nil).AnyTimes()
				mockAccessor.EXPECT().ExtractPodTemplateSpec(ctx, component.definition).Return(podTemplateSpecs, nil)
				mockAccessor.EXPECT().ExtractPodSpec(ctx, component.definition).Return(nil, DefinitionNotFoundError("not found"))
				mockAccessor.EXPECT().ExtractFragmentedPodSpec(ctx, component.definition).Return(nil, DefinitionNotFoundError("not found"))
				mockAccessor.EXPECT().ExtractPodMetadata(ctx, component.definition).Return(nil, DefinitionNotFoundError("not found"))
				mockAccessor.EXPECT().ExtractScale(ctx, component.definition).Return(scales, nil)

				result, err := component.GetExtractedInstances(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(1))

				Expect(result["worker-1"].PodTemplateSpec).NotTo(BeNil())
				Expect(result["worker-1"].PodTemplateSpec.Name).To(Equal("template-1"))
				Expect(result["worker-1"].PodSpec).To(BeNil())
				Expect(result["worker-1"].FragmentedPodSpec).To(BeNil())
				Expect(result["worker-1"].Metadata).To(BeNil())
				Expect(result["worker-1"].Scale).NotTo(BeNil())
				Expect(*result["worker-1"].Scale.Replicas).To(Equal(int32(3)))
			})

			It("should propagate GetInstanceIds errors", func() {
				expectedError := errors.New("instance extraction failed")

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(nil, expectedError)

				result, err := component.GetExtractedInstances(ctx)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to get instance ids"))
				Expect(err.Error()).To(ContainSubstring(expectedError.Error()))
				Expect(result).To(BeNil())
			})

			It("should propagate GetPodTemplateSpec errors", func() {
				instanceIds := []string{"job-1"}
				expectedError := errors.New("template extraction failed")

				mockAccessor.EXPECT().ExtractInstanceIds(ctx, component.definition).Return(instanceIds, nil).AnyTimes()
				mockAccessor.EXPECT().ExtractPodTemplateSpec(ctx, component.definition).Return(nil, expectedError)

				result, err := component.GetExtractedInstances(ctx)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to get pod template specs"))
				Expect(err.Error()).To(ContainSubstring(expectedError.Error()))
				Expect(result).To(BeNil())
			})

			It("should propagate GetPodSpec errors", func() {
				instanceIds := []string{"job-1"}
				podTemplateSpecs := []corev1.PodTemplateSpec{{ObjectMeta: metav1.ObjectMeta{Name: "template-1"}}}
				expectedError := errors.New("podspec extraction failed")

				mockAccessor.EXPECT().ExtractInstanceIds(ctx, component.definition).Return(instanceIds, nil).AnyTimes()
				mockAccessor.EXPECT().ExtractPodTemplateSpec(ctx, component.definition).Return(podTemplateSpecs, nil)
				mockAccessor.EXPECT().ExtractPodSpec(ctx, component.definition).Return(nil, expectedError)

				result, err := component.GetExtractedInstances(ctx)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to get pod specs"))
				Expect(err.Error()).To(ContainSubstring(expectedError.Error()))
				Expect(result).To(BeNil())
			})

			It("should propagate GetFragmentedPodSpec errors", func() {
				instanceIds := []string{"job-1"}
				podTemplateSpecs := []corev1.PodTemplateSpec{{ObjectMeta: metav1.ObjectMeta{Name: "template-1"}}}
				expectedError := errors.New("fragmented extraction failed")

				mockAccessor.EXPECT().ExtractInstanceIds(ctx, component.definition).Return(instanceIds, nil).AnyTimes()
				mockAccessor.EXPECT().ExtractPodTemplateSpec(ctx, component.definition).Return(podTemplateSpecs, nil)
				mockAccessor.EXPECT().ExtractPodSpec(ctx, component.definition).Return(nil, DefinitionNotFoundError("not found"))
				mockAccessor.EXPECT().ExtractFragmentedPodSpec(ctx, component.definition).Return(nil, expectedError)

				result, err := component.GetExtractedInstances(ctx)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to get fragmented pod specs"))
				Expect(err.Error()).To(ContainSubstring(expectedError.Error()))
				Expect(result).To(BeNil())
			})

			It("should propagate GetPodMetadata errors", func() {
				instanceIds := []string{"job-1"}
				podTemplateSpecs := []corev1.PodTemplateSpec{{ObjectMeta: metav1.ObjectMeta{Name: "template-1"}}}
				expectedError := errors.New("metadata extraction failed")

				mockAccessor.EXPECT().ExtractInstanceIds(ctx, component.definition).Return(instanceIds, nil).AnyTimes()
				mockAccessor.EXPECT().ExtractPodTemplateSpec(ctx, component.definition).Return(podTemplateSpecs, nil)
				mockAccessor.EXPECT().ExtractPodSpec(ctx, component.definition).Return(nil, DefinitionNotFoundError("not found"))
				mockAccessor.EXPECT().ExtractFragmentedPodSpec(ctx, component.definition).Return(nil, DefinitionNotFoundError("not found"))
				mockAccessor.EXPECT().ExtractPodMetadata(ctx, component.definition).Return(nil, expectedError)

				result, err := component.GetExtractedInstances(ctx)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to get pod metadata"))
				Expect(err.Error()).To(ContainSubstring(expectedError.Error()))
				Expect(result).To(BeNil())
			})

			It("should propagate GetScale errors", func() {
				instanceIds := []string{"job-1"}
				podTemplateSpecs := []corev1.PodTemplateSpec{{ObjectMeta: metav1.ObjectMeta{Name: "template-1"}}}
				expectedError := errors.New("scale extraction failed")

				mockAccessor.EXPECT().ExtractInstanceIds(ctx, component.definition).Return(instanceIds, nil).AnyTimes()
				mockAccessor.EXPECT().ExtractPodTemplateSpec(ctx, component.definition).Return(podTemplateSpecs, nil)
				mockAccessor.EXPECT().ExtractPodSpec(ctx, component.definition).Return(nil, DefinitionNotFoundError("not found"))
				mockAccessor.EXPECT().ExtractFragmentedPodSpec(ctx, component.definition).Return(nil, DefinitionNotFoundError("not found"))
				mockAccessor.EXPECT().ExtractPodMetadata(ctx, component.definition).Return(nil, DefinitionNotFoundError("not found"))
				mockAccessor.EXPECT().ExtractScale(ctx, component.definition).Return(nil, expectedError)

				result, err := component.GetExtractedInstances(ctx)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to get scales"))
				Expect(err.Error()).To(ContainSubstring(expectedError.Error()))
				Expect(result).To(BeNil())
			})
		})

	})

	Context("Update Methods", func() {
		var component *Component

		BeforeEach(func() {
			component = &Component{
				name:       "test-component",
				definition: v1alpha1.ComponentDefinition{Name: "test-component"},
				accessor:   mockAccessor,
			}
		})

		Context("UpdatePodTemplateSpec", func() {
			It("should update pod template specs for multi-instance component", func() {
				instanceIds := []string{"job-1", "job-2"}
				podTemplateSpecs := map[string]corev1.PodTemplateSpec{
					"job-1": {ObjectMeta: metav1.ObjectMeta{Name: "template-1"}},
					"job-2": {ObjectMeta: metav1.ObjectMeta{Name: "template-2"}},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				mockAccessor.EXPECT().
					UpdatePodTemplateSpec(ctx, component.definition, gomock.Any()).
					DoAndReturn(func(ctx context.Context, def v1alpha1.ComponentDefinition, specs []corev1.PodTemplateSpec) error {
						Expect(specs).To(HaveLen(2))
						return nil
					})

				err := component.UpdatePodTemplateSpec(ctx, podTemplateSpecs)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should update pod template spec for single-instance component", func() {
				instanceIds := []string{""}
				podTemplateSpecs := map[string]corev1.PodTemplateSpec{
					"": {ObjectMeta: metav1.ObjectMeta{Name: "single-template"}},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				mockAccessor.EXPECT().
					UpdatePodTemplateSpec(ctx, component.definition, gomock.Any()).
					DoAndReturn(func(ctx context.Context, def v1alpha1.ComponentDefinition, specs []corev1.PodTemplateSpec) error {
						Expect(specs).To(HaveLen(1))
						return nil
					})

				err := component.UpdatePodTemplateSpec(ctx, podTemplateSpecs)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should propagate GetInstanceIds errors", func() {
				expectedError := errors.New("instance extraction failed")
				podTemplateSpecs := map[string]corev1.PodTemplateSpec{
					"job-1": {ObjectMeta: metav1.ObjectMeta{Name: "template-1"}},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(nil, expectedError)

				err := component.UpdatePodTemplateSpec(ctx, podTemplateSpecs)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(errGetInstanceIds))
				Expect(err.Error()).To(ContainSubstring(expectedError.Error()))
			})

			It("should return error when instance IDs count doesn't match map entries", func() {
				instanceIds := []string{"job-1", "job-2", "job-3"}
				podTemplateSpecs := map[string]corev1.PodTemplateSpec{
					"job-1": {ObjectMeta: metav1.ObjectMeta{Name: "template-1"}},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				err := component.UpdatePodTemplateSpec(ctx, podTemplateSpecs)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to unzip"))
			})

			It("should propagate accessor Update errors", func() {
				instanceIds := []string{"job-1"}
				podTemplateSpecs := map[string]corev1.PodTemplateSpec{
					"job-1": {ObjectMeta: metav1.ObjectMeta{Name: "template-1"}},
				}
				expectedError := errors.New("update failed")

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				mockAccessor.EXPECT().
					UpdatePodTemplateSpec(ctx, component.definition, gomock.Any()).
					Return(expectedError)

				err := component.UpdatePodTemplateSpec(ctx, podTemplateSpecs)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal(expectedError.Error()))
			})
		})

		Context("UpdatePodSpec", func() {
			It("should update pod specs for multi-instance component", func() {
				instanceIds := []string{"worker-1", "worker-2"}
				podSpecs := map[string]corev1.PodSpec{
					"worker-1": {Containers: []corev1.Container{{Name: "container-1"}}},
					"worker-2": {Containers: []corev1.Container{{Name: "container-2"}}},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				mockAccessor.EXPECT().
					UpdatePodSpec(ctx, component.definition, gomock.Any()).
					DoAndReturn(func(ctx context.Context, def v1alpha1.ComponentDefinition, specs []corev1.PodSpec) error {
						Expect(specs).To(HaveLen(2))
						return nil
					})

				err := component.UpdatePodSpec(ctx, podSpecs)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should update pod spec for single-instance component", func() {
				instanceIds := []string{""}
				podSpecs := map[string]corev1.PodSpec{
					"": {Containers: []corev1.Container{{Name: "single-container"}}},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				mockAccessor.EXPECT().
					UpdatePodSpec(ctx, component.definition, gomock.Any()).
					DoAndReturn(func(ctx context.Context, def v1alpha1.ComponentDefinition, specs []corev1.PodSpec) error {
						Expect(specs).To(HaveLen(1))
						return nil
					})

				err := component.UpdatePodSpec(ctx, podSpecs)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should propagate GetInstanceIds errors", func() {
				expectedError := errors.New("instance extraction failed")
				podSpecs := map[string]corev1.PodSpec{
					"worker-1": {Containers: []corev1.Container{{Name: "container-1"}}},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(nil, expectedError)

				err := component.UpdatePodSpec(ctx, podSpecs)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(errGetInstanceIds))
				Expect(err.Error()).To(ContainSubstring(expectedError.Error()))
			})

			It("should return error when instance IDs count doesn't match map entries", func() {
				instanceIds := []string{"worker-1", "worker-2"}
				podSpecs := map[string]corev1.PodSpec{
					"worker-1": {Containers: []corev1.Container{{Name: "container-1"}}},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				err := component.UpdatePodSpec(ctx, podSpecs)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to unzip"))
			})

			It("should propagate accessor Update errors", func() {
				instanceIds := []string{"worker-1"}
				podSpecs := map[string]corev1.PodSpec{
					"worker-1": {Containers: []corev1.Container{{Name: "container-1"}}},
				}
				expectedError := errors.New("update failed")

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				mockAccessor.EXPECT().
					UpdatePodSpec(ctx, component.definition, gomock.Any()).
					Return(expectedError)

				err := component.UpdatePodSpec(ctx, podSpecs)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal(expectedError.Error()))
			})
		})

		Context("UpdatePodMetadata", func() {
			It("should update pod metadata for multi-instance component", func() {
				instanceIds := []string{"meta-1", "meta-2"}
				podMetadata := map[string]metav1.ObjectMeta{
					"meta-1": {Name: "pod-1", Labels: map[string]string{"app": "test"}},
					"meta-2": {Name: "pod-2", Labels: map[string]string{"app": "test"}},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				mockAccessor.EXPECT().
					UpdatePodMetadata(ctx, component.definition, gomock.Any()).
					DoAndReturn(func(ctx context.Context, def v1alpha1.ComponentDefinition, metadata []metav1.ObjectMeta) error {
						Expect(metadata).To(HaveLen(2))
						return nil
					})

				err := component.UpdatePodMetadata(ctx, podMetadata)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should update pod metadata for single-instance component", func() {
				instanceIds := []string{""}
				podMetadata := map[string]metav1.ObjectMeta{
					"": {Name: "single-pod", Labels: map[string]string{"app": "test"}},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				mockAccessor.EXPECT().
					UpdatePodMetadata(ctx, component.definition, gomock.Any()).
					DoAndReturn(func(ctx context.Context, def v1alpha1.ComponentDefinition, metadata []metav1.ObjectMeta) error {
						Expect(metadata).To(HaveLen(1))
						return nil
					})

				err := component.UpdatePodMetadata(ctx, podMetadata)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should propagate GetInstanceIds errors", func() {
				expectedError := errors.New("instance extraction failed")
				podMetadata := map[string]metav1.ObjectMeta{
					"meta-1": {Name: "pod-1", Labels: map[string]string{"app": "test"}},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(nil, expectedError)

				err := component.UpdatePodMetadata(ctx, podMetadata)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(errGetInstanceIds))
				Expect(err.Error()).To(ContainSubstring(expectedError.Error()))
			})

			It("should return error when instance IDs count doesn't match map entries", func() {
				instanceIds := []string{"meta-1", "meta-2"}
				podMetadata := map[string]metav1.ObjectMeta{
					"meta-1": {Name: "pod-1", Labels: map[string]string{"app": "test"}},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				err := component.UpdatePodMetadata(ctx, podMetadata)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to unzip"))
			})

			It("should propagate accessor Update errors", func() {
				instanceIds := []string{"meta-1"}
				podMetadata := map[string]metav1.ObjectMeta{
					"meta-1": {Name: "pod-1", Labels: map[string]string{"app": "test"}},
				}
				expectedError := errors.New("update failed")

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				mockAccessor.EXPECT().
					UpdatePodMetadata(ctx, component.definition, gomock.Any()).
					Return(expectedError)

				err := component.UpdatePodMetadata(ctx, podMetadata)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal(expectedError.Error()))
			})
		})

		Context("UpdateFragmentedPodSpec", func() {
			It("should update fragmented pod specs for multi-instance component", func() {
				instanceIds := []string{"frag-1", "frag-2"}
				fragmentedPodSpecs := map[string]FragmentedPodSpec{
					"frag-1": {SchedulerName: "scheduler-1", Image: "image-1"},
					"frag-2": {SchedulerName: "scheduler-2", Image: "image-2"},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				mockAccessor.EXPECT().
					UpdateFragmentedPodSpec(ctx, component.definition, gomock.Any()).
					DoAndReturn(func(ctx context.Context, def v1alpha1.ComponentDefinition, specs []FragmentedPodSpec) error {
						Expect(specs).To(HaveLen(2))
						return nil
					})

				err := component.UpdateFragmentedPodSpec(ctx, fragmentedPodSpecs)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should update fragmented pod spec for single-instance component", func() {
				instanceIds := []string{""}
				fragmentedPodSpecs := map[string]FragmentedPodSpec{
					"": {SchedulerName: "single-scheduler", Image: "single-image"},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				mockAccessor.EXPECT().
					UpdateFragmentedPodSpec(ctx, component.definition, gomock.Any()).
					DoAndReturn(func(ctx context.Context, def v1alpha1.ComponentDefinition, specs []FragmentedPodSpec) error {
						Expect(specs).To(HaveLen(1))
						return nil
					})

				err := component.UpdateFragmentedPodSpec(ctx, fragmentedPodSpecs)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should propagate GetInstanceIds errors", func() {
				expectedError := errors.New("instance extraction failed")
				fragmentedPodSpecs := map[string]FragmentedPodSpec{
					"frag-1": {SchedulerName: "scheduler-1", Image: "image-1"},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(nil, expectedError)

				err := component.UpdateFragmentedPodSpec(ctx, fragmentedPodSpecs)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(errGetInstanceIds))
				Expect(err.Error()).To(ContainSubstring(expectedError.Error()))
			})

			It("should return error when instance IDs count doesn't match map entries", func() {
				instanceIds := []string{"frag-1", "frag-2"}
				fragmentedPodSpecs := map[string]FragmentedPodSpec{
					"frag-1": {SchedulerName: "scheduler-1", Image: "image-1"},
				}

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				err := component.UpdateFragmentedPodSpec(ctx, fragmentedPodSpecs)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to unzip"))
			})

			It("should propagate accessor Update errors", func() {
				instanceIds := []string{"frag-1"}
				fragmentedPodSpecs := map[string]FragmentedPodSpec{
					"frag-1": {SchedulerName: "scheduler-1", Image: "image-1"},
				}
				expectedError := errors.New("update failed")

				mockAccessor.EXPECT().
					ExtractInstanceIds(ctx, component.definition).
					Return(instanceIds, nil)

				mockAccessor.EXPECT().
					UpdateFragmentedPodSpec(ctx, component.definition, gomock.Any()).
					Return(expectedError)

				err := component.UpdateFragmentedPodSpec(ctx, fragmentedPodSpecs)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal(expectedError.Error()))
			})
		})
	})

	Context("Suspend and Resume", func() {
		var component *Component

		suspendDef := v1alpha1.SuspendDefinition{
			SuspendActions: []v1alpha1.SuspendAction{{Path: ".spec.suspend", Value: "true"}},
			ResumeActions:  []v1alpha1.SuspendAction{{Path: ".spec.suspend", Value: "false"}},
		}

		BeforeEach(func() {
			component = &Component{
				name: "suspendable-component",
				definition: v1alpha1.ComponentDefinition{
					Name:              "suspendable-component",
					SuspendDefinition: &suspendDef,
				},
				accessor: mockAccessor,
			}
		})

		Describe("HasSuspendDefinition", func() {
			It("should return true when SuspendDefinition is set", func() {
				Expect(component.HasSuspendDefinition()).To(BeTrue())
			})

			It("should return false when SuspendDefinition is nil", func() {
				component.definition.SuspendDefinition = nil
				Expect(component.HasSuspendDefinition()).To(BeFalse())
			})
		})

		Describe("Suspend", func() {
			It("should call ApplySuspendActions and succeed", func() {
				mockAccessor.EXPECT().
					ApplySuspendActions(ctx, component.definition).
					Return(nil)

				Expect(component.Suspend(ctx)).To(Succeed())
			})

			It("should return nil when component has no SuspendDefinition", func() {
				component.definition.SuspendDefinition = nil
				mockAccessor.EXPECT().
					ApplySuspendActions(ctx, component.definition).
					Return(DefinitionNotFoundError("no suspend definition"))

				Expect(component.Suspend(ctx)).To(Succeed())
			})

			It("should propagate non-definition errors from ApplySuspendActions", func() {
				expectedErr := errors.New("mutation failed")
				mockAccessor.EXPECT().
					ApplySuspendActions(ctx, component.definition).
					Return(expectedErr)

				err := component.Suspend(ctx)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("apply suspend actions"))
				Expect(err.Error()).To(ContainSubstring(expectedErr.Error()))
			})
		})

		Describe("Resume", func() {
			It("should call ApplyResumeActions and succeed", func() {
				mockAccessor.EXPECT().
					ApplyResumeActions(ctx, component.definition).
					Return(nil)

				Expect(component.Resume(ctx)).To(Succeed())
			})

			It("should return nil when component has no SuspendDefinition", func() {
				component.definition.SuspendDefinition = nil
				mockAccessor.EXPECT().
					ApplyResumeActions(ctx, component.definition).
					Return(DefinitionNotFoundError("no suspend definition"))

				Expect(component.Resume(ctx)).To(Succeed())
			})

			It("should propagate non-definition errors from ApplyResumeActions", func() {
				expectedErr := errors.New("mutation failed")
				mockAccessor.EXPECT().
					ApplyResumeActions(ctx, component.definition).
					Return(expectedErr)

				err := component.Resume(ctx)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("apply resume actions"))
				Expect(err.Error()).To(ContainSubstring(expectedErr.Error()))
			})
		})
	})
})

// Helper functions
func stringPtr(s string) *string {
	return &s
}

func int32Ptr(i int32) *int32 {
	return &i
}
