// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package otel

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	. "github.com/onsi/gomega"
	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	exporter "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
)

type countingCapturer struct {
	store.Capturer
	calls atomic.Int64
}

func (c *countingCapturer) Capture() store.CapturedView {
	c.calls.Add(1)
	return c.Capturer.Capture()
}

func collectorFixture(t *testing.T) (store.PublicationStore, *store.WorkloadResult, *store.PodResult, func() string) {
	t.Helper()
	g := NewWithT(t)
	s := store.New()
	root := store.ObjectRef{UID: "root", Namespace: "ns", Name: "job", GVK: schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}}
	rootSource := store.SourceKey{Resource: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, Namespace: "ns"}
	kartaSource := store.SourceKey{Resource: schema.GroupVersionResource{Group: "run.ai", Version: "v1alpha1", Resource: "kartadefinitions"}}
	s.SetSourceHealth(rootSource, store.SourceHealth{GVK: root.GVK, Synced: true})
	s.SetSourceHealth(kartaSource, store.SourceHealth{GVK: schema.GroupVersionKind{Group: "run.ai", Version: "v1alpha1", Kind: "KartaDefinition"}, Synced: true})
	workload := &store.WorkloadResult{
		Record:   store.WorkloadRecord{UID: root.UID, Ref: root, Karta: "job-karta", KartaSource: "cluster", OwnerResolution: store.Resolved, HasStatus: true, Phases: []string{"Running", "Degraded"}, CreatedTimestamp: 50},
		HasValue: true, Sources: []store.SourceKey{rootSource, kartaSource}, CreationSources: []store.SourceKey{rootSource},
	}
	pod := &store.PodResult{
		Record:   store.PodRecord{UID: "pod", Namespace: "ns", Name: "worker", Workload: root, Karta: "job-karta", KartaSource: "cluster", OwnerResolution: store.Resolved},
		Input:    store.ObjectRef{UID: "pod", Namespace: "ns", Name: "worker", GVK: schema.GroupVersionKind{Version: "v1", Kind: "Pod"}},
		HasValue: true, Sources: workload.Sources,
	}
	s.UpsertWorkload(workload)
	s.UpsertPod(pod)
	registry := prom.NewRegistry()
	reader, err := exporter.New(exporter.WithRegisterer(registry), exporter.WithoutTargetInfo(), exporter.WithoutScopeInfo())
	g.Expect(err).NotTo(HaveOccurred())
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithCardinalityLimit(0))
	t.Cleanup(func() { g.Expect(provider.Shutdown(context.Background())).To(Succeed()) })
	source := &countingCapturer{Capturer: s}
	registration, err := Register(provider.Meter("test"), source)
	g.Expect(err).NotTo(HaveOccurred())
	t.Cleanup(func() { g.Expect(registration.Unregister()).To(Succeed()) })
	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	return s, workload, pod, func() string {
		before := source.calls.Load()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
		g.Expect(response.Code).To(Equal(200))
		g.Expect(source.calls.Load()).To(Equal(before + 1))
		return response.Body.String()
	}
}

func TestFourMetricFamiliesAndDensePhases(t *testing.T) {
	g := NewWithT(t)
	s, workload, pod, scrape := collectorFixture(t)
	labels := map[string]string{}
	for i := 0; i < 20; i++ {
		labels[fmt.Sprintf("custom_%02d", i)] = "saved"
	}
	workload.Record.StatusLabels = labels
	pod.Record.Labels = labels
	s.UpsertWorkload(workload)
	s.UpsertPod(pod)
	output := scrape()
	for _, name := range []string{MetricPodWorkloadInfo, MetricWorkloadStatus, MetricWorkloadCreatedTimestamp, MetricExporterObservationValid} {
		g.Expect(output).To(ContainSubstring(name + "{"))
	}
	g.Expect(strings.Count(output, MetricWorkloadStatus+"{")).To(Equal(11))
	g.Expect(output).To(ContainSubstring(`custom_19="saved"`))
	g.Expect(output).To(ContainSubstring(`reason="none"`))
	g.Expect(output).NotTo(ContainSubstring(`scope="workload"`))
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, MetricWorkloadStatus+"{") {
			expected := " 0"
			if strings.Contains(line, `phase="Running"`) || strings.Contains(line, `phase="Degraded"`) {
				expected = " 1"
			}
			g.Expect(line).To(HaveSuffix(expected))
		}
	}
}

