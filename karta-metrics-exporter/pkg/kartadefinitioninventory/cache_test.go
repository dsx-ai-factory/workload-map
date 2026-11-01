// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartadefinitioninventory

import (
	"context"
	"errors"
	"fmt"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var _ = Describe("inventory observation", func() {
	It("lists invalid winners with provenance, without hiding healthy kinds", func() {
		bad := newKarta("invalid-job", jobGVK, invalid, fromCluster("job-uid", "7"))
		inventory := loadedInventory(Config{
			Catalog: SourceConfig{Enabled: true, Include: []string{"batch-job-v1"}},
			Cluster: SourceConfig{Enabled: true},
		}, bad, newKarta("deployment", deploymentGVK), newRootlessKarta("unkeyable"))
		results, err := inventory.List()
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(2))
		Expect(results[0].GVK).To(Equal(deploymentGVK))
		Expect(results[0].Err).NotTo(HaveOccurred())
		Expect(results[1].GVK).To(Equal(jobGVK))
		Expect(results[1].Err).To(MatchError(ErrInvalidDefinition))
		Expect(results[1].Origin).To(Equal(Origin{Source: SourceCluster, Name: "invalid-job", UID: "job-uid", ResourceVersion: "7"}))
		Expect(results[1].Definition.Name).To(Equal("invalid-job"))
		selection, err := inventory.Lookup(jobGVK)
		Expect(err).To(MatchError(ErrInvalidDefinition))
		Expect(selection).To(Equal(Selection{}))
	})

	It("returns caller-owned cluster and catalog snapshots", func() {
		inventory := loadedInventory(Config{
			Catalog: SourceConfig{Enabled: true, Include: []string{"batch-job-v1"}},
			Cluster: SourceConfig{Enabled: true},
		}, newKarta("deployment", deploymentGVK, withLabels(map[string]string{"team": "original"})))
		before, err := inventory.List()
		Expect(err).NotTo(HaveOccurred())
		for n := range before {
			before[n].Definition.Name = "mutated"
			before[n].Definition.Spec.StructureDefinition.RootComponent.Kind.Kind = "Changed"
		}
		again, err := inventory.List()
		Expect(err).NotTo(HaveOccurred())
		Expect(again[0].Definition.Name).To(Equal("deployment"))
		Expect(again[0].GVK).To(Equal(deploymentGVK))
		Expect(again[1].Definition.Name).To(Equal("batch-job-v1"))
		Expect(again[1].GVK).To(Equal(jobGVK))
		Expect(again[1].Origin).To(Equal(catalogJobOrigin))
	})

	It("separates unavailable snapshots from confirmed absence and signals recovery", func() {
		inventory, err := New(newFakeClient(), clusterOnly)
		Expect(err).NotTo(HaveOccurred())
		Expect(inventory.Changes()).To(Receive())
		results, err := inventory.List()
		Expect(results).To(BeNil())
		Expect(err).To(MatchError(ErrNotReady))

		store := reflectorStore{inventory: inventory}
		Expect(store.Replace(nil, "1")).To(Succeed())
		Expect(inventory.Changes()).To(Receive())
		results, err = inventory.List()
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(BeEmpty())

		inventory.recordFailure(apierrors.NewForbidden(kartasGR, "", errors.New("forbidden")))
		Expect(inventory.Changes()).To(Receive())
		results, err = inventory.List()
		Expect(results).To(BeNil())
		Expect(err).To(MatchError(ErrPermissionDenied))
		Expect(inventory.WaitLoaded(context.Background())).To(Succeed())
		inventory.beginList()
		Expect(store.Replace(nil, "2")).To(Succeed())
		Expect(inventory.Changes()).To(Receive())
		results, err = inventory.List()
		Expect(results).To(BeEmpty())
		Expect(err).NotTo(HaveOccurred())
	})

	It("coalesces accepted updates and reports the latest snapshot", func() {
		inventory := loadedInventory(clusterOnly)
		store := reflectorStore{inventory: inventory}
		for n := range 20 {
			k := newKarta("job", jobGVK, fromCluster("job-uid", fmt.Sprint(n)))
			Expect(store.Update(unstructuredKarta(k))).To(Succeed())
		}
		Expect(inventory.Changes()).To(Receive())
		Expect(inventory.Changes()).NotTo(Receive())
		results, err := inventory.List()
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Origin.ResourceVersion).To(Equal("19"))
		Expect(store.Delete(unstructuredKarta(newKarta("job", jobGVK)))).To(Succeed())
		Expect(inventory.Changes()).To(Receive())
		results, err = inventory.List()
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(BeEmpty())
	})

	It("signals shutdown so observers see unavailable cluster data", func() {
		inventory, err := New(newFakeClient(), clusterOnly)
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- inventory.Run(ctx) }()
		DeferCleanup(cancel)
		Expect(inventory.WaitLoaded(ctx)).To(Succeed())
		Expect(inventory.Changes()).To(Receive())
		cancel()
		Eventually(done, eventuallyTimeout).Should(Receive(BeNil()))
		Expect(inventory.Changes()).To(Receive())
		results, err := inventory.List()
		Expect(results).To(BeNil())
		Expect(err).To(MatchError(ErrUnavailable))
	})

	It("never mixes complete-list generations across selected kinds", func() {
		inventory := loadedInventory(clusterOnly)
		store := reflectorStore{inventory: inventory}
		var workers sync.WaitGroup
		stop := make(chan struct{})
		for range 4 {
			workers.Go(func() {
				defer GinkgoRecover()
				for {
					select {
					case <-stop:
						return
					default:
					}
					results, err := inventory.List()
					Expect(err).NotTo(HaveOccurred())
					if len(results) != 0 {
						Expect(results).To(HaveLen(2))
						Expect(results[0].Origin.ResourceVersion).To(Equal(results[1].Origin.ResourceVersion))
					}
				}
			})
		}
		defer func() { close(stop); workers.Wait() }()
		for n := range 50 {
			version := fmt.Sprint(n)
			Expect(store.Replace([]any{
				unstructuredKarta(newKarta("job", jobGVK, fromCluster("job-uid", version))),
				unstructuredKarta(newKarta("deployment", deploymentGVK, fromCluster("deployment-uid", version))),
			}, version)).To(Succeed())
		}
	})
})

