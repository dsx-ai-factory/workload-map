// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package exporter_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/app"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/bootstrap"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/config"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog"
)

var environment *envtest.Environment
var kubeConfig *rest.Config
var api client.Client
var deployGVK = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}

func TestController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Exporter API integration")
}

var _ = BeforeSuite(func() {
	Expect(os.Getenv("KUBEBUILDER_ASSETS")).NotTo(BeEmpty(), "integration tests require KUBEBUILDER_ASSETS")
	environment = &envtest.Environment{
		CRDDirectoryPaths:     []string{"../../../../charts/karta/crds"},
		ErrorIfCRDPathMissing: true, UseExistingCluster: new(bool),
	}
	environment.ControlPlane.GetAPIServer().Configure().Append("feature-gates", "WatchList=true")
	var err error
	kubeConfig, err = environment.Start()
	Expect(err).NotTo(HaveOccurred())
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	Expect(appsv1.AddToScheme(scheme)).To(Succeed())
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

var _ = Describe("Controller publication lifecycle", func() {
	It("keeps standalone Pods visible across Pod definition changes", func(ctx SpecContext) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "exporter-pod-"}}
		Expect(api.Create(ctx, ns)).To(Succeed())
		p := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "standalone", Namespace: ns.Name, Labels: map[string]string{"example.com/team": "alpha"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "example.test/image:v1"}}},
		}
		Expect(api.Create(ctx, p)).To(Succeed())
		p.Status.Phase = corev1.PodRunning
		Expect(api.Status().Update(ctx, p)).To(Succeed())
		cfg := config.DefaultConfig()
		cfg.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": ns.Name}}
		cfg.KartaDefinitionSource.Catalog.Enabled = false
		cfg.KartaDefinitionSource.Cluster.Enabled = true
		cfg.KartaDefinitionSource.Cluster.Include = []string{"integration-" + ns.Name}
		cfg.Rules = []labelresolver.LabelRule{
			{Name: "team", Metrics: []string{labelresolver.MetricPodWorkloadInfo, labelresolver.MetricWorkloadStatus}, Sources: []labelresolver.Source{labelresolver.SourceWorkload}, Expression: `object.metadata.labels["example.com/team"]`},
		}
		components, err := bootstrap.New(cfg, kubeConfig)
		Expect(err).NotTo(HaveOccurred())
		runCtx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- app.RunController(runCtx, components) }()
		DeferCleanup(func() {
			cancel()
			Eventually(done, 10*time.Second).Should(Receive(Succeed()))
		})
		podGVK := corev1.SchemeGroupVersion.WithKind("Pod")
		expectedPodError := ""
		checkGeneric := func(g Gomega) {
			view := components.Store.Capture()
			result := podResult(view, p.UID)
			g.Expect(result).NotTo(BeNil())
			g.Expect(result.HasValue).To(BeTrue())
			g.Expect(result.Error).To(Equal(expectedPodError))
			g.Expect(result.Record.Workload.UID).To(Equal(p.UID))
			g.Expect(result.Record.Workload.GVK).To(Equal(podGVK))
			g.Expect(result.Record.Karta).To(BeEmpty())
			g.Expect(result.Record.Component).To(Equal("<unknown>"))
			g.Expect(result.Record.Instance).To(Equal("<unknown>"))
			g.Expect(result.Record.Group).To(BeEmpty())
			workload := workloadResult(view, p.UID)
			g.Expect(workload).NotTo(BeNil())
			g.Expect(workload.HasValue).To(BeTrue())
			g.Expect(workload.Record.Karta).To(BeEmpty())
			g.Expect(workload.Record.KartaSource).To(BeEmpty())
			g.Expect(workload.Record.HasStatus).To(BeTrue())
			g.Expect(workload.Record.Phases).To(BeEmpty())
			g.Expect(workload.StatusError).To(Equal(expectedPodError))
			if expectedPodError == "" {
				g.Expect(workload.Record.StatusLabels).To(HaveKeyWithValue("team", "alpha"))
			}
			valid, reason := view.Valid(workload.Sources, workload.StatusError)
			g.Expect(valid).To(Equal(expectedPodError == ""), reason)
			valid, reason = view.Valid(workload.CreationSources, workload.Error)
			g.Expect(valid).To(BeTrue(), reason)
		}
		Eventually(checkGeneric).Should(Succeed())

		for range 2 {
			By("adding a Pod definition to the existing standalone Pod")
			definition, err := catalog.Get(podGVK)
			Expect(err).NotTo(HaveOccurred())
			definition.Name = cfg.KartaDefinitionSource.Cluster.Include[0]
			Expect(api.Create(ctx, definition)).To(Succeed())
			Eventually(func(g Gomega) {
				view := components.Store.Capture()
				result := expectPod(g, view, p, p)
				g.Expect(result.Record.Karta).To(Equal(definition.Name))
				g.Expect(result.Record.Component).To(Equal("pod"))
				workload := expectWorkload(g, view, p)
				g.Expect(workload.Record.Karta).To(Equal(definition.Name))
				g.Expect(workload.Record.HasStatus).To(BeTrue())
				g.Expect(workload.Record.Phases).To(ContainElement("Running"))
			}).Should(Succeed())

			By("retaining mapped results while a configured label fails")
			p.Labels = nil
			Expect(api.Update(ctx, p)).To(Succeed())
			Eventually(func(g Gomega) {
				view := components.Store.Capture()
				result := podResult(view, p.UID)
				g.Expect(result).NotTo(BeNil())
				g.Expect(result.Error).To(Equal(store.ReasonEvaluationFailed))
				g.Expect(result.Record.Karta).To(Equal(definition.Name))
				g.Expect(result.Record.Component).To(Equal("pod"))
				workload := workloadResult(view, p.UID)
				g.Expect(workload).NotTo(BeNil())
				g.Expect(workload.StatusError).To(Equal(store.ReasonEvaluationFailed))
				g.Expect(workload.Record.Karta).To(Equal(definition.Name))
				g.Expect(workload.Record.HasStatus).To(BeTrue())
			}).Should(Succeed())

			By("clearing definition-derived fields despite the label failure")
			deleteObjects(ctx, definition)
			expectedPodError = store.ReasonEvaluationFailed
			Eventually(checkGeneric).Should(Succeed())

			By("recovering Pod labels without restoring a definition")
			p.Labels = map[string]string{"example.com/team": "alpha"}
			Expect(api.Update(ctx, p)).To(Succeed())
			expectedPodError = ""
			Eventually(checkGeneric).Should(Succeed())
		}

		By("removing both records when the standalone Pod is deleted")
		deleteObjects(ctx, p)
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			g.Expect(podResult(view, p.UID)).To(BeNil())
			g.Expect(workloadResult(view, p.UID)).To(BeNil())
		}).Should(Succeed())
	})

	It("discovers required workload kinds without Kartas or Pods", func(ctx SpecContext) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "exporter-required-"}}
		Expect(api.Create(ctx, ns)).To(Succeed())
		listed := deployment(ctx, ns.Name, "listed")
		cfg := config.DefaultConfig()
		cfg.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": ns.Name}}
		cfg.KartaDefinitionSource.Catalog.Enabled = false
		cfg.KartaDefinitionSource.Cluster.Enabled = false
		cfg.RequiredKinds = []schema.GroupVersionKind{deployGVK}
		components, err := bootstrap.New(cfg, kubeConfig)
		Expect(err).NotTo(HaveOccurred())
		runCtx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- app.RunController(runCtx, components) }()
		DeferCleanup(func() {
			cancel()
			Eventually(done, 10*time.Second).Should(Receive(Succeed()))
		})
		Eventually(func(g Gomega) {
			workload := expectWorkload(g, components.Store.Capture(), listed)
			g.Expect(workload.Record.HasStatus).To(BeTrue())
			g.Expect(workload.Record.Phases).To(BeEmpty())
		}).Should(Succeed())
		added := deployment(ctx, ns.Name, "added")
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			for _, root := range []*appsv1.Deployment{listed, added} {
				workload := expectWorkload(g, view, root)
				g.Expect(workload.Record.Karta).To(BeEmpty())
				g.Expect(workload.Record.KartaSource).To(BeEmpty())
				g.Expect(workload.Record.HasStatus).To(BeTrue())
				g.Expect(workload.Record.Phases).To(BeEmpty())
			}
			g.Expect(view.Pods).To(BeEmpty())
		}).Should(Succeed())
	})

	It("updates live ownership, definitions and records and joins workers on cancellation", func(ctx SpecContext) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "exporter-"}}
		Expect(api.Create(ctx, ns)).To(Succeed())
		root := deployment(ctx, ns.Name, "root")
		rs := replicaSet(ctx, root, "middle")
		p := pod(ctx, rs, "pod")
		cfg := config.DefaultConfig()
		cfg.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": ns.Name}}
		cfg.KartaDefinitionSource.Catalog.Enabled = false
		cfg.KartaDefinitionSource.Cluster.Enabled = true
		cfg.KartaDefinitionSource.Cluster.Include = []string{"integration-" + ns.Name}
		cfg.RequestTimeout = "1s"
		cfg.Rules = []labelresolver.LabelRule{
			{Name: "team", Metrics: []string{labelresolver.MetricPodWorkloadInfo, labelresolver.MetricWorkloadStatus}, Sources: []labelresolver.Source{labelresolver.SourceWorkload}, Expression: `object.metadata.labels["example.com/team"]`},
			{Name: "pod_phase", Metrics: []string{labelresolver.MetricPodWorkloadInfo}, Sources: []labelresolver.Source{labelresolver.SourcePod}, Expression: `object.status.phase`},
		}
		components, err := bootstrap.New(cfg, kubeConfig)
		Expect(err).NotTo(HaveOccurred())
		application, err := app.New(components)
		Expect(err).NotTo(HaveOccurred())
		Expect(components.Runtime.Ready()).To(BeFalse())
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		runCtx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		var duplicateDone chan error
		go func() { done <- application.ServeListener(runCtx, listener) }()
		DeferCleanup(func() {
			cancel()
			Eventually(done, 10*time.Second).Should(Receive(Succeed()))
			if duplicateDone != nil {
				Eventually(duplicateDone, time.Second).Should(Receive(Succeed()))
			}
			Expect(components.Runtime.Ready()).To(BeFalse())
			stoppedCtx, stop := context.WithCancel(context.Background())
			defer stop()
			stoppedRun := make(chan error, 1)
			go func() { stoppedRun <- components.Runtime.Run(stoppedCtx) }()
			Eventually(stoppedRun, time.Second).Should(Receive(Succeed()))
			Expect(components.Runtime.Ready()).To(BeFalse())
		})
		httpClient := &http.Client{Timeout: time.Second}
		get := func(g Gomega, path string) string {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String()+path, nil)
			g.Expect(err).NotTo(HaveOccurred())
			response, err := httpClient.Do(request)
			g.Expect(err).NotTo(HaveOccurred())
			defer response.Body.Close()
			g.Expect(response.StatusCode).To(Equal(http.StatusOK))
			body, err := io.ReadAll(response.Body)
			g.Expect(err).NotTo(HaveOccurred())
			return string(body)
		}
		checkMetrics := func(g Gomega) string {
			body := get(g, "/metrics")
			g.Expect(strings.Split(body, "\n")).To(ContainElement(SatisfyAll(
				HavePrefix("dsx_workload_pod_info{"), HaveSuffix(" 1"),
				ContainSubstring(`pod_uid="`+string(p.UID)+`"`),
				ContainSubstring(`workload_uid="`+string(root.UID)+`"`),
				ContainSubstring(`workload_kind="Deployment"`),
				ContainSubstring(`observation_valid="true"`), ContainSubstring(`team="alpha"`),
			)))
			return body
		}
		Eventually(func(g Gomega) {
			result := expectPod(g, components.Store.Capture(), p, root)
			g.Expect(result.Record.Component).To(Equal("<unknown>"))
			expectZeroStatusMetrics(g, checkMetrics(g), root)
		}).Should(Succeed())

		By("interpreting an existing ownership chain after a Karta is added")
		definition, err := catalog.Get(deployGVK)
		Expect(err).NotTo(HaveOccurred())
		definition.Name = "integration-" + ns.Name
		Expect(api.Create(ctx, definition)).To(Succeed())
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			result := expectPod(g, view, p, root)
			g.Expect(result.Record.Karta).To(Equal(definition.Name))
			g.Expect(result.Record.Component).To(Equal("deployment"))
			g.Expect(result.Record.Labels).To(Equal(map[string]string{"team": "alpha", "pod_phase": "Pending"}))
			workload := expectWorkload(g, view, root)
			g.Expect(workload.Record.HasStatus).To(BeTrue())
			g.Expect(workload.Record.Phases).To(ContainElement("Running"))
			g.Expect(workload.Record.StatusLabels).To(Equal(map[string]string{"team": "alpha"}))
			g.Expect(components.Runtime.Ready()).To(BeTrue())
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			get(g, "/readyz")
			checkMetrics(g)
		}).Should(Succeed())

		By("keeping the first run active when a duplicate caller cancels")
		duplicateCtx, cancelDuplicate := context.WithCancel(context.Background())
		cancelDuplicate()
		duplicateDone = make(chan error, 1)
		go func() { duplicateDone <- components.Runtime.Run(duplicateCtx) }()
		Consistently(func(g Gomega) {
			g.Expect(duplicateDone).To(BeEmpty())
			g.Expect(done).To(BeEmpty())
			g.Expect(components.Runtime.Ready()).To(BeTrue())
		}, 200*time.Millisecond).Should(Succeed())

		By("discovering a selected workload without any Pods")
		idle := deployment(ctx, ns.Name, "idle")
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			g.Expect(expectWorkload(g, view, idle).Record.HasStatus).To(BeTrue())
			g.Expect(view.Pods).To(HaveLen(1))
		}).Should(Succeed())

		By("refreshing labels and status after a root-only change")
		podVersion := p.ResourceVersion
		root.Labels["example.com/team"] = "beta"
		Expect(api.Update(ctx, root)).To(Succeed())
		root.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded"}}
		Expect(api.Status().Update(ctx, root)).To(Succeed())
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			g.Expect(expectPod(g, view, p, root).Record.Labels).To(HaveKeyWithValue("team", "beta"))
			workload := expectWorkload(g, view, root)
			g.Expect(workload.Record.Phases).To(ContainElement("Failed"))
			g.Expect(workload.Record.StatusLabels).To(HaveKeyWithValue("team", "beta"))
		}).Should(Succeed())
		Expect(api.Get(ctx, client.ObjectKeyFromObject(p), p)).To(Succeed())
		Expect(p.ResourceVersion).To(Equal(podVersion))
		p.Status.Phase = corev1.PodRunning
		Expect(api.Status().Update(ctx, p)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(expectPod(g, components.Store.Capture(), p, root).Record.Labels).To(HaveKeyWithValue("pod_phase", "Running"))
		}).Should(Succeed())

		By("reinterpreting records after the Karta changes")
		Expect(api.Get(ctx, client.ObjectKeyFromObject(definition), definition)).To(Succeed())
		definition.Spec.StructureDefinition.RootComponent.Name = "renamed"
		definition.Spec.StructureDefinition.ChildComponents[0].OwnerRef = ptr.To("renamed")
		Expect(api.Update(ctx, definition)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(expectPod(g, components.Store.Capture(), p, root).Record.Component).To(Equal("renamed"))
		}).Should(Succeed())

		By("removing an old Pod UID before publishing its same-name replacement")
		oldPodUID := p.UID
		deleteObjects(ctx, p)
		Eventually(func() *store.PodResult { return podResult(components.Store.Capture(), oldPodUID) }).Should(BeNil())
		p = pod(ctx, rs, p.Name)
		Expect(p.UID).NotTo(Equal(oldPodUID))
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			expectPod(g, view, p, root)
			g.Expect(podResult(view, oldPodUID)).To(BeNil())
		}).Should(Succeed())

		By("replacing the whole ownership chain without retaining an old UID")
		oldRootUID, oldReplicaSetUID, secondPodUID := root.UID, rs.UID, p.UID
		// envtest has no garbage collector, so delete the explicit children too.
		deleteObjects(ctx, p, rs, root)
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			g.Expect(workloadResult(view, oldRootUID)).To(BeNil())
			g.Expect(podResult(view, secondPodUID)).To(BeNil())
			expectWorkload(g, view, idle)
		}).Should(Succeed())
		root = deployment(ctx, ns.Name, root.Name)
		rs = replicaSet(ctx, root, rs.Name)
		p = pod(ctx, rs, p.Name)
		Expect(root.UID).NotTo(Equal(oldRootUID))
		Expect(rs.UID).NotTo(Equal(oldReplicaSetUID))
		Expect(p.UID).NotTo(BeElementOf(oldPodUID, secondPodUID))
		checkRecreated := func(g Gomega) {
			view := components.Store.Capture()
			g.Expect(expectPod(g, view, p, root).Record.Component).To(Equal("renamed"))
			g.Expect(expectWorkload(g, view, root).Record.Phases).To(ContainElement("Running"))
			g.Expect(workloadResult(view, oldRootUID)).To(BeNil())
			g.Expect(podResult(view, oldPodUID)).To(BeNil())
			g.Expect(podResult(view, secondPodUID)).To(BeNil())
			expectWorkload(g, view, idle)
		}
		Eventually(checkRecreated).Should(Succeed())
		Consistently(checkRecreated, time.Second).Should(Succeed())
		Eventually(func(g Gomega) {
			body := checkMetrics(g)
			for _, uid := range []types.UID{oldRootUID, oldPodUID, secondPodUID} {
				g.Expect(body).NotTo(ContainSubstring(string(uid)))
			}
		}).Should(Succeed())

		By("retaining generic ownership after the Karta is deleted")
		Expect(api.Delete(ctx, definition)).To(Succeed())
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			g.Expect(expectPod(g, view, p, root).Record.Component).To(Equal("<unknown>"))
			body := checkMetrics(g)
			for _, workload := range []*appsv1.Deployment{root, idle} {
				result := expectWorkload(g, view, workload)
				g.Expect(result.Record.HasStatus).To(BeTrue())
				g.Expect(result.Record.Phases).To(BeEmpty())
				g.Expect(result.Record.Karta).To(BeEmpty())
				g.Expect(result.Record.KartaSource).To(BeEmpty())
				expectZeroStatusMetrics(g, body, workload)
			}
		}).Should(Succeed())

		By("retaining the live root after its final Pod is deleted")
		deleteObjects(ctx, p)
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			g.Expect(podResult(view, p.UID)).To(BeNil())
			expectWorkload(g, view, root)
			expectWorkload(g, view, idle)
		}).Should(Succeed())

		By("withdrawing the workload when the root itself is deleted")
		deleteObjects(ctx, root)
		Eventually(func(g Gomega) {
			view := components.Store.Capture()
			g.Expect(workloadResult(view, root.UID)).To(BeNil())
			expectWorkload(g, view, idle)
		}).Should(Succeed())
	})
})

