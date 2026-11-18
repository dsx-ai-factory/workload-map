// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package integration

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

var _ = Describe("Karta CRD schema (envtest)", func() {
	AfterEach(func() {
		_ = k8sClient.DeleteAllOf(testCtx, &kartav1alpha1.Karta{})
	})

	// The selector is sent as a raw map because the typed fields use omitempty,
	// which drops an empty keyPath or matchLabels before it reaches the API server.
	DescribeTable("componentTypeSelector",
		func(selector map[string]any, expectedErr string) {
			gvk := schema.GroupVersionKind{Group: "test.run.ai", Version: "v1", Kind: "Foo"}
			content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(newValidKarta("envtest-selector", &gvk))
			Expect(err).NotTo(HaveOccurred())
			obj := &unstructured.Unstructured{Object: content}
			obj.SetGroupVersionKind(kartav1alpha1.GroupVersion.WithKind("Karta"))
			Expect(unstructured.SetNestedField(obj.Object, map[string]any{"componentTypeSelector": selector},
				"spec", "structureDefinition", "rootComponent", "podSelector")).To(Succeed())

			err = k8sClient.Create(testCtx, obj)
			if expectedErr == "" {
				Expect(err).NotTo(HaveOccurred())
				return
			}
			Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected an Invalid error, got %v", err)
			Expect(err.Error()).To(ContainSubstring(expectedErr))
		},
		Entry("accepts keyPath and value",
			map[string]any{"keyPath": ".metadata.labels.role", "value": "worker"}, ""),
		Entry("accepts matchLabels only",
			map[string]any{"matchLabels": map[string]any{"app": "pulsar", "component": "proxy"}}, ""),
		Entry("accepts keyPath and matchLabels",
			map[string]any{"keyPath": ".metadata.annotations.leader", "matchLabels": map[string]any{"app": "pulsar"}}, ""),
		Entry("rejects a selector with no conditions",
			map[string]any{}, "at least one of keyPath or matchLabels must be set"),
		Entry("rejects value without keyPath",
			map[string]any{"value": "worker", "matchLabels": map[string]any{"app": "pulsar"}}, "value requires keyPath"),
		Entry("rejects an empty keyPath",
			map[string]any{"keyPath": ""}, "componentTypeSelector.keyPath: Invalid value: \"\": spec.structureDefinition.rootComponent.podSelector.componentTypeSelector.keyPath in body should be at least 1 chars long"),
		Entry("rejects an empty matchLabels",
			map[string]any{"keyPath": ".metadata.labels.role", "matchLabels": map[string]any{}}, "componentTypeSelector.matchLabels: Invalid value: 0: spec.structureDefinition.rootComponent.podSelector.componentTypeSelector.matchLabels in body should have at least 1 properties"),
	)
})
