// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package app_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	collector "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/adapters/otel"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/app"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

type httpTestRuntime struct {
	ready   atomic.Bool
	started chan struct{}
	stopped chan struct{}
}

type httpApplication struct {
	runtime *httpTestRuntime
	client  *http.Client
	address string
	cancel  context.CancelFunc
	done    chan struct{}
	err     error
}

type expectedSample struct {
	labels map[string]string
	value  float64
}

type blockedCaptureStore struct {
	store.PublicationStore
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (r *httpTestRuntime) Run(ctx context.Context) error {
	close(r.started)
	defer close(r.stopped)
	<-ctx.Done()
	return ctx.Err()
}

func (r *httpTestRuntime) Ready() bool { return r.ready.Load() }

func (s *blockedCaptureStore) Capture() store.CapturedView {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return s.PublicationStore.Capture()
}

func startHTTPApplication(t *testing.T, publications store.PublicationStore) *httpApplication {
	t.Helper()
	runtime := &httpTestRuntime{started: make(chan struct{}), stopped: make(chan struct{})}
	application, err := app.New(app.Components{Runtime: runtime, Store: publications})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &httpApplication{
		runtime: runtime,
		client:  &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}},
		address: listener.Addr().String(), cancel: cancel, done: make(chan struct{}),
	}
	go func() {
		h.err = application.ServeListener(ctx, listener)
		close(h.done)
	}()
	t.Cleanup(func() {
		h.stop(t)
		h.client.CloseIdleConnections()
	})
	select {
	case <-runtime.started:
	case <-h.done:
		t.Fatalf("application stopped before runtime started: %v", h.err)
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not start")
	}
	return h
}