func TestPendingProgressingAndLegacyPhaseMetrics(t *testing.T) {
	g := NewWithT(t)
	s, workload, _, scrape := collectorFixture(t)
	for _, test := range []struct {
		name                                        string
		phases                                      []string
		pending, progressing, running, initializing int
	}{
		{"waiting rollout", []string{"Pending", "Progressing"}, 1, 1, 0, 0},
		{"running clears startup", []string{"Running"}, 0, 0, 1, 0},
		{"legacy definition", []string{"Initializing"}, 0, 0, 0, 1},
	} {
		workload.Record.Phases = test.phases
		s.UpsertWorkload(workload)
		var samples []string
		for _, line := range strings.Split(scrape(), "\n") {
			if strings.HasPrefix(line, MetricWorkloadStatus+"{") {
				samples = append(samples, line)
			}
		}
		for phase, value := range map[string]int{
			"Pending": test.pending, "Progressing": test.progressing,
			"Running": test.running, "Initializing": test.initializing,
		} {
			g.Expect(samples).To(ContainElement(SatisfyAll(
				ContainSubstring(`phase="`+phase+`"`), HaveSuffix(fmt.Sprintf(" %d", value)),
			)), test.name)
		}
	}
}

func TestPodCarriesRootIdentityBeforeWorkloadResult(t *testing.T) {
	g := NewWithT(t)
	s, _, pod, scrape := collectorFixture(t)
	s.RemoveWorkload("root")
	pod.Record.Workload.GVK.Version = "v2"
	pod.Record.Workload.Name = "independent-job"
	pod.Record.Workload.UID = "independent-root"
	pod.Record.Component = "trainer"
	s.UpsertPod(pod)
	output := scrape()
	g.Expect(output).To(ContainSubstring(`workload_name="independent-job"`))
	g.Expect(output).To(ContainSubstring(`workload_uid="independent-root"`))
	g.Expect(output).To(ContainSubstring(`workload_version="v2"`))
	g.Expect(output).To(ContainSubstring(`component="trainer"`))
	g.Expect(output).NotTo(ContainSubstring(MetricWorkloadStatus + "{"))
	g.Expect(output).NotTo(ContainSubstring(MetricWorkloadCreatedTimestamp + "{"))
}

func TestClusterRootKeepsPodNamespacesDistinct(t *testing.T) {
	g := NewWithT(t)
	s, workload, pod, scrape := collectorFixture(t)
	workload.Record.Ref.Namespace = ""
	workload.Record.Ref.GVK = schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "ClusterWorkload"}
	pod.Record.Workload = workload.Record.Ref
	pod.Record.Namespace = "team-a"
	second := *pod
	second.Record.UID, second.Record.Namespace = "pod-b", "team-b"
	s.UpsertWorkload(workload)
	s.UpsertPod(pod)
	s.UpsertPod(&second)
	output := scrape()
	g.Expect(strings.Count(output, MetricPodWorkloadInfo+"{")).To(Equal(2))
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.HasPrefix(line, MetricPodWorkloadInfo+"{"):
			if strings.Contains(line, `pod_uid="pod-b"`) {
				g.Expect(line).To(ContainSubstring(`namespace="team-b"`))
			} else {
				g.Expect(line).To(ContainSubstring(`namespace="team-a"`))
			}
		case strings.HasPrefix(line, MetricWorkloadStatus+"{"):
			g.Expect(line).To(ContainSubstring(`namespace=""`))
		}
	}
	s.RemovePod(pod.Record.UID)
	output = scrape()
	g.Expect(strings.Count(output, MetricPodWorkloadInfo+"{")).To(Equal(1))
	g.Expect(output).NotTo(ContainSubstring(`namespace="team-a"`))
}

