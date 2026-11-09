// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	collector "github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/adapters/otel"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/controller"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

type runtimeFunc func(context.Context) error

type definitionRunner struct{}

type failingListener struct {
	net.Listener
	err error
}

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

type metricSample struct {
	labels map[string]string
	value  float64
}

type blockedResponseWriter struct {
	http.ResponseWriter
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

type blockedCaptureStore struct {
	store.PublicationStore
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func TestNewRejectsIncompleteComponents(t *testing.T) {
	runtime := runtimeFunc(func(context.Context) error { return nil })
	for name, components := range map[string]Components{
		"runtime":     {Store: store.New(), Definitions: definitionRunner{}},
		"store":       {Runtime: runtime, Definitions: definitionRunner{}},
		"definitions": {Runtime: runtime, Store: store.New()},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(components); !errors.Is(err, ErrIncompleteExporterComponents) {
				t.Fatalf("got %v, want incomplete components error", err)
			}
		})
	}
}

func TestApplicationBindFailure(t *testing.T) {
	occupied := lifecycleListener(t)
	defer occupied.Close()
	application := lifecycleApplication(t, runtimeFunc(func(context.Context) error {
		t.Error("runtime started after bind failure")
		return nil
	}))
	if err := application.Serve(t.Context(), occupied.Addr().String()); err == nil || !strings.Contains(err.Error(), "listen for metrics") {
		t.Fatalf("got %v, want listen error", err)
	}
	assertProviderStopped(t, application)
}

func TestApplicationCanceledBeforeStartup(t *testing.T) {
	for _, bind := range []bool{false, true} {
		t.Run(map[bool]string{false: "listener", true: "address"}[bind], func(t *testing.T) {
			application := lifecycleApplication(t, runtimeFunc(func(context.Context) error {
				t.Error("runtime started after cancellation")
				return nil
			}))
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if bind {
				if err := application.Serve(ctx, "invalid address"); err != nil {
					t.Fatal(err)
				}
			} else {
				listener := lifecycleListener(t)
				if err := application.ServeListener(ctx, listener); err != nil {
					t.Fatal(err)
				}
				if _, err := listener.Accept(); !errors.Is(err, net.ErrClosed) {
					t.Fatalf("listener remains open: %v", err)
				}
			}
			assertProviderStopped(t, application)
		})
	}
}

func TestApplicationRejectsNilListener(t *testing.T) {
	application := lifecycleApplication(t, runtimeFunc(func(context.Context) error { return nil }))
	if err := application.ServeListener(t.Context(), nil); err == nil || !strings.Contains(err.Error(), "listener is required") {
		t.Fatalf("got %v, want missing listener error", err)
	}
	assertProviderStopped(t, application)
}

func TestApplicationRuntimeTermination(t *testing.T) {
	failure := errors.New("runtime failed")
	for _, tc := range []struct {
		name   string
		result error
		cancel bool
		want   error
	}{
		{name: "failure", result: failure, want: failure},
		{name: "unexpected success"},
		{name: "unrequested cancellation", result: context.Canceled, want: context.Canceled},
		{name: "requested cancellation", result: context.Canceled, cancel: true},
		{name: "failure during cancellation", result: errors.Join(context.Canceled, failure), cancel: true, want: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			application := lifecycleApplication(t, runtimeFunc(func(ctx context.Context) error {
				if tc.cancel {
					cancel()
					<-ctx.Done()
				}
				return tc.result
			}))
			err := application.ServeListener(ctx, lifecycleListener(t))
			switch {
			case tc.want != nil && !errors.Is(err, tc.want):
				t.Fatalf("got %v, want %v", err, tc.want)
			case tc.name == "unexpected success" && (err == nil || !strings.Contains(err.Error(), "stopped unexpectedly")):
				t.Fatalf("got %v, want unexpected exit error", err)
			case tc.cancel && tc.want == nil && err != nil:
				t.Fatal(err)
			}
			assertProviderStopped(t, application)
		})
	}
}

