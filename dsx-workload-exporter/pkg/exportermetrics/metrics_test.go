// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package exportermetrics

import (
	"fmt"
	"net/url"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/metrics"
	"k8s.io/client-go/util/workqueue"
)

// queueRuns names each workqueue test run's queue. The queue metric vectors are
// package-level, so a reused name would carry counts over to the next run.
var queueRuns atomic.Int64

func TestRegisterExposesRuntimeAndProcessMetrics(t *testing.T) {
	registry := prometheus.NewRegistry()
	require.NoError(t, Register(registry))

	families, err := registry.Gather()
	require.NoError(t, err)
	names := make(map[string]bool, len(families))
	for _, family := range families {
		names[family.GetName()] = true
	}
	require.True(t, names["go_goroutines"], "go collector")
	require.True(t, names["go_memstats_heap_inuse_bytes"], "go collector")
	// The process collector reads /proc; other platforms may not report these.
	if runtime.GOOS == "linux" {
		require.True(t, names["process_cpu_seconds_total"], "process collector")
		require.True(t, names["process_resident_memory_bytes"], "process collector")
	}
}

func TestRegisterTwiceInOneRegistryFails(t *testing.T) {
	registry := prometheus.NewRegistry()
	require.NoError(t, Register(registry))
	require.Error(t, Register(registry))
}

func TestRegisterInSeveralRegistries(t *testing.T) {
	require.NoError(t, Register(prometheus.NewRegistry()))
	require.NoError(t, Register(prometheus.NewRegistry()))
}

func TestWorkqueueReportsByQueueName(t *testing.T) {
	require.NoError(t, Register(prometheus.NewRegistry()))
	name := fmt.Sprintf("exportermetrics-test-queue-%d", queueRuns.Add(1))
	queue := workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.DefaultTypedControllerRateLimiter[string](),
		workqueue.TypedRateLimitingQueueConfig[string]{Name: name, MetricsProvider: Workqueue})
	t.Cleanup(queue.ShutDown)

	queue.Add("a")
	queue.Add("b")
	require.InDelta(t, 2, testutil.ToFloat64(queueAdds.WithLabelValues(name)), 0)
	require.InDelta(t, 2, testutil.ToFloat64(queueDepth.WithLabelValues(name)), 0)

	item, _ := queue.Get()
	require.InDelta(t, 1, testutil.ToFloat64(queueDepth.WithLabelValues(name)), 0)
	queue.AddRateLimited(item)
	queue.Done(item)
	require.InDelta(t, 1, testutil.ToFloat64(queueRetries.WithLabelValues(name)), 0)
}

func TestClientGoRequestsAreCounted(t *testing.T) {
	require.NoError(t, Register(prometheus.NewRegistry()))
	host := "exportermetrics-test.example.com"
	// The vectors are package-level, so the checks compare against values read before.
	histogram := func(vec *prometheus.HistogramVec) *dto.Histogram {
		var metric dto.Metric
		require.NoError(t, vec.WithLabelValues("GET", host).(prometheus.Histogram).Write(&metric))
		return metric.GetHistogram()
	}
	before := testutil.ToFloat64(requestResults.WithLabelValues("200", "GET", host))
	latencyBefore, waitBefore := histogram(requestLatency), histogram(rateLimiterLatency)

	metrics.RequestResult.Increment(t.Context(), "200", "GET", host)
	metrics.RequestLatency.Observe(t.Context(), "GET", url.URL{Host: host}, 20*time.Millisecond)
	metrics.RateLimiterLatency.Observe(t.Context(), "GET", url.URL{Host: host}, time.Millisecond)

	require.InDelta(t, before+1, testutil.ToFloat64(requestResults.WithLabelValues("200", "GET", host)), 0)
	latency, wait := histogram(requestLatency), histogram(rateLimiterLatency)
	require.Equal(t, latencyBefore.GetSampleCount()+1, latency.GetSampleCount())
	require.InDelta(t, latencyBefore.GetSampleSum()+0.020, latency.GetSampleSum(), 1e-9)
	require.Equal(t, waitBefore.GetSampleCount()+1, wait.GetSampleCount())
	require.InDelta(t, waitBefore.GetSampleSum()+0.001, wait.GetSampleSum(), 1e-9)
}