func TestBuiltInIdentityOverridesCustomLabels(t *testing.T) {
	g := NewWithT(t)
	s, workload, pod, scrape := collectorFixture(t)
	workload.Record.StatusLabels = map[string]string{"namespace": "wrong", "team": "status"}
	pod.Record.Labels = map[string]string{"namespace": "wrong", "team": "pod"}
	s.UpsertWorkload(workload)
	s.UpsertPod(pod)
	output := scrape()
	g.Expect(output).NotTo(ContainSubstring(`namespace="wrong"`))
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.HasPrefix(line, MetricWorkloadCreatedTimestamp+"{"), strings.HasPrefix(line, MetricExporterObservationValid+"{"):
			g.Expect(line).NotTo(ContainSubstring("team="))
		case strings.HasPrefix(line, MetricWorkloadStatus+"{"):
			g.Expect(line).To(ContainSubstring(`team="status"`))
		case strings.HasPrefix(line, MetricPodWorkloadInfo+"{"):
			g.Expect(line).To(ContainSubstring(`team="pod"`))
		}
	}
}

func TestUnavailableStatusAndUnrelatedSourceFailure(t *testing.T) {
	g := NewWithT(t)
	s, workload, _, scrape := collectorFixture(t)
	workload.Record.HasStatus = false
	s.UpsertWorkload(workload)
	s.SetSourceHealth(store.SourceKey{Namespace: "unrelated"}, store.SourceHealth{Error: store.ReasonPermissionDenied})
	output := scrape()
	g.Expect(output).NotTo(ContainSubstring(MetricWorkloadStatus + "{"))
	g.Expect(output).To(ContainSubstring(`reason="permission_denied"`))
	g.Expect(output).To(ContainSubstring(`scope="resource_type"`))
	g.Expect(output).NotTo(ContainSubstring(`scope="workload"`))
}

func TestDefaultPrometheusScopeLabels(t *testing.T) {
	g := NewWithT(t)
	s, _, _, _ := collectorFixture(t)
	registry := prom.NewRegistry()
	reader, err := exporter.New(exporter.WithRegisterer(registry), exporter.WithoutTargetInfo())
	g.Expect(err).NotTo(HaveOccurred())
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithCardinalityLimit(0))
	t.Cleanup(func() { g.Expect(provider.Shutdown(context.Background())).To(Succeed()) })
	registration, err := Register(provider.Meter("scope-test"), s)
	g.Expect(err).NotTo(HaveOccurred())
	t.Cleanup(func() { g.Expect(registration.Unregister()).To(Succeed()) })
	response := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	g.Expect(response.Code).To(Equal(200))
	for _, label := range []string{"otel_scope_name", "otel_scope_version", "otel_scope_schema_url"} {
		g.Expect(response.Body.String()).To(ContainSubstring(label + "="))
	}
}

func TestMoreThan5000PodIdentitiesWithoutOverflow(t *testing.T) {
	g := NewWithT(t)
	s, _, pod, scrape := collectorFixture(t)
	s.RemovePod(pod.Record.UID)
	for i := 0; i < 5001; i++ {
		row := *pod
		row.Record.UID, row.Record.Name = types.UID(fmt.Sprintf("pod-%d", i)), fmt.Sprintf("pod-%d", i)
		s.UpsertPod(&row)
	}
	output := scrape()
	g.Expect(strings.Count(output, MetricPodWorkloadInfo+"{")).To(Equal(5001))
	g.Expect(output).NotTo(ContainSubstring("otel_metric_overflow"))
	seen := make(map[string]bool)
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, MetricPodWorkloadInfo+"{") {
			start := strings.Index(line, `pod_uid="`) + len(`pod_uid="`)
			uid, _, _ := strings.Cut(line[start:], `"`)
			seen[uid] = true
		}
	}
	for i := 0; i < 5001; i++ {
		g.Expect(seen).To(HaveKey(fmt.Sprintf("pod-%d", i)))
	}
}

