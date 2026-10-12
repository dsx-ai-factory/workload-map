// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package integration

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

var _ = Describe("Karta CRD schema (envtest)", func() {
	worker := "worker"

	AfterEach(func() {
		_ = k8sClient.DeleteAllOf(testCtx, &kartav1alpha1.Karta{})
	})

	DescribeTable("componentTypeSelector",
		func(selector *kartav1alpha1.ComponentTypeSelector, expectedErr string) {
			gvk := schema.GroupVersionKind{Group: "test.run.ai", Version: "v1", Kind: "Foo"}
			k := newValidKarta("envtest-selector", &gvk)
			k.Spec.StructureDefinition.RootComponent.PodSelector = &kartav1alpha1.PodSelector{
				ComponentTypeSelector: selector,
			}

			err := k8sClient.Create(testCtx, k)
			if expectedErr == "" {
				Expect(err).NotTo(HaveOccurred())
				return
			}
			Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected an Invalid error, got %v", err)
			Expect(err.Error()).To(ContainSubstring(expectedErr))
		},
		Entry("accepts keyPath and value",
			&kartav1alpha1.ComponentTypeSelector{KeyPath: ".metadata.labels.role", Value: &worker}, ""),
		Entry("accepts matchLabels only",
			&kartav1alpha1.ComponentTypeSelector{MatchLabels: map[string]string{"app": "pulsar", "component": "proxy"}}, ""),
		Entry("accepts keyPath and matchLabels",
			&kartav1alpha1.ComponentTypeSelector{KeyPath: ".metadata.annotations.leader", MatchLabels: map[string]string{"app": "pulsar"}}, ""),
		Entry("rejects a selector with no conditions",
			&kartav1alpha1.ComponentTypeSelector{}, "at least one of keyPath or matchLabels must be set"),
		Entry("rejects value without keyPath",
			&kartav1alpha1.ComponentTypeSelector{Value: &worker, MatchLabels: map[string]string{"app": "pulsar"}}, "value requires keyPath"),
	)
})
