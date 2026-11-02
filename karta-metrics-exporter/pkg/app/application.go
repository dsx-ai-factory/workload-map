// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/otlptranslator"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	collector "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/adapters/otel"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/controller"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

// Application owns the runtime and HTTP/OTel lifecycle. Serve it once.
type Application struct {
	Handler    http.Handler
	components Components
	provider   *sdkmetric.MeterProvider
	started    atomic.Bool
	running    atomic.Bool
}

// Components supplies the runtime, definition inventory and shared publication store.
type Components struct {
	Runtime     controller.Runner
	Store       store.PublicationStore
	Definitions DefinitionRunner
}

const shutdownTimeout = 10 * time.Second

var (
	errAlreadyStarted          = errors.New("exporter application can only be served once")
	ErrMetricsListenerRequired = errors.New("metrics listener is required")
)

func New(components Components) (*Application, error) {
	if components.Runtime == nil || components.Store == nil {
		return nil, errors.New("incomplete exporter components")
	}
	registry := prometheus.NewRegistry()
	reader, err := otelprom.New(otelprom.WithRegisterer(registry), otelprom.WithTranslationStrategy(otlptranslator.NoTranslation), otelprom.WithoutScopeInfo(), otelprom.WithoutTargetInfo())
	if err != nil {
		return nil, fmt.Errorf("create Prometheus reader: %w", err)
	}
	application := &Application{
		components: components,
		provider:   sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithCardinalityLimit(0)),
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{MaxRequestsInFlight: 1}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if !application.running.Load() || r.Context().Err() != nil || !components.Runtime.Ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	application.Handler = mux
	return application, nil
}

// Serve binds the metrics and health endpoint, then runs until cancellation or failure.
func (a *Application) Serve(ctx context.Context, address string) (err error) {
	if !a.started.CompareAndSwap(false, true) {
		return errAlreadyStarted
	}
	defer func() { err = errors.Join(err, a.shutdown()) }()
	if ctx.Err() != nil {
		return nil
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen for metrics: %w", err)
	}
	return a.serveListener(ctx, listener)
}

// ServeListener takes ownership of an already bound listener, including on failure.
func (a *Application) ServeListener(ctx context.Context, listener net.Listener) (err error) {
	if !a.started.CompareAndSwap(false, true) {
		if listener != nil {
			return errors.Join(errAlreadyStarted, listener.Close())
		}
		return errAlreadyStarted
	}
	defer func() { err = errors.Join(err, a.shutdown()) }()
	if listener == nil {
		return ErrMetricsListenerRequired
	}
	return a.serveListener(ctx, listener)
}

func (a *Application) serveListener(ctx context.Context, listener net.Listener) (err error) {
	if ctx.Err() != nil {
		return listener.Close()
	}
	registration, err := collector.Register(a.provider.Meter("karta-metrics-exporter"), a.components.Store)
	if err != nil {
		return errors.Join(fmt.Errorf("register collector: %w", err), listener.Close())
	}
	// This defer completes before the outer Serve/ServeListener shuts down the provider.
	defer func() { err = errors.Join(err, registration.Unregister()) }()
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	server := &http.Server{
		Handler:           a.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return child },
	}
	runtimeDone := make(chan struct{})
	var runtimeErr error
	a.running.Store(true)
	go func() {
		defer close(runtimeDone)
		if a.components.Definitions != nil {
			runtimeErr = RunController(child, a.components)
		} else {
			runtimeErr = a.components.Runtime.Run(child)
		}
		if runtimeErr == nil && child.Err() == nil {
			runtimeErr = errors.New("exporter runtime stopped unexpectedly")
		} else if runtimeErr == child.Err() {
			runtimeErr = nil
		}
	}()
	serverDone := make(chan error, 1)
	go func() {
		serveErr := server.Serve(listener)
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		serverDone <- serveErr
	}()
	serverStopped := false
	select {
	case <-ctx.Done():
	case <-runtimeDone:
	case err = <-serverDone:
		serverStopped = true
	}
	a.running.Store(false)
	cancel()
	shutdownCtx, stop := context.WithTimeout(context.Background(), shutdownTimeout)
	defer stop()
	if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil {
		err = errors.Join(err, fmt.Errorf("shutdown metrics server: %w", shutdownErr), server.Close())
	}
	<-runtimeDone
	if !serverStopped {
		err = errors.Join(err, <-serverDone)
	}
	return errors.Join(err, runtimeErr)
}

func (a *Application) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := a.provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown meter provider: %w", err)
	}
	return nil
}