func TestCreationErrorsWithoutStatusOrPods(t *testing.T) {
	g := NewWithT(t)
	s, workload, pod, scrape := collectorFixture(t)
	workload.Record.HasStatus = false
	workload.Error = store.ReasonPermissionDenied
	s.UpsertWorkload(workload)
	s.RemovePod(pod.Record.UID)
	output := scrape()
	g.Expect(output).NotTo(ContainSubstring(MetricWorkloadCreatedTimestamp + "{"))
	g.Expect(output).To(ContainSubstring(`reason="permission_denied"`))
	g.Expect(output).To(ContainSubstring(`scope="workload"`))
	workload.HasValue = false
	s.UpsertWorkload(workload)
	output = scrape()
	g.Expect(output).To(ContainSubstring(`workload_uid="root"`))
	g.Expect(output).NotTo(ContainSubstring(MetricWorkloadStatus + "{"))
	g.Expect(output).NotTo(ContainSubstring(MetricPodWorkloadInfo + "{"))
}

func TestReplacementAndIndependentDeletionRemoveSeries(t *testing.T) {
	g := NewWithT(t)
	s, workload, pod, scrape := collectorFixture(t)
	workload.Record.StatusLabels = map[string]string{"team": "old"}
	s.UpsertWorkload(workload)
	g.Expect(scrape()).To(ContainSubstring(`team="old"`))
	workload.Record.StatusLabels["team"] = "new"
	s.UpsertWorkload(workload)
	output := scrape()
	g.Expect(output).NotTo(ContainSubstring(`team="old"`))
	g.Expect(output).To(ContainSubstring(`team="new"`))
	s.RemoveWorkload(workload.Record.UID)
	output = scrape()
	g.Expect(output).NotTo(ContainSubstring(MetricWorkloadStatus + "{"))
	g.Expect(output).To(ContainSubstring(`pod_uid="pod"`))
	s.RemovePod(pod.Record.UID)
	g.Expect(scrape()).NotTo(ContainSubstring(`workload_uid="root"`))
}

func TestInvalidCreationTimestampsAreOmitted(t *testing.T) {
	g := NewWithT(t)
	s, workload, _, scrape := collectorFixture(t)
	for _, timestamp := range []int64{0, -1} {
		workload.Record.CreatedTimestamp = timestamp
		s.UpsertWorkload(workload)
		output := scrape()
		g.Expect(output).To(ContainSubstring(MetricPodWorkloadInfo + "{"))
		g.Expect(output).NotTo(ContainSubstring(MetricWorkloadCreatedTimestamp + "{"))
	}
}

func TestSourceRecoveryDoesNotClearObjectErrorsOrChangeSavedValues(t *testing.T) {
	g := NewWithT(t)
	s, workload, pod, scrape := collectorFixture(t)
	rootSource := workload.Sources[0]
	s.SetSourceHealth(rootSource, store.SourceHealth{GVK: workload.Record.Ref.GVK, Synced: true, Error: store.ReasonPermissionDenied})
	output := scrape()
	g.Expect(output).To(ContainSubstring(`observation_valid="false"`))
	g.Expect(output).NotTo(ContainSubstring(`observation_valid="true"`))
	g.Expect(output).NotTo(ContainSubstring(MetricWorkloadCreatedTimestamp + "{"))
	pod.Error = store.ReasonEvaluationFailed
	pod.Record.Labels = map[string]string{"retained": "value"}
	s.UpsertPod(pod)
	s.SetSourceHealth(rootSource, store.SourceHealth{GVK: workload.Record.Ref.GVK, Synced: true})
	output = scrape()
	g.Expect(output).To(ContainSubstring(`reason="evaluation_failed"`))
	g.Expect(output).To(ContainSubstring(MetricWorkloadCreatedTimestamp + "{"))
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, MetricPodWorkloadInfo+"{") {
			g.Expect(line).To(ContainSubstring(`observation_valid="false"`))
			g.Expect(line).To(ContainSubstring(`retained="value"`))
		}
		if strings.HasPrefix(line, MetricWorkloadStatus+"{") {
			g.Expect(line).To(ContainSubstring(`observation_valid="true"`))
		}
	}
	pod.Error = ""
	s.UpsertPod(pod)
	g.Expect(scrape()).NotTo(ContainSubstring(`scope="workload"`))
}

