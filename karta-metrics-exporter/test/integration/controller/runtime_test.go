// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/app"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/bootstrap"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/config"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog"
)

type requests struct {
	mu              sync.Mutex
	watches         map[string]int
	metadataWatches map[string]int
	streams         map[string][]io.ReadCloser
	deny            string
}
type transport struct {
	next http.RoundTripper
	seen *requests
}
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func (t transport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.seen.mu.Lock()
	denied := t.seen.deny != "" && strings.Contains(request.URL.Path, t.seen.deny)
	t.seen.mu.Unlock()
	if denied {
		return &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","message":"test denial","code":403}`)), Request: request}, nil
	}
	response, err := t.next.RoundTrip(request)
	if err == nil && request.URL.Query().Get("watch") == "true" && response.StatusCode == http.StatusOK {
		t.seen.mu.Lock()
		t.seen.watches[request.URL.Path]++
		if strings.Contains(request.Header.Get("Accept"), "PartialObjectMetadata") {
			t.seen.metadataWatches[request.URL.Path]++
		}
		t.seen.streams[request.URL.Path] = append(t.seen.streams[request.URL.Path], response.Body)
		denied = t.seen.deny != "" && strings.Contains(request.URL.Path, t.seen.deny)
		t.seen.mu.Unlock()
		if denied {
			_ = response.Body.Close()
		}
	}
	return response, err
}

func TestWatchTransportCountsAcceptedWatches(t *testing.T) {
	for _, tc := range []struct {
		name            string
		query           string
		accept          string
		statusCode      int
		err             error
		watches         int
		metadataWatches int
	}{
		{name: "ordinary watch", query: "watch=true", statusCode: http.StatusOK, watches: 1},
		{name: "streaming watch", query: "watch=true&sendInitialEvents=true", statusCode: http.StatusOK, watches: 1},
		{name: "metadata streaming watch", query: "watch=true&sendInitialEvents=true", accept: "application/json;as=PartialObjectMetadata;g=meta.k8s.io;v=v1", statusCode: http.StatusOK, watches: 1, metadataWatches: 1},
		{name: "non-watch request", statusCode: http.StatusOK},
		{name: "rejected streaming watch", query: "watch=true&sendInitialEvents=true", statusCode: http.StatusBadRequest},
		{name: "transport error", query: "watch=true", err: io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test/api/v1/pods?"+tc.query, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Accept", tc.accept)
			var response *http.Response
			if tc.err == nil {
				response = &http.Response{StatusCode: tc.statusCode, Body: io.NopCloser(strings.NewReader("")), Request: request}
				defer response.Body.Close()
			}
			seen := &requests{watches: map[string]int{}, metadataWatches: map[string]int{}, streams: map[string][]io.ReadCloser{}}
			wrapped := transport{next: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response, tc.err
			}), seen: seen}
			got, err := wrapped.RoundTrip(request)
			if got != response || err != tc.err {
				t.Fatalf("RoundTrip() = (%v, %v), want (%v, %v)", got, err, response, tc.err)
			}
			seen.mu.Lock()
			watches, metadataWatches := seen.watches[request.URL.Path], seen.metadataWatches[request.URL.Path]
			seen.mu.Unlock()
			if watches != tc.watches || metadataWatches != tc.metadataWatches {
				t.Errorf("watch counts = (%d, %d), want (%d, %d)", watches, metadataWatches, tc.watches, tc.metadataWatches)
			}
		})
	}
}

// Interrupt the live stream so the reflector observes the changed permission on
// reconnect. A transport rule alone cannot revoke an already accepted WATCH.
func (r *requests) denyRequests(path string) {
	r.mu.Lock()
	r.deny = path
	var streams []io.ReadCloser
	if path != "" {
		for name, bodies := range r.streams {
			if strings.Contains(name, path) {
				streams = append(streams, bodies...)
			}
		}
	}
	r.mu.Unlock()
	for _, stream := range streams {
		_ = stream.Close()
	}
}

var environment *envtest.Environment
var kubeConfig *rest.Config
var api client.Client
var deployGVK = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}

