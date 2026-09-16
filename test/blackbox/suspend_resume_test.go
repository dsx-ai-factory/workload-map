// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package blackbox

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/jq/execution"
	"github.com/dsx-ai-factory/workload-map/pkg/resource"
	"github.com/dsx-ai-factory/workload-map/test/types"
)

// These tests exercise the full suspend/resume path end-to-end:
// types.SuspendablePyFlowKarta → ComponentFactory → Accessor (real JQ runner) → Component.
// No mocks are used so every layer is covered.
var _ = Describe("Suspend and Resume (integration)", func() {
	var (
		ctx       context.Context
		karta     *v1alpha1.Karta
		pyflow    *types.PyFlow
		accessor  *resource.Accessor
		component *resource.Component
	)

	// sharedSetup wires a real accessor and component to the same JQ runner so
	// that mutations applied through component.Suspend/Resume are visible via
	// accessor.GetObject().
	sharedSetup := func(k *v1alpha1.Karta, obj *types.PyFlow, name string) (*resource.Accessor, *resource.Component) {
		runner := execution.NewDefaultRunner(obj)
		a := resource.NewAccessor(runner)
		factory := resource.NewComponentFactory(k, a)
		comp, err := factory.GetComponent(name)
		Expect(err).NotTo(HaveOccurred())
		return a, comp
	}

	BeforeEach(func() {
		ctx = context.Background()
		karta = types.SuspendablePyFlowKarta()
		pyflow = types.NewPyFlowObject()
		accessor, component = sharedSetup(karta, pyflow, "pyflow")
	})

	Describe("HasSuspendDefinition", func() {
		It("should return true for a component with a SuspendDefinition", func() {
			Expect(component.HasSuspendDefinition()).To(BeTrue())
		})

		It("should return false when SuspendDefinition is absent", func() {
			_, plain := sharedSetup(types.PyFlowKarta(), pyflow, "pyflow")
			Expect(plain.HasSuspendDefinition()).To(BeFalse())
		})
	})

	Describe("Suspend", func() {
		It("should set .spec.suspend = true on the underlying object", func() {
			Expect(component.Suspend(ctx)).To(Succeed())

			obj, err := accessor.GetObject()
			Expect(err).NotTo(HaveOccurred())
			Expect(obj["spec"].(map[string]any)["suspend"]).To(BeTrue())
		})

		It("should be idempotent when called twice", func() {
			Expect(component.Suspend(ctx)).To(Succeed())
			Expect(component.Suspend(ctx)).To(Succeed())

			obj, err := accessor.GetObject()
			Expect(err).NotTo(HaveOccurred())
			Expect(obj["spec"].(map[string]any)["suspend"]).To(BeTrue())
		})

		It("should be a no-op when the component has no SuspendDefinition", func() {
			_, plain := sharedSetup(types.PyFlowKarta(), pyflow, "pyflow")
			Expect(plain.Suspend(ctx)).To(Succeed())
		})
	})

	Describe("Resume", func() {
		It("should set .spec.suspend = false on the underlying object", func() {
			Expect(component.Suspend(ctx)).To(Succeed())
			Expect(component.Resume(ctx)).To(Succeed())

			obj, err := accessor.GetObject()
			Expect(err).NotTo(HaveOccurred())
			Expect(obj["spec"].(map[string]any)["suspend"]).To(BeFalse())
		})

		It("should be idempotent when called twice", func() {
			Expect(component.Suspend(ctx)).To(Succeed())
			Expect(component.Resume(ctx)).To(Succeed())
			Expect(component.Resume(ctx)).To(Succeed())

			obj, err := accessor.GetObject()
			Expect(err).NotTo(HaveOccurred())
			Expect(obj["spec"].(map[string]any)["suspend"]).To(BeFalse())
		})

		It("should be a no-op when the component has no SuspendDefinition", func() {
			_, plain := sharedSetup(types.PyFlowKarta(), pyflow, "pyflow")
			Expect(plain.Resume(ctx)).To(Succeed())
		})
	})

	Describe("Suspend then Resume cycle", func() {
		It("should correctly toggle the suspend field through a full cycle", func() {
			obj, err := accessor.GetObject()
			Expect(err).NotTo(HaveOccurred())
			_, hasSuspend := obj["spec"].(map[string]any)["suspend"]
			Expect(hasSuspend).To(BeFalse(), "suspend field should not exist before first suspend")

			Expect(component.Suspend(ctx)).To(Succeed())
			obj, err = accessor.GetObject()
			Expect(err).NotTo(HaveOccurred())
			Expect(obj["spec"].(map[string]any)["suspend"]).To(BeTrue())

			Expect(component.Resume(ctx)).To(Succeed())
			obj, err = accessor.GetObject()
			Expect(err).NotTo(HaveOccurred())
			Expect(obj["spec"].(map[string]any)["suspend"]).To(BeFalse())
		})
	})

	Describe("Multi-action SuspendDefinition", func() {
		It("should apply all suspend actions in sequence", func() {
			karta.Spec.StructureDefinition.RootComponent.SuspendDefinition = &v1alpha1.SuspendDefinition{
				SuspendActions: []v1alpha1.SuspendAction{
					{Path: ".spec.suspend", Value: "true"},
					{Path: `.metadata.annotations["suspended-by"]`, Value: `"karta"`},
				},
				ResumeActions: []v1alpha1.SuspendAction{
					{Path: ".spec.suspend", Value: "false"},
					{Path: `.metadata.annotations["suspended-by"]`, Value: "null"},
				},
			}
			accessor, component = sharedSetup(karta, pyflow, "pyflow")

			Expect(component.Suspend(ctx)).To(Succeed())

			obj, err := accessor.GetObject()
			Expect(err).NotTo(HaveOccurred())
			Expect(obj["spec"].(map[string]any)["suspend"]).To(BeTrue())
			annotations := obj["metadata"].(map[string]any)["annotations"].(map[string]any)
			Expect(annotations["suspended-by"]).To(Equal("karta"))
		})
	})

	Describe("Status detection", func() {
		It("should report SuspendedStatus when the Suspended condition is True", func() {
			pyflow.Status.Conditions = []metav1.Condition{
				{Type: "Suspended", Status: metav1.ConditionTrue},
			}
			accessor, component = sharedSetup(karta, pyflow, "pyflow")

			status, err := component.GetStatus(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(status.MatchedStatuses).To(ConsistOf(v1alpha1.SuspendedStatus))
		})

		It("should report SuspendingStatus when the Suspending condition is True", func() {
			pyflow.Status.Conditions = []metav1.Condition{
				{Type: "Suspending", Status: metav1.ConditionTrue},
			}
			accessor, component = sharedSetup(karta, pyflow, "pyflow")

			status, err := component.GetStatus(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(status.MatchedStatuses).To(ConsistOf(v1alpha1.SuspendingStatus))
		})

		It("should report ResumingStatus when the Resuming condition is True", func() {
			pyflow.Status.Conditions = []metav1.Condition{
				{Type: "Resuming", Status: metav1.ConditionTrue},
			}
			accessor, component = sharedSetup(karta, pyflow, "pyflow")

			status, err := component.GetStatus(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(status.MatchedStatuses).To(ConsistOf(v1alpha1.ResumingStatus))
		})
	})
})
