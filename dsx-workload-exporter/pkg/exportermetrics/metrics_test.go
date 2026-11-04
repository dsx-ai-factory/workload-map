// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package exportermetrics

import (
	"net/url"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/metrics"
	"k8s.io/client-go/util/workqueue"
)

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
	name := "exportermetrics-test-queue"
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
	before := testutil.ToFloat64(requestResults.WithLabelValues("200", "GET", host))

	metrics.RequestResult.Increment(t.Context(), "200", "GET", host)
	metrics.RequestLatency.Observe(t.Context(), "GET", url.URL{Host: host}, 20*time.Millisecond)
	metrics.RateLimiterLatency.Observe(t.Context(), "GET", url.URL{Host: host}, time.Millisecond)

	require.InDelta(t, before+1, testutil.ToFloat64(requestResults.WithLabelValues("200", "GET", host)), 0)
	require.Equal(t, 1, testutil.CollectAndCount(requestLatency.MustCurryWith(prometheus.Labels{"verb": "GET", "host": host})))
}
