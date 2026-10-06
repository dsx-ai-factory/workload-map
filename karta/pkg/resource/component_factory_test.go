// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package resource

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/test/types"
	testutils "github.com/dsx-ai-factory/workload-map/test/types/jsonutils"
)

var _ = Describe("ComponentFactory", func() {
	var (
		ctrl         *gomock.Controller
		mockAccessor *MockComponentAccessor
		karta        *v1alpha1.Karta
		factory      *ComponentFactory
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockAccessor = NewMockComponentAccessor(ctrl)
		karta = types.PyFlowKarta()
		factory = NewComponentFactory(karta, mockAccessor)
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	Context("component access", func() {
		It("should get root component", func() {
			component, err := factory.GetRootComponent()
			Expect(err).NotTo(HaveOccurred())
			Expect(component).NotTo(BeNil())
			Expect(component.name).To(Equal("pyflow"))
		})

		It("should get child components by name", func() {
			master, err := factory.GetComponent("master")
			Expect(err).NotTo(HaveOccurred())
			Expect(master.name).To(Equal("master"))

			worker, err := factory.GetComponent("worker")
			Expect(err).NotTo(HaveOccurred())
			Expect(worker.name).To(Equal("worker"))
		})

		It("should return error for non-existent component", func() {
			component, err := factory.GetComponent("non-existent")
			Expect(err).To(HaveOccurred())
			Expect(component).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("component non-existent not found"))
		})

		It("should return error when Karta is nil", func() {
			// Note: NewComponentFactory panics with nil Karta (by design)
			// Testing GetRootComponent with nil Karta after factory creation
			factory.karta = nil // Simulate nil Karta scenario
			component, err := factory.GetRootComponent()
			Expect(err).To(HaveOccurred())
			Expect(component).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("karta is nil"))
		})
	})

	Context("component sharing", func() {
		It("should share the same accessor instance across components", func() {
			master, err := factory.GetComponent("master")
			Expect(err).NotTo(HaveOccurred())

			worker, err := factory.GetComponent("worker")
			Expect(err).NotTo(HaveOccurred())

			Expect(master.accessor).To(Equal(mockAccessor))
			Expect(worker.accessor).To(Equal(mockAccessor))
		})
	})

	Context("GetChildComponents", func() {
		It("should get all child components", func() {
			components, err := factory.GetChildComponents()
			Expect(err).NotTo(HaveOccurred())
			Expect(components).NotTo(BeNil())
			Expect(components).To(HaveLen(2))

			Expect([]string{components[0].name, components[1].name}).To(ConsistOf("master", "worker"))
		})

		It("should return error when Karta is nil", func() {
			factory.karta = nil
			components, err := factory.GetChildComponents()
			Expect(err).To(HaveOccurred())
			Expect(components).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("karta is nil"))
		})
	})

	Context("GetResource", func() {
		It("should return updated client.Object", func() {
			var object map[string]interface{}
			convertViaJSON(types.NewPyFlowObject(), &object)

			mockAccessor.EXPECT().
				GetObject().
				Return(object, nil)

			result, err := factory.GetResource()
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result).To(testutils.BeJSONEquivalentTo(object))
		})

		It("should propagate GetObject errors", func() {
			expectedError := errors.New("failed to get updated data")

			mockAccessor.EXPECT().
				GetObject().
				Return(nil, expectedError)

			result, err := factory.GetResource()
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(expectedError))
			Expect(result).To(BeNil())
		})

		It("should return error when object is not client.Object", func() {
			nonClientObject := map[string]interface{}{
				"kind": "SomeKind",
			}

			mockAccessor.EXPECT().
				GetObject().
				Return(nonClientObject, nil)

			result, err := factory.GetResource()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid Kubernetes object"))
			Expect(result).To(BeNil())
		})
	})

	Context("IsContainSpecDefinition", func() {
		Context("components with spec definitions", func() {
			It("should return true when child components have PodTemplateSpecPath", func() {
				// PyFlowKarta has master/worker with PodTemplateSpecPath
				result, err := factory.IsContainSpecDefinition()
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeTrue())
			})

			It("should return true when child components have PodSpecPath", func() {
				// JobGroupKarta has job component with PodSpecPath
				factory.karta = types.JobGroupKarta()
				factory = NewComponentFactory(factory.karta, mockAccessor)

				result, err := factory.IsContainSpecDefinition()
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeTrue())
			})

			It("should return true when child components have FragmentedPodSpecDefinition", func() {
				// ReactorKarta has service component with FragmentedPodSpecDefinition
				factory.karta = types.ReactorKarta()
				factory = NewComponentFactory(factory.karta, mockAccessor)

				result, err := factory.IsContainSpecDefinition()
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeTrue())
			})

			It("should return true when root component has spec definition", func() {
				factory.karta = kartaWithRootSpecOnly()
				factory = NewComponentFactory(factory.karta, mockAccessor)

				result, err := factory.IsContainSpecDefinition()
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeTrue())
			})
		})

		Context("components without spec definitions", func() {
			It("should return false when all components have nil SpecDefinition", func() {
				factory.karta = kartaWithNoSpecs()
				factory = NewComponentFactory(factory.karta, mockAccessor)

				result, err := factory.IsContainSpecDefinition()
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeFalse())
			})

			It("should return false when all components have empty SpecDefinition", func() {
				factory.karta = kartaWithEmptySpecs()
				factory = NewComponentFactory(factory.karta, mockAccessor)

				result, err := factory.IsContainSpecDefinition()
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeFalse())
			})
		})
	})
})

