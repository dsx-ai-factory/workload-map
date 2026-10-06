// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package instructions

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/utils/ptr"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

var _ = Describe("StructureSummary", func() {
	Describe("NewStructureSummary", func() {
		Context("when Karta is nil", func() {
			It("should return error", func() {
				summary, err := NewStructureSummary(nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("karta cannot be nil"))
				Expect(summary).To(BeNil())
			})
		})

		Context("with simple root-only Karta", func() {
			It("should build summary correctly", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "simple-job",
								SpecDefinition: &v1alpha1.SpecDefinition{
									PodTemplateSpecPath: ptr.To(".spec.template"),
								},
							},
							ChildComponents: []v1alpha1.ComponentDefinition{},
						},
					},
				}

				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())
				Expect(summary).NotTo(BeNil())
				Expect(summary.GetKarta()).To(Equal(karta))

				// Should identify root as leaf component
				Expect(summary.leafComponents).To(HaveLen(1))
				Expect(summary.leafComponents[0]).To(Equal("simple-job"))

				// Should have component definition
				Expect(summary.componentDefinitionsByName).To(HaveKey("simple-job"))
				Expect(summary.componentDefinitionsByName["simple-job"].Name).To(Equal("simple-job"))

				// No parent-child relationships
				Expect(summary.parentMap).To(BeEmpty())
				Expect(summary.childrenMap).To(BeEmpty())

				// No gang scheduling
				Expect(summary.gangSchedulingSummary).To(BeNil())
			})
		})

		Context("with two-level hierarchy", func() {
			It("should build parent-child relationships correctly", func() {
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
								},
								{
									Name:     "master",
									OwnerRef: ptr.To("pytorch-job"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodSpecPath: ptr.To(".spec.master.podSpec"),
									},
								},
							},
						},
					},
				}

				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				// Parent-child relationships
				Expect(summary.parentMap).To(HaveKeyWithValue("worker", "pytorch-job"))
				Expect(summary.parentMap).To(HaveKeyWithValue("master", "pytorch-job"))
				Expect(summary.childrenMap).To(HaveKeyWithValue("pytorch-job", ConsistOf("worker", "master")))

				// Only children are leaf components (have pod definitions)
				Expect(summary.leafComponents).To(ConsistOf("worker", "master"))

				// All components in definitions map
				Expect(summary.componentDefinitionsByName).To(HaveLen(3))
				Expect(summary.componentDefinitionsByName).To(HaveKey("pytorch-job"))
				Expect(summary.componentDefinitionsByName).To(HaveKey("worker"))
				Expect(summary.componentDefinitionsByName).To(HaveKey("master"))
			})
		})

		Context("with three-level hierarchy", func() {
			It("should handle deep hierarchies correctly", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "cluster",
							},
							ChildComponents: []v1alpha1.ComponentDefinition{
								{
									Name:     "job-group",
									OwnerRef: ptr.To("cluster"),
								},
								{
									Name:     "worker",
									OwnerRef: ptr.To("job-group"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
											ContainersPath: ptr.To(".spec.containers"),
										},
									},
								},
								{
									Name:     "coordinator",
									OwnerRef: ptr.To("cluster"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodTemplateSpecPath: ptr.To(".spec.coordinator.template"),
									},
								},
							},
						},
					},
				}

				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				// Parent-child relationships
				Expect(summary.parentMap).To(HaveKeyWithValue("job-group", "cluster"))
				Expect(summary.parentMap).To(HaveKeyWithValue("worker", "job-group"))
				Expect(summary.parentMap).To(HaveKeyWithValue("coordinator", "cluster"))

				Expect(summary.childrenMap).To(HaveKeyWithValue("cluster", ConsistOf("job-group", "coordinator")))
				Expect(summary.childrenMap).To(HaveKeyWithValue("job-group", ConsistOf("worker")))

				// Only components with pod definitions are leaf components
				Expect(summary.leafComponents).To(ConsistOf("worker", "coordinator"))
			})
		})

		Context("with gang scheduling instructions", func() {
			It("should build gang scheduling summary correctly", func() {
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

				// Gang scheduling summary should be built
				Expect(summary.gangSchedulingSummary).NotTo(BeNil())
				Expect(summary.gangSchedulingSummary.podGroupsByName).To(HaveKey("pytorch-training"))

				// Effective component candidates should be built
				Expect(summary.gangSchedulingSummary.effectiveComponentCandidates).To(HaveKey("worker"))
				Expect(summary.gangSchedulingSummary.effectiveComponentCandidates).To(HaveKey("master"))
				Expect(summary.gangSchedulingSummary.effectiveComponentCandidates).NotTo(HaveKey("pytorch-job"))

				// Worker should have itself as direct candidate
				workerCandidates := summary.gangSchedulingSummary.effectiveComponentCandidates["worker"]
				Expect(workerCandidates).To(HaveLen(1))
				Expect(workerCandidates[0].effectiveComponent).To(Equal("worker"))
				Expect(workerCandidates[0].podGroupName).To(Equal("pytorch-training"))

				// Master should have itself as direct candidate
				masterCandidates := summary.gangSchedulingSummary.effectiveComponentCandidates["master"]
				Expect(masterCandidates).To(HaveLen(1))
				Expect(masterCandidates[0].effectiveComponent).To(Equal("master"))
				Expect(masterCandidates[0].podGroupName).To(Equal("pytorch-training"))
			})
		})

		Context("with multiple pod groups", func() {
			It("should build candidates for all groups correctly", func() {
				karta := &v1alpha1.Karta{
					Spec: v1alpha1.KartaSpec{
						StructureDefinition: v1alpha1.StructureDefinition{
							RootComponent: v1alpha1.ComponentDefinition{
								Name: "cluster",
							},
							ChildComponents: []v1alpha1.ComponentDefinition{
								{
									Name:     "pre-fill",
									OwnerRef: ptr.To("cluster"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodTemplateSpecPath: ptr.To(".spec.pre.template"),
									},
								},
								{
									Name:     "decode",
									OwnerRef: ptr.To("cluster"),
									SpecDefinition: &v1alpha1.SpecDefinition{
										PodTemplateSpecPath: ptr.To(".spec.decode.template"),
									},
								},
							},
						},
						Instructions: v1alpha1.OptimizationInstructions{
							GangScheduling: &v1alpha1.GangSchedulingInstruction{
								PodGroups: []v1alpha1.PodGroupDefinition{
									{
										Name: "group-pre",
										Members: []v1alpha1.PodGroupMemberDefinition{
											{ComponentName: "pre-fill"},
										},
									},
									{
										Name: "group-decode",
										Members: []v1alpha1.PodGroupMemberDefinition{
											{ComponentName: "decode"},
										},
									},
								},
							},
						},
					},
				}

				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())

				// Should have both pod groups
				Expect(summary.gangSchedulingSummary.podGroupsByName).To(HaveKey("group-pre"))
				Expect(summary.gangSchedulingSummary.podGroupsByName).To(HaveKey("group-decode"))

				// should have candidates for both groups
				workerCandidates := summary.gangSchedulingSummary.effectiveComponentCandidates["pre-fill"]
				Expect(workerCandidates).To(HaveLen(1))

				decodeCandidates := summary.gangSchedulingSummary.effectiveComponentCandidates["decode"]
				Expect(decodeCandidates).To(HaveLen(1))
			})
		})

		Context("with component hierarchy and gang scheduling", func() {
			It("should sort candidates by priority correctly", func() {
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
								},
							},
						},
						Instructions: v1alpha1.OptimizationInstructions{
							GangScheduling: &v1alpha1.GangSchedulingInstruction{
								PodGroups: []v1alpha1.PodGroupDefinition{
									// Group 1 with filters
									{
										Name: "worker-group-v1",
										Members: []v1alpha1.PodGroupMemberDefinition{
											{
												ComponentName: "worker",
												Filters: []string{
													`.metadata.labels.version == "v1"`,
												},
											},
										},
									},
									// Fallback group
									{
										Name: "parent-group",
										Members: []v1alpha1.PodGroupMemberDefinition{
											{ComponentName: "pytorch-job"},
										},
									},
									// Group 2 with filters
									{
										Name: "worker-group-v2",
										Members: []v1alpha1.PodGroupMemberDefinition{
											{
												ComponentName: "worker",
												Filters: []string{
													`.metadata.labels.version == "v2"`,
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

				// Worker should have candidates from both groups, but direct mention should come first
				workerCandidates := summary.gangSchedulingSummary.effectiveComponentCandidates["worker"]
				Expect(workerCandidates).To(HaveLen(3))

				// First candidate should be direct mention (worker-group-v1)
				Expect(workerCandidates[0].effectiveComponent).To(Equal("worker"))
				Expect(workerCandidates[0].podGroupName).To(Equal("worker-group-v1"))

				// Second candidate should also be direct mention (worker-group-v2)
				Expect(workerCandidates[1].effectiveComponent).To(Equal("worker"))
				Expect(workerCandidates[1].podGroupName).To(Equal("worker-group-v2"))

				// Second candidate should be the parent fallback (parent-group)
				Expect(workerCandidates[2].effectiveComponent).To(Equal("pytorch-job"))
				Expect(workerCandidates[2].podGroupName).To(Equal("parent-group"))
			})
		})

		Context("with no gang scheduling instructions", func() {
			It("should have nil gang scheduling summary", func() {
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
							// No GangScheduling field
						},
					},
				}

				summary, err := NewStructureSummary(karta)
				Expect(err).NotTo(HaveOccurred())
				Expect(summary.gangSchedulingSummary).To(BeNil())
			})
		})
	})

	Describe("sortEffectiveComponentCandidatesByPriority", func() {
		var summary *StructureSummary

		BeforeEach(func() {
			karta := &v1alpha1.Karta{
				Spec: v1alpha1.KartaSpec{
					StructureDefinition: v1alpha1.StructureDefinition{
						RootComponent: v1alpha1.ComponentDefinition{Name: "root"},
					},
				},
			}
			var err error
			summary, err = NewStructureSummary(karta)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should prioritize direct component mentions first", func() {
			candidates := []effectiveComponentCandidate{
				{effectiveComponent: "parent"},
				{effectiveComponent: "worker"},
				{effectiveComponent: "grandparent"},
				{effectiveComponent: "worker"},
			}

			sorted := summary.sortEffectiveComponentCandidatesByPriority(candidates, "worker")

			// Direct mentions first, then others in original order
			Expect([]string{
				sorted[0].effectiveComponent,
				sorted[1].effectiveComponent,
				sorted[2].effectiveComponent,
				sorted[3].effectiveComponent,
			}).To(Equal([]string{"worker", "worker", "parent", "grandparent"}))
		})

		It("should preserve original order when no direct mentions exist", func() {
			candidates := []effectiveComponentCandidate{
				{effectiveComponent: "parent"},
				{effectiveComponent: "grandparent"},
				{effectiveComponent: "root"},
			}

			sorted := summary.sortEffectiveComponentCandidatesByPriority(candidates, "worker")

			Expect([]string{
				sorted[0].effectiveComponent,
				sorted[1].effectiveComponent,
				sorted[2].effectiveComponent,
			}).To(Equal([]string{"parent", "grandparent", "root"}))
		})

		It("should handle empty list", func() {
			candidates := []effectiveComponentCandidate{}
			sorted := summary.sortEffectiveComponentCandidatesByPriority(candidates, "worker")
			Expect(sorted).To(BeEmpty())
		})
	})

	Describe("hasScaleDefinition field", func() {
		It("should be true when any component has scale definition", func() {
			karta := &v1alpha1.Karta{
				Spec: v1alpha1.KartaSpec{
					StructureDefinition: v1alpha1.StructureDefinition{
						RootComponent: v1alpha1.ComponentDefinition{
							Name: "job",
						},
						ChildComponents: []v1alpha1.ComponentDefinition{
							{
								Name:     "worker",
								OwnerRef: ptr.To("job"),
								ScaleDefinition: &v1alpha1.ScaleDefinition{
									ReplicasPath: ptr.To(".spec.replicas"),
								},
							},
						},
					},
				},
			}

			summary, err := NewStructureSummary(karta)
			Expect(err).NotTo(HaveOccurred())
			Expect(summary.hasScaleDefinition).To(BeTrue())
		})

		It("should be false when no component has scale definition", func() {
			karta := &v1alpha1.Karta{
				Spec: v1alpha1.KartaSpec{
					StructureDefinition: v1alpha1.StructureDefinition{
						RootComponent: v1alpha1.ComponentDefinition{Name: "job"},
						ChildComponents: []v1alpha1.ComponentDefinition{
							{Name: "worker", OwnerRef: ptr.To("job")},
							{Name: "master", OwnerRef: ptr.To("job")},
						},
					},
				},
			}

			summary, err := NewStructureSummary(karta)
			Expect(err).NotTo(HaveOccurred())
			Expect(summary.hasScaleDefinition).To(BeFalse())
		})
	})

	Describe("GetKarta", func() {
		It("should return the original Karta", func() {
			karta := &v1alpha1.Karta{
				Spec: v1alpha1.KartaSpec{
					StructureDefinition: v1alpha1.StructureDefinition{
						RootComponent: v1alpha1.ComponentDefinition{
							Name: "test-job",
						},
					},
				},
			}

			summary, err := NewStructureSummary(karta)
			Expect(err).NotTo(HaveOccurred())
			Expect(summary.GetKarta()).To(BeIdenticalTo(karta))
		})
	})
})
