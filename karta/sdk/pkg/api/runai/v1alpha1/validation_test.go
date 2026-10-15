// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

var _ = Describe("KartaValidator", func() {
	var (
		validator *KartaValidator
		baseKarta *Karta
	)

	BeforeEach(func() {
		// Base valid Karta that can be modified for specific tests
		baseKarta = &Karta{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-karta",
			},
			Spec: KartaSpec{
				StructureDefinition: StructureDefinition{
					RootComponent: ComponentDefinition{
						Name: "root",
						Kind: &GroupVersionKind{
							Group:   "apps",
							Version: "v1",
							Kind:    "Deployment",
						},
						StatusDefinition: &StatusDefinition{
							StatusMappings: StatusMappings{},
						},
						SpecDefinition: &SpecDefinition{
							PodTemplateSpecPath: ptr.To(".spec.template"),
						},
						ScaleDefinition: &ScaleDefinition{
							ReplicasPath: ptr.To(".spec.replicas"),
						},
					},
					ChildComponents: []ComponentDefinition{
						{
							Name:     "worker",
							OwnerRef: ptr.To("root"),
							SpecDefinition: &SpecDefinition{
								PodSpecPath: ptr.To(".spec.template.spec"),
							},
							ScaleDefinition: &ScaleDefinition{
								ReplicasPath: ptr.To(".spec.replicas"),
							},
						},
					},
				},
				Instructions: OptimizationInstructions{
					GangScheduling: &GangSchedulingInstruction{
						PodGroups: []PodGroupDefinition{
							{
								Name: "main-group",
								Members: []PodGroupMemberDefinition{
									{ComponentName: "root"},
									{ComponentName: "worker"},
								},
							},
						},
					},
				},
			},
		}

		validator = NewKartaValidator(baseKarta)
	})

	Describe("Validate", func() {
		Context("when Karta is nil", func() {
			It("should return error", func() {
				validator = NewKartaValidator(nil)
				err := validator.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("karta is nil"))
			})
		})

		Context("with valid Karta", func() {
			It("should pass validation", func() {
				err := validator.Validate()
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("with multiple validation errors", func() {
			It("should aggregate all errors", func() {
				// Create Karta with multiple issues
				baseKarta.Spec.StructureDefinition.RootComponent.Kind = nil
				baseKarta.Spec.StructureDefinition.ChildComponents[0].OwnerRef = nil
				baseKarta.Spec.Instructions.GangScheduling.PodGroups[0].Members[0].ComponentName = "nonexistent"

				err := validator.Validate()
				Expect(err).To(HaveOccurred())
				errStr := err.Error()
				Expect(errStr).To(ContainSubstring("root component must have full kind"))
				Expect(errStr).To(ContainSubstring("has no owner ref"))
				Expect(errStr).To(ContainSubstring("is not defined"))
			})
		})
	})

	Describe("initialize", func() {
		Context("with duplicate component names", func() {
			It("should return error", func() {
				baseKarta.Spec.StructureDefinition.ChildComponents = append(
					baseKarta.Spec.StructureDefinition.ChildComponents,
					ComponentDefinition{Name: "root", OwnerRef: ptr.To("root")},
				)

				errs := validator.initialize()
				Expect(errs).To(HaveLen(1))
				Expect(errs[0].Error()).To(ContainSubstring("component name root is not unique"))
			})
		})

		Context("with unique component names", func() {
			It("should build allComponents map correctly", func() {
				errs := validator.initialize()
				Expect(errs).To(BeEmpty())
				Expect(validator.allComponents).To(HaveLen(2))
				Expect(validator.allComponents["root"]).To(Equal(baseKarta.Spec.StructureDefinition.RootComponent))
				Expect(validator.allComponents["worker"]).To(Equal(baseKarta.Spec.StructureDefinition.ChildComponents[0]))
			})
		})
	})

	Describe("validateStructureDefinition", func() {
		Context("root component validation", func() {
			It("should fail when root has no GVK", func() {
				baseKarta.Spec.StructureDefinition.RootComponent.Kind = nil
				validator.initialize()

				errs := validator.validateStructureDefinition()
				Expect(errs).To(HaveLen(1))
				Expect(errs).To(ContainElement(MatchError(ContainSubstring("root component must have full kind"))))
			})

			It("should fail when root has incomplete GVK", func() {
				baseKarta.Spec.StructureDefinition.RootComponent.Kind.Group = ""
				validator.initialize()

				errs := validator.validateStructureDefinition()
				Expect(errs).To(HaveLen(1))
				Expect(errs).To(ContainElement(MatchError(ContainSubstring("root component must have full kind"))))
			})

			It("should fail when root has owner ref", func() {
				baseKarta.Spec.StructureDefinition.RootComponent.OwnerRef = ptr.To("someone")
				validator.initialize()

				errs := validator.validateStructureDefinition()
				Expect(errs).To(HaveLen(1))
				Expect(errs).To(ContainElement(MatchError(ContainSubstring("root component cannot have owner ref"))))
			})

			It("should fail when root has no status definition", func() {
				baseKarta.Spec.StructureDefinition.RootComponent.StatusDefinition = nil
				validator.initialize()

				errs := validator.validateStructureDefinition()
				Expect(errs).To(HaveLen(1))
				Expect(errs).To(ContainElement(MatchError(ContainSubstring("root component must have status definition"))))
			})
		})

		Context("child component validation", func() {
			It("should fail when child has no owner ref", func() {
				baseKarta.Spec.StructureDefinition.ChildComponents[0].OwnerRef = nil
				validator.initialize()

				errs := validator.validateStructureDefinition()
				Expect(errs).To(HaveLen(1))
				Expect(errs).To(ContainElement(MatchError(ContainSubstring("child component 'worker' has no owner ref"))))
			})

			It("should fail when child has empty owner ref", func() {
				baseKarta.Spec.StructureDefinition.ChildComponents[0].OwnerRef = ptr.To("")
				validator.initialize()

				errs := validator.validateStructureDefinition()
				Expect(errs).To(HaveLen(1))
				Expect(errs).To(ContainElement(MatchError(ContainSubstring("child component 'worker' has no owner ref"))))
			})

			It("should fail when owner ref points to nonexistent component", func() {
				baseKarta.Spec.StructureDefinition.ChildComponents[0].OwnerRef = ptr.To("nonexistent")
				validator.initialize()

				errs := validator.validateStructureDefinition()
				Expect(errs).To(HaveLen(1))
				Expect(errs).To(ContainElement(MatchError(ContainSubstring("owner ref to non-existing component 'nonexistent'"))))
			})
		})

		Context("ownership cycles", func() {
			It("should detect simple cycle", func() {
				// Create A -> B -> A cycle
				baseKarta.Spec.StructureDefinition.ChildComponents = []ComponentDefinition{
					{Name: "A", OwnerRef: ptr.To("B")},
					{Name: "B", OwnerRef: ptr.To("A")},
				}
				validator.initialize()

				errs := validator.validateStructureDefinition()
				Expect(errs).To(HaveLen(1))
				Expect(errs).To(ContainElement(MatchError(ContainSubstring("ownership cycle detected"))))
			})

			It("should detect complex cycle", func() {
				// Create A -> B -> C -> A cycle
				baseKarta.Spec.StructureDefinition.ChildComponents = []ComponentDefinition{
					{Name: "A", OwnerRef: ptr.To("B")},
					{Name: "B", OwnerRef: ptr.To("C")},
					{Name: "C", OwnerRef: ptr.To("A")},
				}
				validator.initialize()

				errs := validator.validateStructureDefinition()
				Expect(errs).To(HaveLen(1))
				Expect(errs).To(ContainElement(MatchError(ContainSubstring("ownership cycle detected"))))
			})

			It("should pass with valid hierarchy", func() {
				// Create root -> A -> B (no cycle)
				baseKarta.Spec.StructureDefinition.ChildComponents = []ComponentDefinition{
					{Name: "A", OwnerRef: ptr.To("root")},
					{Name: "B", OwnerRef: ptr.To("A")},
				}
				validator.initialize()

				errs := validator.validateStructureDefinition()
				Expect(errs).To(BeEmpty())
			})
		})
	})

	Describe("validateComponent", func() {
		Context("empty component name", func() {
			It("should return error", func() {
				component := ComponentDefinition{Name: ""}
				validator.initialize()

				errs := validator.validateComponent(component)
				Expect(errs).To(HaveLen(1))
				Expect(errs).To(ContainElement(MatchError(ContainSubstring("component name is empty"))))
			})
		})

		DescribeTable("multiple pod spec definitions",
			func(podTemplateSpecPath, podSpecPath *string, fragmentedPodSpec *FragmentedPodSpecDefinition) {
				component := ComponentDefinition{
					Name: "test",
					SpecDefinition: &SpecDefinition{
						PodTemplateSpecPath:         podTemplateSpecPath,
						PodSpecPath:                 podSpecPath,
						FragmentedPodSpecDefinition: fragmentedPodSpec,
					},
				}
				validator.initialize()

				errs := validator.validateComponent(component)
				Expect(errs).To(HaveLen(1))
				Expect(errs).To(ContainElement(MatchError(ContainSubstring("has multiple pod spec definitions"))))
			},
			Entry("PodTemplateSpecPath + PodSpecPath",
				ptr.To(".spec.template"),
				ptr.To(".spec.template.spec"),
				nil),
			Entry("PodTemplateSpecPath + FragmentedPodSpec",
				ptr.To(".spec.template"),
				nil,
				&FragmentedPodSpecDefinition{ContainersPath: ptr.To(".spec.containers")}),
			Entry("PodSpecPath + FragmentedPodSpec",
				nil,
				ptr.To(".spec.template.spec"),
				&FragmentedPodSpecDefinition{ContainersPath: ptr.To(".spec.containers")}),
			Entry("All three pod spec definitions",
				ptr.To(".spec.template"),
				ptr.To(".spec.template.spec"),
				&FragmentedPodSpecDefinition{ContainersPath: ptr.To(".spec.containers")}),
		)

		Context("multi-instance component validation", func() {
			It("should fail when has instance id path but no instance selector", func() {
				component := ComponentDefinition{
					Name:           "test",
					InstanceIdPath: ptr.To(".metadata.name"),
				}
				validator.initialize()

				errs := validator.validateComponent(component)
				Expect(errs).To(HaveLen(1))
				Expect(errs).To(ContainElement(MatchError(ContainSubstring("has instance id path but no pod component instance selector"))))
			})

			It("should fail when has instance selector but no instance id path", func() {
				component := ComponentDefinition{
					Name: "test",
					PodSelector: &PodSelector{
						ComponentInstanceSelector: &ComponentInstanceSelector{
							IdPath: ".metadata.labels[\"instance-id\"]",
						},
					},
				}
				validator.initialize()

				errs := validator.validateComponent(component)
				Expect(errs).To(HaveLen(1))
				Expect(errs).To(ContainElement(MatchError(ContainSubstring("has pod component instance selector but no instance id path"))))
			})

			It("should pass when both instance id path and selector are present", func() {
				component := ComponentDefinition{
					Name:           "test",
					InstanceIdPath: ptr.To(".metadata.name"),
					PodSelector: &PodSelector{
						ComponentInstanceSelector: &ComponentInstanceSelector{
							IdPath: ".metadata.labels[\"instance-id\"]",
						},
					},
				}
				validator.initialize()

				errs := validator.validateComponent(component)
				Expect(errs).To(BeEmpty())
			})
		})
	})

	Describe("validateInstructions", func() {
		Context("gang scheduling validation", func() {
			It("should pass when gang scheduling is nil", func() {
				baseKarta.Spec.Instructions.GangScheduling = nil
				validator.initialize()

				errs := validator.validateInstructions()
				Expect(errs).To(BeEmpty())
			})

			It("should fail when member component doesn't exist", func() {
				baseKarta.Spec.Instructions.GangScheduling.PodGroups[0].Members[0].ComponentName = "nonexistent"
				validator.initialize()

				errs := validator.validateInstructions()
				Expect(errs).To(HaveLen(1))
				Expect(errs).To(ContainElement(MatchError(ContainSubstring("pod-group member component 'nonexistent' is not defined"))))
			})

			It("should pass when all member components exist", func() {
				validator.initialize()

				errs := validator.validateInstructions()
				Expect(errs).To(BeEmpty())
			})
		})
	})

	Describe("JQ expressions validation is called", func() {
		var kartaWithJQPaths *Karta

		BeforeEach(func() {
			kartaWithJQPaths = &Karta{
				ObjectMeta: metav1.ObjectMeta{Name: "test-jq"},
				Spec: KartaSpec{
					StructureDefinition: StructureDefinition{
						RootComponent: ComponentDefinition{
							Name:             "root",
							Kind:             &GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
							StatusDefinition: &StatusDefinition{StatusMappings: StatusMappings{}},
							SpecDefinition: &SpecDefinition{
								PodTemplateSpecPath: ptr.To(".spec.template"), // Valid JQ
							},
							ScaleDefinition: &ScaleDefinition{
								ReplicasPath: ptr.To(".spec.replicas"), // Valid JQ
							},
						},
					},
				},
			}
		})

		It("should pass with valid JQ expressions", func() {
			validator = NewKartaValidator(kartaWithJQPaths)

			err := validator.Validate()
			Expect(err).ToNot(HaveOccurred())
		})

		It("should fail with dangerous JQ expressions", func() {
			kartaWithJQPaths.Spec.StructureDefinition.RootComponent.SpecDefinition.PodTemplateSpecPath = ptr.To("del(.spec.template)")
			validator = NewKartaValidator(kartaWithJQPaths)

			err := validator.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("del function is not allowed"))
		})

		Context("ByExpression validation", func() {
			It("should pass with valid ByExpression", func() {
				kartaWithJQPaths.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings.Running = []StatusMatcher{
					{
						ByExpression: &ExpressionMatcher{
							Expression:     ".status.phase == \"Running\"",
							ExpectedResult: "true",
						},
					},
				}
				validator = NewKartaValidator(kartaWithJQPaths)

				err := validator.Validate()
				Expect(err).ToNot(HaveOccurred())
			})

			It("should fail with dangerous ByExpression using del", func() {
				kartaWithJQPaths.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings.Running = []StatusMatcher{
					{
						ByExpression: &ExpressionMatcher{
							Expression:     "del(.status)",
							ExpectedResult: "true",
						},
					},
				}
				validator = NewKartaValidator(kartaWithJQPaths)

				err := validator.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("del function is not allowed"))
			})

			It("should fail with invalid ByExpression syntax", func() {
				kartaWithJQPaths.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings.Running = []StatusMatcher{
					{
						ByExpression: &ExpressionMatcher{
							Expression:     ".status.phase == ",
							ExpectedResult: "true",
						},
					},
				}
				validator = NewKartaValidator(kartaWithJQPaths)

				err := validator.Validate()
				Expect(err).To(HaveOccurred())
			})

			It("should validate ByExpression in multiple status matchers", func() {
				kartaWithJQPaths.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = StatusMappings{
					Initializing: []StatusMatcher{
						{ByExpression: &ExpressionMatcher{Expression: ".status.phase == \"Pending\"", ExpectedResult: "true"}},
					},
					Running: []StatusMatcher{
						{ByExpression: &ExpressionMatcher{Expression: ".status.phase == \"Running\"", ExpectedResult: "true"}},
					},
					Completed: []StatusMatcher{
						{ByExpression: &ExpressionMatcher{Expression: ".status.phase == \"Succeeded\"", ExpectedResult: "true"}},
					},
					Failed: []StatusMatcher{
						{ByExpression: &ExpressionMatcher{Expression: ".status.phase == \"Failed\"", ExpectedResult: "true"}},
					},
				}
				validator = NewKartaValidator(kartaWithJQPaths)

				err := validator.Validate()
				Expect(err).ToNot(HaveOccurred())
			})

			It("should fail when one ByExpression in multiple matchers is invalid", func() {
				kartaWithJQPaths.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = StatusMappings{
					Initializing: []StatusMatcher{
						{ByExpression: &ExpressionMatcher{Expression: ".status.phase == \"Pending\"", ExpectedResult: "true"}},
					},
					Running: []StatusMatcher{
						{ByExpression: &ExpressionMatcher{Expression: "del(.status)", ExpectedResult: "true"}},
					},
					Completed: []StatusMatcher{
						{ByExpression: &ExpressionMatcher{Expression: ".status.phase == \"Succeeded\"", ExpectedResult: "true"}},
					},
				}
				validator = NewKartaValidator(kartaWithJQPaths)

				err := validator.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("del function is not allowed"))
			})
		})
	})

	Describe("StatusMappings.Entries", func() {
		It("should return all status-to-matchers pairs", func() {
			mappings := StatusMappings{
				Running:      []StatusMatcher{{ByPhase: "Running"}},
				Failed:       []StatusMatcher{{ByPhase: "Failed"}},
				Completed:    []StatusMatcher{{ByPhase: "Completed"}},
				Initializing: []StatusMatcher{{ByPhase: "Initializing"}},
				Degraded:     []StatusMatcher{{ByPhase: "Degraded"}},
				Suspended:    []StatusMatcher{{ByPhase: "Suspended"}},
				Resuming:     []StatusMatcher{{ByPhase: "Resuming"}},
				Suspending:   []StatusMatcher{{ByPhase: "Suspending"}},
			}

			entries := mappings.Entries()
			Expect(entries).To(HaveLen(8))

			statusToMatchers := make(map[ResourceStatus][]StatusMatcher)
			for _, entry := range entries {
				statusToMatchers[entry.Status] = entry.Matchers
			}

			Expect(statusToMatchers).To(HaveKey(RunningStatus))
			Expect(statusToMatchers[RunningStatus]).To(Equal(mappings.Running))
			Expect(statusToMatchers).To(HaveKey(FailedStatus))
			Expect(statusToMatchers[FailedStatus]).To(Equal(mappings.Failed))
			Expect(statusToMatchers).To(HaveKey(CompletedStatus))
			Expect(statusToMatchers[CompletedStatus]).To(Equal(mappings.Completed))
			Expect(statusToMatchers).To(HaveKey(InitializingStatus))
			Expect(statusToMatchers[InitializingStatus]).To(Equal(mappings.Initializing))
			Expect(statusToMatchers).To(HaveKey(DegradedStatus))
			Expect(statusToMatchers[DegradedStatus]).To(Equal(mappings.Degraded))
			Expect(statusToMatchers).To(HaveKey(SuspendedStatus))
			Expect(statusToMatchers[SuspendedStatus]).To(Equal(mappings.Suspended))
			Expect(statusToMatchers).To(HaveKey(SuspendingStatus))
			Expect(statusToMatchers[SuspendingStatus]).To(Equal(mappings.Suspending))
			Expect(statusToMatchers).To(HaveKey(ResumingStatus))
			Expect(statusToMatchers[ResumingStatus]).To(Equal(mappings.Resuming))
		})

		It("should return entries with nil matchers for empty mappings", func() {
			mappings := StatusMappings{}
			entries := mappings.Entries()
			Expect(entries).To(HaveLen(8))
			for _, entry := range entries {
				Expect(entry.Matchers).To(BeNil())
			}
		})
	})

	Describe("short circuit on errors", func() {
		It("should stop validation if has init errors", func() {
			baseKarta.Spec.StructureDefinition.ChildComponents = []ComponentDefinition{
				{Name: "A", OwnerRef: ptr.To("B")},
				{Name: "B", OwnerRef: ptr.To("A")},
				{Name: "C", OwnerRef: ptr.To("D")}, // Invalid owner ref
				{Name: "C", OwnerRef: ptr.To("A")}, // Duplicate name
			}

			//Should stop after init errors
			err := validator.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("component name C is not unique"))
			Expect(err.Error()).NotTo(ContainSubstring("owner ref to non-existing component 'D'"))
		})

		It("should stop structure validation if definition is invalid", func() {
			baseKarta.Spec.StructureDefinition.ChildComponents = []ComponentDefinition{
				{Name: "A", OwnerRef: ptr.To("B")},
				{Name: "B", OwnerRef: ptr.To("A")},
				{Name: "C", OwnerRef: ptr.To("D")}, // Invalid owner ref
			}
			validator.initialize()

			// Should only have one error - stop after found invalid structure, no need to check ownership cycles
			errs := validator.validateStructureDefinition()
			Expect(errs).To(HaveLen(1))
			Expect(errs).To(ContainElement(MatchError(ContainSubstring("owner ref to non-existing component 'D'"))))
		})
	})
})
