// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package bootstrap

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/config"
)

type timeoutTransport func(*http.Request) (*http.Response, error)

func (f timeoutTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestGlobalRequestTimeoutBoundsInventoryList(t *testing.T) {
	finished := make(chan error, 1)
	transport := timeoutTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("watch") == "true" {
			return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": {"application/json"}}, Request: request,
				Body: io.NopCloser(strings.NewReader(`{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"Invalid","code":400,"message":"streaming lists are disabled"}`))}, nil
		}
		<-request.Context().Done()
		finished <- request.Context().Err()
		return nil, request.Context().Err()
	})
	cfg := config.DefaultConfig()
	cfg.RequestTimeout = "25ms"
	components, err := New(cfg, &rest.Config{Host: "https://example.test", Transport: transport})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- components.Definitions.Run(ctx) }()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("global requestTimeout did not bound the Karta LIST")
	}
	cancel()
	require.NoError(t, <-done)
}
