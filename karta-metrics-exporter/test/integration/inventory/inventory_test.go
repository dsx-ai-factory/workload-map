// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package inventory

import (
	"context"
	"fmt"
	"sync/atomic"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	inventory "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/kartadefinitioninventory"
	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog"
)

var (
	kartasGVR   = v1alpha1.GroupVersion.WithResource("kartas")
	crdsGVR     = apiextensionsv1.SchemeGroupVersion.WithResource("customresourcedefinitions")
	jobGVK      = schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
	withCatalog = inventory.Config{
		Catalog: inventory.SourceConfig{Enabled: true, Exclude: []string{"core-pod-v1"}},
		Cluster: inventory.SourceConfig{Enabled: true},
	}
	errRefused = fmt.Errorf("dial tcp: %w", syscall.ECONNREFUSED)
)

// recordingClient counts LIST calls and streaming lists, and can opt out of
// streaming lists or have them refused, to pin which path the reflector takes.
// refuse fails every call as an unreachable API server does.
type recordingClient struct {
	dynamic.Interface
	optOut        bool
	rejectStreams bool
	refuse        atomic.Bool
	lists         atomic.Int32
	streams       atomic.Int32
	latest        atomic.Pointer[watch.Interface]
}

type recordingResource struct {
	dynamic.NamespaceableResourceInterface
	client *recordingClient
}

func (c *recordingClient) IsWatchListSemanticsUnSupported() bool { return c.optOut }

func (c *recordingClient) Resource(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return recordingResource{NamespaceableResourceInterface: c.Interface.Resource(gvr), client: c}
}

func (r recordingResource) List(ctx context.Context, options metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	r.client.lists.Add(1)
	if r.client.refuse.Load() {
		return nil, errRefused
	}
	return r.NamespaceableResourceInterface.List(ctx, options)
}

func (r recordingResource) Watch(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
	if options.SendInitialEvents != nil && *options.SendInitialEvents {
		r.client.streams.Add(1)
		if r.client.rejectStreams {
			// What an API server with the WatchList feature gate off answers.
			return nil, apierrors.NewInvalid(schema.GroupKind{Group: "meta.k8s.io", Kind: "ListOptions"}, "",
				field.ErrorList{field.Forbidden(field.NewPath("sendInitialEvents"), "sendInitialEvents is forbidden for watch unless the WatchList feature gate is enabled")})
		}
	}
	if r.client.refuse.Load() {
		return nil, errRefused
	}
	w, err := r.NamespaceableResourceInterface.Watch(ctx, options)
	if err == nil {
		r.client.latest.Store(&w)
	}
	return w, err
}

func clientFor(config *rest.Config) dynamic.Interface {
	GinkgoHelper()
	client, err := dynamic.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred())
	return client
}

// clusterKarta copies the catalog definition for gvk under a new name.
func clusterKarta(name string, gvk schema.GroupVersionKind) *unstructured.Unstructured {
	GinkgoHelper()
	k, err := catalog.Get(gvk)
	Expect(err).NotTo(HaveOccurred())
	k.ObjectMeta = metav1.ObjectMeta{Name: name}
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(k)
	Expect(err).NotTo(HaveOccurred())
	u := &unstructured.Unstructured{Object: m}
	u.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("Karta"))
	return u
}

func create(objects ...*unstructured.Unstructured) {
	GinkgoHelper()
	for _, u := range objects {
		_, err := adminClient.Resource(kartasGVR).Create(testCtx, u, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
	}
}

// start runs a new inventory reading the cluster through client until the spec ends.
func start(client dynamic.Interface, config inventory.Config) *inventory.Inventory {
	GinkgoHelper()
	inv, err := inventory.New(client, config)
	Expect(err).NotTo(HaveOccurred())
	ctx, cancel := context.WithCancel(testCtx)
	done := make(chan error, 1)
	go func() { done <- inv.Run(ctx) }()
	DeferCleanup(func() {
		cancel()
		Eventually(done, eventuallyTimeout, eventuallyInterval).Should(Receive(BeNil()))
	})
	return inv
}

func expectOrigin(inv *inventory.Inventory, gvk schema.GroupVersionKind, want func(inventory.Origin) bool) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		sel, err := inv.Lookup(gvk)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(want(sel.Origin)).To(BeTrue(), "origin %+v", sel.Origin)
	}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
}

