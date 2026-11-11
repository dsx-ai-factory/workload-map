// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package load_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	collector "github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/adapters/otel"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/app"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/bootstrap"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/config"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelresolver"
	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog"
)

type workload struct {
	root     *appsv1.Deployment
	owner    *appsv1.ReplicaSet
	pods     []*corev1.Pod
	revision int
	phase    string
}

type scrapeStats struct {
	count int
	max   time.Duration
	err   error
}

const loadNamespace = "exporter-load"

var deploymentGVK = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}

func TestLoad(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "DSX Workload Exporter Load Suite")
}

var _ = Describe("Exporter under continuous API updates", func() {
	It("publishes current metrics and removes them after deletion", func() {
		if os.Getenv("EXPORTER_LOAD") != "1" {
			Skip("set EXPORTER_LOAD=1 to run the real API-server load test")
		}
		count := integerSetting("EXPORTER_LOAD_WORKLOADS", 100, 1, 100000)
		writers := integerSetting("EXPORTER_LOAD_WRITERS", 4, 1, 32)
		duration := 5 * time.Second
		if value := os.Getenv("EXPORTER_LOAD_DURATION"); value != "" {
			var err error
			duration, err = time.ParseDuration(value)
			if err != nil || duration < time.Second || duration > 30*time.Minute {
				Fail(fmt.Sprintf("EXPORTER_LOAD_DURATION must be between 1s and 30m: %q", value))
			}
		}
		if os.Getenv("KUBEBUILDER_ASSETS") == "" {
			Fail("KUBEBUILDER_ASSETS is required; run make test-exporter-load")
		}
		By("starting an isolated API server and etcd")
		environment := &envtest.Environment{
			CRDDirectoryPaths:     []string{"../../../../charts/karta/crds"},
			ErrorIfCRDPathMissing: true,
			UseExistingCluster:    ptr.To(false),
		}
		DeferCleanup(environment.Stop)
		kube, err := environment.Start()
		Expect(err).NotTo(HaveOccurred())
		kube.QPS = 1000
		kube.Burst = 2000
		scheme := kruntime.NewScheme()
		for _, add := range []func(*kruntime.Scheme) error{corev1.AddToScheme, appsv1.AddToScheme, v1alpha1.AddToScheme} {
			Expect(add(scheme)).To(Succeed())
		}
		api, err := client.New(kube, client.Options{Scheme: scheme})
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithTimeout(context.Background(), duration+5*time.Minute)
		DeferCleanup(cancel)
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: loadNamespace}}
		Expect(api.Create(ctx, ns)).To(Succeed())
		definition, err := catalog.Get(deploymentGVK)
		Expect(err).NotTo(HaveOccurred())
		definition.Name = "load-deployment"
		Expect(api.Create(ctx, definition)).To(Succeed())
		By("starting the production exporter with revision label rules")
		cfg := config.DefaultConfig()
		cfg.APIQPS = 1000
		cfg.APIBurst = 2000
		cfg.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": ns.Name}}
		cfg.KartaDefinitionSource.Cluster.Include = []string{definition.Name}
		cfg.RequiredKinds = config.Kinds{deploymentGVK}
		cfg.Rules = []labelresolver.LabelRule{
			{
				Name:       "root_revision",
				Metrics:    []string{labelresolver.MetricPodWorkloadInfo, labelresolver.MetricWorkloadStatus},
				Sources:    []labelresolver.Source{labelresolver.SourceWorkload},
				Expression: `object.metadata.labels["load-revision"]`,
			},
			{
				Name:       "pod_revision",
				Metrics:    []string{labelresolver.MetricPodWorkloadInfo},
				Sources:    []labelresolver.Source{labelresolver.SourcePod},
				Expression: `object.metadata.labels["load-revision"]`,
			},
		}
		components, err := bootstrap.New(cfg, kube)
		Expect(err).NotTo(HaveOccurred())
		application, err := app.New(components)
		Expect(err).NotTo(HaveOccurred())
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		runCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- application.ServeListener(runCtx, listener) }()
		DeferCleanup(func() {
			stop()
			Eventually(done, 15*time.Second).Should(Receive(BeNil()), "exporter failed to stop cleanly")
		})
		httpClient := &http.Client{Timeout: 10 * time.Second}
		endpoint := "http://" + listener.Addr().String() + "/metrics"
		By("creating workloads and waiting for their initial metrics")
		fleet := make([]workload, count)
		for i := range fleet {
			fleet[i], err = newWorkload(ctx, api, ns.Name, i)
			Expect(err).NotTo(HaveOccurred())
		}
		waitForFleet(ctx, httpClient, endpoint, fleet)

		By("updating workloads and replacing Pods while scraping metrics")
		// Each writer owns disjoint objects. Scrapes overlap updates, while final
		// checks wait for eventual consistency rather than requiring every event.
		var mutations atomic.Int64
		var workers sync.WaitGroup
		errors := make(chan error, writers)
		end := time.Now().Add(duration)
		scrapeCtx, stopScraping := context.WithCancel(ctx)
		scraped := make(chan scrapeStats, 1)
		go func() { scraped <- scrapeContinuously(scrapeCtx, httpClient, endpoint) }()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		started := time.Now()
		for worker := range min(writers, len(fleet)) {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for {
					for i := worker; i < len(fleet); i += writers {
						if err := updateWorkload(ctx, api, &fleet[i], &mutations); err != nil {
							errors <- err
							return
						}
					}
					if !time.Now().Before(end) {
						break
					}
				}
			}()
		}
		workers.Wait()
		churnElapsed := time.Since(started)
		stopScraping()
		stats := <-scraped
		close(errors)
		for err := range errors {
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(stats.err).NotTo(HaveOccurred())
		Expect(mutations.Load()).To(BeNumerically(">", 0), "no API mutations completed")
		Expect(stats.count).To(BeNumerically(">=", 2), "insufficient scrapes during load")
		By("waiting for the final identities, revisions, phases and health")
		converging := time.Now()
		waitForFleet(ctx, httpClient, endpoint, fleet)
		convergence := time.Since(converging)
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		GinkgoWriter.Printf("workloads=%d pods=%d writers=%d mutations=%d churn_elapsed=%s mutations/s=%.1f convergence=%s scrapes=%d scrape_max=%s shared_heap_before=%d shared_heap_after=%d\n", count, count*2, writers, mutations.Load(), churnElapsed, float64(mutations.Load())/churnElapsed.Seconds(), convergence, stats.count, stats.max, before.HeapAlloc, after.HeapAlloc)

		By("deleting the fleet and checking its metrics disappear")
		// Every final Pod UID must be current, and removing the whole fleet must
		// remove its status, creation and Pod series as well.
		for i := range fleet {
			for _, pod := range fleet[i].pods {
				Expect(api.Delete(ctx, pod, client.GracePeriodSeconds(0))).To(Succeed())
			}
			Expect(api.Delete(ctx, fleet[i].owner)).To(Succeed())
			Expect(api.Delete(ctx, fleet[i].root)).To(Succeed())
		}
		waitForFleet(ctx, httpClient, endpoint, nil)
	})
})