var _ = Describe("cluster label selection", func() {
	It("combines names and labels and removes a definition that loses its match", func() {
		cfg := Config{Cluster: SourceConfig{
			Enabled:       true,
			Include:       []string{"selected", "excluded", "wrong-label"},
			Exclude:       []string{"excluded"},
			LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"enabled": "yes"}},
		}}
		k := newKarta("selected", jobGVK, withLabels(map[string]string{"enabled": "yes"}))
		inventory := loadedInventory(cfg, k,
			newKarta("excluded", deploymentGVK, withLabels(map[string]string{"enabled": "yes"})),
			newKarta("wrong-label", podGVK, withLabels(map[string]string{"enabled": "no"})))
		results, err := inventory.List()
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Origin.Name).To(Equal("selected"))
		Expect(inventory.Changes()).To(Receive())
		k.Labels["enabled"] = "no"
		Expect((reflectorStore{inventory: inventory}).Update(unstructuredKarta(k))).To(Succeed())
		Expect(inventory.Changes()).To(Receive())
		results, err = inventory.List()
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(BeEmpty())
		k.Labels["enabled"] = "yes"
		Expect((reflectorStore{inventory: inventory}).Update(unstructuredKarta(k))).To(Succeed())
		Expect(inventory.Changes()).To(Receive())
		selection, err := inventory.Lookup(schema.GroupVersionKind{Group: "batch", Kind: "Job"})
		Expect(err).NotTo(HaveOccurred())
		Expect(selection.Origin.Name).To(Equal("selected"))
	})

	It("rejects catalog selectors and malformed cluster selectors", func() {
		_, err := New(nil, Config{Catalog: SourceConfig{LabelSelector: &metav1.LabelSelector{}}})
		Expect(err).To(MatchError(ErrInvalidConfig))
		_, err = New(newFakeClient(), Config{Cluster: SourceConfig{LabelSelector: &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "enabled", Operator: "Invalid"}},
		}}})
		Expect(err).To(MatchError(ErrInvalidConfig))
	})
})