func (h *httpApplication) stop(t *testing.T) {
	t.Helper()
	h.cancel()
	select {
	case <-h.done:
		if h.err != nil {
			t.Fatalf("application shutdown: %v", h.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("application did not shut down")
	}
}

func (h *httpApplication) status(t *testing.T, path string, expected int) {
	t.Helper()
	response, err := h.client.Get("http://" + h.address + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != expected {
		t.Fatalf("%s: got HTTP %d, want %d", path, response.StatusCode, expected)
	}
}

func (h *httpApplication) scrape(t *testing.T) map[string]*dto.MetricFamily {
	t.Helper()
	response, err := h.client.Get("http://" + h.address + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("metrics: got HTTP %d", response.StatusCode)
	}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return families
}

func checkHTTPMetrics(t *testing.T, got map[string]*dto.MetricFamily, expected map[string][]expectedSample) {
	t.Helper()
	if len(got) != len(expected) {
		t.Fatalf("got %d metric families, want %d", len(got), len(expected))
	}
	for name, samples := range expected {
		family := got[name]
		if family == nil || family.GetType() != dto.MetricType_GAUGE || len(family.Metric) != len(samples) {
			t.Fatalf("%s: got %v, want gauge with %d samples", name, family, len(samples))
		}
		unmatched := append([]expectedSample(nil), samples...)
		for _, metric := range family.Metric {
			labels := make(map[string]string, len(metric.Label))
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			found := false
			for i, sample := range unmatched {
				if reflect.DeepEqual(labels, sample.labels) && metric.GetGauge().GetValue() == sample.value {
					unmatched = append(unmatched[:i], unmatched[i+1:]...)
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s: unexpected labels %v and value %v", name, labels, metric.GetGauge().GetValue())
			}
		}
	}
}

func TestHTTPMetricsPublicationRemovalAndReadiness(t *testing.T) {
	publications := store.New()
	root := store.ObjectRef{UID: "root", Namespace: "ns", Name: "training", GVK: schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}}
	source := store.SourceKey{Resource: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, Namespace: "ns"}
	publications.SetSourceHealth(source, store.SourceHealth{GVK: root.GVK, Synced: true})
	publications.UpsertWorkload(&store.WorkloadResult{
		Record: store.WorkloadRecord{UID: root.UID, Ref: root, Karta: "job-karta", KartaSource: "cluster", OwnerResolution: store.Resolved,
			HasStatus: true, Phases: []string{"Running", "Degraded"}, CreatedTimestamp: 50, StatusLabels: map[string]string{"team": "research"}},
		HasValue: true, Sources: []store.SourceKey{source}, CreationSources: []store.SourceKey{source},
	})
	podSamples := make([]expectedSample, 0, 2)
	for i := 1; i <= 2; i++ {
		uid, name := fmt.Sprintf("pod-%d", i), fmt.Sprintf("worker-%d", i)
		publications.UpsertPod(&store.PodResult{Record: store.PodRecord{UID: types.UID(uid), Namespace: "ns", Name: name, Workload: root,
			OwnerResolution: store.Resolved, Component: "trainer", Instance: "0", Group: "workers", Labels: map[string]string{"queue": "gpu"}},
			HasValue: true, Sources: []store.SourceKey{source}})
		podSamples = append(podSamples, expectedSample{labels: map[string]string{
			"namespace": "ns", "workload_name": "training", "workload_group": "batch", "workload_version": "v1", "workload_kind": "Job", "workload_uid": "root",
			"owner_resolution": "resolved", "pod_name": name, "pod_uid": uid, "component": "trainer", "component_instance": "0", "replica_group_id": "workers", "observation_valid": "true", "queue": "gpu",
		}, value: 1})
	}
	statusSamples := make([]expectedSample, 0, 9)
	for _, phase := range []string{"Initializing", "Running", "Completed", "Failed", "Degraded", "Suspended", "Suspending", "Resuming", "Undefined"} {
		value := 0.0
		if phase == "Running" || phase == "Degraded" {
			value = 1
		}
		statusSamples = append(statusSamples, expectedSample{labels: map[string]string{
			"namespace": "ns", "workload_name": "training", "workload_group": "batch", "workload_version": "v1", "workload_kind": "Job", "workload_uid": "root",
			"phase": phase, "owner_resolution": "resolved", "karta": "job-karta", "karta_source": "cluster", "observation_valid": "true", "team": "research",
		}, value: value})
	}
	expected := map[string][]expectedSample{
		collector.MetricPodWorkloadInfo:          podSamples,
		collector.MetricWorkloadStatus:           statusSamples,
		collector.MetricWorkloadCreatedTimestamp: {{labels: map[string]string{"namespace": "ns", "workload_name": "training", "workload_uid": "root"}, value: 50}},
		collector.MetricExporterObservationValid: {{labels: map[string]string{
			"scope": "resource_type", "namespace": "ns", "resource_group": "batch", "resource_version": "v1", "resource_kind": "Job", "workload_name": "", "workload_uid": "", "reason": "none",
		}, value: 1}},
	}
	h := startHTTPApplication(t, publications)
	h.status(t, "/healthz", http.StatusOK)
	h.status(t, "/readyz", http.StatusServiceUnavailable)
	checkHTTPMetrics(t, h.scrape(t), expected)
	h.status(t, "/readyz", http.StatusServiceUnavailable)
	h.runtime.ready.Store(true)
	h.status(t, "/readyz", http.StatusOK)
	h.runtime.ready.Store(false)
	h.status(t, "/readyz", http.StatusServiceUnavailable)

	publications.RemovePod("pod-1")
	expected[collector.MetricPodWorkloadInfo] = podSamples[1:]
	checkHTTPMetrics(t, h.scrape(t), expected)
	publications.RemoveWorkload("root")
	delete(expected, collector.MetricWorkloadStatus)
	delete(expected, collector.MetricWorkloadCreatedTimestamp)
	checkHTTPMetrics(t, h.scrape(t), expected)
	publications.RemovePod("pod-2")
	delete(expected, collector.MetricPodWorkloadInfo)
	checkHTTPMetrics(t, h.scrape(t), expected)

	h.stop(t)
	connection, err := net.DialTimeout("tcp", h.address, time.Second)
	if err == nil {
		connection.Close()
		t.Fatal("metrics listener still accepts connections after shutdown")
	}
}

func TestHTTPMetricsRetainsAllPodIdentities(t *testing.T) {
	publications := store.New()
	root := store.ObjectRef{UID: "root", Namespace: "ns", Name: "training", GVK: schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}}
	const pods = 2501
	for i := 0; i < pods; i++ {
		publications.UpsertPod(&store.PodResult{Record: store.PodRecord{UID: types.UID(fmt.Sprintf("pod-%d", i)), Namespace: "ns", Name: fmt.Sprintf("worker-%d", i), Workload: root}, HasValue: true})
	}
	h := startHTTPApplication(t, publications)
	families := h.scrape(t)
	family := families[collector.MetricPodWorkloadInfo]
	if len(families) != 1 || family == nil || len(family.Metric) != pods {
		t.Fatalf("got %d families and %d Pod identities, want one family and %d", len(families), len(family.GetMetric()), pods)
	}
	seen := make(map[string]bool, pods)
	for _, metric := range family.Metric {
		uid := ""
		for _, label := range metric.Label {
			if label.GetName() == "otel_metric_overflow" || label.GetName() == "otel.metric.overflow" {
				t.Fatal("metrics contains an OTel overflow series")
			}
			if label.GetName() == "pod_uid" {
				uid = label.GetValue()
			}
		}
		if uid == "" || seen[uid] {
			t.Fatalf("missing or repeated Pod identity %q", uid)
		}
		seen[uid] = true
	}
	for i := 0; i < pods; i++ {
		if !seen[fmt.Sprintf("pod-%d", i)] {
			t.Fatalf("missing Pod identity %d", i)
		}
	}
}

func TestHTTPShutdownDrainsCollection(t *testing.T) {
	publications := &blockedCaptureStore{PublicationStore: store.New(), entered: make(chan struct{}), release: make(chan struct{})}
	h := startHTTPApplication(t, publications)
	var release sync.Once
	unblock := func() { release.Do(func() { close(publications.release) }) }
	t.Cleanup(unblock)
	requestDone := make(chan error, 1)
	go func() {
		response, err := h.client.Get("http://" + h.address + "/metrics")
		if response != nil {
			response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-publications.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("collection did not start")
	}
	h.status(t, "/metrics", http.StatusServiceUnavailable)
	h.status(t, "/healthz", http.StatusOK)
	h.cancel()
	select {
	case <-h.runtime.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not receive cancellation")
	}
	select {
	case <-h.done:
		t.Fatal("application stopped before the active collection finished")
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	h.stop(t)
	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatalf("in-flight scrape failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight scrape did not finish")
	}
}
