// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

var _ = Describe("Identity lifecycle through the API", func() {
	It("stops at the ReplicaSet below an explicitly ignored Deployment", func(ctx SpecContext) {
		ns := namespace(ctx)
		root := deployment(ctx, ns, "ignored-deployment")
		rs := replicaSet(ctx, root, "observed-replicaset")
		p := pod(ctx, rs, "observed-pod")
		cfg := defaults(ns)
		cfg.KartaDefinitionSource.Catalog.Enabled = false
		cfg.IgnoredOwnerKinds = []schema.GroupVersionKind{deployGVK}
		components, _ := start(cfg)
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			result := expectPod(g, view, p, rs)
			g.Expect(result.Record.Workload.Name).To(Equal(rs.Name))
			g.Expect(result.Record.Workload.GVK).To(Equal(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"}))
			g.Expect(result.Record.OwnerResolution).To(Equal(store.IgnoredOwner))
			g.Expect(result.Record.Component).To(Equal("<unknown>"))
			g.Expect(result.Record.Instance).To(Equal("<unknown>"))
			g.Expect(result.Record.Group).To(BeEmpty())
			g.Expect(workloadResult(view, root.UID)).To(BeNil())
			expectWorkload(g, view, rs)
		}).Should(Succeed())
	})

	It("replaces same-name Pods and roots without retaining old records or changing a sibling", func(ctx SpecContext) {
		ns := namespace(ctx)
		root := deployment(ctx, ns, "recreated-root")
		rs := replicaSet(ctx, root, "recreated-middle")
		p := pod(ctx, rs, "recreated-pod")
		stable := deployment(ctx, ns, "stable-root")
		sibling := pod(ctx, replicaSet(ctx, stable, "stable-middle"), "stable-pod")
		components, _ := start(defaults(ns))
		checkSibling := func(g Gomega, view store.CapturedView) { expectPod(g, view, sibling, stable) }
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			expectPod(g, view, p, root)
			checkSibling(g, view)
		}).Should(Succeed())
		oldPodUID := p.UID
		Expect(api.Delete(ctx, p, client.GracePeriodSeconds(0))).To(Succeed())
		Eventually(func() bool { return apierrors.IsNotFound(api.Get(ctx, client.ObjectKeyFromObject(p), &corev1.Pod{})) }).Should(BeTrue())
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			g.Expect(podResult(view, oldPodUID)).To(BeNil())
			checkSibling(g, view)
		}).Should(Succeed())
		p = pod(ctx, rs, p.Name)
		Expect(p.UID).NotTo(Equal(oldPodUID))
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			expectPod(g, view, p, root)
			g.Expect(podResult(view, oldPodUID)).To(BeNil())
			checkSibling(g, view)
		}).Should(Succeed())

		oldRootUID, secondPodUID := root.UID, p.UID
		// envtest has no garbage collector, so remove the explicit child fixtures.
		for _, object := range []client.Object{p, rs, root} {
			Expect(api.Delete(ctx, object, client.GracePeriodSeconds(0))).To(Succeed())
		}
		Eventually(func() bool {
			return apierrors.IsNotFound(api.Get(ctx, client.ObjectKeyFromObject(root), &appsv1.Deployment{}))
		}).Should(BeTrue())
		Eventually(func() bool {
			return apierrors.IsNotFound(api.Get(ctx, client.ObjectKeyFromObject(rs), &appsv1.ReplicaSet{}))
		}).Should(BeTrue())
		Eventually(func() bool { return apierrors.IsNotFound(api.Get(ctx, client.ObjectKeyFromObject(p), &corev1.Pod{})) }).Should(BeTrue())
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			g.Expect(workloadResult(view, oldRootUID)).To(BeNil())
			g.Expect(podResult(view, secondPodUID)).To(BeNil())
			checkSibling(g, view)
		}).Should(Succeed())
		root = deployment(ctx, ns, root.Name)
		rs = replicaSet(ctx, root, rs.Name)
		p = pod(ctx, rs, p.Name)
		Expect(root.UID).NotTo(Equal(oldRootUID))
		Expect(p.UID).NotTo(BeElementOf(oldPodUID, secondPodUID))
		checkRecreated := func(g Gomega) {
			view := components.Store.Capture()
			expectPod(g, view, p, root)
			g.Expect(workloadResult(view, oldRootUID)).To(BeNil())
			for _, uid := range []types.UID{oldPodUID, secondPodUID} {
				g.Expect(podResult(view, uid)).To(BeNil())
			}
			result := expectWorkload(g, view, root)
			g.Expect(result.Record.HasStatus).To(BeTrue())
			g.Expect(result.StatusError).To(BeEmpty())
			checkSibling(g, view)
		}
		Eventually(checkRecreated).Should(Succeed())
		Consistently(checkRecreated, time.Second).Should(Succeed())
	})
})
