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

	"github.com/dsx-ai-factory/workload-map/pkg/catalog/kartas"
	"github.com/dsx-ai-factory/workload-map/pkg/instructions"
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

	It("maps any other pod owned by the Sandbox to the same component", func() {
		Expect(component(companionPod)).To(Equal("agent"))
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
