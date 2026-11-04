// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package exportermetrics exposes the exporter's own Go, process, client-go and workqueue metrics.
package exportermetrics

import (
	"context"
	"net/url"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"k8s.io/client-go/tools/metrics"
	"k8s.io/client-go/util/workqueue"
)

type queueProvider struct{}

type requestResult struct{}

type latency struct {
	histogram *prometheus.HistogramVec
}

// Workqueue reports each named queue as workqueue_* series, labeled by queue name.
var Workqueue workqueue.MetricsProvider = queueProvider{}

var (
	queueDepth = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "workqueue_depth", Help: "Current depth of the workqueue.",
	}, []string{"name"})
	queueAdds = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "workqueue_adds_total", Help: "Total number of adds handled by the workqueue.",
	}, []string{"name"})
	queueLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "workqueue_queue_duration_seconds", Help: "Seconds an item stays in the workqueue before it is requested.",
		Buckets: prometheus.ExponentialBuckets(10e-9, 10, 12),
	}, []string{"name"})
	queueWorkDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "workqueue_work_duration_seconds", Help: "Seconds spent processing an item from the workqueue.",
		Buckets: prometheus.ExponentialBuckets(10e-9, 10, 12),
	}, []string{"name"})
	queueUnfinished = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "workqueue_unfinished_work_seconds", Help: "Seconds of work in progress that has not been observed by work_duration.",
	}, []string{"name"})
	queueLongestRunning = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "workqueue_longest_running_processor_seconds", Help: "Seconds the longest running processor of the workqueue has run.",
	}, []string{"name"})
	queueRetries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "workqueue_retries_total", Help: "Total number of retries handled by the workqueue.",
	}, []string{"name"})

	requestResults = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "rest_client_requests_total", Help: "Number of Kubernetes API requests, by status code, method and host.",
	}, []string{"code", "method", "host"})
	requestLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "rest_client_request_duration_seconds", Help: "Kubernetes API request latency, by verb and host.",
		Buckets: []float64{0.005, 0.025, 0.1, 0.25, 0.5, 1, 2, 4, 8, 15, 30, 60},
	}, []string{"verb", "host"})
	rateLimiterLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "rest_client_rate_limiter_duration_seconds", Help: "Client-side rate limiter wait before a Kubernetes API request, by verb and host.",
		Buckets: []float64{0.005, 0.025, 0.1, 0.25, 0.5, 1, 2, 4, 8, 15, 30, 60},
	}, []string{"verb", "host"})

	registerClientMetrics sync.Once
)

// Register adds the exporter's own metrics to registry. The client-go hooks are process wide and install once.
func Register(registry prometheus.Registerer) error {
	registerClientMetrics.Do(func() {
		metrics.Register(metrics.RegisterOpts{
			RequestResult:      requestResult{},
			RequestLatency:     latency{requestLatency},
			RateLimiterLatency: latency{rateLimiterLatency},
		})
	})
	for _, collector := range []prometheus.Collector{
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		queueDepth, queueAdds, queueLatency, queueWorkDuration, queueUnfinished, queueLongestRunning, queueRetries,
		requestResults, requestLatency, rateLimiterLatency,
	} {
		if err := registry.Register(collector); err != nil {
			return err
		}
	}
	return nil
}

func (queueProvider) NewDepthMetric(name string) workqueue.GaugeMetric {
	return queueDepth.WithLabelValues(name)
}

func (queueProvider) NewAddsMetric(name string) workqueue.CounterMetric {
	return queueAdds.WithLabelValues(name)
}

func (queueProvider) NewLatencyMetric(name string) workqueue.HistogramMetric {
	return queueLatency.WithLabelValues(name)
}

func (queueProvider) NewWorkDurationMetric(name string) workqueue.HistogramMetric {
	return queueWorkDuration.WithLabelValues(name)
}

func (queueProvider) NewUnfinishedWorkSecondsMetric(name string) workqueue.SettableGaugeMetric {
	return queueUnfinished.WithLabelValues(name)
}

func (queueProvider) NewLongestRunningProcessorSecondsMetric(name string) workqueue.SettableGaugeMetric {
	return queueLongestRunning.WithLabelValues(name)
}

func (queueProvider) NewRetriesMetric(name string) workqueue.CounterMetric {
	return queueRetries.WithLabelValues(name)
}

func (requestResult) Increment(_ context.Context, code, method, host string) {
	requestResults.WithLabelValues(code, method, host).Inc()
}

func (l latency) Observe(_ context.Context, verb string, u url.URL, duration time.Duration) {
	l.histogram.WithLabelValues(verb, u.Host).Observe(duration.Seconds())
}
