// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/otlptranslator"
	"go.opentelemetry.io/otel"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	collector "github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/adapters/otel"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/controller"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/exportermetrics"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/version"
)

// Application owns the runtime and HTTP/OTel lifecycle. Serve it once.
type Application struct {
	Handler http.Handler
	// TLSConfig, when set, serves the listener over HTTPS only.
	TLSConfig  *tls.Config
	components Components
	provider   *sdkmetric.MeterProvider
	started    atomic.Bool
	running    atomic.Bool
}

// Components supplies the runtime, definition inventory and shared publication store.
type Components struct {
	Runtime         controller.Runner
	Store           store.PublicationStore
	Definitions     DefinitionRunner
	ExcludeStatuses []v1alpha1.ResourceStatus
	// Push, when set, delivers the gauges through this reader instead of /metrics.
	Push sdkmetric.Reader
}

const shutdownTimeout = 10 * time.Second

var (
	errAlreadyStarted               = errors.New("exporter application can only be served once")
	ErrMetricsListenerRequired      = errors.New("metrics listener is required")
	ErrIncompleteExporterComponents = errors.New("incomplete exporter components")
)

func New(components Components) (*Application, error) {
	if components.Runtime == nil || components.Store == nil || components.Definitions == nil {
		return nil, ErrIncompleteExporterComponents
	}
	mux := http.NewServeMux()
	options := []sdkmetric.Option{sdkmetric.WithCardinalityLimit(0)}
	if components.Push != nil {
		options = append(options, sdkmetric.WithReader(components.Push), sdkmetric.WithResource(pushResource()))
	} else {
		registry := prometheus.NewRegistry()
		if err := exportermetrics.Register(registry); err != nil {
			return nil, fmt.Errorf("register exporter metrics: %w", err)
		}
		reader, err := otelprom.New(otelprom.WithRegisterer(registry), otelprom.WithTranslationStrategy(otlptranslator.NoTranslation), otelprom.WithoutScopeInfo(), otelprom.WithoutTargetInfo())
		if err != nil {
			return nil, fmt.Errorf("create Prometheus reader: %w", err)
		}
		options = append(options, sdkmetric.WithReader(reader))
		mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	}
	application := &Application{
		components: components,
		provider:   sdkmetric.NewMeterProvider(options...),
	}
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

// pushResource names the exporter to the receiver. OTEL_SERVICE_NAME and
// OTEL_RESOURCE_ATTRIBUTES override or add attributes.
func pushResource() *resource.Resource {
	res, err := resource.New(context.Background(),
		resource.WithAttributes(semconv.ServiceName("dsx-workload-exporter"), semconv.ServiceVersion(version.String())),
		resource.WithFromEnv(), resource.WithTelemetrySDK())
	if err != nil {
		otel.Handle(err)
	}
	if res == nil {
		return resource.Default()
	}
	return res
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
	if a.TLSConfig != nil {
		listener = tls.NewListener(listener, a.TLSConfig)
	}
	registration, err := collector.Register(a.provider.Meter("dsx-workload-exporter"), a.components.Store, a.components.ExcludeStatuses)
	if err != nil {
		return errors.Join(fmt.Errorf("register collector: %w", err), listener.Close())
	}
	// This defer completes before the outer Serve/ServeListener shuts down the provider.
	defer func() { err = errors.Join(err, registration.Unregister()) }()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	server := &http.Server{
		Handler:           a.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return runCtx },
	}
	runtimeDone := make(chan struct{})
	var runtimeErr error
	a.running.Store(true)
	go func() {
		defer close(runtimeDone)
		runtimeErr = RunController(runCtx, a.components)
		if runtimeErr == nil && runCtx.Err() == nil {
			runtimeErr = errors.New("exporter runtime stopped unexpectedly")
		} else if runtimeErr == runCtx.Err() {
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