// Helper functions for test Karta instances

// kartaWithNoSpecs creates a Karta where all components have nil SpecDefinition
func kartaWithNoSpecs() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		ObjectMeta: metav1.ObjectMeta{
			Name: "no-specs",
		},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "root",
					Kind: &v1alpha1.GroupVersionKind{
						Group:   "test.example.com",
						Version: "v1",
						Kind:    "NoSpecs",
					},
					// SpecDefinition is nil
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:     "child1",
						OwnerRef: ptr.To("root"),
						// SpecDefinition is nil
					},
					{
						Name:     "child2",
						OwnerRef: ptr.To("root"),
						// SpecDefinition is nil
					},
				},
			},
		},
	}
}

// kartaWithEmptySpecs creates a Karta where all components have empty SpecDefinition
func kartaWithEmptySpecs() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		ObjectMeta: metav1.ObjectMeta{
			Name: "empty-specs",
		},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "root",
					Kind: &v1alpha1.GroupVersionKind{
						Group:   "test.example.com",
						Version: "v1",
						Kind:    "EmptySpecs",
					},
					SpecDefinition: &v1alpha1.SpecDefinition{
						// All fields nil
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:           "child1",
						OwnerRef:       ptr.To("root"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							// All fields nil
						},
					},
					{
						Name:           "child2",
						OwnerRef:       ptr.To("root"),
						SpecDefinition: &v1alpha1.SpecDefinition{
							// All fields nil
						},
					},
				},
			},
		},
	}
}

// kartaWithRootSpecOnly creates a Karta where only root has spec definition
func kartaWithRootSpecOnly() *v1alpha1.Karta {
	return &v1alpha1.Karta{
		ObjectMeta: metav1.ObjectMeta{
			Name: "root-spec-only",
		},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "root",
					Kind: &v1alpha1.GroupVersionKind{
						Group:   "test.example.com",
						Version: "v1",
						Kind:    "RootSpecOnly",
					},
					SpecDefinition: &v1alpha1.SpecDefinition{
						PodTemplateSpecPath: ptr.To(".spec.template"),
					},
				},
				ChildComponents: []v1alpha1.ComponentDefinition{
					{
						Name:     "child1",
						OwnerRef: ptr.To("root"),
						// SpecDefinition is nil
					},
					{
						Name:     "child2",
						OwnerRef: ptr.To("root"),
						// SpecDefinition is nil
					},
				},
			},
		},
	}
}
