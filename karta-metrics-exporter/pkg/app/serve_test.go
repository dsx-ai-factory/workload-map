// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package app_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/app"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/controller"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

func TestServeRejectsMissingInputsAndClosesListener(t *testing.T) {
	require.ErrorContains(t, app.Serve(t.Context(), app.Components{}, nil), "metrics listener is required")
	for _, missing := range []string{"runtime", "store", "definitions"} {
		t.Run(missing, func(t *testing.T) {
			components := app.Components{
				Runtime: controller.NewMockRuntime(t), Store: store.New(),
				Definitions: definitionRunner(func(context.Context) error {
					t.Error("inventory started with incomplete components")
					return nil
				}),
			}
			switch missing {
			case "runtime":
				components.Runtime = nil
			case "store":
				components.Store = nil
			case "definitions":
				components.Definitions = nil
			}
			listener := serveListener(t)
			require.ErrorContains(t, app.Serve(t.Context(), components, listener), "incomplete exporter components")
			require.ErrorIs(t, listener.SetDeadline(time.Now()), net.ErrClosed)
		})
	}
}

func TestServeReadinessAndCancellationJoinWorkers(t *testing.T) {
	for _, held := range []string{"runtime", "inventory"} {
		t.Run("waits for "+held, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			defer unblock()
			runtimeStarted, inventoryStarted := make(chan struct{}), make(chan struct{})
			runtimeCanceled, inventoryCanceled := make(chan struct{}), make(chan struct{})
			runtimeStopped, inventoryStopped := make(chan struct{}), make(chan struct{})
			var ready atomic.Bool
			runtime := controller.NewMockRuntime(t)
			runtime.EXPECT().Ready().RunAndReturn(ready.Load)
			runtime.EXPECT().Run(mock.Anything).RunAndReturn(func(ctx context.Context) error {
				defer close(runtimeStopped)
				close(runtimeStarted)
				<-ctx.Done()
				close(runtimeCanceled)
				if held == "runtime" {
					<-release
				}
				return nil
			}).Once()
			definitions := definitionRunner(func(ctx context.Context) error {
				defer close(inventoryStopped)
				close(inventoryStarted)
				<-ctx.Done()
				close(inventoryCanceled)
				if held == "inventory" {
					<-release
				}
				return nil
			})
			listener := serveListener(t)
			done := make(chan error, 1)
			go func() {
				done <- app.Serve(ctx, app.Components{Runtime: runtime, Store: store.New(), Definitions: definitions}, listener)
			}()
			receiveLifecycle(t, runtimeStarted)
			receiveLifecycle(t, inventoryStarted)
			for _, value := range []bool{false, true, false} {
				ready.Store(value)
				status := serveStatus(t, listener, "/healthz")
				require.Equal(t, http.StatusOK, status)
				status = serveStatus(t, listener, "/readyz")
				if value {
					require.Equal(t, http.StatusOK, status)
				} else {
					require.Equal(t, http.StatusServiceUnavailable, status)
				}
			}
			cancel()
			receiveLifecycle(t, runtimeCanceled)
			receiveLifecycle(t, inventoryCanceled)
			select {
			case err := <-done:
				t.Fatalf("Serve returned before %s stopped: %v", held, err)
			case <-time.After(20 * time.Millisecond):
			}
			unblock()
			require.NoError(t, receiveLifecycle(t, done))
			requireClosed(t, runtimeStopped)
			requireClosed(t, inventoryStopped)
			require.ErrorIs(t, listener.SetDeadline(time.Now()), net.ErrClosed)
		})
	}
}

func TestServeFailuresCancelPeersAndCloseListener(t *testing.T) {
	failure := errors.New("worker failed")
	for _, tc := range []struct {
		name, component string
		err             error
	}{
		{name: "inventory error", component: "inventory", err: failure},
		{name: "inventory exits early", component: "inventory"},
		{name: "runtime error", component: "runtime", err: failure},
		{name: "runtime exits early", component: "runtime"},
		{name: "HTTP listener fails", component: "server"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			trigger := make(chan struct{})
			runtimeStarted, inventoryStarted := make(chan struct{}), make(chan struct{})
			runtimeStopped, inventoryStopped := make(chan struct{}), make(chan struct{})
			runtime := controller.NewMockRuntime(t)
			runtime.EXPECT().Run(mock.Anything).RunAndReturn(func(ctx context.Context) error {
				defer close(runtimeStopped)
				close(runtimeStarted)
				if tc.component == "runtime" {
					select {
					case <-trigger:
						return tc.err
					case <-ctx.Done():
						return nil
					}
				}
				<-ctx.Done()
				return nil
			}).Once()
			definitions := definitionRunner(func(ctx context.Context) error {
				defer close(inventoryStopped)
				close(inventoryStarted)
				if tc.component == "inventory" {
					select {
					case <-trigger:
						return tc.err
					case <-ctx.Done():
						return nil
					}
				}
				<-ctx.Done()
				return nil
			})
			listener := serveListener(t)
			done := make(chan error, 1)
			go func() {
				done <- app.Serve(ctx, app.Components{Runtime: runtime, Store: store.New(), Definitions: definitions}, listener)
			}()
			receiveLifecycle(t, runtimeStarted)
			receiveLifecycle(t, inventoryStarted)
			status := serveStatus(t, listener, "/healthz")
			require.Equal(t, http.StatusOK, status)
			if tc.component == "server" {
				require.NoError(t, listener.Close())
			} else {
				close(trigger)
			}
			err := receiveLifecycle(t, done)
			switch {
			case tc.component == "server":
				require.ErrorIs(t, err, net.ErrClosed)
			case tc.err != nil:
				require.ErrorIs(t, err, tc.err)
			case tc.component == "inventory":
				require.ErrorContains(t, err, "definition inventory stopped before cancellation")
			default:
				require.ErrorContains(t, err, "controller stopped before cancellation")
			}
			requireClosed(t, runtimeStopped)
			requireClosed(t, inventoryStopped)
			require.ErrorIs(t, listener.SetDeadline(time.Now()), net.ErrClosed)
		})
	}
}

func serveListener(t *testing.T) *net.TCPListener {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func serveStatus(t *testing.T, listener net.Listener, path string) int {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+listener.Addr().String()+path, nil)
	require.NoError(t, err)
	client := &http.Client{Timeout: time.Second}
	response, err := client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	return response.StatusCode
}
