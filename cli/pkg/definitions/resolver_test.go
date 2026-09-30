// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package definitions

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

var (
	deploymentGVK  = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	jobGVK         = schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
	podGVK         = schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	dynamoAlphaGVK = schema.GroupVersionKind{Group: "nvidia.com", Version: "v1alpha1", Kind: "DynamoGraphDeployment"}
	dynamoBetaGVK  = schema.GroupVersionKind{Group: "nvidia.com", Version: "v1beta1", Kind: "DynamoGraphDeployment"}
	milvusGVK      = schema.GroupVersionKind{Group: "milvus.io", Version: "v1beta1", Kind: "Milvus"}
)

// newKarta builds a minimal indexable Karta claiming gvk as its root component.
func newKarta(name string, gvk schema.GroupVersionKind) *v1alpha1.Karta {
	k := newRootlessKarta(name)
	k.Spec.StructureDefinition.RootComponent.Kind = &v1alpha1.GroupVersionKind{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    gvk.Kind,
	}
	return k
}

// newRootlessKarta builds a Karta whose root component carries no GVK at all.
func newRootlessKarta(name string) *v1alpha1.Karta {
	return &v1alpha1.Karta{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{Name: "root"},
			},
		},
	}
}

func namesOf(defs []Definition) []string {
	out := make([]string, 0, len(defs))
	for _, def := range defs {
		out = append(out, def.Karta.Name)
	}
	return out
}

