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

func TestExporterLoad(t *testing.T) {
	if os.Getenv("EXPORTER_LOAD") != "1" {
		t.Skip("set EXPORTER_LOAD=1 to run the real API-server load test")
	}
	count := integerSetting(t, "EXPORTER_LOAD_WORKLOADS", 100, 1, 100000)
	writers := integerSetting(t, "EXPORTER_LOAD_WRITERS", 4, 1, 32)
	duration := 5 * time.Second
	if value := os.Getenv("EXPORTER_LOAD_DURATION"); value != "" {
		var err error
		duration, err = time.ParseDuration(value)
		if err != nil || duration < time.Second || duration > 30*time.Minute {
			t.Fatalf("EXPORTER_LOAD_DURATION must be between 1s and 30m: %q", value)
		}
	}
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Fatal("KUBEBUILDER_ASSETS is required; run make test-exporter-load")
	}
	environment := &envtest.Environment{
		CRDDirectoryPaths:     []string{"../../../../charts/karta/crds"},
		ErrorIfCRDPathMissing: true,
		UseExistingCluster:    ptr.To(false),
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Error(err)
		}
	})
	kube, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	kube.QPS = 1000
	kube.Burst = 2000
	scheme := kruntime.NewScheme()
	for _, add := range []func(*kruntime.Scheme) error{corev1.AddToScheme, appsv1.AddToScheme, v1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	api, err := client.New(kube, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration+5*time.Minute)
	defer cancel()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: loadNamespace}}
	if err := api.Create(ctx, ns); err != nil {
		t.Fatal(err)
	}
	definition, err := catalog.Get(deploymentGVK)
	if err != nil {
		t.Fatal(err)
	}
	definition.Name = "load-deployment"
	if err := api.Create(ctx, definition); err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	application, err := app.New(components)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- application.ServeListener(runCtx, listener) }()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(15 * time.Second):
			t.Error("exporter failed to stop")
		}
	})
	httpClient := &http.Client{Timeout: 10 * time.Second}
	endpoint := "http://" + listener.Addr().String() + "/metrics"
	fleet := make([]workload, count)
	for i := range fleet {
		fleet[i], err = newWorkload(ctx, api, ns.Name, i)
		if err != nil {
			t.Fatal(err)
		}
	}
	waitForFleet(ctx, t, httpClient, endpoint, fleet)

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
		t.Error(err)
	}
	if stats.err != nil {
		t.Error(stats.err)
	}
	if t.Failed() {
		t.FailNow()
	}
	if mutations.Load() == 0 || stats.count < 2 {
		t.Fatalf("insufficient activity: mutations=%d scrapes=%d", mutations.Load(), stats.count)
	}
	converging := time.Now()
	waitForFleet(ctx, t, httpClient, endpoint, fleet)
	convergence := time.Since(converging)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	t.Logf("workloads=%d pods=%d writers=%d mutations=%d churn_elapsed=%s mutations/s=%.1f convergence=%s scrapes=%d scrape_max=%s shared_heap_before=%d shared_heap_after=%d", count, count*2, writers, mutations.Load(), churnElapsed, float64(mutations.Load())/churnElapsed.Seconds(), convergence, stats.count, stats.max, before.HeapAlloc, after.HeapAlloc)

	// Every final Pod UID must be current, and removing the whole fleet must
	// remove its status, creation and Pod series as well.
	for i := range fleet {
		for _, pod := range fleet[i].pods {
			if err := api.Delete(ctx, pod, client.GracePeriodSeconds(0)); err != nil {
				t.Fatal(err)
			}
		}
		if err := api.Delete(ctx, fleet[i].owner); err != nil {
			t.Fatal(err)
		}
		if err := api.Delete(ctx, fleet[i].root); err != nil {
			t.Fatal(err)
		}
	}
	waitForFleet(ctx, t, httpClient, endpoint, nil)
}

func integerSetting(t *testing.T, name string, fallback, minValue, maxValue int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minValue || value > maxValue {
		t.Fatalf("%s must be between %d and %d: %q", name, minValue, maxValue, raw)
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

func waitForFleet(ctx context.Context, t *testing.T, httpClient *http.Client, endpoint string, fleet []workload) {
	t.Helper()
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		families, err := scrape(ctx, httpClient, endpoint)
		if err == nil {
			err = checkFleet(families, fleet)
		}
		if err == nil {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("metrics did not converge: %v", err)
		case <-ctx.Done():
			t.Fatalf("metrics wait: %v; last: %v", ctx.Err(), err)
		case <-tick.C:
		}
	}
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