func TestCreationIndependentOfStatusAndKartaSource(t *testing.T) {
	g := NewWithT(t)
	s, workload, _, scrape := collectorFixture(t)
	workload.StatusError = store.ReasonEvaluationFailed
	s.UpsertWorkload(workload)
	output := scrape()
	g.Expect(output).To(ContainSubstring(MetricWorkloadCreatedTimestamp + "{"))
	g.Expect(output).To(ContainSubstring(`reason="evaluation_failed"`))
	workload.StatusError = ""
	s.UpsertWorkload(workload)
	s.SetSourceHealth(workload.Sources[1], store.SourceHealth{Error: store.ReasonPermissionDenied})
	output = scrape()
	g.Expect(output).To(ContainSubstring(MetricWorkloadCreatedTimestamp + "{"))
	g.Expect(output).To(ContainSubstring(`reason="permission_denied"`))
	g.Expect(output).NotTo(ContainSubstring(`observation_valid="true"`))
}

func TestKnownRootErrorsAggregateDeterministically(t *testing.T) {
	g := NewWithT(t)
	s, workload, pod, scrape := collectorFixture(t)
	workload.StatusError = "unexpected detail"
	s.UpsertWorkload(workload)
	pod.Error = store.ReasonUnresolvedOwner
	s.UpsertPod(pod)
	second := *pod
	second.Record.UID = "second"
	second.Record.Workload.GVK.Version = "v2"
	second.Error = store.ReasonPermissionDenied
	s.UpsertPod(&second)
	for i := 0; i < 10; i++ {
		output := scrape()
		g.Expect(strings.Count(output, `scope="workload"`)).To(Equal(1))
		g.Expect(output).NotTo(ContainSubstring("unexpected detail"))
		for _, line := range strings.Split(output, "\n") {
			if strings.Contains(line, `scope="workload"`) {
				g.Expect(line).To(ContainSubstring(`reason="permission_denied"`))
				g.Expect(line).To(ContainSubstring(`resource_version="v1"`))
			}
		}
	}
	second.Error = ""
	s.UpsertPod(&second)
	g.Expect(scrape()).To(ContainSubstring(`reason="api_unavailable"`))
	workload.StatusError = ""
	s.UpsertWorkload(workload)
	g.Expect(scrape()).To(ContainSubstring(`reason="unresolved_owner"`))
}

func TestFirstDiscoveryErrorsHaveNoFabricatedRoot(t *testing.T) {
	g := NewWithT(t)
	s, workload, pod, scrape := collectorFixture(t)
	s.RemoveWorkload(workload.Record.UID)
	pod.Record.Workload = store.ObjectRef{}
	pod.HasValue = false
	pod.Error = store.ReasonUnresolvedOwner
	s.UpsertPod(pod)
	second := *pod
	second.Record.UID, second.Input.UID = "second", "second"
	s.UpsertPod(&second)
	third := *pod
	third.Record.UID, third.Input.UID = "third", "third"
	third.Error = store.ReasonEvaluationFailed
	s.UpsertPod(&third)
	candidate := store.ObjectRef{UID: "candidate", Namespace: "ns", Name: "selected", GVK: schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Worker"}}
	s.SetDiscoveryFailure(candidate, store.ReasonUnresolvedOwner)
	output := scrape()
	g.Expect(strings.Count(output, `scope="discovery"`)).To(Equal(3))
	g.Expect(output).NotTo(ContainSubstring(`scope="workload"`))
	g.Expect(output).NotTo(ContainSubstring(MetricWorkloadStatus + "{"))
	g.Expect(output).NotTo(ContainSubstring(MetricPodWorkloadInfo + "{"))
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, `scope="discovery"`) {
			g.Expect(line).To(ContainSubstring(`workload_name=""`))
			g.Expect(line).To(ContainSubstring(`workload_uid=""`))
			g.Expect(line).To(ContainSubstring(`namespace="ns"`))
		}
	}
	s.RemoveDiscoveryFailure(candidate.UID)
	g.Expect(strings.Count(scrape(), `scope="discovery"`)).To(Equal(2))
}

