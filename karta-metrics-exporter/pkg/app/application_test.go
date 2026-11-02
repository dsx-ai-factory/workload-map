// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/controller"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

type runtimeFunc func(context.Context) error

func (f runtimeFunc) Run(ctx context.Context) error { return f(ctx) }

type failingListener struct {
	net.Listener
	err error
}

func (l failingListener) Accept() (net.Conn, error) { return nil, l.err }

func TestNewRejectsIncompleteComponents(t *testing.T) {
	for _, components := range []Components{{}, {Store: store.New()}, {Runtime: runtimeFunc(func(context.Context) error { return nil })}} {
		if _, err := New(components); err == nil || !strings.Contains(err.Error(), "incomplete") {
			t.Fatalf("got %v, want incomplete components error", err)
		}
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

func lifecycleApplication(t *testing.T, runtime controller.Runner) *Application {
	t.Helper()
	application, err := New(Components{Runtime: runtime, Store: store.New()})
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
	return listener
}

func assertProviderStopped(t *testing.T, application *Application) {
	t.Helper()
	if err := application.provider.Shutdown(t.Context()); !errors.Is(err, sdkmetric.ErrReaderShutdown) {
		t.Fatalf("provider was not already shut down: %v", err)
	}
}
