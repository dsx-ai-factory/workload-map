// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package app

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
)

func TestPushServesOnlyHealthProbes(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "k8s.pod.name=exporter-0")
	push := sdkmetric.NewManualReader()
	runtime := &httpTestRuntime{started: make(chan struct{}), stopped: make(chan struct{})}
	runtime.ready.Store(true)
	application, err := New(Components{Runtime: runtime, Store: store.New(), Definitions: definitionRunner{}, Push: push})
	require.NoError(t, err)
	listener := lifecycleListener(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- application.ServeListener(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("application did not shut down")
		}
	})
	select {
	case <-runtime.started:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not start")
	}

	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	for path, want := range map[string]int{"/metrics": http.StatusNotFound, "/healthz": http.StatusOK, "/readyz": http.StatusOK} {
		resp, err := client.Get("http://" + listener.Addr().String() + path)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, want, resp.StatusCode, path)
	}

	var data metricdata.ResourceMetrics
	require.NoError(t, push.Collect(t.Context(), &data))
	for key, want := range map[attribute.Key]string{"service.name": "dsx-workload-exporter", "k8s.pod.name": "exporter-0"} {
		value, ok := data.Resource.Set().Value(key)
		require.True(t, ok, key)
		require.Equal(t, want, value.AsString())
	}
}
