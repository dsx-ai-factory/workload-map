// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var _ = Describe("Object selectors", func() {
	workloadGVK := schema.GroupVersionKind{Group: "example.org", Version: "v1", Kind: "Workload"}
	global := ObjectSelector{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"environment": "production"}}}
	workload := ObjectSelector{APIVersion: "example.org/v1", Kind: "Workload", Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"managed": "true"}}}
	DescribeTable("matches objects using global and exact GVK selectors",
		func(rules []ObjectSelector, gvk schema.GroupVersionKind, labels map[string]string, matches bool) {
			selectors, err := compileObjectSelectors(rules)
			Expect(err).NotTo(HaveOccurred())
			r := controllerState{objectSelectors: selectors}
			object := &unstructured.Unstructured{}
			object.SetGroupVersionKind(gvk)
			object.SetLabels(labels)
			Expect(r.isObjectSelected(object)).To(Equal(matches))
		},
		Entry("no selectors", nil, workloadGVK, nil, true),
		Entry("empty list", []ObjectSelector{}, workloadGVK, nil, true),
		Entry("empty selector", []ObjectSelector{{}}, workloadGVK, nil, true),
		Entry("global matches Pod", []ObjectSelector{global}, podGVK, map[string]string{"environment": "production"}, true),
		Entry("global rejects Pod", []ObjectSelector{global}, podGVK, nil, false),
		Entry("exact GVK matches", []ObjectSelector{workload}, workloadGVK, map[string]string{"managed": "true"}, true),
		Entry("exact GVK rejects", []ObjectSelector{workload}, workloadGVK, nil, false),
		Entry("other kind is unrestricted", []ObjectSelector{workload}, podGVK, nil, true),
		Entry("other version is unrestricted", []ObjectSelector{workload}, schema.GroupVersionKind{Group: "example.org", Version: "v2", Kind: "Workload"}, nil, true),
		Entry("other group is unrestricted", []ObjectSelector{workload}, schema.GroupVersionKind{Group: "other.example.org", Version: "v1", Kind: "Workload"}, nil, true),
		Entry("both selectors match", []ObjectSelector{global, workload}, workloadGVK, map[string]string{"environment": "production", "managed": "true"}, true),
		Entry("global rejects despite exact GVK match", []ObjectSelector{global, workload}, workloadGVK, map[string]string{"managed": "true"}, false),
		Entry("exact GVK rejects despite global match", []ObjectSelector{global, workload}, workloadGVK, map[string]string{"environment": "production"}, false),
		Entry("global applies to other kinds", []ObjectSelector{global, workload}, podGVK, nil, false),
		Entry("only global applies to other kinds", []ObjectSelector{global, workload}, podGVK, map[string]string{"environment": "production"}, true),
	)

	It("updates Pod associations as target labels change", func() {
		r, podWatch, targetWatch := newAssociationRuntime()
		var err error
		r.state.objectSelectors, err = compileObjectSelectors([]ObjectSelector{{APIVersion: associationTestGVK.GroupVersion().String(), Kind: associationTestGVK.Kind, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"managed": "true"}}}})
		Expect(err).NotTo(HaveOccurred())
		pod := associationPod("member", "pod")
		target := rootLifetimeObject(associationTestGVK, "ns", "group", "target")
		target.SetOwnerReferences([]metav1.OwnerReference{associationOwner(pod)})
		Expect(r.state.isObjectSelected(pod)).To(BeTrue(), "other kinds remain selected")
		Expect(r.state.applyWatchEvent(podWatch, pod, false)).To(Succeed())
		for i, selected := range []bool{false, true, false, true} {
			target = target.DeepCopy()
			target.SetResourceVersion(string(rune('1' + i)))
			target.SetLabels(nil)
			if selected {
				target.SetLabels(map[string]string{"managed": "true"})
			}
			Expect(r.state.applyWatchEvent(targetWatch, target, false)).To(Succeed())
			drainRootLifetimeQueues(GinkgoTB(), r)
			result := r.state.deps.Publications.Pod(pod.GetUID())
			Expect(result).NotTo(BeNil())
			if selected {
				Expect(result.Record.Workload.UID).To(Equal(target.GetUID()))
				Expect(result.Error).To(BeEmpty())
			} else {
				Expect(result.Record.Workload.UID).To(Equal(pod.GetUID()))
				Expect(result.Error).To(Equal("unresolved_owner"))
				Expect(r.state.deps.Publications.Workload(target.GetUID())).To(BeNil())
			}
		}
	})

	Describe("validation", func() {
		valid := ObjectSelector{APIVersion: "example.org/v1", Kind: "Workload"}
		invalidSelector := &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "managed", Operator: "Bad"}}}
		DescribeTable("rejects invalid selectors",
			func(rules []ObjectSelector) {
				Expect(ValidateObjectSelectors(rules)).To(HaveOccurred())
			},
			Entry("missing apiVersion", []ObjectSelector{{Kind: "Workload"}}),
			Entry("missing kind", []ObjectSelector{{APIVersion: "v1"}}),
			Entry("invalid apiVersion", []ObjectSelector{{APIVersion: "example.org/", Kind: "Workload"}}),
			Entry("invalid group", []ObjectSelector{{APIVersion: "bad group/v1", Kind: "Workload"}}),
			Entry("invalid kind", []ObjectSelector{{APIVersion: "v1", Kind: "*"}}),
			Entry("duplicate exact GVK", []ObjectSelector{valid, valid}),
			Entry("duplicate global", []ObjectSelector{{}, {}}),
			Entry("invalid exact GVK selector", []ObjectSelector{{APIVersion: "v1", Kind: "Pod", Selector: invalidSelector}}),
			Entry("invalid global selector", []ObjectSelector{{Selector: invalidSelector}}),
		)

		DescribeTable("accepts valid selectors",
			func(rules []ObjectSelector) {
				Expect(ValidateObjectSelectors(rules)).To(Succeed())
			},
			Entry("one exact GVK", []ObjectSelector{valid}),
			Entry("global and exact GVK", []ObjectSelector{{}, valid}),
			Entry("different versions of the same kind", []ObjectSelector{valid, {APIVersion: "example.org/v2", Kind: "Workload"}}),
		)
	})
})
