// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartadefinitioninventory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

var (
	deploymentGVK  = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	jobGVK         = schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
	podGVK         = schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	dynamoAlphaGVK = schema.GroupVersionKind{Group: "nvidia.com", Version: "v1alpha1", Kind: "DynamoGraphDeployment"}
	dynamoBetaGVK  = schema.GroupVersionKind{Group: "nvidia.com", Version: "v1beta1", Kind: "DynamoGraphDeployment"}

	epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
)

func newKarta(name string, gvk schema.GroupVersionKind, opts ...func(*v1alpha1.Karta)) *v1alpha1.Karta {
	k := newRootlessKarta(name)
	k.Spec.StructureDefinition.RootComponent.Kind = &v1alpha1.GroupVersionKind{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    gvk.Kind,
	}
	for _, opt := range opts {
		opt(k)
	}
	return k
}

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

func createdAt(t time.Time) func(*v1alpha1.Karta) {
	return func(k *v1alpha1.Karta) { k.CreationTimestamp = metav1.NewTime(t) }
}

func withLabels(l map[string]string) func(*v1alpha1.Karta) {
	return func(k *v1alpha1.Karta) { k.Labels = l }
}

func fromCluster(uid, resourceVersion string) func(*v1alpha1.Karta) {
	return func(k *v1alpha1.Karta) {
		k.UID = types.UID(uid)
		k.ResourceVersion = resourceVersion
	}
}

// invalid empties the root component name, which the SDK validator rejects.
func invalid(k *v1alpha1.Karta) {
	k.Spec.StructureDefinition.RootComponent.Name = ""
}

// The reflector backs off for up to a few seconds between attempts.
const (
	eventuallyTimeout  = 10 * time.Second
	eventuallyInterval = 200 * time.Millisecond
)

var (
	kartasGVR        = v1alpha1.GroupVersion.WithResource("kartas")
	kartasGR         = kartasGVR.GroupResource()
	withCatalog      = Config{Catalog: SourceConfig{Enabled: true, Exclude: []string{"core-pod-v1"}}, Cluster: SourceConfig{Enabled: true}}
	clusterOnly      = Config{Cluster: SourceConfig{Enabled: true}}
	catalogJobOrigin = Origin{Source: SourceCatalog, Name: "batch-job-v1"}
)

// pausedStore holds its first Replace until released, so a spec can act after
// a streaming list ends and before the store applies it.
type pausedStore struct {
	cache.ReflectorStore
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *pausedStore) Replace(list []any, resourceVersion string) error {
	s.once.Do(func() {
		close(s.entered)
		<-s.release
	})
	return s.ReflectorStore.Replace(list, resourceVersion)
}

// lateStatus holds the forwarder while it decodes a watch error, so a spec can
// stop the watch before the error is recorded.
type lateStatus struct {
	*unstructured.Unstructured
	read    chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *lateStatus) UnstructuredContent() map[string]any {
	s.once.Do(func() {
		close(s.read)
		<-s.release
	})
	return s.Object
}

func unstructuredKarta(k *v1alpha1.Karta) *unstructured.Unstructured {
	GinkgoHelper()
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(k)
	Expect(err).NotTo(HaveOccurred())
	u := &unstructured.Unstructured{Object: m}
	u.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("Karta"))
	return u
}

func newFakeClient(objects ...runtime.Object) *fake.FakeDynamicClient {
	return fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{kartasGVR: "KartaList"}, objects...)
}

// start runs the inventory until the spec ends and checks that Run returns cleanly.
func start(inventory *Inventory) context.CancelFunc {
	GinkgoHelper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- inventory.Run(ctx) }()
	DeferCleanup(func() {
		cancel()
		Eventually(done, eventuallyTimeout, eventuallyInterval).Should(Receive(BeNil()))
	})
	return cancel
}

func lookupOrigin(inventory *Inventory, gvk schema.GroupVersionKind) Origin {
	GinkgoHelper()
	sel, err := inventory.Lookup(gvk)
	Expect(err).NotTo(HaveOccurred())
	return sel.Origin
}

