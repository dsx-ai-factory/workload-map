// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package pkg

import (
	"context"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func kartaWithRootKind(gvk *kartav1alpha1.GroupVersionKind) *kartav1alpha1.Karta {
	return &kartav1alpha1.Karta{
		Spec: kartav1alpha1.KartaSpec{
			StructureDefinition: kartav1alpha1.StructureDefinition{
				RootComponent: kartav1alpha1.ComponentDefinition{
					Name: "root",
					Kind: gvk,
				},
			},
		},
	}
}

// namedKarta builds a spec-valid Karta with a name and root GVK.
func namedKarta(name string, gvk *kartav1alpha1.GroupVersionKind) *kartav1alpha1.Karta {
	k := kartaWithRootKind(gvk)
	k.Name = name
	k.Spec.StructureDefinition.RootComponent.StatusDefinition = &kartav1alpha1.StatusDefinition{}
	k.Labels = desiredRootLabels(k)
	return k
}

var _ = Describe("KartaLabeler.Default", func() {
	var labeler *KartaLabeler
	ctx := context.Background()

	BeforeEach(func() {
		labeler = &KartaLabeler{}
	})

	It("stamps the GVK index labels for a Karta with a root component kind", func() {
		karta := kartaWithRootKind(&kartav1alpha1.GroupVersionKind{Group: "ray.io", Version: "v1", Kind: "RayCluster"})
		Expect(labeler.Default(ctx, karta)).To(Succeed())
		Expect(karta.Labels).To(HaveKeyWithValue(kartav1alpha1.LabelRootGroup, "ray.io"))
		Expect(karta.Labels).To(HaveKeyWithValue(kartav1alpha1.LabelRootVersion, "v1"))
		Expect(karta.Labels).To(HaveKeyWithValue(kartav1alpha1.LabelRootKind, "RayCluster"))
	})

	It("is a no-op when the Karta has no root component kind", func() {
		karta := &kartav1alpha1.Karta{}
		Expect(labeler.Default(ctx, karta)).To(Succeed())
		Expect(karta.Labels).To(BeEmpty())
	})

	It("preserves existing unrelated labels", func() {
		karta := kartaWithRootKind(&kartav1alpha1.GroupVersionKind{Group: "ray.io", Version: "v1", Kind: "RayCluster"})
		karta.ObjectMeta = metav1.ObjectMeta{Labels: map[string]string{"team": "ml"}}
		Expect(labeler.Default(ctx, karta)).To(Succeed())
		Expect(karta.Labels).To(HaveKeyWithValue("team", "ml"))
		Expect(karta.Labels).To(HaveKeyWithValue(kartav1alpha1.LabelRootKind, "RayCluster"))
	})

	It("restores an edited GVK label back to the value derived from the GVK", func() {
		karta := kartaWithRootKind(&kartav1alpha1.GroupVersionKind{Group: "ray.io", Version: "v1", Kind: "RayCluster"})
		karta.ObjectMeta = metav1.ObjectMeta{Labels: map[string]string{kartav1alpha1.LabelRootKind: "Tampered"}}
		Expect(labeler.Default(ctx, karta)).To(Succeed())
		Expect(karta.Labels).To(HaveKeyWithValue(kartav1alpha1.LabelRootKind, "RayCluster"))
	})

	It("re-adds a deleted GVK label", func() {
		karta := kartaWithRootKind(&kartav1alpha1.GroupVersionKind{Group: "ray.io", Version: "v1", Kind: "RayCluster"})
		karta.ObjectMeta = metav1.ObjectMeta{Labels: map[string]string{"team": "ml"}}
		Expect(labeler.Default(ctx, karta)).To(Succeed())
		Expect(karta.Labels).To(HaveKeyWithValue(kartav1alpha1.LabelRootGroup, "ray.io"))
		Expect(karta.Labels).To(HaveKeyWithValue(kartav1alpha1.LabelRootVersion, "v1"))
		Expect(karta.Labels).To(HaveKeyWithValue(kartav1alpha1.LabelRootKind, "RayCluster"))
	})

	It("produces the same labels the reconciler would (shared helper)", func() {
		karta := kartaWithRootKind(&kartav1alpha1.GroupVersionKind{Group: "kubeflow.org", Version: "v1", Kind: "PyTorchJob"})
		Expect(labeler.Default(ctx, karta)).To(Succeed())
		Expect(karta.Labels).To(Equal(desiredRootLabels(karta)))
	})
})

var _ = Describe("ResolveNamespace", func() {
	It("prefers the explicit override and trims it", func() {
		ns, err := ResolveNamespace("  team-x  ")
		Expect(err).NotTo(HaveOccurred())
		Expect(ns).To(Equal("team-x"))
	})
})

var _ = Describe("KartaValidator", func() {
	ctx := context.Background()

	rayGVK := &kartav1alpha1.GroupVersionKind{Group: "ray.io", Version: "v1", Kind: "RayCluster"}

	// newValidator builds a validator backed by a fake client holding the given
	// Kartas, with the same root GVK field index the manager registers.
	newValidator := func(existing ...*kartav1alpha1.Karta) *KartaValidator {
		scheme := runtime.NewScheme()
		_ = kartav1alpha1.AddToScheme(scheme)
		objs := make([]client.Object, 0, len(existing))
		for _, k := range existing {
			objs = append(objs, k)
		}
		c := fake.NewClientBuilder().WithScheme(scheme).
			WithIndex(&kartav1alpha1.Karta{}, kartaGVKIndexKey, indexKartaByRootGVK).
			WithObjects(objs...).Build()
		return &KartaValidator{client: c}
	}

	validKarta := func() *kartav1alpha1.Karta { return namedKarta("valid", rayGVK) }

	It("accepts a valid Karta on create", func() {
		_, err := newValidator().ValidateCreate(ctx, validKarta())
		Expect(err).NotTo(HaveOccurred())
	})

	It("rejects a Karta with no root component kind on create", func() {
		_, err := newValidator().ValidateCreate(ctx, kartaWithRootKind(nil))
		Expect(err).To(HaveOccurred())
	})

	It("rejects a second Karta for the same root GVK", func() {
		v := newValidator(namedKarta("first", rayGVK))
		_, err := v.ValidateCreate(ctx, namedKarta("second", rayGVK))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("already exists"))
	})

	It("allows a Karta for the same group/kind but a different version", func() {
		v := newValidator(namedKarta("ray-v1", rayGVK))
		rayV1alpha1 := &kartav1alpha1.GroupVersionKind{Group: "ray.io", Version: "v1alpha1", Kind: "RayCluster"}
		_, err := v.ValidateCreate(ctx, namedKarta("ray-v1alpha1", rayV1alpha1))
		Expect(err).NotTo(HaveOccurred())
	})

	It("allows a Karta for a group/kind that has none yet", func() {
		v := newValidator(namedKarta("first", rayGVK))
		jobGVK := &kartav1alpha1.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
		_, err := v.ValidateCreate(ctx, namedKarta("second", jobGVK))
		Expect(err).NotTo(HaveOccurred())
	})

	It("rejects an invalid Karta on update", func() {
		_, err := newValidator().ValidateUpdate(ctx, validKarta(), kartaWithRootKind(nil))
		Expect(err).To(HaveOccurred())
	})

	It("rejects an update that changes the root GVK version", func() {
		v := newValidator(namedKarta("ray", rayGVK))
		rayV1alpha1 := &kartav1alpha1.GroupVersionKind{Group: "ray.io", Version: "v1alpha1", Kind: "RayCluster"}
		_, err := v.ValidateUpdate(ctx, namedKarta("ray", rayGVK), namedKarta("ray", rayV1alpha1))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("immutable"))
	})

	It("rejects an update that changes the root GVK to one another Karta already has", func() {
		jobGVK := &kartav1alpha1.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
		v := newValidator(namedKarta("ray", rayGVK), namedKarta("job", jobGVK))
		_, err := v.ValidateUpdate(ctx, namedKarta("job", jobGVK), namedKarta("job", rayGVK))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("immutable"))
	})

	It("rejects an update that adds a root GVK where there was none", func() {
		v := newValidator()
		_, err := v.ValidateUpdate(ctx, namedKarta("ray", nil), namedKarta("ray", rayGVK))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("immutable"))
	})

	It("allows updating a Karta that keeps its own root GVK", func() {
		v := newValidator(namedKarta("ray", rayGVK))
		_, err := v.ValidateUpdate(ctx, namedKarta("ray", rayGVK), namedKarta("ray", rayGVK))
		Expect(err).NotTo(HaveOccurred())
	})

	It("rejects an update that edits a managed GVK label", func() {
		v := newValidator(namedKarta("ray", rayGVK))
		tampered := namedKarta("ray", rayGVK)
		tampered.Labels[kartav1alpha1.LabelRootKind] = "Tampered"
		_, err := v.ValidateUpdate(ctx, namedKarta("ray", rayGVK), tampered)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("managed by the operator"))
	})

	It("rejects an update that deletes a managed GVK label", func() {
		v := newValidator(namedKarta("ray", rayGVK))
		tampered := namedKarta("ray", rayGVK)
		delete(tampered.Labels, kartav1alpha1.LabelRootVersion)
		_, err := v.ValidateUpdate(ctx, namedKarta("ray", rayGVK), tampered)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("managed by the operator"))
	})

	It("allows an update that adds an unrelated label", func() {
		v := newValidator(namedKarta("ray", rayGVK))
		updated := namedKarta("ray", rayGVK)
		updated.Labels["team"] = "ml"
		_, err := v.ValidateUpdate(ctx, namedKarta("ray", rayGVK), updated)
		Expect(err).NotTo(HaveOccurred())
	})

	It("allows delete", func() {
		_, err := newValidator().ValidateDelete(ctx, kartaWithRootKind(nil))
		Expect(err).NotTo(HaveOccurred())
	})
})