func integerSetting(name string, fallback, minValue, maxValue int) int {
	GinkgoHelper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minValue || value > maxValue {
		Fail(fmt.Sprintf("%s must be between %d and %d: %q", name, minValue, maxValue, raw))
	}
	return value
}

func newWorkload(ctx context.Context, api client.Client, namespace string, index int) (workload, error) {
	name := fmt.Sprintf("workload-%06d", index)
	labels := map[string]string{"app": name, "load-revision": "0"}
	template := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labels},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "main", Image: "example.test/load:v1"}},
		},
	}
	root := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(0)),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: template,
		},
	}
	if err := api.Create(ctx, root); err != nil {
		return workload{}, err
	}
	root.Status.Conditions = []appsv1.DeploymentCondition{{
		Type:   appsv1.DeploymentProgressing,
		Status: corev1.ConditionTrue,
		Reason: "NewReplicaSetAvailable",
	}}
	if err := api.Status().Update(ctx, root); err != nil {
		return workload{}, err
	}
	owner := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(root, deploymentGVK)},
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: ptr.To(int32(0)),
			Selector: root.Spec.Selector,
			Template: template,
		},
	}
	if err := api.Create(ctx, owner); err != nil {
		return workload{}, err
	}
	result := workload{root: root, owner: owner, phase: "Running"}
	for i := range 2 {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:            fmt.Sprintf("%s-%d", name, i),
				Namespace:       namespace,
				Labels:          map[string]string{"load-revision": "0"},
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(owner, appsv1.SchemeGroupVersion.WithKind("ReplicaSet"))},
			},
			Spec: template.Spec,
		}
		if err := api.Create(ctx, pod); err != nil {
			return workload{}, err
		}
		result.pods = append(result.pods, pod)
	}
	return result, nil
}