var _ = Describe("Karta definition inventory", func() {
	AfterEach(func() {
		Expect(adminClient.Resource(kartasGVR).DeleteCollection(testCtx, metav1.DeleteOptions{}, metav1.ListOptions{})).To(Succeed())
		Eventually(func(g Gomega) {
			list, err := adminClient.Resource(kartasGVR).List(testCtx, metav1.ListOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(list.Items).To(BeEmpty())
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
	})

	It("serves a cluster Karta with the server's identity, follows changes, and serves the catalog after a delete", func() {
		create(clusterKarta("cluster-job", jobGVK))
		created, err := adminClient.Resource(kartasGVR).Get(testCtx, "cluster-job", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		inv := start(clientFor(adminConfig), withCatalog)

		expectOrigin(inv, jobGVK, func(o inventory.Origin) bool {
			return o == inventory.Origin{Source: inventory.SourceCluster, Name: "cluster-job", UID: created.GetUID(), ResourceVersion: created.GetResourceVersion()}
		})

		created.SetLabels(map[string]string{"example.com/owner": "platform"})
		updated, err := adminClient.Resource(kartasGVR).Update(testCtx, created, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())
		expectOrigin(inv, jobGVK, func(o inventory.Origin) bool { return o.ResourceVersion == updated.GetResourceVersion() })

		Expect(adminClient.Resource(kartasGVR).Delete(testCtx, "cluster-job", metav1.DeleteOptions{})).To(Succeed())
		expectOrigin(inv, jobGVK, func(o inventory.Origin) bool {
			return o == inventory.Origin{Source: inventory.SourceCatalog, Name: "batch-job-v1"}
		})
	})

	It("is unavailable while the API server is down, and recovers once it is back", func() {
		create(clusterKarta("cluster-job", jobGVK))
		inv := start(clientFor(adminConfig), withCatalog)
		expectOrigin(inv, jobGVK, func(o inventory.Origin) bool { return o.Source == inventory.SourceCluster })
		apiServer := testEnv.ControlPlane.GetAPIServer()

		Expect(apiServer.Stop()).To(Succeed())
		running := false
		// Restarted before AfterEach, which needs the server, even when an assertion fails.
		defer func() {
			if !running {
				Expect(apiServer.Start()).To(Succeed())
			}
		}()

		Eventually(inventoryHealth(inv), 2*eventuallyTimeout, eventuallyInterval).Should(MatchError(inventory.ErrUnavailable))
		Expect(apiServer.Start()).To(Succeed())
		running = true
		Eventually(inventoryHealth(inv), 2*eventuallyTimeout, eventuallyInterval).Should(Succeed())
		expectOrigin(inv, jobGVK, func(o inventory.Origin) bool { return o.Source == inventory.SourceCluster })
	})

	DescribeTable("classifies a LIST the server refuses, and never serves the catalog",
		func(config func() *rest.Config, want error) {
			inv := start(clientFor(config()), withCatalog)

			Eventually(inventoryHealth(inv), eventuallyTimeout, eventuallyInterval).Should(MatchError(want))
			_, err := inv.Lookup(jobGVK)
			Expect(err).To(MatchError(want))
		},
		Entry("a user without Karta access is forbidden", func() *rest.Config {
			GinkgoHelper()
			user, err := testEnv.AddUser(envtest.User{Name: "no-karta-access"}, nil)
			Expect(err).NotTo(HaveOccurred())
			return user.Config()
		}, inventory.ErrPermissionDenied),
		Entry("an unknown bearer token is unauthorized", func() *rest.Config {
			return &rest.Config{Host: adminConfig.Host, BearerToken: "not-a-real-token", TLSClientConfig: rest.TLSClientConfig{CAData: adminConfig.CAData}}
		}, inventory.ErrPermissionDenied),
	)

	It("reports refused streaming reconnects, which the reflector retries without LIST, and recovers", func() {
		create(clusterKarta("cluster-job", jobGVK))
		client := &recordingClient{Interface: clientFor(adminConfig)}
		inv := start(client, withCatalog)
		expectOrigin(inv, jobGVK, func(o inventory.Origin) bool { return o.Source == inventory.SourceCluster })
		Expect(client.streams.Load()).To(BeNumerically(">", 0))

		client.refuse.Store(true)
		(*client.latest.Load()).Stop()

		Eventually(inventoryHealth(inv), eventuallyTimeout, eventuallyInterval).Should(MatchError(inventory.ErrUnavailable))
		_, err := inv.Lookup(jobGVK)
		Expect(err).To(MatchError(inventory.ErrUnavailable))
		Expect(client.lists.Load()).To(BeZero())

		client.refuse.Store(false)
		Eventually(inventoryHealth(inv), eventuallyTimeout, eventuallyInterval).Should(Succeed())
		expectOrigin(inv, jobGVK, func(o inventory.Origin) bool { return o.Source == inventory.SourceCluster })
	})

	DescribeTable("is unavailable while the Karta CRD is missing and recovers once it is reinstalled",
		func(optOut, rejectStreams, wantStreams, wantLists bool) {
			create(clusterKarta("cluster-job", jobGVK))
			client := &recordingClient{Interface: clientFor(adminConfig), optOut: optOut, rejectStreams: rejectStreams}
			inv := start(client, withCatalog)
			expectOrigin(inv, jobGVK, func(o inventory.Origin) bool { return o.Source == inventory.SourceCluster })
			Expect(client.streams.Load() > 0).To(Equal(wantStreams), "streaming lists")
			Expect(client.lists.Load() > 0).To(Equal(wantLists), "LIST calls")

			Expect(adminClient.Resource(crdsGVR).Delete(testCtx, "kartas.run.ai", metav1.DeleteOptions{})).To(Succeed())
			DeferCleanup(func() {
				_, err := envtest.InstallCRDs(adminConfig, envtest.CRDInstallOptions{Paths: crdPaths, ErrorIfPathMissing: true})
				Expect(err).NotTo(HaveOccurred())
			})

			Eventually(inventoryHealth(inv), eventuallyTimeout, eventuallyInterval).Should(MatchError(inventory.ErrUnavailable))
			_, err := inv.Lookup(jobGVK)
			Expect(err).To(MatchError(inventory.ErrUnavailable))

			_, err = envtest.InstallCRDs(adminConfig, envtest.CRDInstallOptions{Paths: crdPaths, ErrorIfPathMissing: true})
			Expect(err).NotTo(HaveOccurred())
			// The CRD came back empty, so recovery is an empty list replacing the old one.
			Eventually(inventoryHealth(inv), eventuallyTimeout, eventuallyInterval).Should(Succeed())
			expectOrigin(inv, jobGVK, func(o inventory.Origin) bool {
				return o == inventory.Origin{Source: inventory.SourceCatalog, Name: "batch-job-v1"}
			})
		},
		Entry("over streaming lists", false, false, true, false),
		Entry("over LIST when the client opts out of streaming", true, false, false, true),
		Entry("over LIST when the server refuses streaming", false, true, true, true),
	)
})

// inventoryHealth probes an absent kind to isolate source failures from definition validation.
func inventoryHealth(inv *inventory.Inventory) func() error {
	return func() error {
		_, err := inv.Lookup(schema.GroupVersionKind{Group: "health.example", Version: "v1", Kind: "Absent"})
		return err
	}
}