func TestApplicationListenerFailure(t *testing.T) {
	failure := errors.New("accept failed")
	application := lifecycleApplication(t, runtimeFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	listener := failingListener{Listener: lifecycleListener(t), err: failure}
	if err := application.ServeListener(t.Context(), listener); !errors.Is(err, failure) {
		t.Fatalf("got %v, want listener failure", err)
	}
	assertProviderStopped(t, application)
}

func TestApplicationCannotServeTwice(t *testing.T) {
	application := lifecycleApplication(t, runtimeFunc(func(context.Context) error { return nil }))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := application.Serve(ctx, "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	listener := lifecycleListener(t)
	if err := application.ServeListener(t.Context(), listener); !errors.Is(err, errAlreadyStarted) {
		t.Fatalf("got %v, want already-started error", err)
	}
	if _, err := listener.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("rejected listener remains open: %v", err)
	}
	if err := application.Serve(t.Context(), "127.0.0.1:0"); !errors.Is(err, errAlreadyStarted) {
		t.Fatalf("got %v, want already-started error", err)
	}
}

func TestApplicationClosesConnectionsWhenShutdownTimesOut(t *testing.T) {
	application := lifecycleApplication(t, runtimeFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}))
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	application.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-release
	})
	listener := lifecycleListener(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- application.ServeListener(ctx, listener) }()
	client := &http.Client{Timeout: 2 * shutdownTimeout}
	defer client.CloseIdleConnections()
	requestDone := make(chan error, 1)
	go func() {
		response, err := client.Get("http://" + listener.Addr().String() + "/metrics")
		if err == nil {
			err = response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP handler did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v, want graceful-shutdown timeout", err)
		}
	case <-time.After(shutdownTimeout + 5*time.Second):
		t.Fatal("application did not stop after graceful-shutdown timeout")
	}
	select {
	case err := <-requestDone:
		if err == nil {
			t.Fatal("active HTTP connection was not forcibly closed")
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP connection remains open after shutdown")
	}
	assertProviderStopped(t, application)
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
	podSamples := make([]metricSample, 0, 2)
	for i := 1; i <= 2; i++ {
		uid, name := fmt.Sprintf("pod-%d", i), fmt.Sprintf("worker-%d", i)
		publications.UpsertPod(&store.PodResult{Record: store.PodRecord{UID: types.UID(uid), Namespace: "ns", Name: name, Workload: root,
			OwnerResolution: store.Resolved, Component: "trainer", Instance: "0", Group: "workers", Labels: map[string]string{"queue": "gpu"}},
			HasValue: true, Sources: []store.SourceKey{source}})
		podSamples = append(podSamples, metricSample{labels: map[string]string{
			"namespace": "ns", "workload_name": "training", "workload_group": "batch", "workload_version": "v1", "workload_kind": "Job", "workload_uid": "root",
			"owner_resolution": "resolved", "pod_name": name, "pod_uid": uid, "component": "trainer", "component_instance": "0", "replica_group_id": "workers", "observation_valid": "true", "queue": "gpu",
		}, value: 1})
	}
	statusSamples := make([]metricSample, 0, 9)
	for _, phase := range []string{"Initializing", "Running", "Completed", "Failed", "Degraded", "Suspended", "Suspending", "Resuming", "Undefined"} {
		value := 0.0
		if phase == "Running" || phase == "Degraded" {
			value = 1
		}
		statusSamples = append(statusSamples, metricSample{labels: map[string]string{
			"namespace": "ns", "workload_name": "training", "workload_group": "batch", "workload_version": "v1", "workload_kind": "Job", "workload_uid": "root",
			"phase": phase, "owner_resolution": "resolved", "karta": "job-karta", "karta_source": "cluster", "observation_valid": "true", "team": "research",
		}, value: value})
	}
	expected := map[string][]metricSample{
		collector.MetricPodWorkloadInfo:          podSamples,
		collector.MetricWorkloadStatus:           statusSamples,
		collector.MetricWorkloadCreatedTimestamp: {{labels: map[string]string{"namespace": "ns", "workload_name": "training", "workload_uid": "root"}, value: 50}},
		collector.MetricExporterObservationValid: {{labels: map[string]string{
			"scope": "resource_type", "namespace": "ns", "resource_group": "batch", "resource_version": "v1", "resource_kind": "Job", "workload_name": "", "workload_uid": "", "reason": "none",
		}, value: 1}},
	}
	h := startHTTPApplication(t, publications, nil)
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

func TestHTTPMetricsExcludesConfiguredPhases(t *testing.T) {
	publications := store.New()
	publications.UpsertWorkload(&store.WorkloadResult{
		Record: store.WorkloadRecord{
			UID: "root", Ref: store.ObjectRef{UID: "root", Name: "sandbox"},
			HasStatus: true, Phases: []string{"Suspending"},
		},
		HasValue: true,
	})
	h := startHTTPApplication(t, publications, nil, v1alpha1.SuspendingStatus, v1alpha1.ResumingStatus)
	family := h.scrape(t)[collector.MetricWorkloadStatus]
	require.NotNil(t, family)
	require.Len(t, family.Metric, 7)
	for _, sample := range family.Metric {
		for _, label := range sample.Label {
			if label.GetName() == collector.LabelPhase {
				require.NotContains(t, []string{"Suspending", "Resuming"}, label.GetValue())
			}
		}
		require.Zero(t, sample.GetGauge().GetValue())
	}
}

func TestHTTPMetricsRetainsAllPodIdentities(t *testing.T) {
	publications := store.New()
	root := store.ObjectRef{UID: "root", Namespace: "ns", Name: "training", GVK: schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}}
	const pods = 2501
	for i := 0; i < pods; i++ {
		publications.UpsertPod(&store.PodResult{Record: store.PodRecord{UID: types.UID(fmt.Sprintf("pod-%d", i)), Namespace: "ns", Name: fmt.Sprintf("worker-%d", i), Workload: root}, HasValue: true})
	}
	h := startHTTPApplication(t, publications, nil)
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

func TestHTTPOverlappingScrapesKeepIndependentSnapshots(t *testing.T) {
	publications := store.New()
	root := store.ObjectRef{UID: "root", Namespace: "ns", Name: "training", GVK: schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}}
	publish := func(revision string) map[string][]metricSample {
		var expected []metricSample
		for i := 1; i <= 2; i++ {
			uid, name := fmt.Sprintf("pod-%d", i), fmt.Sprintf("worker-%d", i)
			publications.UpsertPod(&store.PodResult{
				Record: store.PodRecord{UID: types.UID(uid), Namespace: "ns", Name: name, Workload: root,
					OwnerResolution: store.Resolved, Labels: map[string]string{"revision": revision}},
				HasValue: true,
			})
			expected = append(expected, metricSample{labels: map[string]string{
				"namespace": "ns", "workload_name": "training", "workload_group": "batch", "workload_version": "v1", "workload_kind": "Job", "workload_uid": "root",
				"owner_resolution": "resolved", "pod_name": name, "pod_uid": uid, "component": "", "component_instance": "", "replica_group_id": "", "observation_valid": "true", "revision": revision,
			}, value: 1})
		}
		return map[string][]metricSample{collector.MetricPodWorkloadInfo: expected}
	}
	oldSnapshot := publish("old")
	entered, release := make(chan struct{}), make(chan struct{})
	var firstRequest atomic.Bool
	h := startHTTPApplication(t, publications, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/metrics" && !firstRequest.Swap(true) {
				w = &blockedResponseWriter{ResponseWriter: w, entered: entered, release: release}
			}
			next.ServeHTTP(w, request)
		})
	})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	type scrapeResponse struct {
		status int
		body   string
		err    error
	}
	firstDone := make(chan scrapeResponse, 1)
	go func() {
		response, err := h.client.Get("http://" + h.address + "/metrics")
		if err != nil {
			firstDone <- scrapeResponse{err: err}
			return
		}
		body, readErr := io.ReadAll(response.Body)
		firstDone <- scrapeResponse{status: response.StatusCode, body: string(body), err: errors.Join(readErr, response.Body.Close())}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first scrape did not reach response writing")
	}
	// Collection has finished, but the HTTP request is still in flight.
	newSnapshot := publish("new")
	checkHTTPMetrics(t, h.scrape(t), newSnapshot)
	h.status(t, "/healthz", http.StatusOK)
	h.cancel()
	select {
	case <-h.runtime.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not receive cancellation")
	}
	select {
	case <-h.done:
		t.Fatal("application stopped before the first scrape finished writing")
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	select {
	case response := <-firstDone:
		require.NoError(t, response.err)
		require.Equal(t, http.StatusOK, response.status)
		parser := expfmt.NewTextParser(model.UTF8Validation)
		families, err := parser.TextToMetricFamilies(strings.NewReader(response.body))
		require.NoError(t, err)
		checkHTTPMetrics(t, families, oldSnapshot)
	case <-time.After(5 * time.Second):
		t.Fatal("first scrape did not finish")
	}
	h.stop(t)
}

