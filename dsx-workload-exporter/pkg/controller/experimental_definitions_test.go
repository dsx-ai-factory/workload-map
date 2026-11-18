// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"

	inventory "github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
)

func TestExperimentalDefinitionWatchRefreshesQueuesAndSourceHealth(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%v", enabled), func(t *testing.T) {
			g := gomega.NewWithT(t)
			gvk := schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
			gvr := kartaGVK.GroupVersion().WithResource("kartas")
			client := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
				map[schema.GroupVersionResource]string{gvr: "KartaList"})
			definitions, err := inventory.New(client, inventory.Config{
				EnableExperimental: enabled,
				Catalog:            inventory.SourceConfig{Enabled: true, Include: []string{"batch-job-v1"}},
				Cluster:            inventory.SourceConfig{Enabled: true},
			})
			g.Expect(err).NotTo(gomega.HaveOccurred())
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			done := make(chan error, 1)
			go func() { done <- definitions.Run(ctx) }()
			t.Cleanup(func() {
				cancel()
				g.Eventually(done, time.Second).Should(gomega.Receive(gomega.BeNil()))
			})
			g.Expect(definitions.WaitLoaded(ctx)).To(gomega.Succeed())
			initial, err := definitions.Lookup(gvk)
			g.Expect(err).NotTo(gomega.HaveOccurred())
			r, _ := definitionRefreshState(t, selectedDefinition{Selection: initial})
			publications := store.New()
			r.deps.Publications = publications
			r.deps.Inventory = definitions
			r.config.ClusterDefinitions = true
			definition := initial.Definition.DeepCopy()
			definition.Name = "cluster-job"
			definition.UID = "cluster-job"
			resource := client.Resource(gvr)

			for n, step := range []struct {
				name         string
				experimental bool
			}{
				{name: "create"},
				{name: "mark experimental", experimental: true},
				{name: "mark stable"},
				{name: "delete"},
			} {
				t.Run(step.name, func(t *testing.T) {
					g := gomega.NewWithT(t)
					definition.Spec.Experimental = step.experimental
					definition.ResourceVersion = fmt.Sprint(n + 1)
					object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(definition)
					g.Expect(err).NotTo(gomega.HaveOccurred())
					value := &unstructured.Unstructured{Object: object}
					switch step.name {
					case "create":
						_, err = resource.Create(ctx, value, metav1.CreateOptions{})
					case "delete":
						err = resource.Delete(ctx, definition.Name, metav1.DeleteOptions{})
					default:
						_, err = resource.Update(ctx, value, metav1.UpdateOptions{})
					}
					g.Expect(err).NotTo(gomega.HaveOccurred())
					want := inventory.Origin{Source: inventory.SourceCluster, Name: definition.Name,
						UID: definition.UID, ResourceVersion: definition.ResourceVersion}
					if step.name == "delete" || step.experimental && !enabled {
						want = initial.Origin
					}
					g.Eventually(func() inventory.Origin {
						selection, _ := definitions.Lookup(gvk)
						return selection.Origin
					}, 5*time.Second, 10*time.Millisecond).Should(gomega.Equal(want))
					g.Expect(definitions.Changes()).To(gomega.Receive())
					next, sourceErr := r.prepareDefinitions()
					g.Expect(r.refreshDefinitions(next, sourceErr)).To(gomega.Succeed())
					g.Expect(r.podQueue.Len()).To(gomega.BeNumerically(">", 0))
					g.Expect(r.workloadQueue.Len()).To(gomega.BeNumerically(">", 0))
					view := publications.Capture()
					g.Expect(view.Sources[definitionSource]).To(gomega.Equal(store.SourceHealth{
						GVK: kartaGVK, Synced: true,
					}))
					g.Expect(view.Failures).To(gomega.BeEmpty())
					for r.podQueue.Len() > 0 {
						key, _ := r.podQueue.Get()
						r.podQueue.Done(key)
					}
					for r.workloadQueue.Len() > 0 {
						key, _ := r.workloadQueue.Get()
						r.workloadQueue.Done(key)
					}
				})
			}
		})
	}
}
