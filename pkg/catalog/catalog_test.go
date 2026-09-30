// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package catalog

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/catalog/kartas"
)

// catalogDir returns the docs/catalog directory relative to this test file.
func catalogDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	Expect(ok).To(BeTrue(), "cannot locate test file")
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "docs", "catalog")
}

var _ = Describe("Get", func() {
	It("resolves a known workload GVK to its built-in Karta", func() {
		gvk := schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}

		k, err := Get(gvk)

		Expect(err).NotTo(HaveOccurred())
		Expect(k.Spec.StructureDefinition.RootComponent.Kind).NotTo(BeNil())
		Expect(k.Spec.StructureDefinition.RootComponent.Kind.Kind).To(Equal("Job"))
	})

	It("returns ErrNotFound for an unregistered GVK", func() {
		_, err := Get(schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Nope"})

		Expect(err).To(MatchError(ErrNotFound))
	})

	It("returns a deep copy so mutations do not affect the catalog", func() {
		gvk := schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
		first, err := Get(gvk)
		Expect(err).NotTo(HaveOccurred())
		original := first.Spec.StructureDefinition.RootComponent.Name

		first.Spec.StructureDefinition.RootComponent.Name = original + "-mutated"

		second, err := Get(gvk)
		Expect(err).NotTo(HaveOccurred())
		Expect(second.Spec.StructureDefinition.RootComponent.Name).To(Equal(original))
	})
})

var _ = Describe("List", func() {
	It("is sorted by GVK and stable across calls", func() {
		first := List()
		second := List()

		Expect(second).To(HaveLen(len(first)))
		for i := range first {
			Expect(RootKey(first[i])).To(Equal(RootKey(second[i])), "List order not stable at %d", i)
			if i > 0 {
				prev := RootKey(first[i-1])
				Expect(prev.String() <= RootKey(first[i]).String()).To(BeTrue(),
					"List not sorted: %s before %s", prev, RootKey(first[i]))
			}
		}
	})

	It("returns a defensive copy of the slice", func() {
		l := List()
		if len(l) < 2 {
			Skip("need at least two definitions")
		}

		l[0], l[1] = l[1], l[0]

		again := List()
		Expect(again[0]).NotTo(BeIdenticalTo(l[0]))
	})

	It("returns deep copies so mutations do not affect the catalog", func() {
		l := List()
		Expect(l).NotTo(BeEmpty())
		original := l[0].Spec.StructureDefinition.RootComponent.Name

		l[0].Spec.StructureDefinition.RootComponent.Name = original + "-mutated"

		again := List()
		Expect(again[0].Spec.StructureDefinition.RootComponent.Name).To(Equal(original))
	})

	It("contains only entries with Karta TypeMeta that pass the shared validator", func() {
		entries := List()

		Expect(entries).NotTo(BeEmpty())
		for _, k := range entries {
			Expect(k.APIVersion).To(Equal("run.ai/v1alpha1"), k.Name)
			Expect(k.Kind).To(Equal("Karta"), k.Name)
			Expect(v1alpha1.NewKartaValidator(k).Validate()).To(Succeed(), k.Name)
		}
	})
})

var _ = Describe("newCatalog", func() {
	newKartaWithKind := func(gvk *v1alpha1.GroupVersionKind) func() *v1alpha1.Karta {
		return func() *v1alpha1.Karta {
			return &v1alpha1.Karta{Spec: v1alpha1.KartaSpec{StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{Name: "root", Kind: gvk},
			}}}
		}
	}

	It("rejects two definitions with the same root GVK", func() {
		_, err := newCatalog([]func() *v1alpha1.Karta{kartas.BatchJob, kartas.BatchJob})

		Expect(err).To(HaveOccurred())
	})

	// An empty group stays valid (core workloads such as Pod); a missing version
	// or kind must fail construction rather than be indexed under a partial GVK.
	DescribeTable("rejects a definition whose root GVK is incomplete",
		func(gvk *v1alpha1.GroupVersionKind) {
			_, err := newCatalog([]func() *v1alpha1.Karta{newKartaWithKind(gvk)})

			Expect(err).To(MatchError(ErrInvalidGVK))
		},
		Entry("missing version", &v1alpha1.GroupVersionKind{Group: "example.com", Kind: "Thing"}),
		Entry("missing kind", &v1alpha1.GroupVersionKind{Group: "example.com", Version: "v1"}),
	)
})

var _ = Describe("MarshalYAML", func() {
	It("renders fields in struct declaration order", func() {
		out, err := MarshalYAML(kartas.BatchJob())

		Expect(err).NotTo(HaveOccurred())
		yamlText := string(out)
		structureIdx := strings.Index(yamlText, "structureDefinition:")
		instructionsIdx := strings.Index(yamlText, "optimizationInstructions:")
		Expect(structureIdx).To(BeNumerically(">", -1))
		Expect(instructionsIdx).To(BeNumerically(">", -1))
		Expect(structureIdx).To(BeNumerically("<", instructionsIdx),
			"structureDefinition must render before optimizationInstructions")
	})
})

// Reading each committed docs/catalog file back through the shared validator
// guards the on-disk YAML directly: it proves the generated files parse and
// satisfy the same validation as the Go definitions.
var _ = Describe("catalog files", func() {
	It("unmarshal into valid Kartas", func() {
		files, err := filepath.Glob(filepath.Join(catalogDir(), "*.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(files).NotTo(BeEmpty())

		for _, path := range files {
			data, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred(), path)
			var k v1alpha1.Karta
			Expect(yaml.Unmarshal(data, &k)).To(Succeed(), path)
			Expect(v1alpha1.NewKartaValidator(&k).Validate()).To(Succeed(), path)
		}
	})
})