func TestController(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Exporter API integration") }

var _ = BeforeSuite(func() {
	Expect(os.Getenv("KUBEBUILDER_ASSETS")).NotTo(BeEmpty(), "integration tests require KUBEBUILDER_ASSETS")
	environment = &envtest.Environment{CRDDirectoryPaths: []string{"../../../../charts/karta/crds"}, ErrorIfCRDPathMissing: true, UseExistingCluster: new(bool)}
	environment.ControlPlane.GetAPIServer().Configure().Append("feature-gates", "WatchList=true")
	var err error
	kubeConfig, err = environment.Start()
	Expect(err).NotTo(HaveOccurred())
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	Expect(appsv1.AddToScheme(scheme)).To(Succeed())
	Expect(batchv1.AddToScheme(scheme)).To(Succeed())
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	api, err = client.New(kubeConfig, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	SetDefaultEventuallyTimeout(15 * time.Second)
	SetDefaultEventuallyPollingInterval(50 * time.Millisecond)
})
var _ = AfterSuite(func() {
	if environment != nil {
		Expect(environment.Stop()).To(Succeed())
	}
})

func namespace(ctx context.Context) string {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "exporter-", Labels: map[string]string{"exporter-test": "selected"}}}
	Expect(api.Create(ctx, ns)).To(Succeed())
	return ns.Name
}

// envtest runs no workload controllers. These helpers create the ownership chain
// and seed Deployment status explicitly to test API observation and publication.
func deployment(ctx context.Context, ns, name string) *appsv1.Deployment {
	root := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: map[string]string{"example.com/team": "alpha"}}, Spec: appsv1.DeploymentSpec{Replicas: ptr.To(int32(1)), Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "example.test/image:v1"}}}}}}
	Expect(api.Create(ctx, root)).To(Succeed())
	root.Status = appsv1.DeploymentStatus{Replicas: 1, ReadyReplicas: 1, AvailableReplicas: 1, UpdatedReplicas: 1, Conditions: []appsv1.DeploymentCondition{
		{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionTrue},
		{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionTrue, Reason: "NewReplicaSetAvailable"},
	}}
	Expect(api.Status().Update(ctx, root)).To(Succeed())
	return root
}
func replicaSet(ctx context.Context, root *appsv1.Deployment, name string) *appsv1.ReplicaSet {
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: root.Namespace, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: root.Name, UID: root.UID, Controller: ptr.To(true)}}}, Spec: appsv1.ReplicaSetSpec{Replicas: ptr.To(int32(1)), Selector: root.Spec.Selector, Template: root.Spec.Template}}
	Expect(api.Create(ctx, rs)).To(Succeed())
	return rs
}
func pod(ctx context.Context, rs *appsv1.ReplicaSet, name string) *corev1.Pod {
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: rs.Namespace, Labels: map[string]string{"example.com/team": "pod-team"}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: rs.Name, UID: rs.UID, Controller: ptr.To(true)}}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "example.test/image:v1"}}}}
	Expect(api.Create(ctx, p)).To(Succeed())
	return p
}
func start(cfg config.Config, initialDeny ...string) (app.Components, *requests) {
	seen := &requests{watches: map[string]int{}, metadataWatches: map[string]int{}, streams: map[string][]io.ReadCloser{}}
	if len(initialDeny) > 0 {
		seen.deny = initialDeny[0]
	}
	connection := rest.CopyConfig(kubeConfig)
	connection.WrapTransport = func(next http.RoundTripper) http.RoundTripper { return transport{next, seen} }
	components, err := bootstrap.New(cfg, connection)
	Expect(err).NotTo(HaveOccurred())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.RunController(ctx, components) }()
	DeferCleanup(func() {
		cancel()
		Eventually(done, 10*time.Second).Should(Receive(Succeed()))
		Expect(components.Runtime.Ready()).To(BeFalse())
	})
	return components, seen
}
func defaults(ns string) config.Config {
	cfg := config.DefaultConfig()
	cfg.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": ns}}
	cfg.KartaDefinitionSource.Catalog.Enabled = true
	cfg.KartaDefinitionSource.Catalog.Include = []string{"apps-deployment-v1"}
	cfg.KartaDefinitionSource.Cluster.Enabled = false
	cfg.RequestTimeout = "1s"
	return cfg
}

func podResult(view store.CapturedView, uid types.UID) *store.PodResult {
	for _, result := range view.Pods {
		if result.Record.UID == uid {
			return result
		}
	}
	return nil
}

