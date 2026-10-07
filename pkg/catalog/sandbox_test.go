// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package catalog

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/catalog/kartas"
	"github.com/dsx-ai-factory/workload-map/pkg/instructions"
	"github.com/dsx-ai-factory/workload-map/pkg/jq/execution"
	"github.com/dsx-ai-factory/workload-map/pkg/resource"
)

// sandboxOwnedPod builds a pod owned by a Sandbox, shaped like the pods the Agent
// Sandbox controller creates (labels set, controller owner) or that an integration
// adds next to it (no Sandbox label, non-controller owner).
func sandboxOwnedPod(name string, labels map[string]string, controller bool) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "agents.x-k8s.io/v1beta1",
				Kind:       "Sandbox",
				Name:       "demo",
				UID:        types.UID("11111111-2222-3333-4444-555555555555"),
				Controller: ptr.To(controller),
			}},
		},
	}
}

var _ = Describe("Sandbox Karta pod mapping", func() {
	var summary *instructions.StructureSummary
	ctx := context.Background()

	BeforeEach(func() {
		var err error
		summary, err = instructions.NewStructureSummary(kartas.Sandbox())
		Expect(err).NotTo(HaveOccurred())
	})

	// component and group resolve a pod's component and its gang group key, the
	// way a scheduler integration does.
	component := func(pod *corev1.Pod) string {
		name, err := instructions.InferPodComponent(ctx, resource.NewPodQuerier(pod), summary)
		Expect(err).NotTo(HaveOccurred())
		return name
	}
	group := func(pod *corev1.Pod) (string, []string) {
		pq := resource.NewPodQuerier(pod)
		eff, err := instructions.GetPodGroupingEffectiveComponent(ctx, pq, component(pod), summary)
		Expect(err).NotTo(HaveOccurred())
		Expect(eff).NotTo(BeNil())
		keys, err := pq.ExtractGroupKeys(ctx, eff.MemberDefinition.GroupByKeyPaths)
		Expect(err).NotTo(HaveOccurred())
		return eff.PodGroupName, keys
	}

	sandboxPod := sandboxOwnedPod("demo", map[string]string{"agents.x-k8s.io/sandbox-name-hash": "1a2b3c4d"}, true)
	companionPod := sandboxOwnedPod("demo-supervisor", map[string]string{"example.com/role": "supervisor"}, false)

	It("maps the Sandbox controller's pod to the agent component", func() {
		Expect(component(sandboxPod)).To(Equal("agent"))
	})

	// The companion component has no pod spec, so it is not a leaf, and
	// InferPodComponent resolves every Sandbox-owned pod to the single leaf.
	It("resolves any other pod owned by the Sandbox to the leaf component", func() {
		Expect(component(companionPod)).To(Equal("agent"))
	})

	// Integrations that build a workload structure match each pod against every
	// component's selector, so the selectors must split Sandbox-owned pods
	// between agent and companion with no overlap.
	It("selects each Sandbox-owned pod into exactly one of agent and companion", func() {
		selectors := map[string]*v1alpha1.ComponentTypeSelector{}
		for _, c := range kartas.Sandbox().Spec.StructureDefinition.ChildComponents {
			selectors[c.Name] = c.PodSelector.ComponentTypeSelector
		}
		matching := func(pod *corev1.Pod) []string {
			var names []string
			for _, name := range []string{"agent", "companion"} {
				ok, err := resource.NewPodQuerier(pod).MatchesComponentType(ctx, selectors[name])
				Expect(err).NotTo(HaveOccurred())
				if ok {
					names = append(names, name)
				}
			}
			return names
		}
		Expect(matching(sandboxPod)).To(Equal([]string{"agent"}))
		Expect(matching(companionPod)).To(Equal([]string{"companion"}))
		unowned := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "default"}}
		Expect(matching(unowned)).To(BeEmpty())
	})

	It("keys on the Agent Sandbox owner when another API group also has a Sandbox kind", func() {
		mixed := sandboxOwnedPod("demo-proxy", map[string]string{"example.com/role": "proxy"}, false)
		mixed.OwnerReferences = append([]metav1.OwnerReference{{
			APIVersion: "sandbox.example.com/v1",
			Kind:       "Sandbox",
			Name:       "other",
			UID:        types.UID("99999999-8888-7777-6666-555555555555"),
		}}, mixed.OwnerReferences...)
		_, keys := group(mixed)
		Expect(keys).To(Equal([]string{"11111111-2222-3333-4444-555555555555"}))
	})

	It("gangs the Sandbox pod and other pods it owns on the owning Sandbox", func() {
		agentGroup, agentKeys := group(sandboxPod)
		companionGroup, companionKeys := group(companionPod)
		Expect(agentGroup).To(Equal("sandbox"))
		Expect(companionGroup).To(Equal(agentGroup))
		Expect(agentKeys).To(Equal([]string{"11111111-2222-3333-4444-555555555555"}))
		Expect(companionKeys).To(Equal(agentKeys))
	})
})

var _ = Describe("Sandbox Karta status mapping", func() {
	ctx := context.Background()

	// status resolves the Sandbox statuses for the given operating mode and conditions.
	status := func(mode string, conditions ...map[string]any) []v1alpha1.ResourceStatus {
		conds := make([]any, len(conditions))
		for i, c := range conditions {
			conds[i] = c
		}
		obj := map[string]any{
			"spec":   map[string]any{"operatingMode": mode},
			"status": map[string]any{"conditions": conds},
		}
		accessor := resource.NewAccessor(execution.NewDefaultRunner(obj))
		result, err := accessor.ExtractStatus(ctx, kartas.Sandbox().Spec.StructureDefinition.RootComponent)
		Expect(err).NotTo(HaveOccurred())
		return result.MatchedStatuses
	}
	cond := func(t, s string) map[string]any { return map[string]any{"type": t, "status": s} }

	It("resolves a ready Sandbox to Running", func() {
		Expect(status("Running", cond("Ready", "True"), cond("Suspended", "False"))).
			To(ConsistOf(v1alpha1.RunningStatus))
	})

	It("resolves a ready Sandbox without a Suspended condition to Running", func() {
		Expect(status("Running", cond("Ready", "True"))).To(ConsistOf(v1alpha1.RunningStatus))
	})

	It("resolves a resumed Sandbox with a stale Suspended condition to Resuming only", func() {
		Expect(status("Running", cond("Ready", "True"), cond("Suspended", "True"))).
			To(ConsistOf(v1alpha1.ResumingStatus))
	})
})
