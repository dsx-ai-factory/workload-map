// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package workload

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	metadatafake "k8s.io/client-go/metadata/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
)

var replicaSetsGR = schema.GroupResource{Group: "apps", Resource: "replicasets"}

// denyList makes listing ReplicaSets forbidden.
func denyList(client *metadatafake.FakeMetadataClient) {
	client.PrependReactor("list", "replicasets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(replicaSetsGR, "", errors.New("nope"))
	})
}

// partialList builds one page of a metadata list, in the v1.List shape the
// fake client decodes a reactor's answer from.
func partialList(continueToken string, objects ...*unstructured.Unstructured) *metav1.List {
	GinkgoHelper()
	list := &metav1.List{ListMeta: metav1.ListMeta{Continue: continueToken}}
	for _, obj := range objects {
		var partial metav1.PartialObjectMetadata
		Expect(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &partial)).To(Succeed())
		list.Items = append(list.Items, runtime.RawExtension{Object: &partial})
	}
	return list
}

var _ = Describe("OwnerWalker.TopLevel", func() {
	// Deployment stands in for a Karta-covered kind; ReplicaSet is not covered.
	covered := func(gk schema.GroupKind) bool { return gk == deploymentGVK.GroupKind() }

	var (
		deployment *unstructured.Unstructured
		replicaSet *unstructured.Unstructured
	)

	BeforeEach(func() {
		deployment = owned(deploymentGVK, "web", "deploy-uid", nil)
		replicaSet = owned(replicaSetGVK, "web-abc", "rs-uid", controllerOf(deploymentGVK, "web", "deploy-uid"))
	})

	topLevel := func(w *OwnerWalker, obj *unstructured.Unstructured) bool {
		GinkgoHelper()
		top, err := w.TopLevel(context.Background(), obj, covered)
		Expect(err).NotTo(HaveOccurred())
		return top
	}

	It("keeps an object with no owner, without a read", func() {
		client, mapper := fakeCluster()
		reads := countReads(client)

		Expect(topLevel(NewOwnerWalker(client, mapper), deployment)).To(BeTrue())
		Expect(reads).To(BeEmpty())
	})

	// The owner reference already names the kind, so no read is needed.
	It("drops an object whose controller is covered, without a read", func() {
		client, mapper := fakeCluster()
		reads := countReads(client)

		Expect(topLevel(NewOwnerWalker(client, mapper), replicaSet)).To(BeFalse())
		Expect(reads).To(BeEmpty())
	})

	It("drops an object whose chain reaches a covered kind through an uncovered one", func() {
		client, mapper := fakeCluster(deployment, replicaSet)
		reads := countReads(client)

		p := pod("web-abc-1", controllerOf(replicaSetGVK, "web-abc", "rs-uid"))
		Expect(topLevel(NewOwnerWalker(client, mapper), &p)).To(BeFalse())
		Expect(reads).To(Equal(map[string]int{"list": 1}))
	})

	// An uncovered wrapper is transparent: the walk passes through it, and the
	// object it owns surfaces as the workload.
	It("keeps an object whose chain ends at an uncovered owner", func() {
		wrapper := owned(replicaSetGVK, "wrapper", "wrapper-uid", nil)
		client, mapper := fakeCluster(wrapper)

		p := pod("wrapped", controllerOf(replicaSetGVK, "wrapper", "wrapper-uid"))
		Expect(topLevel(NewOwnerWalker(client, mapper), &p)).To(BeTrue())
	})

	// Only the controller reference says who manages an object.
	It("ignores an owner reference that is not the controller", func() {
		client, mapper := fakeCluster()

		ref := controllerOf(deploymentGVK, "web", "deploy-uid")
		ref.Controller = ptr.To(false)
		p := pod("adopted", ref)
		Expect(topLevel(NewOwnerWalker(client, mapper), &p)).To(BeTrue())
	})

	// The owner is gone, so nothing owns the object any more.
	It("keeps an object whose owner no longer exists", func() {
		client, mapper := fakeCluster()

		p := pod("orphan", controllerOf(replicaSetGVK, "gone", "gone-uid"))
		Expect(topLevel(NewOwnerWalker(client, mapper), &p)).To(BeTrue())
	})

	// The question cannot be answered, so the caller must hear about it.
	It("reports an owner kind it may not list, and keeps the object", func() {
		client, mapper := fakeCluster(deployment, replicaSet)
		denyList(client)

		p := pod("web-abc-1", controllerOf(replicaSetGVK, "web-abc", "rs-uid"))
		top, err := NewOwnerWalker(client, mapper).TopLevel(context.Background(), &p, covered)
		Expect(apierrors.IsForbidden(err)).To(BeTrue())
		Expect(top).To(BeTrue())
	})

	// A page boundary must not hide an owner from the index.
	It("follows continuation tokens when listing owners", func() {
		client, mapper := fakeCluster()
		page := 0
		client.PrependReactor("list", "replicasets", func(k8stesting.Action) (bool, runtime.Object, error) {
			page++
			if page == 1 {
				return true, partialList("more", owned(replicaSetGVK, "first", "first-uid", nil)), nil
			}
			return true, partialList("", replicaSet), nil
		})

		p := pod("web-abc-1", controllerOf(replicaSetGVK, "web-abc", "rs-uid"))
		Expect(topLevel(NewOwnerWalker(client, mapper), &p)).To(BeFalse())
		Expect(page).To(Equal(2))
	})
})