var _ = Describe("Inventory", func() {
	It("confirms absence for every kind when both sources are disabled, without a client or Run", func() {
		var logs bytes.Buffer
		previous := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
		DeferCleanup(func() { slog.SetDefault(previous) })

		inventory, err := New(nil, Config{})
		Expect(err).NotTo(HaveOccurred())
		Expect(logs.String()).To(ContainSubstring("both Karta definition sources are disabled"))

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		Expect(inventory.WaitLoaded(ctx)).To(Succeed())
		for _, gvk := range []schema.GroupVersionKind{jobGVK, deploymentGVK, podGVK, dynamoBetaGVK} {
			sel, err := inventory.Lookup(gvk)
			Expect(err).NotTo(HaveOccurred())
			Expect(sel).To(Equal(Selection{}), "%v", gvk)
		}
	})

	It("rejects a missing client when the cluster source is enabled", func() {
		_, err := New(nil, clusterOnly)

		Expect(err).To(MatchError(ErrNoClient))
	})

	It("rejects invalid options", func() {
		_, err := New(newFakeClient(), Config{Catalog: SourceConfig{Include: []string{"nope"}}})

		Expect(err).To(MatchError(ErrInvalidConfig))
	})

	It("serves a catalog-only configuration before Run and after it returns", func() {
		inventory, err := New(nil, Config{Catalog: SourceConfig{Enabled: true}})
		Expect(err).NotTo(HaveOccurred())
		Expect(inventoryHealth(inventory)()).To(Succeed())
		Expect(lookupOrigin(inventory, jobGVK)).To(Equal(catalogJobOrigin))

		start(inventory)()

		Consistently(func(g Gomega) {
			sel, err := inventory.Lookup(jobGVK)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(sel.Origin).To(Equal(catalogJobOrigin))
		}, time.Second, eventuallyInterval).Should(Succeed())
	})

	It("is not ready before Run, and serves nothing from the catalog meanwhile", func() {
		inventory, err := New(newFakeClient(), withCatalog)
		Expect(err).NotTo(HaveOccurred())

		Expect(inventoryHealth(inventory)()).To(MatchError(ErrNotReady))
		_, err = inventory.Lookup(jobGVK)
		Expect(err).To(MatchError(ErrNotReady))
	})

	It("runs once when many callers start it together, refusing the rest at once", func() {
		inventory, err := New(newFakeClient(), withCatalog)
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		const callers = 16
		results := make(chan error, callers)
		for range callers {
			go func() { results <- inventory.Run(ctx) }()
		}

		for range callers - 1 {
			Eventually(results, eventuallyTimeout).Should(Receive(MatchError(ContainSubstring("already started"))))
		}
		Consistently(results, "100ms").ShouldNot(Receive())
		Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(Succeed())
		cancel()
		Eventually(results, eventuallyTimeout).Should(Receive(BeNil()))
	})

	Context("with a cluster", func() {
		var (
			client    *fake.FakeDynamicClient
			inventory *Inventory
		)

		BeforeEach(func() {
			client = newFakeClient(unstructuredKarta(newKarta("cluster-job", jobGVK, fromCluster("uid-1", "1"))))
			var err error
			inventory, err = New(client, withCatalog)
			Expect(err).NotTo(HaveOccurred())
		})

		It("serves cluster definitions once the initial list is in", func() {
			start(inventory)

			Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(Succeed())
			Expect(lookupOrigin(inventory, jobGVK)).To(Equal(Origin{Source: SourceCluster, Name: "cluster-job", UID: "uid-1", ResourceVersion: "1"}))
			Expect(lookupOrigin(inventory, deploymentGVK)).To(Equal(Origin{Source: SourceCatalog, Name: "apps-deployment-v1"}))
		})

		It("follows adds, updates and deletes, and serves the catalog again after a delete", func() {
			start(inventory)
			Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(Succeed())
			resource := client.Resource(kartasGVR)
			ctx := context.Background()

			_, err := resource.Create(ctx, unstructuredKarta(newKarta("cluster-deployment", deploymentGVK, fromCluster("uid-2", "1"))), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				sel, err := inventory.Lookup(deploymentGVK)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(sel.Origin.Name).To(Equal("cluster-deployment"))
			}, eventuallyTimeout, eventuallyInterval).Should(Succeed())

			_, err = resource.Update(ctx, unstructuredKarta(newKarta("cluster-deployment", deploymentGVK, fromCluster("uid-2", "2"))), metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				sel, err := inventory.Lookup(deploymentGVK)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(sel.Origin.ResourceVersion).To(Equal("2"))
			}, eventuallyTimeout, eventuallyInterval).Should(Succeed())

			Expect(resource.Delete(ctx, "cluster-job", metav1.DeleteOptions{})).To(Succeed())
			Eventually(func(g Gomega) {
				sel, err := inventory.Lookup(jobGVK)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(sel.Origin).To(Equal(catalogJobOrigin))
			}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
		})

		It("serves the next oldest cluster Karta for a kind once the winner is deleted, not the catalog", func() {
			resource := client.Resource(kartasGVR)
			newer := newKarta("cluster-job-newer", jobGVK.GroupKind().WithVersion("v2"), fromCluster("uid-3", "1"), createdAt(epoch))
			_, err := resource.Create(context.Background(), unstructuredKarta(newer), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			start(inventory)
			Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(Succeed())
			Expect(lookupOrigin(inventory, jobGVK).Name).To(Equal("cluster-job"))

			Expect(resource.Delete(context.Background(), "cluster-job", metav1.DeleteOptions{})).To(Succeed())

			Eventually(func(g Gomega) {
				sel, err := inventory.Lookup(jobGVK)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(sel.Origin).To(Equal(Origin{Source: SourceCluster, Name: "cluster-job-newer", UID: "uid-3", ResourceVersion: "1"}))
				g.Expect(sel.GVK.Version).To(Equal("v2"))
			}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
		})

		It("reports an invalid cluster winner without falling back", func() {
			start(inventory)
			Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(Succeed())

			_, err := client.Resource(kartasGVR).Update(context.Background(),
				unstructuredKarta(newKarta("cluster-job", jobGVK, fromCluster("uid-1", "2"), invalid)), metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() error {
				_, err := inventory.Lookup(jobGVK)
				return err
			}, eventuallyTimeout, eventuallyInterval).Should(MatchError(ErrInvalidDefinition))
			Expect(inventoryHealth(inventory)()).To(Succeed())
		})

		It("fails closed on a Karta it cannot decode, and recovers once it is gone", func() {
			start(inventory)
			Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(Succeed())
			resource := client.Resource(kartasGVR)
			broken := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "run.ai/v1alpha1", "kind": "Karta",
				"metadata": map[string]any{"name": "broken"},
				"spec":     map[string]any{"structureDefinition": "not-an-object"},
			}}

			_, err := resource.Create(context.Background(), broken, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(MatchError(ContainSubstring(`decode Karta "broken"`)))
			Expect(inventoryHealth(inventory)()).To(MatchError(ErrUnavailable))
			_, err = inventory.Lookup(jobGVK)
			Expect(err).To(MatchError(ErrUnavailable))

			Expect(resource.Delete(context.Background(), "broken", metav1.DeleteOptions{})).To(Succeed())
			Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(Succeed())
		})

		It("reports the source as stopped after Run returns", func() {
			cancel := start(inventory)
			Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(Succeed())

			cancel()

			Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(MatchError(ErrUnavailable))
			Expect(inventoryHealth(inventory)()).To(MatchError(errStopped))
		})

		It("serves concurrent readers while definitions change", func() {
			start(inventory)
			Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(Succeed())
			resource := client.Resource(kartasGVR)

			var wg sync.WaitGroup
			stop := make(chan struct{})
			for range 8 {
				wg.Go(func() {
					defer GinkgoRecover()
					for {
						select {
						case <-stop:
							return
						default:
						}
						sel, err := inventory.Lookup(jobGVK)
						Expect(err).NotTo(HaveOccurred())
						Expect(sel.Definition).NotTo(BeNil())
					}
				})
			}
			for n := range 20 {
				k := unstructuredKarta(newKarta("churn", deploymentGVK))
				if n%2 == 0 {
					_, err := resource.Create(context.Background(), k, metav1.CreateOptions{})
					Expect(err).NotTo(HaveOccurred())
				} else {
					Expect(resource.Delete(context.Background(), "churn", metav1.DeleteOptions{})).To(Succeed())
				}
			}
			close(stop)
			wg.Wait()
		})
	})

	Context("with a failing source", func() {
		var (
			client    *fake.FakeDynamicClient
			inventory *Inventory
			listErr   atomic.Pointer[error]
			watchErr  atomic.Pointer[error]
			watches   atomic.Pointer[watch.RaceFreeFakeWatcher]
		)

		BeforeEach(func() {
			client = newFakeClient(unstructuredKarta(newKarta("cluster-job", jobGVK)))
			listErr.Store(nil)
			watchErr.Store(nil)
			watches.Store(nil)
			client.PrependReactor("list", "kartas", func(ktesting.Action) (bool, runtime.Object, error) {
				if err := listErr.Load(); err != nil {
					return true, nil, *err
				}
				return false, nil, nil
			})
			// A watch delivers only what a spec sends to it, so it never replays on its own.
			client.PrependWatchReactor("kartas", func(ktesting.Action) (bool, watch.Interface, error) {
				if err := watchErr.Load(); err != nil {
					return true, nil, *err
				}
				w := watch.NewRaceFreeFake()
				watches.Store(w)
				return true, w, nil
			})
			var err error
			inventory, err = New(client, withCatalog)
			Expect(err).NotTo(HaveOccurred())
		})

		fail := func(err error) { listErr.Store(&err) }
		// established waits for the reflector's watch, which opens after Replace has made the inventory ready.
		established := func() *watch.RaceFreeFakeWatcher {
			GinkgoHelper()
			Eventually(watches.Load, eventuallyTimeout, eventuallyInterval).ShouldNot(BeNil())
			return watches.Load()
		}

		DescribeTable("classifies a failing LIST and never serves the catalog meanwhile",
			func(cause error, want error) {
				fail(cause)
				start(inventory)

				Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(MatchError(want))
				_, err := inventory.Lookup(jobGVK)
				Expect(err).To(MatchError(want))
				Expect(err).To(MatchError(cause))
			},
			Entry("forbidden", apierrors.NewForbidden(kartasGR, "", errors.New("no list permission")), ErrPermissionDenied),
			Entry("unauthorized", apierrors.NewUnauthorized("unknown token"), ErrPermissionDenied),
			Entry("a missing Karta CRD, which the API server reports as not found", apierrors.NewNotFound(kartasGR, ""), ErrUnavailable),
			Entry("a server error", apierrors.NewInternalError(errors.New("etcd is down")), ErrUnavailable),
		)

		It("recovers once LIST succeeds again", func() {
			fail(apierrors.NewForbidden(kartasGR, "", errors.New("no list permission")))
			start(inventory)
			Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(MatchError(ErrPermissionDenied))

			listErr.Store(nil)

			Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(Succeed())
			Expect(lookupOrigin(inventory, jobGVK).Source).To(Equal(SourceCluster))
		})

		It("reports a refused watch, and recovers through a relist rather than a resumed watch that has not replayed", func() {
			start(inventory)
			Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(Succeed())
			Expect(lookupOrigin(inventory, deploymentGVK)).To(Equal(Origin{Source: SourceCatalog, Name: "apps-deployment-v1"}))
			resumable := established()

			refused := fmt.Errorf("dial tcp: %w", syscall.ECONNREFUSED)
			fail(refused)
			watchErr.Store(&refused)
			// An event before the close makes the reflector resume the watch instead of relisting.
			bookmark := &unstructured.Unstructured{}
			bookmark.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("Karta"))
			bookmark.SetResourceVersion("2")
			resumable.Action(watch.Bookmark, bookmark)
			resumable.Stop()

			Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(MatchError(ErrUnavailable))
			_, err := inventory.Lookup(jobGVK)
			Expect(err).To(MatchError(ErrUnavailable))

			_, err = client.Resource(kartasGVR).Create(context.Background(),
				unstructuredKarta(newKarta("cluster-deployment", deploymentGVK)), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			listErr.Store(nil)
			watchErr.Store(nil)

			Eventually(func(g Gomega) {
				sel, err := inventory.Lookup(deploymentGVK)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(sel.Origin.Name).To(Equal("cluster-deployment"))
			}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
		})
	})

	Context("while a streaming list is being applied", func() {
		DescribeTable("keeps an error that arrives before Replace, and recovers only through a fresh list",
			func(cause metav1.Status, want error) {
				streams := make(chan *watch.RaceFreeFakeWatcher, 8)
				var resumed atomic.Int32
				client := newFakeClient()
				client.PrependWatchReactor("kartas", func(action ktesting.Action) (bool, watch.Interface, error) {
					w := watch.NewRaceFreeFake()
					if initial := action.(ktesting.WatchActionImpl).ListOptions.SendInitialEvents; initial != nil && *initial {
						streams <- w
					} else {
						resumed.Add(1)
					}
					return true, w, nil
				})
				inventory, err := New(client, withCatalog)
				Expect(err).NotTo(HaveOccurred())
				paused := &pausedStore{ReflectorStore: reflectorStore{inventory: inventory}, entered: make(chan struct{}), release: make(chan struct{})}
				// Without the fake client's opt-out, the reflector uses streaming lists.
				reflector := cache.NewReflectorWithOptions(
					cache.ToListWatcherWithWatchListSemantics(inventory.listWatch(client.Resource(kartasGVR)), nil),
					&unstructured.Unstructured{}, paused, cache.ReflectorOptions{})
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan struct{})
				go func() {
					defer close(done)
					reflector.RunWithContext(ctx)
				}()
				release := sync.OnceFunc(func() { close(paused.release) })
				DeferCleanup(func() {
					cancel()
					release()
					Eventually(done, eventuallyTimeout).Should(BeClosed())
				})
				sendInitialEvents := func(stream *watch.RaceFreeFakeWatcher) {
					stream.Add(unstructuredKarta(newKarta("cluster-job", jobGVK, fromCluster("uid-1", "1"))))
					end := &unstructured.Unstructured{}
					end.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("Karta"))
					end.SetResourceVersion("2")
					end.SetAnnotations(map[string]string{metav1.InitialEventsAnnotationKey: "true"})
					stream.Action(watch.Bookmark, end)
				}

				var first *watch.RaceFreeFakeWatcher
				Eventually(streams, eventuallyTimeout).Should(Receive(&first))
				sendInitialEvents(first)
				Eventually(paused.entered, eventuallyTimeout).Should(BeClosed())

				first.Error(&cause)
				Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(MatchError(want))

				release()

				var second *watch.RaceFreeFakeWatcher
				Eventually(streams, eventuallyTimeout).Should(Receive(&second))
				Expect(inventoryHealth(inventory)()).To(MatchError(want))
				Expect(resumed.Load()).To(BeZero())

				sendInitialEvents(second)
				Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(Succeed())
				Expect(lookupOrigin(inventory, jobGVK).Name).To(Equal("cluster-job"))
			},
			Entry("forbidden", apierrors.NewForbidden(kartasGR, "", errors.New("permission revoked")).ErrStatus, ErrPermissionDenied),
			Entry("too many requests, after which the reflector would resume the watch",
				apierrors.NewTooManyRequests("slow down", 1).ErrStatus, ErrUnavailable),
		)
	})

	Context("health between calls and store writes", func() {
		var (
			ctx       = context.Background()
			inventory *Inventory
			store     reflectorStore
			lw        *cache.ListWatch
			listErr   atomic.Pointer[error]
			watchErr  atomic.Pointer[error]
			served    atomic.Pointer[watch.RaceFreeFakeWatcher]
		)

		list := func() { _, _ = lw.ListWithContextFunc(ctx, metav1.ListOptions{}) }
		watchCall := func(streaming bool) {
			options := metav1.ListOptions{}
			if streaming {
				options.SendInitialEvents = &streaming
			}
			if w, err := lw.WatchFuncWithContext(ctx, options); err == nil {
				w.Stop()
			}
		}
		replace := func(definitions ...*v1alpha1.Karta) {
			GinkgoHelper()
			items := make([]any, 0, len(definitions))
			for _, k := range definitions {
				items = append(items, unstructuredKarta(k))
			}
			Expect(store.Replace(items, "1")).To(Succeed())
		}

		BeforeEach(func() {
			listErr.Store(nil)
			watchErr.Store(nil)
			served.Store(nil)
			client := newFakeClient()
			client.PrependReactor("list", "kartas", func(ktesting.Action) (bool, runtime.Object, error) {
				if err := listErr.Load(); err != nil {
					return true, nil, *err
				}
				return false, nil, nil
			})
			client.PrependWatchReactor("kartas", func(ktesting.Action) (bool, watch.Interface, error) {
				if err := watchErr.Load(); err != nil {
					return true, nil, *err
				}
				w := watch.NewRaceFreeFake()
				served.Store(w)
				return true, w, nil
			})
			var err error
			inventory, err = New(client, withCatalog)
			Expect(err).NotTo(HaveOccurred())
			store = reflectorStore{inventory: inventory}
			lw = inventory.listWatch(client.Resource(kartasGVR))
		})

		It("stays pending after a successful LIST until Replace applies it", func() {
			list()
			Expect(inventoryHealth(inventory)()).To(MatchError(ErrNotReady))

			replace()

			Expect(inventoryHealth(inventory)()).To(Succeed())
		})

		It("keeps a LIST failure past a successful LIST, until an empty Replace applies fresh data", func() {
			replace(newKarta("cluster-job", jobGVK))
			Expect(lookupOrigin(inventory, jobGVK).Source).To(Equal(SourceCluster))
			denied := error(apierrors.NewForbidden(kartasGR, "", errors.New("revoked")))
			listErr.Store(&denied)
			list()
			Expect(inventoryHealth(inventory)()).To(MatchError(ErrPermissionDenied))

			listErr.Store(nil)
			list()

			Expect(inventoryHealth(inventory)()).To(MatchError(ErrPermissionDenied))
			_, err := inventory.Lookup(jobGVK)
			Expect(err).To(MatchError(ErrPermissionDenied))

			replace()

			Expect(inventoryHealth(inventory)()).To(Succeed())
			Expect(lookupOrigin(inventory, jobGVK)).To(Equal(catalogJobOrigin))
		})

		It("keeps a failure through a streaming list until its Replace arrives", func() {
			replace()
			down := error(apierrors.NewServiceUnavailable("restarting"))
			listErr.Store(&down)
			list()
			Expect(inventoryHealth(inventory)()).To(MatchError(ErrUnavailable))

			listErr.Store(nil)
			watchCall(true)

			Expect(inventoryHealth(inventory)()).To(MatchError(ErrUnavailable))

			replace(newKarta("cluster-job", jobGVK))

			Expect(inventoryHealth(inventory)()).To(Succeed())
			Expect(lookupOrigin(inventory, jobGVK).Name).To(Equal("cluster-job"))
		})

		DescribeTable("reports a refused watch call, which the reflector retries on its own",
			func(streaming bool) {
				replace()
				refused := fmt.Errorf("dial tcp: %w", syscall.ECONNREFUSED)
				watchErr.Store(&refused)

				watchCall(streaming)

				Expect(inventoryHealth(inventory)()).To(MatchError(ErrUnavailable))
				Expect(inventoryHealth(inventory)()).To(MatchError(refused))
			},
			Entry("a resumed watch", false),
			Entry("a streaming list, retried without falling back to LIST", true),
		)

		It("does not let a later page clear a failure recorded after its list began", func() {
			list()
			inventory.mu.Lock()
			inventory.fail(apierrors.NewServiceUnavailable("after the first page"))
			inventory.mu.Unlock()
			_, err := lw.ListWithContextFunc(ctx, metav1.ListOptions{Continue: "page-2"})
			Expect(err).NotTo(HaveOccurred())

			replace()

			Expect(inventoryHealth(inventory)()).To(MatchError(ErrUnavailable))
			list()
			replace()
			Expect(inventoryHealth(inventory)()).To(Succeed())
		})

		It("drops an error from a watch stopped before a fresh list", func() {
			replace()
			w, err := lw.WatchFuncWithContext(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			late := &lateStatus{
				Unstructured: &unstructured.Unstructured{Object: map[string]any{
					"apiVersion": "v1", "kind": "Status", "status": "Failure", "reason": "Forbidden", "code": int64(403),
				}},
				read:    make(chan struct{}),
				release: make(chan struct{}),
			}
			release := sync.OnceFunc(func() { close(late.release) })
			DeferCleanup(release)
			served.Load().Error(late)
			Eventually(late.read, eventuallyTimeout).Should(BeClosed())
			w.Stop()
			list()
			replace()

			release()

			Eventually(w.ResultChan(), eventuallyTimeout).Should(BeClosed())
			Expect(inventoryHealth(inventory)()).To(Succeed())
		})

		It("logs the unkeyable and unmatched-include reports only when they change", func() {
			var logs bytes.Buffer
			inventory.log = slog.New(slog.NewTextHandler(&logs, nil))
			var err error
			inventory, err = New(newFakeClient(), Config{Cluster: SourceConfig{Enabled: true, Include: []string{"rootless", "missing"}}})
			store = reflectorStore{inventory: inventory}
			inventory.log = slog.New(slog.NewTextHandler(&logs, nil))
			Expect(err).NotTo(HaveOccurred())
			unkeyable := func() int { return strings.Count(logs.String(), "whose root has no version or kind") }
			unmatched := func() int { return strings.Count(logs.String(), "include names match no Karta") }
			rootless := unstructuredKarta(newRootlessKarta("rootless"))

			Expect(store.Replace([]any{rootless}, "1")).To(Succeed())
			for range 3 {
				Expect(store.Update(rootless.DeepCopy())).To(Succeed())
			}
			Expect(unkeyable()).To(Equal(1))
			Expect(unmatched()).To(Equal(1))

			Expect(store.Update(unstructuredKarta(newKarta("rootless", jobGVK)))).To(Succeed())
			Expect(store.Update(rootless)).To(Succeed())
			missing := unstructuredKarta(newKarta("missing", jobGVK))
			Expect(store.Add(missing)).To(Succeed())
			Expect(store.Delete(missing)).To(Succeed())
			Expect(unkeyable()).To(Equal(2))
			Expect(unmatched()).To(Equal(2))
		})

		It("does not count a server without streaming lists as a failure, since LIST follows", func() {
			// What an API server with the WatchList feature gate off answers.
			rejected := error(apierrors.NewInvalid(schema.GroupKind{Group: "meta.k8s.io", Kind: "ListOptions"}, "",
				field.ErrorList{field.Forbidden(field.NewPath("sendInitialEvents"), "sendInitialEvents is forbidden for watch unless the WatchList feature gate is enabled")}))
			watchErr.Store(&rejected)

			watchCall(true)
			Expect(inventoryHealth(inventory)()).To(MatchError(ErrNotReady))

			replace()
			watchCall(true)
			Expect(inventoryHealth(inventory)()).To(Succeed())
		})

		It("records an error sent on an established watch before passing it on, and passes on every event in order", func() {
			replace()
			w, err := lw.WatchFuncWithContext(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(w.Stop)
			server := served.Load()
			server.Add(unstructuredKarta(newKarta("cluster-job", jobGVK)))
			server.Error(&apierrors.NewForbidden(kartasGR, "", errors.New("revoked")).ErrStatus)

			var event watch.Event
			Eventually(w.ResultChan(), eventuallyTimeout).Should(Receive(&event))
			Expect(event.Type).To(Equal(watch.Added))
			Eventually(w.ResultChan(), eventuallyTimeout).Should(Receive(&event))
			Expect(event.Type).To(Equal(watch.Error))

			Expect(inventoryHealth(inventory)()).To(MatchError(ErrPermissionDenied))
		})

		It("stops the server's watch and closes when stopped with an event still pending", func() {
			w, err := lw.WatchFuncWithContext(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			server := served.Load()
			server.Add(unstructuredKarta(newKarta("cluster-job", jobGVK)))

			w.Stop()

			Eventually(server.IsStopped, eventuallyTimeout, eventuallyInterval).Should(BeTrue())
			Eventually(w.ResultChan(), eventuallyTimeout).Should(BeClosed())
		})
	})
})

var _ = Describe("Inventory store updates", func() {
	It("removes an old group-kind entry when a definition changes kind", func() {
		inventory, err := New(newFakeClient(), withCatalog)
		Expect(err).NotTo(HaveOccurred())
		store := reflectorStore{inventory: inventory}
		Expect(store.Replace([]any{unstructuredKarta(newKarta("moving", jobGVK))}, "1")).To(Succeed())
		Expect(lookupOrigin(inventory, jobGVK).Name).To(Equal("moving"))

		Expect(store.Update(unstructuredKarta(newKarta("moving", deploymentGVK)))).To(Succeed())

		Expect(lookupOrigin(inventory, jobGVK)).To(Equal(catalogJobOrigin))
		Expect(lookupOrigin(inventory, deploymentGVK).Name).To(Equal("moving"))
	})

	It("neither decodes nor stores an excluded cluster Karta", func() {
		config := withCatalog
		config.Cluster.Exclude = []string{"broken"}
		inventory, err := New(newFakeClient(), config)
		Expect(err).NotTo(HaveOccurred())
		store := reflectorStore{inventory: inventory}
		broken := unstructuredKarta(newKarta("broken", jobGVK))
		broken.Object["spec"] = "not-an-object"
		Expect(store.Replace([]any{broken}, "1")).To(Succeed())
		Expect(store.Add(unstructuredKarta(newKarta("cluster-job", jobGVK)))).To(Succeed())

		Expect(inventoryHealth(inventory)()).To(Succeed())
		Expect(lookupOrigin(inventory, jobGVK).Name).To(Equal("cluster-job"))
		Expect(store.Update(broken)).To(Succeed())
		Expect(inventoryHealth(inventory)()).To(Succeed())
	})

	It("recovers from a decode error when the same definition is repaired", func() {
		inventory, err := New(newFakeClient(), withCatalog)
		Expect(err).NotTo(HaveOccurred())
		store := reflectorStore{inventory: inventory}
		broken := unstructuredKarta(newKarta("repaired", jobGVK))
		broken.Object["spec"] = "not-an-object"
		Expect(store.Replace([]any{broken}, "1")).To(Succeed())
		Expect(inventoryHealth(inventory)()).To(MatchError(ErrUnavailable))

		Expect(store.Update(unstructuredKarta(newKarta("repaired", jobGVK)))).To(Succeed())

		Expect(inventoryHealth(inventory)()).To(Succeed())
		Expect(lookupOrigin(inventory, jobGVK).Name).To(Equal("repaired"))
	})

})

var _ = Describe("WaitLoaded", func() {
	It("waits for a delayed initial list and releases every waiter after applying it", func() {
		client := newFakeClient(unstructuredKarta(newKarta("cluster-job", jobGVK)))
		entered, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		client.PrependReactor("list", "kartas", func(ktesting.Action) (bool, runtime.Object, error) {
			close(entered)
			<-release
			return false, nil, nil
		})
		inventory, err := New(client, withCatalog)
		Expect(err).NotTo(HaveOccurred())
		start(inventory)
		DeferCleanup(unblock)
		Eventually(entered, eventuallyTimeout).Should(BeClosed())
		ctx, cancel := context.WithTimeout(context.Background(), eventuallyTimeout)
		defer cancel()
		results := make(chan error, 8)
		for range cap(results) {
			go func() { results <- inventory.WaitLoaded(ctx) }()
		}
		Consistently(results, 100*time.Millisecond).ShouldNot(Receive())
		Expect(inventoryHealth(inventory)()).To(MatchError(ErrNotReady))

		unblock()

		for range cap(results) {
			Eventually(results, eventuallyTimeout).Should(Receive(BeNil()))
		}
		Expect(lookupOrigin(inventory, jobGVK).Name).To(Equal("cluster-job"))
	})

	It("loads an empty list and serves the catalog", func() {
		inventory, err := New(newFakeClient(), withCatalog)
		Expect(err).NotTo(HaveOccurred())
		start(inventory)
		ctx, cancel := context.WithTimeout(context.Background(), eventuallyTimeout)
		defer cancel()

		Expect(inventory.WaitLoaded(ctx)).To(Succeed())
		Expect(lookupOrigin(inventory, jobGVK)).To(Equal(catalogJobOrigin))
	})

	It("cancels one waiter without canceling another or starting Run", func() {
		inventory, err := New(newFakeClient(), withCatalog)
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		canceled := make(chan error, 1)
		go func() { canceled <- inventory.WaitLoaded(ctx) }()
		otherCtx, otherCancel := context.WithTimeout(context.Background(), eventuallyTimeout)
		defer otherCancel()
		other := make(chan error, 1)
		go func() { other <- inventory.WaitLoaded(otherCtx) }()

		cancel()

		Eventually(canceled, eventuallyTimeout).Should(Receive(MatchError(context.Canceled)))
		Expect(inventoryHealth(inventory)()).To(MatchError(ErrNotReady))
		Expect(other).NotTo(Receive())
		start(inventory)
		Eventually(other, eventuallyTimeout).Should(Receive(BeNil()))
	})

	It("returns immediately for catalog-only inventories before Run", func() {
		inventory, err := New(nil, Config{Catalog: SourceConfig{Enabled: true}})
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithTimeout(context.Background(), eventuallyTimeout)
		defer cancel()

		Expect(inventory.WaitLoaded(ctx)).To(Succeed())
		Expect(lookupOrigin(inventory, jobGVK)).To(Equal(catalogJobOrigin))
	})

	It("releases waiters when Run stops before the first list succeeds", func() {
		client := newFakeClient()
		client.PrependReactor("list", "kartas", func(ktesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewServiceUnavailable("not yet available")
		})
		inventory, err := New(client, withCatalog)
		Expect(err).NotTo(HaveOccurred())
		cancelRun := start(inventory)
		Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(MatchError(ErrUnavailable))
		ctx, cancel := context.WithTimeout(context.Background(), eventuallyTimeout)
		defer cancel()
		result := make(chan error, 1)
		go func() { result <- inventory.WaitLoaded(ctx) }()

		cancelRun()

		Eventually(result, eventuallyTimeout).Should(Receive(MatchError(errStopped)))
		Expect(inventory.WaitLoaded(ctx)).To(MatchError(errStopped))
	})

	It("remembers loading after a later source failure and shutdown", func() {
		inventory, err := New(newFakeClient(), withCatalog)
		Expect(err).NotTo(HaveOccurred())
		cancelRun := start(inventory)
		ctx, cancel := context.WithTimeout(context.Background(), eventuallyTimeout)
		defer cancel()
		Expect(inventory.WaitLoaded(ctx)).To(Succeed())
		inventory.mu.Lock()
		inventory.fail(apierrors.NewServiceUnavailable("disconnected"))
		inventory.mu.Unlock()

		Expect(inventoryHealth(inventory)()).To(MatchError(ErrUnavailable))
		Expect(inventory.WaitLoaded(ctx)).To(Succeed())
		cancelRun()
		Eventually(inventoryHealth(inventory), eventuallyTimeout).Should(MatchError(errStopped))
		Expect(inventory.WaitLoaded(ctx)).To(Succeed())
	})

	It("marks a complete list loaded even when a definition cannot be decoded", func() {
		broken := unstructuredKarta(newKarta("broken", jobGVK))
		broken.Object["spec"] = "not-an-object"
		inventory, err := New(newFakeClient(broken), withCatalog)
		Expect(err).NotTo(HaveOccurred())
		start(inventory)
		ctx, cancel := context.WithTimeout(context.Background(), eventuallyTimeout)
		defer cancel()

		Expect(inventory.WaitLoaded(ctx)).To(Succeed())
		Expect(inventoryHealth(inventory)()).To(MatchError(ErrUnavailable))
	})
})

// truncatedDecoder optionally sends a bookmark, then ends with a decode error.
// StreamWatcher closes without an Error event for io.ErrUnexpectedEOF.
type truncatedDecoder struct {
	bookmark bool
	broken   chan struct{}
	once     sync.Once
}

func (d *truncatedDecoder) Decode() (watch.EventType, runtime.Object, error) {
	if d.bookmark {
		d.bookmark = false
		bookmark := &unstructured.Unstructured{}
		bookmark.SetResourceVersion("2")
		return watch.Bookmark, bookmark, nil
	}
	<-d.broken
	return "", nil, io.ErrUnexpectedEOF
}

func (d *truncatedDecoder) Close() { d.once.Do(func() { close(d.broken) }) }

type streamErrorReporter struct{}

func (streamErrorReporter) AsObject(error) runtime.Object {
	return &metav1.Status{Status: "Failure", Code: 500}
}

var _ = Describe("Inventory watch recovery", func() {
	It("does not let a stopped old watch closing invalidate a fresh list", func() {
		client := newFakeClient()
		server := watch.NewRaceFreeFake()
		client.PrependWatchReactor("kartas", func(ktesting.Action) (bool, watch.Interface, error) {
			return true, server, nil
		})
		inventory, err := New(client, withCatalog)
		Expect(err).NotTo(HaveOccurred())
		store := reflectorStore{inventory: inventory}
		Expect(store.Replace(nil, "1")).To(Succeed())
		w, err := inventory.listWatch(client.Resource(kartasGVR)).WatchFuncWithContext(context.Background(), metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(w.Stop)

		w.Stop()
		Expect(store.Replace([]any{unstructuredKarta(newKarta("fresh", jobGVK))}, "2")).To(Succeed())
		server.Stop()

		Eventually(w.ResultChan(), eventuallyTimeout).Should(BeClosed())
		Expect(inventoryHealth(inventory)()).To(Succeed())
		Expect(lookupOrigin(inventory, jobGVK).Name).To(Equal("fresh"))
	})

	It("preserves the permission error when the failed watch subsequently closes", func() {
		client := newFakeClient()
		server := watch.NewRaceFreeFake()
		client.PrependWatchReactor("kartas", func(ktesting.Action) (bool, watch.Interface, error) {
			return true, server, nil
		})
		inventory, err := New(client, withCatalog)
		Expect(err).NotTo(HaveOccurred())
		store := reflectorStore{inventory: inventory}
		Expect(store.Replace(nil, "1")).To(Succeed())
		w, err := inventory.listWatch(client.Resource(kartasGVR)).WatchFuncWithContext(context.Background(), metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(w.Stop)
		denied := apierrors.NewForbidden(kartasGR, "", errors.New("permission revoked"))
		server.Error(&denied.ErrStatus)
		Eventually(w.ResultChan(), eventuallyTimeout).Should(Receive())
		Expect(inventoryHealth(inventory)()).To(MatchError(ErrPermissionDenied))

		server.Stop()

		Eventually(w.ResultChan(), eventuallyTimeout).Should(BeClosed())
		Expect(inventoryHealth(inventory)()).To(MatchError(ErrPermissionDenied))
		_, err = inventory.Lookup(jobGVK)
		Expect(err).To(MatchError(ErrPermissionDenied))
		Expect(err).To(MatchError(ContainSubstring("permission revoked")))
	})

	DescribeTable("stays healthy and lets the watch resume when it ends without an Error event", func(truncated bool) {
		client := newFakeClient()
		// A bookmark first, so the close is not a very short watch.
		fake := watch.NewRaceFreeFake()
		bookmark := &unstructured.Unstructured{}
		bookmark.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("Karta"))
		bookmark.SetResourceVersion("2")
		fake.Action(watch.Bookmark, bookmark)
		var server watch.Interface = fake
		end := server.Stop
		if truncated {
			decoder := &truncatedDecoder{bookmark: true, broken: make(chan struct{})}
			server = watch.NewStreamWatcher(decoder, streamErrorReporter{})
			end = decoder.Close
		}
		DeferCleanup(end)
		client.PrependWatchReactor("kartas", func(ktesting.Action) (bool, watch.Interface, error) {
			return true, server, nil
		})
		inventory, err := New(client, withCatalog)
		Expect(err).NotTo(HaveOccurred())
		store := reflectorStore{inventory: inventory}
		Expect(store.Replace(nil, "1")).To(Succeed())
		Expect(lookupOrigin(inventory, jobGVK)).To(Equal(catalogJobOrigin))
		w, err := inventory.listWatch(client.Resource(kartasGVR)).WatchFuncWithContext(context.Background(), metav1.ListOptions{ResourceVersion: "1"})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(w.Stop)

		Eventually(w.ResultChan(), eventuallyTimeout).Should(Receive())
		end()

		Eventually(w.ResultChan(), eventuallyTimeout).Should(BeClosed())
		Expect(inventoryHealth(inventory)()).To(Succeed())
		Expect(lookupOrigin(inventory, jobGVK)).To(Equal(catalogJobOrigin))
		resumed, err := inventory.listWatch(client.Resource(kartasGVR)).WatchFuncWithContext(context.Background(), metav1.ListOptions{ResourceVersion: "1"})
		Expect(err).NotTo(HaveOccurred())
		resumed.Stop()
	},
		Entry("unexpected EOF during decoding", true),
		Entry("clean closure of an idle watch", false),
	)

	It("is unavailable after a very short watch until the relist applies", func() {
		client := newFakeClient()
		var lists atomic.Int32
		relisting, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		DeferCleanup(unblock)
		client.PrependReactor("list", "kartas", func(ktesting.Action) (bool, runtime.Object, error) {
			list := &unstructured.UnstructuredList{}
			list.SetResourceVersion("1")
			if lists.Add(1) == 2 {
				close(relisting)
				<-release
				list.Items = []unstructured.Unstructured{*unstructuredKarta(newKarta("cluster-job", jobGVK))}
			}
			return true, list, nil
		})
		var watches atomic.Int32
		client.PrependWatchReactor("kartas", func(ktesting.Action) (bool, watch.Interface, error) {
			w := watch.NewRaceFreeFake()
			if watches.Add(1) == 1 {
				// Closed at once with no events, which client-go relists as a very short watch.
				w.Stop()
			}
			return true, w, nil
		})
		inventory, err := New(client, withCatalog)
		Expect(err).NotTo(HaveOccurred())
		start(inventory)

		Eventually(relisting, eventuallyTimeout).Should(BeClosed())
		_, err = inventory.Lookup(jobGVK)
		Expect(err).To(MatchError(ErrUnavailable), "neither the catalog Job nor a stale cluster one")

		unblock()
		Eventually(func(g Gomega) {
			sel, err := inventory.Lookup(jobGVK)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(sel.Origin.Name).To(Equal("cluster-job"))
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
	})

	It("resumes a truncated stream from its last resource version without a relist", func() {
		client := newFakeClient()
		decoder := &truncatedDecoder{bookmark: true, broken: make(chan struct{})}
		var lists atomic.Int32
		client.PrependReactor("list", "kartas", func(ktesting.Action) (bool, runtime.Object, error) {
			lists.Add(1)
			list := &unstructured.UnstructuredList{}
			list.SetResourceVersion("1")
			return true, list, nil
		})
		var watches atomic.Int32
		var resumedFrom atomic.Pointer[string]
		resumed := watch.NewRaceFreeFake()
		client.PrependWatchReactor("kartas", func(action ktesting.Action) (bool, watch.Interface, error) {
			if watches.Add(1) == 1 {
				return true, watch.NewStreamWatcher(decoder, streamErrorReporter{}), nil
			}
			rv := action.(ktesting.WatchAction).GetWatchRestrictions().ResourceVersion
			resumedFrom.Store(&rv)
			return true, resumed, nil
		})
		inventory, err := New(client, withCatalog)
		Expect(err).NotTo(HaveOccurred())
		start(inventory)
		DeferCleanup(decoder.Close)
		Eventually(inventoryHealth(inventory), eventuallyTimeout, eventuallyInterval).Should(Succeed())
		Eventually(watches.Load, eventuallyTimeout).Should(Equal(int32(1)))
		Expect(lookupOrigin(inventory, jobGVK)).To(Equal(catalogJobOrigin))

		decoder.Close()

		Eventually(watches.Load, eventuallyTimeout).Should(Equal(int32(2)))
		Expect(*resumedFrom.Load()).To(Equal("2"), "resumes after the bookmark, the last applied resource version")
		Expect(lists.Load()).To(Equal(int32(1)), "no relist")
		Expect(inventoryHealth(inventory)()).To(Succeed())

		// A real API server replays what changed after that resource version.
		resumed.Add(unstructuredKarta(newKarta("arrived-while-disconnected", jobGVK)))
		Eventually(func(g Gomega) {
			sel, err := inventory.Lookup(jobGVK)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(sel.Origin.Name).To(Equal("arrived-while-disconnected"))
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
	})
})

var _ = Describe("Config", func() {
	DescribeTable("rejects a config that cannot be applied",
		func(config Config, wantProblems ...string) {
			_, err := New(newFakeClient(), config)

			Expect(err).To(MatchError(ErrInvalidConfig))
			for _, problem := range wantProblems {
				Expect(err.Error()).To(ContainSubstring(problem))
			}
		},
		Entry("an unknown catalog include name",
			Config{Catalog: SourceConfig{Enabled: true, Include: []string{"batch-job-v2"}}},
			`catalog.include: unknown definition "batch-job-v2"`),
		Entry("an unknown catalog exclude name, even with the catalog disabled",
			Config{Catalog: SourceConfig{Exclude: []string{"core-pod"}}},
			`catalog.exclude: unknown definition "core-pod"`),
		Entry("an empty name",
			Config{Cluster: SourceConfig{Enabled: true, Include: []string{""}}},
			"cluster.include: empty definition name"),
		Entry("a duplicate name",
			Config{Cluster: SourceConfig{Enabled: true, Exclude: []string{"a", "a"}}},
			`cluster.exclude: duplicate definition name "a"`),
		Entry("every problem at once",
			Config{
				Catalog: SourceConfig{Include: []string{"nope"}},
				Cluster: SourceConfig{Enabled: true, Exclude: []string{""}},
			},
			`catalog.include: unknown definition "nope"`, "cluster.exclude: empty definition name"),
	)

	It("lists the known catalog names when a name is unknown", func() {
		_, err := New(newFakeClient(), Config{Catalog: SourceConfig{Include: []string{"nope"}}})

		Expect(err).To(MatchError(ContainSubstring("known: ")))
		Expect(err.Error()).To(ContainSubstring("batch-job-v1"))
		Expect(err.Error()).To(ContainSubstring("core-pod-v1"))
	})

	DescribeTable("accepts",
		func(config Config) {
			_, err := New(newFakeClient(), config)
			Expect(err).NotTo(HaveOccurred())
		},
		Entry("the cluster alone", clusterOnly),
		Entry("both sources disabled", Config{}),
		Entry("a cluster include name no definition has yet",
			Config{Cluster: SourceConfig{Enabled: true, Include: []string{"not-created-yet"}}}),
		Entry("the same name in include and exclude, where exclude wins",
			Config{Cluster: SourceConfig{Enabled: true, Include: []string{"a"}, Exclude: []string{"a"}}}),
	)
})

// inventoryHealth probes an absent kind so a selected definition's validation
// error cannot hide the global source state.
func inventoryHealth(inventory *Inventory) func() error {
	return func() error {
		_, err := inventory.Lookup(schema.GroupVersionKind{Group: "health.example", Version: "v1", Kind: "Absent"})
		return err
	}
}

// loadedInventory applies objects through the same store used by the reflector.
func loadedInventory(config Config, definitions ...*v1alpha1.Karta) *Inventory {
	GinkgoHelper()
	inventory, err := New(newFakeClient(), config)
	Expect(err).NotTo(HaveOccurred())
	if config.Cluster.Enabled {
		objects := make([]any, 0, len(definitions))
		for _, definition := range definitions {
			objects = append(objects, unstructuredKarta(definition))
		}
		Expect((reflectorStore{inventory: inventory}).Replace(objects, "1")).To(Succeed())
	}
	return inventory
}

var _ = Describe("Inventory selection", func() {
	DescribeTable("applies the cluster include and exclude names",
		func(source SourceConfig, want []string) {
			definitions := []*v1alpha1.Karta{
				newKarta("a", deploymentGVK),
				newKarta("b", jobGVK),
				newKarta("c", podGVK),
			}
			inventory := loadedInventory(Config{Cluster: source}, definitions...)
			for n, gvk := range []schema.GroupVersionKind{deploymentGVK, jobGVK, podGVK} {
				sel, err := inventory.Lookup(gvk)
				Expect(err).NotTo(HaveOccurred())
				if slices.Contains(want, definitions[n].Name) {
					Expect(sel.Origin.Name).To(Equal(definitions[n].Name))
				} else {
					Expect(sel).To(Equal(Selection{}))
				}
			}
		},
		Entry("all names", SourceConfig{Enabled: true}, []string{"a", "b", "c"}),
		Entry("include", SourceConfig{Enabled: true, Include: []string{"a"}}, []string{"a"}),
		Entry("exclude wins over include", SourceConfig{Enabled: true, Include: []string{"a", "b"}, Exclude: []string{"b"}}, []string{"a"}),
		Entry("exclude alone", SourceConfig{Enabled: true, Exclude: []string{"b"}}, []string{"a", "c"}),
		Entry("disabled", SourceConfig{Include: []string{"a"}}, []string{}),
	)

	DescribeTable("elects the same cluster winner for every arrival order",
		func(definitions []*v1alpha1.Karta, winner string) {
			for seed := int64(1); seed <= 10; seed++ {
				shuffled := slices.Clone(definitions)
				rand.New(rand.NewSource(seed)).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
				inventory := loadedInventory(clusterOnly, shuffled...)
				Expect(lookupOrigin(inventory, jobGVK).Name).To(Equal(winner))
			}
		},
		Entry("oldest before name", []*v1alpha1.Karta{newKarta("a-newer", jobGVK, createdAt(epoch.Add(time.Hour))), newKarta("b-older", jobGVK, createdAt(epoch))}, "b-older"),
		Entry("equal timestamps use name", []*v1alpha1.Karta{newKarta("b", jobGVK, createdAt(epoch)), newKarta("a", jobGVK, createdAt(epoch))}, "a"),
		Entry("missing timestamp is oldest", []*v1alpha1.Karta{newKarta("a", jobGVK, createdAt(epoch)), newKarta("z-unstamped", jobGVK)}, "z-unstamped"),
	)

	It("selects the catalog's highest version priority independent of requested version", func() {
		inventory := loadedInventory(Config{Catalog: SourceConfig{Enabled: true}})
		for _, version := range []string{"v1alpha1", "v1beta1", "v9"} {
			sel, err := inventory.Lookup(dynamoAlphaGVK.GroupKind().WithVersion(version))
			Expect(err).NotTo(HaveOccurred())
			Expect(sel.GVK).To(Equal(dynamoBetaGVK))
			Expect(sel.Origin.Name).To(Equal("nvidia-com-dynamographdeployment-v1beta1"))
		}
	})

	It("filters the catalog and lets an excluded cluster definition reveal it", func() {
		inventory := loadedInventory(Config{Catalog: SourceConfig{Enabled: true, Exclude: []string{"core-pod-v1"}}, Cluster: SourceConfig{Enabled: true, Exclude: []string{"cluster-job"}}}, newKarta("cluster-job", jobGVK))
		Expect(lookupOrigin(inventory, jobGVK)).To(Equal(catalogJobOrigin))
		sel, err := inventory.Lookup(podGVK)
		Expect(err).NotTo(HaveOccurred())
		Expect(sel).To(Equal(Selection{}))
	})

	It("shadows the catalog across versions without changing the selected version", func() {
		inventory := loadedInventory(withCatalog, newKarta("alpha", dynamoAlphaGVK, fromCluster("uid", "7")))
		sel, err := inventory.Lookup(dynamoBetaGVK)
		Expect(err).NotTo(HaveOccurred())
		Expect(sel.GVK).To(Equal(dynamoAlphaGVK))
		Expect(sel.Origin).To(Equal(Origin{Source: SourceCluster, Name: "alpha", UID: "uid", ResourceVersion: "7"}))
	})

	DescribeTable("ignores definitions without a complete root kind",
		func(definition *v1alpha1.Karta) {
			inventory := loadedInventory(withCatalog, definition)
			Expect(inventoryHealth(inventory)()).To(Succeed())
			Expect(lookupOrigin(inventory, jobGVK)).To(Equal(catalogJobOrigin))
		},
		Entry("nil kind", newRootlessKarta("rootless")),
		Entry("empty version", newKarta("versionless", schema.GroupVersionKind{Group: "batch", Kind: "Job"})),
		Entry("empty kind", newKarta("kindless", schema.GroupVersionKind{Group: "batch", Version: "v1"})),
	)

	It("returns caller-owned deep copies for cluster and catalog definitions", func() {
		inventory := loadedInventory(withCatalog, newKarta("cluster-job", jobGVK, withLabels(map[string]string{"team": "x"})))
		for _, gvk := range []schema.GroupVersionKind{jobGVK, deploymentGVK} {
			first, err := inventory.Lookup(gvk)
			Expect(err).NotTo(HaveOccurred())
			original := first.Definition.DeepCopy()
			first.Definition.Name = "mutated"
			if first.Definition.Labels != nil {
				first.Definition.Labels["team"] = "mutated"
			}
			first.Definition.Spec.StructureDefinition.RootComponent.Kind.Kind = "Mutated"
			second, err := inventory.Lookup(gvk)
			Expect(err).NotTo(HaveOccurred())
			Expect(second.Definition).To(Equal(original))
		}
	})

	It("owns decoded objects after store ingestion", func() {
		inventory := loadedInventory(clusterOnly)
		obj := unstructuredKarta(newKarta("owned", jobGVK, withLabels(map[string]string{"team": "x"})))
		Expect((reflectorStore{inventory: inventory}).Add(obj)).To(Succeed())
		obj.Object["metadata"].(map[string]any)["labels"].(map[string]any)["team"] = "changed"
		obj.Object["spec"].(map[string]any)["structureDefinition"].(map[string]any)["rootComponent"].(map[string]any)["name"] = "changed"
		sel, err := inventory.Lookup(jobGVK)
		Expect(err).NotTo(HaveOccurred())
		Expect(sel.Definition.Labels).To(Equal(map[string]string{"team": "x"}))
		Expect(sel.Definition.Spec.StructureDefinition.RootComponent.Name).To(Equal("root"))
	})

	It("keeps an invalid oldest winner until deletion elects the valid runner-up", func() {
		bad := newKarta("older-invalid", jobGVK, createdAt(epoch), invalid)
		inventory := loadedInventory(withCatalog, bad, newKarta("newer-valid", jobGVK, createdAt(epoch.Add(time.Hour))))
		var readers sync.WaitGroup
		for range 10 {
			readers.Go(func() {
				defer GinkgoRecover()
				for range 10 {
					sel, err := inventory.Lookup(jobGVK)
					Expect(err).To(MatchError(ErrInvalidDefinition))
					Expect(err.Error()).To(ContainSubstring("older-invalid"))
					Expect(sel).To(Equal(Selection{}))
				}
			})
		}
		readers.Wait()
		Expect(inventoryHealth(inventory)()).To(Succeed())
		Expect((reflectorStore{inventory: inventory}).Delete(unstructuredKarta(bad))).To(Succeed())
		Expect(lookupOrigin(inventory, jobGVK).Name).To(Equal("newer-valid"))
	})

	It("serves a valid winner until deletion elects its retained invalid runner-up", func() {
		winner := newKarta("older-valid", jobGVK, createdAt(epoch))
		inventory := loadedInventory(withCatalog, winner,
			newKarta("newer-invalid", jobGVK, createdAt(epoch.Add(time.Hour)), invalid))

		Expect(lookupOrigin(inventory, jobGVK).Name).To(Equal("older-valid"))
		Expect((reflectorStore{inventory: inventory}).Delete(unstructuredKarta(winner))).To(Succeed())

		sel, err := inventory.Lookup(jobGVK)
		Expect(err).To(MatchError(ErrInvalidDefinition))
		Expect(err.Error()).To(ContainSubstring("newer-invalid"))
		Expect(sel).To(Equal(Selection{}))
		Expect(inventoryHealth(inventory)()).To(Succeed())
	})

	It("accepts a core group and leaves an omitted status definition unchanged", func() {
		inventory := loadedInventory(clusterOnly, newKarta("pod", podGVK))
		sel, err := inventory.Lookup(podGVK)
		Expect(err).NotTo(HaveOccurred())
		Expect(sel.GVK).To(Equal(podGVK))
		Expect(sel.Definition.Spec.StructureDefinition.RootComponent.StatusDefinition).To(BeNil())
	})
})

func TestKartaDefinitionInventory(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Karta Definition Inventory Suite")
}

var _ = Describe("Inventory generation", func() {

	It("returns one coherent definition and origin while full lists replace the cluster", func() {
		inventory := loadedInventory(clusterOnly)
		store := reflectorStore{inventory: inventory}
		generations := make([][]any, 2)
		for n := range generations {
			generations[n] = []any{unstructuredKarta(newKarta(fmt.Sprintf("generation-%d", n), jobGVK.GroupKind().WithVersion(fmt.Sprintf("v%d", n+1)), fromCluster(fmt.Sprintf("uid-%d", n), fmt.Sprint(n))))}
		}
		Expect(store.Replace(generations[0], "0")).To(Succeed())
		var readers sync.WaitGroup
		begin := make(chan struct{})
		for range 4 {
			readers.Go(func() {
				defer GinkgoRecover()
				<-begin
				for range 100 {
					sel, err := inventory.Lookup(jobGVK)
					Expect(err).NotTo(HaveOccurred())
					generation := sel.Origin.ResourceVersion
					Expect(sel.Origin.Name).To(Equal("generation-" + generation))
					Expect(sel.Origin.UID).To(Equal(types.UID("uid-" + generation)))
					Expect(sel.Definition.Name).To(Equal(sel.Origin.Name))
					Expect(sel.Definition.ResourceVersion).To(Equal(generation))
					Expect(sel.Definition.UID).To(Equal(sel.Origin.UID))
					expectedVersion := "v1"
					if generation == "1" {
						expectedVersion = "v2"
					}
					Expect(sel.GVK).To(Equal(jobGVK.GroupKind().WithVersion(expectedVersion)))
					Expect(sel.Definition.Spec.StructureDefinition.RootComponent.Kind.Version).To(Equal(expectedVersion))
				}
			})
		}
		close(begin)
		for n := range 100 {
			Expect(store.Replace(generations[n%2], fmt.Sprint(n))).To(Succeed())
		}
		readers.Wait()
	})
})