func TestMissingSourceInvalidatesDependentResults(t *testing.T) {
	g := NewWithT(t)
	s, workload, _, scrape := collectorFixture(t)
	s.RemoveSourceHealth(workload.Sources[0])
	output := scrape()
	g.Expect(output).To(ContainSubstring(`reason="api_unavailable"`))
	g.Expect(output).NotTo(ContainSubstring(`observation_valid="true"`))
	g.Expect(output).NotTo(ContainSubstring(MetricWorkloadCreatedTimestamp + "{"))
}

func TestConcurrentCollectionAndIndependentUpdates(t *testing.T) {
	g := NewWithT(t)
	s, workload, pod, scrape := collectorFixture(t)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			workload.Record.Phases = []string{"Failed"}
			workload.Record.StatusLabels = map[string]string{"revision": fmt.Sprint(i)}
			s.UpsertWorkload(workload)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			pod.Record.Component = fmt.Sprint(i)
			s.UpsertPod(pod)
		}
	}()
	for i := 0; i < 20; i++ {
		output := scrape()
		g.Expect(strings.Count(output, MetricPodWorkloadInfo+"{")).To(Equal(1))
		g.Expect(strings.Count(output, MetricWorkloadStatus+"{")).To(Equal(11))
	}
	wg.Wait()
	output := scrape()
	g.Expect(output).To(ContainSubstring(`component="199"`))
	g.Expect(output).To(ContainSubstring(`revision="199"`))
	g.Expect(s.Workload("root").Record.Phases).To(Equal([]string{"Failed"}))
}

func TestFailureReasonPrecedenceAndBounds(t *testing.T) {
	g := NewWithT(t)
	for _, pair := range []struct{ first, second, expected string }{
		{store.ReasonNone, "", ""},
		{store.ReasonEvaluationFailed, store.ReasonUnresolvedOwner, store.ReasonUnresolvedOwner},
		{store.ReasonInvalidDefinition, store.ReasonAPIUnavailable, store.ReasonAPIUnavailable},
		{store.ReasonPermissionDenied, store.ReasonAPIUnavailable, store.ReasonPermissionDenied},
		{"unexpected detail", store.ReasonEvaluationFailed, store.ReasonAPIUnavailable},
		{"unexpected detail", store.ReasonPermissionDenied, store.ReasonPermissionDenied},
	} {
		g.Expect(failureReason(pair.first, pair.second)).To(Equal(pair.expected))
		g.Expect(failureReason(pair.second, pair.first)).To(Equal(pair.expected))
	}
}

func TestRetainedPodUsesItsOwnProvenanceAfterWorkloadChanges(t *testing.T) {
	g := NewWithT(t)
	s, workload, pod, scrape := collectorFixture(t)
	workload.Record.Ref.GVK.Version = "v2"
	workload.Record.Karta = "replacement-karta"
	workload.Record.KartaSource = "namespace"
	s.UpsertWorkload(workload)
	pod.Input.ResourceVersion = "new-input"
	pod.Error = store.ReasonInvalidDefinition
	s.UpsertPod(pod)
	for _, line := range strings.Split(scrape(), "\n") {
		switch {
		case strings.HasPrefix(line, MetricPodWorkloadInfo+"{"):
			g.Expect(line).To(ContainSubstring(`workload_version="v1"`))
			g.Expect(line).To(ContainSubstring(`observation_valid="false"`))
		case strings.HasPrefix(line, MetricWorkloadStatus+"{"):
			g.Expect(line).To(ContainSubstring(`workload_version="v2"`))
			g.Expect(line).To(ContainSubstring(`karta="replacement-karta"`))
			g.Expect(line).To(ContainSubstring(`karta_source="namespace"`))
		}
	}
	g.Expect(s.Pod("pod").Record.Karta).To(Equal("job-karta"))
	g.Expect(s.Pod("pod").Record.KartaSource).To(Equal("cluster"))
}
