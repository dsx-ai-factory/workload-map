// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Selection transitions through the API", func() {
	DescribeTable("withdraws and restores only matching observations", func(ctx SpecContext, selectPods bool) {
		ns := namespace(ctx)
		root := deployment(ctx, ns, "moving-root")
		p := pod(ctx, replicaSet(ctx, root, "moving-middle"), "moving-pod")
		stable := deployment(ctx, ns, "stable-root")
		sibling := pod(ctx, replicaSet(ctx, stable, "stable-middle"), "stable-pod")
		const label = "example.com/selected"
		for _, object := range []client.Object{root, p, stable, sibling} {
			labels := object.GetLabels()
			labels[label] = "yes"
			object.SetLabels(labels)
			Expect(api.Update(ctx, object)).To(Succeed())
		}
		cfg := defaults(ns)
		selector := &metav1.LabelSelector{MatchLabels: map[string]string{label: "yes"}}
		var changed client.Object = root
		if selectPods {
			cfg.PodSelector, changed = selector, p
		} else {
			cfg.ObjectSelector = selector
		}
		components, _ := start(cfg)
		check := func(g Gomega, selected bool) {
			view := components.Store.Capture()
			if selected {
				expectPod(g, view, p, root)
			} else {
				g.Expect(podResult(view, p.UID)).To(BeNil())
			}
			if selected || selectPods {
				result := expectWorkload(g, view, root)
				g.Expect(result.Record.HasStatus).To(BeTrue())
				g.Expect(result.StatusError).To(BeEmpty())
			} else {
				g.Expect(workloadResult(view, root.UID)).To(BeNil())
			}
			expectPod(g, view, sibling, stable)
			g.Expect(view.Failures).To(BeEmpty(), "selection is not an observation failure")
			healthy := map[string]bool{}
			for key, health := range view.Sources {
				if key.Namespace == ns && (health.GVK.Kind == "Pod" || health.GVK.Kind == "Deployment") {
					g.Expect(health.Synced).To(BeTrue())
					g.Expect(health.Error).To(BeEmpty())
					healthy[health.GVK.Kind] = true
				}
			}
			g.Expect(healthy).To(HaveKey("Pod"))
			g.Expect(healthy).To(HaveKey("Deployment"))
		}
		Eventually(func(g Gomega) { check(g, true) }).Should(Succeed())
		for _, selected := range []bool{false, true} {
			Expect(api.Get(ctx, client.ObjectKeyFromObject(changed), changed)).To(Succeed())
			labels := changed.GetLabels()
			if selected {
				labels[label] = "yes"
			} else {
				delete(labels, label)
			}
			changed.SetLabels(labels)
			Expect(api.Update(ctx, changed)).To(Succeed())
			Eventually(func(g Gomega) { check(g, selected) }).Should(Succeed())
			Consistently(func(g Gomega) { check(g, selected) }, time.Second).Should(Succeed())
		}
	}, Entry("PodSelector", true), Entry("ObjectSelector", false))
})
