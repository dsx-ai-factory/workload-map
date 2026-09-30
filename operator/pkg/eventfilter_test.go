// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package pkg

import (
	"context"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/client-go/tools/events"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// labeledKarta returns a Karta pre-stamped with the GVK index labels, as if
// ensureLabels had already run. Used to set up the label-selector tests.
func labeledKarta(name string, gvk schema.GroupVersionKind) *kartav1alpha1.Karta {
	k := newKarta(name, &gvk)
	k.Labels = map[string]string{
		kartav1alpha1.LabelRootGroup:   gvk.Group,
		kartav1alpha1.LabelRootVersion: gvk.Version,
		kartav1alpha1.LabelRootKind:    gvk.Kind,
	}
	return k
}

var _ = Describe("Reconciler.MapCRDToKartaEvent", func() {
	var (
		ctx context.Context
		k8s client.WithWatch
		r   *Reconciler
	)

	BeforeEach(func() {
		ctx = context.Background()
		k8s = fake.NewClientBuilder().WithScheme(buildScheme()).Build()
		r = NewReconciler(k8s, events.NewFakeRecorder(64))
	})

	It("returns nil when the object is not a CRD", func() {
		Expect(r.MapCRDToKartaEvent(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}})).To(BeNil())
	})

	It("returns no requests when there are no Kartas", func() {
		crd := newCRD("fooTestRunai", "test.run.ai", "Foo", "v1")
		Expect(r.MapCRDToKartaEvent(ctx, crd)).To(BeEmpty())
	})

	It("enqueues all Kartas sharing the CRD group+kind, regardless of version", func() {
		gvkV1 := schema.GroupVersionKind{Group: "test.run.ai", Version: "v1", Kind: "Foo"}
		gvkV2 := schema.GroupVersionKind{Group: "test.run.ai", Version: "v2", Kind: "Foo"}
		other := schema.GroupVersionKind{Group: "other.run.ai", Version: "v1", Kind: "Bar"}

		Expect(k8s.Create(ctx, labeledKarta("karta-v1", gvkV1))).To(Succeed())
		Expect(k8s.Create(ctx, labeledKarta("karta-v2", gvkV2))).To(Succeed())
		Expect(k8s.Create(ctx, labeledKarta("karta-other", other))).To(Succeed())
		Expect(k8s.Create(ctx, newKarta("karta-no-gvk", nil))).To(Succeed())

		crd := newCRD("foos.test.run.ai", "test.run.ai", "Foo", "v1")
		reqs := r.MapCRDToKartaEvent(ctx, crd)

		names := make([]string, 0, len(reqs))
		for _, req := range reqs {
			names = append(names, req.Name)
		}
		Expect(names).To(ConsistOf("karta-v1", "karta-v2"))
	})

	It("enqueues a Karta even when its referenced version was just removed from the CRD", func() {
		gvk := schema.GroupVersionKind{Group: "test.run.ai", Version: "v1", Kind: "Foo"}
		Expect(k8s.Create(ctx, labeledKarta("karta-v1", gvk))).To(Succeed())

		// enqueued so the reconciler can set CRDExists=False.
		crd := newCRD("foos.test.run.ai", gvk.Group, gvk.Kind, "v2")
		reqs := r.MapCRDToKartaEvent(ctx, crd)

		names := make([]string, 0, len(reqs))
		for _, req := range reqs {
			names = append(names, req.Name)
		}
		Expect(names).To(ConsistOf("karta-v1"))
	})

	It("skips Kartas whose group or kind label does not match", func() {
		differentGroup := schema.GroupVersionKind{Group: "other.run.ai", Version: "v1", Kind: "Foo"}
		differentKind := schema.GroupVersionKind{Group: "test.run.ai", Version: "v1", Kind: "Bar"}

		Expect(k8s.Create(ctx, labeledKarta("karta-diff-group", differentGroup))).To(Succeed())
		Expect(k8s.Create(ctx, labeledKarta("karta-diff-kind", differentKind))).To(Succeed())

		crd := newCRD("foos.test.run.ai", "test.run.ai", "Foo", "v1")
		Expect(r.MapCRDToKartaEvent(ctx, crd)).To(BeEmpty())
	})
})

// newKarta builds a Karta with the given name and optional root GVK. The root
// component has no StatusDefinition so it will fail spec validation — use
// newValidKarta when Validated=True is expected.
func newKarta(name string, gvk *schema.GroupVersionKind) *kartav1alpha1.Karta {
	k := &kartav1alpha1.Karta{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	if gvk != nil {
		k.Spec.StructureDefinition.RootComponent.Name = name + "-root"
		k.Spec.StructureDefinition.RootComponent.Kind = &kartav1alpha1.GroupVersionKind{
			Group:   gvk.Group,
			Version: gvk.Version,
			Kind:    gvk.Kind,
		}
	}
	return k
}

// newCRD builds a CustomResourceDefinition serving the given versions for
// the supplied group/kind. The first version is marked as storage.
func newCRD(name, group, kind string, versions ...string) *apiextensionsv1.CustomResourceDefinition {
	v := make([]apiextensionsv1.CustomResourceDefinitionVersion, 0, len(versions))
	for _, ver := range versions {
		v = append(v, apiextensionsv1.CustomResourceDefinitionVersion{
			Name: ver, Served: true, Storage: ver == versions[0],
		})
	}
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: group,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:   kind,
				Plural: strings.ToLower(kind) + "s",
			},
			Scope:    apiextensionsv1.ClusterScoped,
			Versions: v,
		},
	}
}