func workloadResult(view store.CapturedView, uid types.UID) *store.WorkloadResult {
	for _, result := range view.Workloads {
		if result.Record.UID == uid {
			return result
		}
	}
	return nil
}

func expectPod(g Gomega, view store.CapturedView, p *corev1.Pod, root client.Object) *store.PodResult {
	result := podResult(view, p.UID)
	g.Expect(result).NotTo(BeNil())
	g.Expect(result.HasValue).To(BeTrue())
	g.Expect(result.Record.Name).To(Equal(p.Name))
	g.Expect(result.Record.Namespace).To(Equal(p.Namespace))
	g.Expect(result.Record.Workload.UID).To(Equal(root.GetUID()))
	valid, reason := view.Valid(result.Sources, result.Error)
	g.Expect(valid).To(BeTrue(), reason)
	return result
}

func expectWorkload(g Gomega, view store.CapturedView, root client.Object) *store.WorkloadResult {
	result := workloadResult(view, root.GetUID())
	g.Expect(result).NotTo(BeNil())
	g.Expect(result.HasValue).To(BeTrue())
	g.Expect(result.Record.Ref.Name).To(Equal(root.GetName()))
	g.Expect(result.Record.Ref.Namespace).To(Equal(root.GetNamespace()))
	g.Expect(result.Record.CreatedTimestamp).To(Equal(root.GetCreationTimestamp().Unix()))
	valid, reason := view.Valid(result.Sources, result.Error)
	g.Expect(valid).To(BeTrue(), reason)
	return result
}