// envtest runs no workload controllers. Seed the ownership chain and status
// explicitly so this test exercises API observation and publication.
func deployment(ctx context.Context, ns, name string) *appsv1.Deployment {
	root := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: map[string]string{"example.com/team": "alpha"}},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)), Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "example.test/image:v1"}}},
			},
		},
	}
	Expect(api.Create(ctx, root)).To(Succeed())
	root.Status = appsv1.DeploymentStatus{Replicas: 1, ReadyReplicas: 1, AvailableReplicas: 1, UpdatedReplicas: 1, Conditions: []appsv1.DeploymentCondition{
		{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionTrue},
		{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionTrue, Reason: "NewReplicaSetAvailable"},
	}}
	Expect(api.Status().Update(ctx, root)).To(Succeed())
	return root
}

func replicaSet(ctx context.Context, root *appsv1.Deployment, name string) *appsv1.ReplicaSet {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: root.Namespace, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(root, deployGVK)}},
		Spec:       appsv1.ReplicaSetSpec{Replicas: ptr.To(int32(1)), Selector: root.Spec.Selector, Template: root.Spec.Template},
	}
	Expect(api.Create(ctx, rs)).To(Succeed())
	return rs
}

func pod(ctx context.Context, rs *appsv1.ReplicaSet, name string) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: rs.Namespace, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(rs, appsv1.SchemeGroupVersion.WithKind("ReplicaSet"))}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "example.test/image:v1"}}},
	}
	Expect(api.Create(ctx, p)).To(Succeed())
	p.Status.Phase = corev1.PodPending
	Expect(api.Status().Update(ctx, p)).To(Succeed())
	return p
}

