// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

var _ = Describe("Zero-Pod candidate admission through the API", func() {
	It("discovers an unsupported generic ancestor from a catalog-selected child", func(ctx SpecContext) {
		ns := namespace(ctx)
		ancestor := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "generic-parent", Namespace: ns}}
		Expect(api.Create(ctx, ancestor)).To(Succeed())
		child := deployment(ctx, ns, "selected-child")
		child.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: ancestor.Name, UID: ancestor.UID, Controller: ptr.To(true)}}
		Expect(api.Update(ctx, child)).To(Succeed())
		components, seen := start(defaults(ns))

		check := func(g Gomega) {
			view := components.Store.Capture()
			expectGenericCandidateRoot(g, view, ancestor, "ConfigMap")
			g.Expect(workloadResult(view, child.UID)).To(BeNil())
		}
		Eventually(check).Should(Succeed())
		Eventually(func(g Gomega) {
			seen.mu.Lock()
			defer seen.mu.Unlock()
			path := "/api/v1/namespaces/" + ns + "/configmaps"
			g.Expect(seen.metadataWatches[path]).To(Equal(1))
			g.Expect(seen.watches[path]).To(Equal(2), "the generic root must replace its metadata watch with a full-object watch")
		}).Should(Succeed())
		// Keep the quiet path valid without requiring Pod events.
		Consistently(check, 3*time.Second).Should(Succeed())
		pods := &corev1.PodList{}
		Expect(api.List(ctx, pods, client.InNamespace(ns))).To(Succeed())
		Expect(pods.Items).To(BeEmpty())
	})

	It("follows generic ancestor reparenting without a child or Pod event", func(ctx SpecContext) {
		ns := namespace(ctx)
		ancestor := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "moving-parent", Namespace: ns}}
		Expect(api.Create(ctx, ancestor)).To(Succeed())
		child := deployment(ctx, ns, "quiet-child")
		child.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: ancestor.Name, UID: ancestor.UID, Controller: ptr.To(true)}}
		Expect(api.Update(ctx, child)).To(Succeed())
		childUID, childVersion := child.UID, child.ResourceVersion
		components, _ := start(defaults(ns))
		Eventually(func(g Gomega) {
			expectGenericCandidateRoot(g, components.Store.Capture(), ancestor, "ConfigMap")
		}).Should(Succeed())

		parent := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "generic-root-"}}
		Expect(api.Create(ctx, parent)).To(Succeed())
		Expect(api.Get(ctx, client.ObjectKeyFromObject(ancestor), ancestor)).To(Succeed())
		ancestor.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "Namespace", Name: parent.Name, UID: parent.UID, Controller: ptr.To(true)}}
		Expect(api.Update(ctx, ancestor)).To(Succeed())

		check := func(g Gomega) {
			view := components.Store.Capture()
			expectGenericCandidateRoot(g, view, parent, "Namespace")
			for _, uid := range []types.UID{child.UID, ancestor.UID} {
				g.Expect(workloadResult(view, uid)).To(BeNil())
			}
		}
		Eventually(check).Should(Succeed())
		Consistently(check, 3*time.Second).Should(Succeed())
		Expect(api.Get(ctx, client.ObjectKeyFromObject(child), child)).To(Succeed())
		Expect(child.UID).To(Equal(childUID))
		Expect(child.ResourceVersion).To(Equal(childVersion))
		pods := &corev1.PodList{}
		Expect(api.List(ctx, pods, client.InNamespace(ns))).To(Succeed())
		Expect(pods.Items).To(BeEmpty())
	})
})

func expectGenericCandidateRoot(g Gomega, view store.CapturedView, root client.Object, kind string) {
	g.Expect(view.Pods).To(BeEmpty())
	g.Expect(view.Failures).To(BeEmpty())
	result := expectWorkload(g, view, root)
	g.Expect(result.Record.HasStatus).To(BeFalse())
	g.Expect(result.StatusError).To(BeEmpty())
	g.Expect(result.Record.Ref.GVK.Kind).To(Equal(kind))
	healthy := false
	for key, health := range view.Sources {
		if health.GVK.Kind == kind && key.Namespace == root.GetNamespace() {
			g.Expect(health.Synced).To(BeTrue())
			g.Expect(health.Error).To(BeEmpty())
			healthy = true
		}
	}
	g.Expect(healthy).To(BeTrue(), "the generic root resource must have healthy collection")
}