func TestHTTPShutdownDrainsCollection(t *testing.T) {
	publications := &blockedCaptureStore{PublicationStore: store.New(), entered: make(chan struct{}), release: make(chan struct{})}
	h := startHTTPApplication(t, publications, nil)
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

func (definitionRunner) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (definitionRunner) WaitLoaded(ctx context.Context) error { return ctx.Err() }

func (f runtimeFunc) Run(ctx context.Context) error { return f(ctx) }

func (runtimeFunc) Ready() bool { return false }

func (l failingListener) Accept() (net.Conn, error) { return nil, l.err }

func lifecycleApplication(t *testing.T, runtime controller.Runner) *Application {
	t.Helper()
	application, err := New(Components{Runtime: runtime, Store: store.New(), Definitions: definitionRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	return application
}

func lifecycleListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	return listener
}

func assertProviderStopped(t *testing.T, application *Application) {
	t.Helper()
	if err := application.provider.Shutdown(t.Context()); !errors.Is(err, sdkmetric.ErrReaderShutdown) {
		t.Fatalf("provider was not already shut down: %v", err)
	}
}

func (r *httpTestRuntime) Run(ctx context.Context) error {
	close(r.started)
	defer close(r.stopped)
	<-ctx.Done()
	return ctx.Err()
}

func (r *httpTestRuntime) Ready() bool { return r.ready.Load() }

func (w *blockedResponseWriter) Write(data []byte) (int, error) {
	w.once.Do(func() {
		close(w.entered)
		<-w.release
	})
	return w.ResponseWriter.Write(data)
}

func (s *blockedCaptureStore) Capture() store.CapturedView {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return s.PublicationStore.Capture()
}

func startHTTPApplication(t *testing.T, publications store.PublicationStore, wrapHandler func(http.Handler) http.Handler, excludeStatuses ...v1alpha1.ResourceStatus) *httpApplication {
	t.Helper()
	runtime := &httpTestRuntime{started: make(chan struct{}), stopped: make(chan struct{})}
	application, err := New(Components{Runtime: runtime, Store: publications, Definitions: definitionRunner{}, ExcludeStatuses: excludeStatuses})
	if err != nil {
		t.Fatal(err)
	}
	if wrapHandler != nil {
		application.Handler = wrapHandler(application.Handler)
	}
	listener := lifecycleListener(t)
	ctx, cancel := context.WithCancel(t.Context())
	h := &httpApplication{
		runtime: runtime,
		client:  &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{}},
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

func checkHTTPMetrics(t *testing.T, got map[string]*dto.MetricFamily, expected map[string][]metricSample) {
	t.Helper()
	require.Len(t, got, len(expected), "metric families")
	for name, samples := range expected {
		family := got[name]
		require.NotNil(t, family, name)
		require.Equal(t, dto.MetricType_GAUGE, family.GetType(), name)
		actual := make([]metricSample, 0, len(family.Metric))
		for _, metric := range family.Metric {
			labels := make(map[string]string, len(metric.Label))
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			actual = append(actual, metricSample{labels: labels, value: metric.GetGauge().GetValue()})
		}
		require.ElementsMatch(t, samples, actual, name)
	}
}