var _ = Describe("Runtime and publication store", func() {
	It("keeps fallback errors visible and retains the last valid Pod labels", func(ctx SpecContext) {
		ns := namespace(ctx)
		root := deployment(ctx, ns, "label-root")
		p := pod(ctx, replicaSet(ctx, root, "label-middle"), "label-pod")
		Expect(api.Get(ctx, client.ObjectKeyFromObject(root), root)).To(Succeed())
		delete(root.Labels, "example.com/team")
		Expect(api.Update(ctx, root)).To(Succeed())
		cfg := defaults(ns)
		cfg.Rules = []labelresolver.LabelRule{{Name: "team", Metrics: []string{labelresolver.MetricPodWorkloadInfo}, Sources: []labelresolver.Source{labelresolver.SourceWorkload, labelresolver.SourcePod}, Expression: `object.metadata.labels["example.com/team"]`}}
		components, _ := start(cfg)
		check := func(g Gomega, team string, valid bool) {
			view := components.Store.Capture()
			result := podResult(view, p.UID)
			g.Expect(result).NotTo(BeNil())
			g.Expect(result.HasValue).To(BeTrue())
			g.Expect(result.Record.Workload.UID).To(Equal(root.UID))
			g.Expect(result.Record.Labels).To(HaveKeyWithValue("team", team))
			actual, reason := view.Valid(result.Sources, result.Error)
			g.Expect(actual).To(Equal(valid))
			if !valid {
				g.Expect(reason).To(Equal(store.ReasonEvaluationFailed))
			}
		}
		Eventually(func(g Gomega) { check(g, "pod-team", false) }).Should(Succeed())
		for _, tc := range []struct {
			input, retained string
			valid           bool
		}{{"valid-team", "valid-team", true}, {"", "valid-team", false}, {"recovered-team", "recovered-team", true}} {
			Expect(api.Get(ctx, client.ObjectKeyFromObject(root), root)).To(Succeed())
			root.Labels = map[string]string{}
			if tc.input != "" {
				root.Labels["example.com/team"] = tc.input
			}
			Expect(api.Update(ctx, root)).To(Succeed())
			Eventually(func(g Gomega) { check(g, tc.retained, tc.valid) }).Should(Succeed())
		}
	})

	It("shares root watches, reads intermediate metadata, and publishes CEL labels and status", func(ctx SpecContext) {
		ns := namespace(ctx)
		root := deployment(ctx, ns, "root")
		rs := replicaSet(ctx, root, "middle")
		p, sibling := pod(ctx, rs, "first"), pod(ctx, rs, "second")
		cfg := defaults(ns)
		cfg.Rules = []labelresolver.LabelRule{
			{Name: "team", Metrics: []string{labelresolver.MetricPodWorkloadInfo, labelresolver.MetricWorkloadStatus}, Sources: []labelresolver.Source{labelresolver.SourceWorkload}, Expression: `object.metadata.labels["example.com/team"]`},
			{Name: "pod_team", Metrics: []string{labelresolver.MetricPodWorkloadInfo}, Sources: []labelresolver.Source{labelresolver.SourcePod}, Expression: `object.metadata.labels["example.com/team"]`},
		}
		components, seen := start(cfg)
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			result := expectPod(g, view, p, root)
			g.Expect(result.Record.Labels).To(Equal(map[string]string{"team": "alpha", "pod_team": "pod-team"}))
			workload := expectWorkload(g, view, root)
			g.Expect(workload.Record.HasStatus).To(BeTrue())
			g.Expect(workload.Record.Phases).To(ContainElement("Running"))
			g.Expect(workload.Record.StatusLabels).To(Equal(map[string]string{"team": "alpha"}))
			g.Expect(workload.StatusError).To(BeEmpty())
			g.Expect(components.Runtime.Ready()).To(BeTrue())
		}).Should(Succeed())
		Expect(api.Get(ctx, client.ObjectKeyFromObject(p), p)).To(Succeed())
		p.Labels["example.com/team"] = "updated-pod-team"
		Expect(api.Update(ctx, p)).To(Succeed())
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			g.Expect(expectPod(g, view, p, root).Record.Labels).To(Equal(map[string]string{"team": "alpha", "pod_team": "updated-pod-team"}))
			g.Expect(expectPod(g, view, sibling, root).Record.Labels).To(HaveKeyWithValue("pod_team", "pod-team"))
		}).Should(Succeed())
		Eventually(func() int {
			seen.mu.Lock()
			defer seen.mu.Unlock()
			return seen.watches["/apis/apps/v1/namespaces/"+ns+"/deployments"]
		}).Should(Equal(1))
		Eventually(func() int {
			seen.mu.Lock()
			defer seen.mu.Unlock()
			return seen.metadataWatches["/apis/apps/v1/namespaces/"+ns+"/replicasets"]
		}).Should(Equal(1))
	})

	It("publishes zero-Pod roots", func(ctx SpecContext) {
		ns := namespace(ctx)
		root := deployment(ctx, ns, "empty")
		components, _ := start(defaults(ns))
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			g.Expect(expectWorkload(g, view, root).Record.HasStatus).To(BeTrue())
			g.Expect(view.Pods).To(BeEmpty())
		}).Should(Succeed())
	})

	It("evaluates full Pod fields and refreshes Pod labels after a root-only change", func(ctx SpecContext) {
		ns := namespace(ctx)
		root := deployment(ctx, ns, "full-input")
		p := pod(ctx, replicaSet(ctx, root, "full-middle"), "full-pod")
		cfg := defaults(ns)
		cfg.Rules = []labelresolver.LabelRule{
			{Name: "scheduler", Metrics: []string{labelresolver.MetricPodWorkloadInfo}, Sources: []labelresolver.Source{labelresolver.SourcePod}, Expression: `object.spec.schedulerName`},
			{Name: "pod_phase", Metrics: []string{labelresolver.MetricPodWorkloadInfo}, Sources: []labelresolver.Source{labelresolver.SourcePod}, Expression: `object.status.phase`},
			{Name: "desired", Metrics: []string{labelresolver.MetricPodWorkloadInfo}, Sources: []labelresolver.Source{labelresolver.SourceWorkload}, Expression: `string(object.spec.replicas)`},
		}
		Expect(api.Get(ctx, client.ObjectKeyFromObject(p), p)).To(Succeed())
		p.Status.Phase = corev1.PodPending
		Expect(api.Status().Update(ctx, p)).To(Succeed())
		components, _ := start(cfg)
		check := func(g Gomega, phase, desired string) {
			result := expectPod(g, components.Store.Capture(), p, root)
			g.Expect(result.Record.Labels).To(Equal(map[string]string{"scheduler": "default-scheduler", "pod_phase": phase, "desired": desired}))
		}
		Eventually(func(g Gomega) { check(g, "Pending", "1") }).Should(Succeed())
		before := p.ResourceVersion
		Expect(api.Get(ctx, client.ObjectKeyFromObject(root), root)).To(Succeed())
		root.Spec.Replicas = ptr.To(int32(2))
		Expect(api.Update(ctx, root)).To(Succeed())
		Eventually(func(g Gomega) { check(g, "Pending", "2") }).Should(Succeed())
		Expect(api.Get(ctx, client.ObjectKeyFromObject(p), p)).To(Succeed())
		Expect(p.ResourceVersion).To(Equal(before), "envtest has no controller to update this Pod")
		p.Status.Phase = corev1.PodRunning
		Expect(api.Status().Update(ctx, p)).To(Succeed())
		Eventually(func(g Gomega) { check(g, "Running", "2") }).Should(Succeed())
	})

	It("refreshes intermediate reparenting and handles same-name UID replacement without stale metadata", func(ctx SpecContext) {
		ns := namespace(ctx)
		first, second := deployment(ctx, ns, "first"), deployment(ctx, ns, "second")
		rs := replicaSet(ctx, first, "middle")
		p := pod(ctx, rs, "moving")
		components, _ := start(defaults(ns))
		Eventually(func(g Gomega) { expectPod(g, components.Store.Capture(), p, first) }).Should(Succeed())
		rs.OwnerReferences[0].Name, rs.OwnerReferences[0].UID = second.Name, second.UID
		Expect(api.Update(ctx, rs)).To(Succeed())
		Eventually(func(g Gomega) { expectPod(g, components.Store.Capture(), p, second) }).Should(Succeed())
		Expect(api.Delete(ctx, rs, client.GracePeriodSeconds(0))).To(Succeed())
		Eventually(func() error { return api.Get(ctx, client.ObjectKeyFromObject(rs), &appsv1.ReplicaSet{}) }).ShouldNot(Succeed())
		replacement := replicaSet(ctx, first, "middle")
		Expect(api.Get(ctx, client.ObjectKeyFromObject(p), p)).To(Succeed())
		p.OwnerReferences[0].UID = replacement.UID
		Expect(api.Update(ctx, p)).To(Succeed())
		Eventually(func(g Gomega) { expectPod(g, components.Store.Capture(), p, first) }).Should(Succeed())
		Expect(api.Delete(ctx, p, client.GracePeriodSeconds(0))).To(Succeed())
		Eventually(func() *store.PodResult { return podResult(components.Store.Capture(), p.UID) }).Should(BeNil())
	})

	It("changes interpretation after Karta addition update and deletion without losing generic identity", func(ctx SpecContext) {
		ns := namespace(ctx)
		root := deployment(ctx, ns, "root")
		p := pod(ctx, replicaSet(ctx, root, "middle"), "pod")
		cfg := defaults(ns)
		cfg.KartaDefinitionSource.Catalog.Enabled = false
		cfg.KartaDefinitionSource.Cluster.Enabled = true
		components, _ := start(cfg)
		Eventually(func(g Gomega) {
			g.Expect(expectPod(g, components.Store.Capture(), p, root).Record.Component).To(Equal("<unknown>"))
		}).Should(Succeed())
		definition, err := catalog.Get(deployGVK)
		Expect(err).NotTo(HaveOccurred())
		definition.Name = "integration-" + ns
		Expect(api.Create(ctx, definition)).To(Succeed())
		DeferCleanup(func() { _ = api.Delete(context.Background(), definition) })
		Eventually(func(g Gomega) {
			g.Expect(expectPod(g, components.Store.Capture(), p, root).Record.Karta).To(Equal(definition.Name))
		}).Should(Succeed())
		Expect(api.Get(ctx, types.NamespacedName{Name: definition.Name}, definition)).To(Succeed())
		definition.Spec.StructureDefinition.RootComponent.Name = "renamed"
		definition.Spec.Instructions.GangScheduling = nil
		definition.Spec.StructureDefinition.ChildComponents[0].OwnerRef = ptr.To("renamed")
		Expect(api.Update(ctx, definition)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(expectPod(g, components.Store.Capture(), p, root).Record.Component).To(Equal("renamed"))
		}).Should(Succeed())
		Expect(api.Delete(ctx, definition)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(expectPod(g, components.Store.Capture(), p, root).Record.Component).To(Equal("<unknown>"))
		}).Should(Succeed())
	})

	It("invalidates an interrupted intermediate watch and recovers after permission returns", func(ctx SpecContext) {
		ns := namespace(ctx)
		root := deployment(ctx, ns, "root")
		p := pod(ctx, replicaSet(ctx, root, "middle"), "pod")
		components, seen := start(defaults(ns))
		Eventually(func(g Gomega) { expectPod(g, components.Store.Capture(), p, root) }).Should(Succeed())
		seen.denyRequests("/replicasets")
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			result := podResult(view, p.UID)
			g.Expect(result).NotTo(BeNil())
			g.Expect(result.HasValue).To(BeTrue())
			g.Expect(result.Record.OwnerResolution).To(Equal(store.Resolved))
			valid, reason := view.Valid(result.Sources, result.Error)
			g.Expect(valid).To(BeFalse())
			g.Expect(reason).To(Equal(store.ReasonPermissionDenied))
		}).Should(Succeed())
		seen.denyRequests("")
		Eventually(func(g Gomega) { expectPod(g, components.Store.Capture(), p, root) }).Should(Succeed())
	})

	It("keeps unrelated roots available when a required API is absent", func(ctx SpecContext) {
		ns := namespace(ctx)
		root := deployment(ctx, ns, "root")
		cfg := defaults(ns)
		missing := schema.GroupVersionKind{Group: "absent.example.test", Version: "v1", Kind: "Missing"}
		cfg.RequiredKinds = []schema.GroupVersionKind{missing}
		components, _ := start(cfg)
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			expectWorkload(g, view, root)
			g.Expect(view.Failures).To(ContainElement(SatisfyAll(
				HaveField("Input.GVK", missing), HaveField("Input.Namespace", ns), HaveField("Error", store.ReasonAPIUnavailable),
			)))
		}).Should(Succeed())
		Expect(components.Runtime.Ready()).To(BeFalse())
	})

	It("uses one cluster-wide Pod watch for empty namespace selectors", func(ctx SpecContext) {
		ns := namespace(ctx)
		deployment(ctx, ns, "root")
		cfg := defaults(ns)
		cfg.NamespaceSelector = &metav1.LabelSelector{}
		components, seen := start(cfg)
		Eventually(components.Runtime.Ready).Should(BeTrue())
		Eventually(func() int {
			seen.mu.Lock()
			defer seen.mu.Unlock()
			return seen.watches["/api/v1/pods"]
		}).Should(Equal(1))
	})

	It("keeps quiet paths valid without periodic metadata refresh", func(ctx SpecContext) {
		ns := namespace(ctx)
		root := deployment(ctx, ns, "quiet")
		p := pod(ctx, replicaSet(ctx, root, "quiet-middle"), "quiet-pod")
		components, _ := start(defaults(ns))
		check := func(g Gomega) { expectPod(g, components.Store.Capture(), p, root) }
		Eventually(check).Should(Succeed())
		Consistently(check, 3*time.Second, 50*time.Millisecond).Should(Succeed())
	})

	It("releases namespaced watches and Pod records when the namespace stops matching", func(ctx SpecContext) {
		ns := namespace(ctx)
		root := deployment(ctx, ns, "selected")
		p := pod(ctx, replicaSet(ctx, root, "middle"), "selected-pod")
		cfg := defaults(ns)
		cfg.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"exporter-test": "selected", "kubernetes.io/metadata.name": ns}}
		components, _ := start(cfg)
		Eventually(func(g Gomega) { expectPod(g, components.Store.Capture(), p, root) }).Should(Succeed())
		namespaceObject := &corev1.Namespace{}
		Expect(api.Get(ctx, types.NamespacedName{Name: ns}, namespaceObject)).To(Succeed())
		delete(namespaceObject.Labels, "exporter-test")
		Expect(api.Update(ctx, namespaceObject)).To(Succeed())
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			g.Expect(podResult(view, p.UID)).To(BeNil())
			g.Expect(workloadResult(view, root.UID)).To(BeNil())
			for key := range view.Sources {
				g.Expect(key.Namespace).NotTo(Equal(ns))
			}
		}).Should(Succeed())
		namespaceObject.Labels["exporter-test"] = "selected"
		Expect(api.Update(ctx, namespaceObject)).To(Succeed())
		Eventually(func(g Gomega) { expectPod(g, components.Store.Capture(), p, root) }).Should(Succeed())
	})

	It("isolates an invalid Deployment definition from a valid Job", func(ctx SpecContext) {
		ns := namespace(ctx)
		invalid := deployment(ctx, ns, "invalid")
		job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "valid", Namespace: ns}, Spec: batchv1.JobSpec{Suspend: ptr.To(true), Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{Name: "main", Image: "example.test/image:v1"}}}}}}
		Expect(api.Create(ctx, job)).To(Succeed())
		definition, err := catalog.Get(deployGVK)
		Expect(err).NotTo(HaveOccurred())
		definition.Name, definition.Labels = "invalid-"+ns, map[string]string{"test": ns}
		definition.Spec.StructureDefinition.RootComponent.Name = ""
		Expect(api.Create(ctx, definition)).To(Succeed())
		DeferCleanup(func() { _ = api.Delete(context.Background(), definition) })
		cfg := defaults(ns)
		cfg.KartaDefinitionSource.Catalog.Include = []string{"batch-job-v1"}
		cfg.KartaDefinitionSource.Cluster.Enabled = true
		cfg.KartaDefinitionSource.Cluster.LabelSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"test": ns}}
		components, _ := start(cfg)
		check := func(g Gomega) {
			view := components.Store.Capture()
			result := expectWorkload(g, view, job)
			g.Expect(result.Record.HasStatus).To(BeTrue())
			g.Expect(result.StatusError).To(BeEmpty())
			invalidResult := expectWorkload(g, view, invalid)
			g.Expect(invalidResult.StatusError).To(Equal(store.ReasonInvalidDefinition))
			g.Expect(invalidResult.Record.HasStatus).To(BeFalse())
		}
		Eventually(check).Should(Succeed())
		Consistently(check, time.Second).Should(Succeed())
	})

	It("reports a denied initial intermediate without inventing a root and recovers generic identity", func(ctx SpecContext) {
		ns := namespace(ctx)
		root := deployment(ctx, ns, "unknown-root")
		p := pod(ctx, replicaSet(ctx, root, "denied-middle"), "initial-denied")
		cfg := defaults(ns)
		cfg.KartaDefinitionSource.Catalog.Enabled = false
		components, seen := start(cfg, "/replicasets")
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			key := store.SourceKey{Resource: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, Namespace: ns}
			g.Expect(view.Sources).To(HaveKeyWithValue(key, HaveField("Error", store.ReasonPermissionDenied)))
			result := podResult(view, p.UID)
			if result != nil {
				g.Expect(result.HasValue).To(BeFalse())
			}
			g.Expect(view.Workloads).To(BeEmpty())
		}).Should(Succeed())
		seen.denyRequests("")
		Eventually(func(g Gomega) {
			result := expectPod(g, components.Store.Capture(), p, root)
			g.Expect(result.Record.Component).To(Equal("<unknown>"))
			g.Expect(result.Record.Instance).To(Equal("<unknown>"))
		}).Should(Succeed())
	})

	It("keeps verified creation while a selected definition source fails", func(ctx SpecContext) {
		ns := namespace(ctx)
		root := deployment(ctx, ns, "retained")
		pod(ctx, replicaSet(ctx, root, "middle"), "retained-pod")
		definition, err := catalog.Get(deployGVK)
		Expect(err).NotTo(HaveOccurred())
		definition.Name, definition.Labels = "retained-"+ns, map[string]string{"test": ns}
		Expect(api.Create(ctx, definition)).To(Succeed())
		DeferCleanup(func() { _ = api.Delete(context.Background(), definition) })
		cfg := defaults(ns)
		cfg.KartaDefinitionSource.Catalog.Enabled = false
		cfg.KartaDefinitionSource.Cluster.Enabled = true
		cfg.KartaDefinitionSource.Cluster.LabelSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"test": ns}}
		components, seen := start(cfg)
		Eventually(func(g Gomega) {
			g.Expect(expectWorkload(g, components.Store.Capture(), root).Record.HasStatus).To(BeTrue())
		}).Should(Succeed())
		seen.denyRequests("/kartas")
		check := func(g Gomega) {
			view := components.Store.Capture()
			result := workloadResult(view, root.UID)
			g.Expect(result).NotTo(BeNil())
			g.Expect(result.HasValue).To(BeTrue())
			g.Expect(result.Record.OwnerResolution).To(Equal(store.Resolved))
			valid, reason := view.Valid(result.Sources, result.StatusError)
			g.Expect(valid).To(BeFalse())
			g.Expect(reason).To(Equal(store.ReasonPermissionDenied))
			valid, reason = view.Valid(result.CreationSources, result.Error)
			g.Expect(valid).To(BeTrue(), reason)
			g.Expect(result.Record.CreatedTimestamp).To(Equal(root.CreationTimestamp.Unix()))
			g.Expect(components.Runtime.Ready()).To(BeFalse())
		}
		Eventually(check).Should(Succeed())
		Consistently(check, time.Second).Should(Succeed())
		seen.denyRequests("")
		Eventually(func(g Gomega) {
			g.Expect(expectWorkload(g, components.Store.Capture(), root).StatusError).To(BeEmpty())
			g.Expect(components.Runtime.Ready()).To(BeTrue())
		}).Should(Succeed())
	})

	It("discovers a new root and its creation time while the initial Karta list is denied", func(ctx SpecContext) {
		ns := namespace(ctx)
		root := deployment(ctx, ns, "initial-karta-denial")
		p := pod(ctx, replicaSet(ctx, root, "initial-karta-middle"), "initial-karta-pod")
		definition, err := catalog.Get(deployGVK)
		Expect(err).NotTo(HaveOccurred())
		definition.Name, definition.Labels = "initial-karta-"+ns, map[string]string{"test": ns}
		Expect(api.Create(ctx, definition)).To(Succeed())
		DeferCleanup(func() { _ = api.Delete(context.Background(), definition) })
		cfg := defaults(ns)
		cfg.KartaDefinitionSource.Catalog.Enabled = false
		cfg.KartaDefinitionSource.Cluster.Enabled = true
		cfg.KartaDefinitionSource.Cluster.LabelSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"test": ns}}
		components, seen := start(cfg, "/kartas")
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			result := workloadResult(view, root.UID)
			g.Expect(result).NotTo(BeNil())
			g.Expect(result.HasValue).To(BeTrue())
			g.Expect(result.Record.CreatedTimestamp).To(Equal(root.CreationTimestamp.Unix()))
			valid, reason := view.Valid(result.CreationSources, result.Error)
			g.Expect(valid).To(BeTrue(), reason)
			valid, reason = view.Valid(result.Sources, result.StatusError)
			g.Expect(valid).To(BeFalse())
			g.Expect(reason).To(Equal(store.ReasonPermissionDenied))
			g.Expect(components.Runtime.Ready()).To(BeFalse())
		}).Should(Succeed())
		seen.denyRequests("")
		Eventually(func(g Gomega) {
			g.Expect(expectPod(g, components.Store.Capture(), p, root).Record.Karta).To(Equal(definition.Name))
			g.Expect(components.Runtime.Ready()).To(BeTrue())
		}).Should(Succeed())
	})

	It("removes only exiting-namespace Pods below a shared cluster-scoped root", func(ctx SpecContext) {
		owner := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "cluster-root-"}}
		Expect(api.Create(ctx, owner)).To(Succeed())
		first, second := namespace(ctx), namespace(ctx)
		makePod := func(ns string) *corev1.Pod {
			p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "shared-name", Namespace: ns, OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Namespace", Name: owner.Name, UID: owner.UID, Controller: ptr.To(true)}}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "example.test/image:v1"}}}}
			Expect(api.Create(ctx, p)).To(Succeed())
			return p
		}
		a, b := makePod(first), makePod(second)
		cfg := defaults(first)
		cfg.KartaDefinitionSource.Catalog.Enabled = false
		cfg.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"exporter-test": "selected"}, MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "kubernetes.io/metadata.name", Operator: metav1.LabelSelectorOpIn, Values: []string{first, second}}}}
		components, _ := start(cfg)
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			expectPod(g, view, a, owner)
			expectPod(g, view, b, owner)
		}).Should(Succeed())
		namespaceObject := &corev1.Namespace{}
		Expect(api.Get(ctx, types.NamespacedName{Name: first}, namespaceObject)).To(Succeed())
		delete(namespaceObject.Labels, "exporter-test")
		Expect(api.Update(ctx, namespaceObject)).To(Succeed())
		check := func(g Gomega) {
			view := components.Store.Capture()
			g.Expect(podResult(view, a.UID)).To(BeNil())
			expectPod(g, view, b, owner)
		}
		Eventually(check).Should(Succeed())
		Consistently(check, time.Second).Should(Succeed())
	})
})
