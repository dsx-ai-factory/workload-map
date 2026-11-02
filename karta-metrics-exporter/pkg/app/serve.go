// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/otlptranslator"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	collector "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/adapters/otel"
)

// Serve owns the listener, runs the controller and serves metrics until cancellation.
func Serve(ctx context.Context, components Components, listener net.Listener) (err error) {
	if listener == nil {
		return errors.New("metrics listener is required")
	}
	defer func() { _ = listener.Close() }()
	if components.Runtime == nil || components.Store == nil || components.Definitions == nil {
		return errors.New("incomplete exporter components")
	}
	registry := prometheus.NewRegistry()
	exporter, err := otelprom.New(otelprom.WithRegisterer(registry),
		otelprom.WithTranslationStrategy(otlptranslator.NoTranslation),
		otelprom.WithoutScopeInfo(), otelprom.WithoutTargetInfo())
	if err != nil {
		return fmt.Errorf("create metrics exporter: %w", err)
	}
	// Each Pod and workload needs its own series; the SDK's default limit would drop identities.
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter), sdkmetric.WithCardinalityLimit(0))
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err = errors.Join(err, provider.Shutdown(shutdown))
	}()
	registration, err := collector.Register(provider.Meter("karta-metrics-exporter"), components.Store)
	if err != nil {
		return fmt.Errorf("register collector: %w", err)
	}
	defer func() { err = errors.Join(err, registration.Unregister()) }()

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{MaxRequestsInFlight: 1}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !components.Runtime.Ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, 2)
	go func() {
		err := RunController(runCtx, components)
		if err == nil && runCtx.Err() == nil {
			err = errors.New("controller stopped before cancellation")
		}
		results <- err
	}()
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		results <- err
	}()

	remaining := 2
	select {
	case <-ctx.Done():
	case err = <-results:
		remaining--
	}
	cancel()
	shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if shutdownErr := server.Shutdown(shutdown); shutdownErr != nil {
		err = errors.Join(err, shutdownErr, server.Close())
	}
	for range remaining {
		err = errors.Join(err, <-results)
	}
	return err
}