var _ = Describe("Resolver merge and precedence", func() {
	It("resolves a catalog definition when no cluster definitions exist", func() {
		r := New([]*v1alpha1.Karta{newKarta("catalog-deployment", deploymentGVK)}, nil)

		def, err := r.Resolve(deploymentGVK)
		Expect(err).NotTo(HaveOccurred())
		Expect(def.Karta.Name).To(Equal("catalog-deployment"))
		Expect(def.Origin).To(Equal(OriginCatalog))
	})

	It("resolves a cluster definition when the catalog is empty", func() {
		r := New(nil, []*v1alpha1.Karta{newKarta("cluster-deployment", deploymentGVK)})

		def, err := r.Resolve(deploymentGVK)
		Expect(err).NotTo(HaveOccurred())
		Expect(def.Karta.Name).To(Equal("cluster-deployment"))
		Expect(def.Origin).To(Equal(OriginCluster))
	})

	It("unions disjoint sources", func() {
		r := New(
			[]*v1alpha1.Karta{newKarta("catalog-deployment", deploymentGVK)},
			[]*v1alpha1.Karta{newKarta("cluster-job", jobGVK)},
		)

		deployment, err := r.Resolve(deploymentGVK)
		Expect(err).NotTo(HaveOccurred())
		Expect(deployment.Origin).To(Equal(OriginCatalog))

		job, err := r.Resolve(jobGVK)
		Expect(err).NotTo(HaveOccurred())
		Expect(job.Origin).To(Equal(OriginCluster))

		Expect(namesOf(r.List())).To(Equal([]string{"catalog-deployment", "cluster-job"}))
	})

	It("lets a cluster definition override a catalog one on the same GVK", func() {
		r := New(
			[]*v1alpha1.Karta{newKarta("catalog-deployment", deploymentGVK)},
			[]*v1alpha1.Karta{newKarta("cluster-deployment", deploymentGVK)},
		)

		def, err := r.Resolve(deploymentGVK)
		Expect(err).NotTo(HaveOccurred())
		Expect(def.Karta.Name).To(Equal("cluster-deployment"))
		Expect(def.Origin).To(Equal(OriginCluster))
	})

	It("returns ErrNotFound for an unknown GVK", func() {
		r := New([]*v1alpha1.Karta{newKarta("catalog-deployment", deploymentGVK)}, nil)

		def, err := r.Resolve(jobGVK)
		Expect(err).To(MatchError(ErrNotFound))
		Expect(err.Error()).To(ContainSubstring("batch/v1, Kind=Job"))
		Expect(def.Karta).To(BeNil())
	})

	DescribeTable("lists a definition whose root GVK is incomplete but never resolves it",
		func(karta *v1alpha1.Karta, miss schema.GroupVersionKind) {
			r := New(nil, []*v1alpha1.Karta{karta})

			// Unusable for lookup, but it exists in the cluster, so an operator has
			// to be able to see the one they wrote.
			Expect(namesOf(r.List())).To(Equal([]string{karta.Name}))
			Expect(r.Collisions()).To(BeEmpty())

			byName, err := r.ByName(karta.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(byName.Karta.Name).To(Equal(karta.Name))

			_, err = r.Resolve(miss)
			Expect(err).To(MatchError(ErrNotFound))
		},
		Entry("nil root kind", newRootlessKarta("no-kind"), schema.GroupVersionKind{}),
		Entry("empty version",
			newKarta("no-version", schema.GroupVersionKind{Group: "apps", Kind: "Deployment"}),
			schema.GroupVersionKind{Group: "apps", Kind: "Deployment"},
		),
		Entry("empty kind",
			newKarta("no-kind-field", schema.GroupVersionKind{Group: "apps", Version: "v1"}),
			schema.GroupVersionKind{Group: "apps", Version: "v1"},
		),
	)

	It("indexes a core-group definition, where an empty group is legitimate", func() {
		r := New([]*v1alpha1.Karta{newKarta("core-pod-v1", podGVK)}, nil)

		def, err := r.Resolve(podGVK)
		Expect(err).NotTo(HaveOccurred())
		Expect(def.Karta.Name).To(Equal("core-pod-v1"))
	})

	DescribeTable("refuses to resolve a GVK claimed more than once, whatever the input order",
		func(cluster []*v1alpha1.Karta) {
			def, err := New(nil, cluster).Resolve(deploymentGVK)
			Expect(err).To(MatchError(ErrAmbiguous))
			// Naming both is what lets a user fix it; picking one would hide it.
			Expect(err.Error()).To(ContainSubstring(`"aaa-deployment"`))
			Expect(err.Error()).To(ContainSubstring(`"zzz-deployment"`))
			Expect(def.Karta).To(BeNil())
		},
		Entry("already sorted", []*v1alpha1.Karta{
			newKarta("aaa-deployment", deploymentGVK),
			newKarta("zzz-deployment", deploymentGVK),
		}),
		Entry("reversed", []*v1alpha1.Karta{
			newKarta("zzz-deployment", deploymentGVK),
			newKarta("aaa-deployment", deploymentGVK),
		}),
	)

	It("does not reorder the caller's slice", func() {
		input := []*v1alpha1.Karta{
			newKarta("zzz-deployment", deploymentGVK),
			newKarta("aaa-job", jobGVK),
		}

		New(nil, input)

		Expect(input[0].Name).To(Equal("zzz-deployment"))
		Expect(input[1].Name).To(Equal("aaa-job"))
	})
})

var _ = Describe("Resolver List", func() {
	It("lists both same-source claimants of a GVK while Resolve refuses it", func() {
		r := New(nil, []*v1alpha1.Karta{
			newKarta("zzz-deployment", deploymentGVK),
			newKarta("aaa-deployment", deploymentGVK),
		})

		Expect(namesOf(r.List())).To(Equal([]string{"aaa-deployment", "zzz-deployment"}))

		_, err := r.Resolve(deploymentGVK)
		Expect(err).To(MatchError(ErrAmbiguous))
	})

	It("lists an overridden GVK exactly once, as the cluster definition", func() {
		r := New(
			[]*v1alpha1.Karta{newKarta("catalog-deployment", deploymentGVK)},
			[]*v1alpha1.Karta{newKarta("cluster-deployment", deploymentGVK)},
		)

		list := r.List()
		Expect(namesOf(list)).To(Equal([]string{"cluster-deployment"}))
		Expect(list[0].Origin).To(Equal(OriginCluster))
	})

	It("orders deterministically across reordered input", func() {
		catalog := []*v1alpha1.Karta{
			newKarta("core-pod-v1", podGVK),
			newKarta("batch-job-v1", jobGVK),
		}
		cluster := []*v1alpha1.Karta{
			newKarta("zzz-deployment", deploymentGVK),
			newKarta("aaa-deployment", deploymentGVK),
		}
		want := []string{"core-pod-v1", "aaa-deployment", "zzz-deployment", "batch-job-v1"}

		Expect(namesOf(New(catalog, cluster).List())).To(Equal(want))

		reversedCatalog := []*v1alpha1.Karta{catalog[1], catalog[0]}
		reversedCluster := []*v1alpha1.Karta{cluster[1], cluster[0]}
		Expect(namesOf(New(reversedCatalog, reversedCluster).List())).To(Equal(want))
	})
})

var _ = Describe("Resolver Collisions", func() {
	It("records the winner and the name-sorted shadowed names for two claimants", func() {
		r := New(nil, []*v1alpha1.Karta{
			newKarta("zzz-deployment", deploymentGVK),
			newKarta("aaa-deployment", deploymentGVK),
		})

		Expect(r.Collisions()).To(Equal([]Collision{{
			GVK:   deploymentGVK,
			Names: []string{"aaa-deployment", "zzz-deployment"},
		}}))
	})

	It("records every shadowed name for three claimants", func() {
		r := New(nil, []*v1alpha1.Karta{
			newKarta("mmm-deployment", deploymentGVK),
			newKarta("zzz-deployment", deploymentGVK),
			newKarta("aaa-deployment", deploymentGVK),
		})

		Expect(r.Collisions()).To(Equal([]Collision{{
			GVK:   deploymentGVK,
			Names: []string{"aaa-deployment", "mmm-deployment", "zzz-deployment"},
		}}))
	})

	It("does not record a collision when a cluster definition overrides a catalog one", func() {
		r := New(
			[]*v1alpha1.Karta{newKarta("catalog-deployment", deploymentGVK)},
			[]*v1alpha1.Karta{newKarta("cluster-deployment", deploymentGVK)},
		)

		Expect(r.Collisions()).To(BeEmpty())
	})

	It("records nothing when no GVK is claimed twice", func() {
		r := New(
			[]*v1alpha1.Karta{newKarta("catalog-deployment", deploymentGVK)},
			[]*v1alpha1.Karta{newKarta("cluster-job", jobGVK)},
		)

		Expect(r.Collisions()).To(BeEmpty())
	})

	It("records no collision for definitions with an incomplete root GVK", func() {
		r := New(nil, []*v1alpha1.Karta{
			newRootlessKarta("aaa-no-kind"),
			newRootlessKarta("zzz-no-kind"),
		})

		Expect(r.Collisions()).To(BeEmpty())
	})

	It("sorts collisions by GVK", func() {
		r := New(nil, []*v1alpha1.Karta{
			newKarta("zzz-job", jobGVK),
			newKarta("aaa-job", jobGVK),
			newKarta("zzz-deployment", deploymentGVK),
			newKarta("aaa-deployment", deploymentGVK),
		})

		collisions := r.Collisions()
		Expect(collisions).To(HaveLen(2))
		Expect(collisions[0].GVK).To(Equal(deploymentGVK))
		Expect(collisions[1].GVK).To(Equal(jobGVK))
	})
})

var _ = Describe("Resolver ByRootKind", func() {
	var r *Resolver

	BeforeEach(func() {
		r = New(
			[]*v1alpha1.Karta{
				newKarta("apps-deployment-v1", deploymentGVK),
				newKarta("batch-job-v1", jobGVK),
			},
			[]*v1alpha1.Karta{
				newKarta("nvidia-com-dynamographdeployment-v1beta1", dynamoBetaGVK),
				newKarta("nvidia-com-dynamographdeployment-v1alpha1", dynamoAlphaGVK),
				newKarta("milvus-io-milvus-v1beta1", milvusGVK),
			},
		)
	})

	DescribeTable("matches the kind exactly, apart from case",
		func(query string, want []string) {
			Expect(namesOf(r.ByRootKind(query))).To(Equal(want))
		},
		Entry("exact", "Deployment", []string{"apps-deployment-v1"}),
		Entry("lowercase", "deployment", []string{"apps-deployment-v1"}),
		Entry("uppercase", "DEPLOYMENT", []string{"apps-deployment-v1"}),
		// A plural needs the real singular name, which only discovery supplies.
		Entry("plural", "deployments", []string{}),
		Entry("mixed case plural", "Jobs", []string{}),
		Entry("a kind covered at two versions", "dynamographdeployment", []string{
			"nvidia-com-dynamographdeployment-v1alpha1",
			"nvidia-com-dynamographdeployment-v1beta1",
		}),
		Entry("a kind that itself ends in s", "Milvus", []string{"milvus-io-milvus-v1beta1"}),
		Entry("its plural", "milvuses", []string{}),
		Entry("the query one character short", "milvu", []string{}),
	)

	It("returns an empty non-nil slice when nothing matches", func() {
		matches := r.ByRootKind("StatefulSet")
		Expect(matches).NotTo(BeNil())
		Expect(matches).To(BeEmpty())
	})
})

var _ = Describe("Resolver ByName", func() {
	var r *Resolver

	BeforeEach(func() {
		r = New(
			[]*v1alpha1.Karta{newKarta("batch-job-v1", jobGVK)},
			[]*v1alpha1.Karta{
				newKarta("zzz-deployment", deploymentGVK),
				newKarta("aaa-deployment", deploymentGVK),
			},
		)
	})

	It("finds a definition by its exact name", func() {
		def, err := r.ByName("batch-job-v1")
		Expect(err).NotTo(HaveOccurred())
		Expect(def.Karta.Name).To(Equal("batch-job-v1"))
		Expect(def.Origin).To(Equal(OriginCatalog))
	})

	It("finds a definition that shares its GVK with another", func() {
		def, err := r.ByName("aaa-deployment")
		Expect(err).NotTo(HaveOccurred())
		Expect(def.Karta.Name).To(Equal("aaa-deployment"))
		Expect(def.Origin).To(Equal(OriginCluster))
	})

	DescribeTable("prefers the cluster definition when a name is shared with the catalog",
		func(catalogGVK, clusterGVK schema.GroupVersionKind) {
			shared := New(
				[]*v1alpha1.Karta{newKarta("apps-deployment-v1", catalogGVK)},
				[]*v1alpha1.Karta{newKarta("apps-deployment-v1", clusterGVK)},
			)

			def, err := shared.ByName("apps-deployment-v1")
			Expect(err).NotTo(HaveOccurred())
			Expect(def.Origin).To(Equal(OriginCluster))
		},
		// Names are unique per source, so a shared name means the root GVKs
		// differ, and definitions are ordered by GVK. Taking the first match
		// would answer with the catalog one in the first case.
		Entry("the catalog GVK sorts first", deploymentGVK, jobGVK),
		Entry("the cluster GVK sorts first", jobGVK, deploymentGVK),
	)

	It("is case-sensitive", func() {
		def, err := r.ByName("Batch-Job-V1")
		Expect(err).To(MatchError(ErrNameNotFound))
		Expect(err.Error()).To(ContainSubstring(`"Batch-Job-V1"`))
		Expect(def.Karta).To(BeNil())
	})
})

var _ = Describe("Resolver definitions that name no GVK", func() {
	var r *Resolver

	BeforeEach(func() {
		r = New(nil, []*v1alpha1.Karta{
			newKarta("apps-deployment-v1", deploymentGVK),
			newRootlessKarta("zzz-rootless"),
			newRootlessKarta("aaa-rootless"),
		})
	})

	It("lists them after the definitions that do, name-sorted", func() {
		Expect(namesOf(r.List())).To(Equal([]string{
			"apps-deployment-v1", "aaa-rootless", "zzz-rootless",
		}))
	})

	It("never matches them by kind, since they name none", func() {
		Expect(namesOf(r.ByRootKind("Deployment"))).To(Equal([]string{"apps-deployment-v1"}))
		Expect(r.ByRootKind("root")).To(BeEmpty())
	})

	It("never matches them for a query that trims to empty", func() {
		Expect(r.ByRootKind("s")).To(BeEmpty())
		Expect(r.ByRootKind("")).To(BeEmpty())
	})
})
