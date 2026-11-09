// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/discovery"
)

var _ = Describe("Workload metric selector", func() {
	It("keeps Pod info and rejects stale publications while suppressing workload metrics", func() {
		r := newRootLifetimeRuntime(GinkgoTB(), Config{WorkloadMetricSelector: &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "example.org/group", Operator: metav1.LabelSelectorOpDoesNotExist}},
		}})
		podWatch := rootLifetimeWatch(GinkgoTB(), r, podGVK, "", true)
		pod := rootLifetimeObject(podGVK, "ns", "pod", "pod")
		Expect(r.state.applyWatchEvent(podWatch, pod, false)).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		previous := *r.state.deps.Publications.Workload(pod.GetUID())
		selection, err := r.state.lookupDefinition(podGVK)
		Expect(err).NotTo(HaveOccurred())
		oldRef := refOf(pod)
		pod = pod.DeepCopy()
		pod.SetResourceVersion("2")
		pod.SetLabels(map[string]string{"example.org/group": "missing-target"})
		Expect(r.state.applyWatchEvent(podWatch, pod, false)).To(Succeed())
		err = r.state.publishWorkload(pod.GetUID(), oldRef, selection, previous)
		Expect(errors.Is(err, discovery.ErrStale)).To(BeTrue(), "expected ErrStale, got %v", err)
		err = r.state.failWorkload(pod.GetUID(), refOf(pod), discovery.ErrPermissionDenied)
		Expect(errors.Is(err, discovery.ErrPermissionDenied)).To(BeTrue(), "expected ErrPermissionDenied, got %v", err)
		Expect(r.state.deps.Publications.Workload(pod.GetUID())).To(BeNil(), "source errors must not restore suppressed metrics")
		drainRootLifetimeQueues(GinkgoTB(), r)
		Expect(r.state.deps.Publications.Workload(pod.GetUID())).To(BeNil())
		result := r.state.deps.Publications.Pod(pod.GetUID())
		Expect(result.HasValue).To(BeTrue())
		Expect(result.Error).To(BeEmpty())
		Expect(result.Record.Workload.UID).To(Equal(pod.GetUID()))

		pod = pod.DeepCopy()
		pod.SetResourceVersion("3")
		pod.SetLabels(nil)
		Expect(r.state.applyWatchEvent(podWatch, pod, false)).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		Expect(r.state.deps.Publications.Workload(pod.GetUID()).HasValue).To(BeTrue())
		Expect(r.state.deps.Publications.Pod(pod.GetUID()).HasValue).To(BeTrue())
	})
})