func deleteObjects(ctx context.Context, objects ...client.Object) {
	for _, object := range objects {
		Expect(api.Delete(ctx, object, client.GracePeriodSeconds(0))).To(Succeed())
		Eventually(func() bool { return apierrors.IsNotFound(api.Get(ctx, client.ObjectKeyFromObject(object), object)) }).Should(BeTrue())
	}
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
	g.Expect(result.Record.OwnerResolution).To(Equal(store.Resolved))
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
	g.Expect(result.StatusError).To(BeEmpty())
	valid, reason := view.Valid(result.Sources, result.Error)
	g.Expect(valid).To(BeTrue(), reason)
	return result
}

func expectZeroStatusMetrics(g Gomega, body string, root client.Object) {
	var samples []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "dsx_workload_status{") && strings.Contains(line, `workload_uid="`+string(root.GetUID())+`"`) {
			samples = append(samples, line)
		}
	}
	g.Expect(samples).To(HaveLen(9))
	for _, phase := range []string{"Initializing", "Running", "Completed", "Failed", "Degraded", "Suspended", "Suspending", "Resuming", "Undefined"} {
		g.Expect(samples).To(ContainElement(SatisfyAll(
			HaveSuffix(" 0"), ContainSubstring(`phase="`+phase+`"`),
			ContainSubstring(`karta=""`), ContainSubstring(`karta_source=""`),
			ContainSubstring(`observation_valid="true"`), ContainSubstring(`team="alpha"`),
		)))
	}
}
