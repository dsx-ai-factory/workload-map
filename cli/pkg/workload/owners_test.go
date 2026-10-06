// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package workload

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
)

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

	// countGets counts the owner reads, which is what the short-circuits save.
	countGets := func(dyn *dynamicfake.FakeDynamicClient) *atomic.Int32 {
		var gets atomic.Int32
		dyn.PrependReactor("get", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
			gets.Add(1)
			return false, nil, nil
		})
		return &gets
	}

	topLevel := func(w *OwnerWalker, obj *unstructured.Unstructured) bool {
		GinkgoHelper()
		top, err := w.TopLevel(context.Background(), obj, covered)
		Expect(err).NotTo(HaveOccurred())
		return top
	}

	It("keeps an object with no owner, without a read", func() {
		dyn, mapper := fakeCluster()
		gets := countGets(dyn.(*dynamicfake.FakeDynamicClient))

		Expect(topLevel(NewOwnerWalker(dyn, mapper), deployment)).To(BeTrue())
		Expect(gets.Load()).To(BeZero())
	})

	// The owner reference already names the kind, so no read is needed.
	It("drops an object whose controller is covered, without a read", func() {
		dyn, mapper := fakeCluster()
		gets := countGets(dyn.(*dynamicfake.FakeDynamicClient))

		Expect(topLevel(NewOwnerWalker(dyn, mapper), replicaSet)).To(BeFalse())
		Expect(gets.Load()).To(BeZero())
	})

	It("drops an object whose chain reaches a covered kind through an uncovered one", func() {
		dyn, mapper := fakeCluster(deployment, replicaSet)
		gets := countGets(dyn.(*dynamicfake.FakeDynamicClient))

		p := pod("web-abc-1", controllerOf(replicaSetGVK, "web-abc", "rs-uid"))
		Expect(topLevel(NewOwnerWalker(dyn, mapper), &p)).To(BeFalse())
		Expect(gets.Load()).To(Equal(int32(1)))
	})

	// An uncovered wrapper is transparent: the walk passes through it, and the
	// object it owns surfaces as the workload.
	It("keeps an object whose chain ends at an uncovered owner", func() {
		wrapper := owned(replicaSetGVK, "wrapper", "wrapper-uid", nil)
		dyn, mapper := fakeCluster(wrapper)

		p := pod("wrapped", controllerOf(replicaSetGVK, "wrapper", "wrapper-uid"))
		Expect(topLevel(NewOwnerWalker(dyn, mapper), &p)).To(BeTrue())
	})

	// Only the controller reference says who manages an object.
	It("ignores an owner reference that is not the controller", func() {
		dyn, mapper := fakeCluster()

		ref := controllerOf(deploymentGVK, "web", "deploy-uid")
		ref.Controller = ptr.To(false)
		p := pod("adopted", ref)
		Expect(topLevel(NewOwnerWalker(dyn, mapper), &p)).To(BeTrue())
	})

	// The owner is gone, so nothing owns the object any more.
	It("keeps an object whose owner no longer exists", func() {
		dyn, mapper := fakeCluster()

		p := pod("orphan", controllerOf(replicaSetGVK, "gone", "gone-uid"))
		Expect(topLevel(NewOwnerWalker(dyn, mapper), &p)).To(BeTrue())
	})

	// The question cannot be answered, so the caller must hear about it.
	It("reports an owner it may not read, and keeps the object", func() {
		dyn, mapper := fakeCluster(deployment, replicaSet)
		dyn.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "replicasets",
			func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, apierrors.NewForbidden(
					schema.GroupResource{Group: "apps", Resource: "replicasets"}, "web-abc", errors.New("nope"))
			})

		p := pod("web-abc-1", controllerOf(replicaSetGVK, "web-abc", "rs-uid"))
		top, err := NewOwnerWalker(dyn, mapper).TopLevel(context.Background(), &p, covered)
		Expect(apierrors.IsForbidden(err)).To(BeTrue())
		Expect(top).To(BeTrue())
	})

	// Concurrent walks through one owner must share a single read.
	It("reads a shared owner once across concurrent walks", func() {
		dyn, mapper := fakeCluster(deployment, replicaSet)
		gets := countGets(dyn.(*dynamicfake.FakeDynamicClient))
		walker := NewOwnerWalker(dyn, mapper)

		var wg sync.WaitGroup
		for range 16 {
			wg.Go(func() {
				defer GinkgoRecover()
				p := pod("web-abc-x", controllerOf(replicaSetGVK, "web-abc", "rs-uid"))
				top, err := walker.TopLevel(context.Background(), &p, covered)
				Expect(err).NotTo(HaveOccurred())
				Expect(top).To(BeFalse())
			})
		}
		wg.Wait()
		Expect(gets.Load()).To(Equal(int32(1)))
	})
})
