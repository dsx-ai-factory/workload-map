// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartadefinitioninventory

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

var _ = Describe("Experimental definitions", func() {
	DescribeTable("gates catalog entries even when explicitly included",
		func(enabled bool) {
			gvk := schema.GroupVersionKind{Group: "kueue.x-k8s.io", Version: "v1beta2", Kind: "Workload"}
			inventory := loadedInventory(Config{
				EnableExperimental: enabled,
				Catalog: SourceConfig{Enabled: true, Include: []string{
					"kueue-x-k8s-io-workload-v1beta2", "batch-job-v1",
				}},
			})
			selection, err := inventory.Lookup(gvk)
			Expect(err).NotTo(HaveOccurred())
			if enabled {
				Expect(selection.Origin.Name).To(Equal("kueue-x-k8s-io-workload-v1beta2"))
				Expect(selection.Definition.Spec.Experimental).To(BeTrue())
			} else {
				Expect(selection).To(Equal(Selection{}))
			}
			Expect(lookupOrigin(inventory, jobGVK)).To(Equal(catalogJobOrigin))
			results, err := inventory.List()
			Expect(err).NotTo(HaveOccurred())
			if enabled {
				Expect(results).To(HaveLen(2))
			} else {
				Expect(results).To(HaveLen(1))
			}
		},
		Entry("default", false),
		Entry("opted in", true),
	)

	DescribeTable("filters cluster candidates before precedence",
		func(enabled, stableCluster, excluded bool, expected string) {
			experimental := newKarta("experimental-job", jobGVK)
			experimental.Spec.Experimental = true
			definitions := []*v1alpha1.Karta{experimental}
			if stableCluster {
				definitions = append(definitions, newKarta("stable-job", jobGVK, createdAt(epoch)))
			}
			config := Config{
				EnableExperimental: enabled,
				Catalog:            SourceConfig{Enabled: true},
				Cluster:            SourceConfig{Enabled: true, Include: []string{"experimental-job", "stable-job"}},
			}
			if excluded {
				config.Cluster.Exclude = []string{"experimental-job"}
			}
			inventory := loadedInventory(config, definitions...)
			Expect(inventoryHealth(inventory)()).To(Succeed())
			Expect(lookupOrigin(inventory, jobGVK).Name).To(Equal(expected))
		},
		Entry("stable catalog fallback", false, false, false, "batch-job-v1"),
		Entry("stable cluster fallback", false, true, false, "stable-job"),
		Entry("opt-in uses oldest cluster candidate", true, true, false, "experimental-job"),
		Entry("opt-in still respects excludes", true, true, true, "stable-job"),
	)

	It("signals eligibility transitions and applies the gate to complete relists", func() {
		definition := newKarta("cluster-job", jobGVK)
		definition.Spec.Experimental = true
		inventory := loadedInventory(withCatalog, definition)
		store := reflectorStore{inventory: inventory}
		Expect(lookupOrigin(inventory, jobGVK)).To(Equal(catalogJobOrigin))
		Expect(inventory.Changes()).To(Receive())

		for _, experimental := range []bool{false, true, false} {
			definition.Spec.Experimental = experimental
			Expect(store.Update(unstructuredKarta(definition))).To(Succeed())
			Expect(inventory.Changes()).To(Receive())
			want := "cluster-job"
			if experimental {
				want = "batch-job-v1"
			}
			Expect(lookupOrigin(inventory, jobGVK).Name).To(Equal(want))
		}
		Expect(store.Delete(unstructuredKarta(definition))).To(Succeed())
		Expect(inventory.Changes()).To(Receive())
		Expect(lookupOrigin(inventory, jobGVK)).To(Equal(catalogJobOrigin))
		Expect(store.Add(unstructuredKarta(definition))).To(Succeed())
		Expect(inventory.Changes()).To(Receive())
		Expect(lookupOrigin(inventory, jobGVK).Name).To(Equal("cluster-job"))

		definition.Spec.Experimental = true
		Expect(store.Replace([]any{unstructuredKarta(definition)}, "2")).To(Succeed())
		Expect(inventory.Changes()).To(Receive())
		Expect(lookupOrigin(inventory, jobGVK)).To(Equal(catalogJobOrigin))
		Expect(store.Delete(unstructuredKarta(definition))).To(Succeed())
		Expect(inventory.Changes()).NotTo(Receive())
		Expect(inventoryHealth(inventory)()).To(Succeed())
	})

	It("skips excluded experimental definitions before decoding or validation", func() {
		inventory := loadedInventory(withCatalog)
		store := reflectorStore{inventory: inventory}
		definition := newKarta("invalid-job", jobGVK, invalid)
		definition.Spec.Experimental = true
		object := unstructuredKarta(definition)
		Expect(store.Add(object)).To(Succeed())
		Expect(lookupOrigin(inventory, jobGVK)).To(Equal(catalogJobOrigin))

		Expect(unstructured.SetNestedField(object.Object, "invalid", "spec", "structureDefinition")).To(Succeed())
		Expect(store.Replace([]any{object}, "2")).To(Succeed())
		Expect(inventoryHealth(inventory)()).To(Succeed())
		Expect(lookupOrigin(inventory, jobGVK)).To(Equal(catalogJobOrigin))
		Expect(unstructured.SetNestedField(object.Object, false, "spec", "experimental")).To(Succeed())
		Expect(store.Update(object)).To(Succeed())
		Expect(inventoryHealth(inventory)()).To(MatchError(ErrUnavailable))
		Expect(unstructured.SetNestedField(object.Object, true, "spec", "experimental")).To(Succeed())
		Expect(store.Update(object)).To(Succeed())
		Expect(inventoryHealth(inventory)()).To(Succeed())
	})

	It("reports a malformed experimental flag as a decode failure", func() {
		inventory := loadedInventory(withCatalog)
		object := unstructuredKarta(newKarta("malformed", jobGVK))
		Expect(unstructured.SetNestedField(object.Object, "true", "spec", "experimental")).To(Succeed())
		Expect((reflectorStore{inventory: inventory}).Add(object)).To(Succeed())
		Expect(inventoryHealth(inventory)()).To(MatchError(ErrUnavailable))
	})
})