func updateWorkload(ctx context.Context, api client.Client, w *workload, mutations *atomic.Int64) error {
	w.revision++
	revision := strconv.Itoa(w.revision)
	w.root.Labels["load-revision"] = revision
	if err := api.Update(ctx, w.root); err != nil {
		return err
	}
	mutations.Add(1)
	condition := appsv1.DeploymentCondition{
		Type:   appsv1.DeploymentProgressing,
		Status: corev1.ConditionTrue,
		Reason: "NewReplicaSetAvailable",
	}
	w.phase = "Running"
	if w.revision%2 == 1 {
		condition.Reason = "NewReplicaSetCreated"
		w.phase = "Initializing"
	}
	w.root.Status.Conditions = []appsv1.DeploymentCondition{condition}
	if err := api.Status().Update(ctx, w.root); err != nil {
		return err
	}
	mutations.Add(1)
	for i, pod := range w.pods {
		if w.revision%4 == 1 && i == 0 {
			if err := api.Delete(ctx, pod, client.GracePeriodSeconds(0)); err != nil {
				return err
			}
			mutations.Add(1)
			oldUID := pod.UID
			pod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:            pod.Name,
					Namespace:       pod.Namespace,
					Labels:          map[string]string{"load-revision": revision},
					OwnerReferences: pod.OwnerReferences,
				},
				Spec: pod.Spec,
			}
			if err := api.Create(ctx, pod); err != nil {
				return err
			}
			if pod.UID == oldUID {
				return fmt.Errorf("recreated Pod %s retained UID %s", pod.Name, oldUID)
			}
			w.pods[i] = pod
		} else {
			pod.Labels["load-revision"] = revision
			if err := api.Update(ctx, pod); err != nil {
				return err
			}
		}
		mutations.Add(1)
	}
	return nil
}

func waitForFleet(ctx context.Context, httpClient *http.Client, endpoint string, fleet []workload) {
	GinkgoHelper()
	Eventually(ctx, func(ctx context.Context) error {
		families, err := scrape(ctx, httpClient, endpoint)
		if err != nil {
			return err
		}
		return checkFleet(families, fleet)
	}, 60*time.Second, 100*time.Millisecond).Should(Succeed(), "metrics did not converge")
}

func scrapeContinuously(ctx context.Context, httpClient *http.Client, endpoint string) scrapeStats {
	var stats scrapeStats
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return stats
		case <-ticker.C:
		}
		started := time.Now()
		// Finish an in-flight scrape when the churn window ends, but keep its HTTP
		// timeout. Cancellation must not disguise a failed completed scrape.
		families, err := scrape(context.WithoutCancel(ctx), httpClient, endpoint)
		if err == nil {
			for _, name := range []string{collector.MetricPodWorkloadInfo, collector.MetricWorkloadStatus, collector.MetricWorkloadCreatedTimestamp, collector.MetricExporterObservationValid} {
				if len(families[name].GetMetric()) == 0 {
					err = fmt.Errorf("empty metric family during load: %s", name)
					break
				}
			}
		}
		if err == nil {
			err = checkSamples(families)
		}
		if err != nil {
			stats.err = err
			return stats
		}
		stats.count++
		stats.max = max(stats.max, time.Since(started))
	}
}
