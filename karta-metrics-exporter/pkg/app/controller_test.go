// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/adapters/otel"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/app"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/controller"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

type definitionRunner func(context.Context) error

func (d definitionRunner) Run(ctx context.Context) error { return d(ctx) }

func TestRunControllerStartsRuntimeWhileDefinitionsAreUnready(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inventoryStarted := make(chan context.Context, 1)
	inventoryStopped := make(chan struct{})
	runtimeStarted := make(chan context.Context, 1)
	runtimeStopped := make(chan struct{})
	definitions := definitionRunner(func(ctx context.Context) error {
		defer close(inventoryStopped)
		inventoryStarted <- ctx
		<-ctx.Done()
		return nil
	})
	runtime := controller.NewMockRuntime(t)
	runtime.EXPECT().Run(mock.Anything).RunAndReturn(func(ctx context.Context) error {
		defer close(runtimeStopped)
		runtimeStarted <- ctx
		<-ctx.Done()
		return nil
	}).Once()
	done := make(chan error, 1)
	go func() {
		done <- app.RunController(ctx, app.Components{Runtime: runtime, Store: store.New(), Definitions: definitions})
	}()

	require.Same(t, receiveLifecycle(t, inventoryStarted), receiveLifecycle(t, runtimeStarted))
	cancel()
	require.NoError(t, receiveLifecycle(t, done))
	requireClosed(t, inventoryStopped)
	requireClosed(t, runtimeStopped)
}

func TestRunControllerCancellationJoinsBothComponents(t *testing.T) {
	for _, held := range []string{"inventory", "runtime"} {
		t.Run("waits for "+held, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			inventoryCanceled := make(chan struct{})
			inventoryStopped := make(chan struct{})
			runtimeStarted := make(chan struct{})
			runtimeCanceled := make(chan struct{})
			runtimeStopped := make(chan struct{})
			allowExit := make(chan struct{})
			t.Cleanup(func() {
				select {
				case <-allowExit:
				default:
					close(allowExit)
				}
			})
			definitions := definitionRunner(func(ctx context.Context) error {
				defer close(inventoryStopped)
				<-ctx.Done()
				close(inventoryCanceled)
				if held == "inventory" {
					<-allowExit
				}
				return nil
			})
			runtime := controller.NewMockRuntime(t)
			runtime.EXPECT().Run(mock.Anything).RunAndReturn(func(ctx context.Context) error {
				defer close(runtimeStopped)
				close(runtimeStarted)
				<-ctx.Done()
				close(runtimeCanceled)
				if held == "runtime" {
					<-allowExit
				}
				return nil
			}).Once()
			done := make(chan error, 1)
			go func() {
				done <- app.RunController(ctx, app.Components{Runtime: runtime, Store: store.New(), Definitions: definitions})
			}()
			receiveLifecycle(t, runtimeStarted)
			cancel()
			receiveLifecycle(t, inventoryCanceled)
			receiveLifecycle(t, runtimeCanceled)
			select {
			case err := <-done:
				t.Fatalf("returned before %s stopped: %v", held, err)
			case <-time.After(20 * time.Millisecond):
			}
			close(allowExit)
			require.NoError(t, receiveLifecycle(t, done))
			requireClosed(t, inventoryStopped)
			requireClosed(t, runtimeStopped)
		})
	}
}

func TestRunControllerRuntimeErrorCancelsAndJoinsInventory(t *testing.T) {
	failure := errors.New("runtime failed")
	inventoryStopped := make(chan struct{})
	definitions := definitionRunner(func(ctx context.Context) error {
		defer close(inventoryStopped)
		<-ctx.Done()
		return nil
	})
	runtime := controller.NewMockRuntime(t)
	runtime.EXPECT().Run(mock.Anything).Return(failure).Once()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- app.RunController(ctx, app.Components{Runtime: runtime, Store: store.New(), Definitions: definitions})
	}()
	require.ErrorIs(t, receiveLifecycle(t, done), failure)
	requireClosed(t, inventoryStopped)
}

func TestRunControllerInventoryFailureStopsRuntime(t *testing.T) {
	for _, inventoryErr := range []error{errors.New("inventory failed"), nil} {
		name := "error"
		if inventoryErr == nil {
			name = "premature successful exit"
		}
		t.Run(name, func(t *testing.T) {
			runtimeStarted := make(chan struct{})
			runtimeStopped := make(chan struct{})
			inventoryStopped := make(chan struct{})
			definitions := definitionRunner(func(ctx context.Context) error {
				defer close(inventoryStopped)
				select {
				case <-runtimeStarted:
					return inventoryErr
				case <-ctx.Done():
					return nil
				}
			})
			runtime := controller.NewMockRuntime(t)
			runtime.EXPECT().Run(mock.Anything).RunAndReturn(func(ctx context.Context) error {
				defer close(runtimeStopped)
				close(runtimeStarted)
				<-ctx.Done()
				return nil
			}).Once()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- app.RunController(ctx, app.Components{Runtime: runtime, Store: store.New(), Definitions: definitions})
			}()
			err := receiveLifecycle(t, done)
			if inventoryErr != nil {
				require.ErrorIs(t, err, inventoryErr)
			} else {
				require.ErrorContains(t, err, "definition inventory stopped before cancellation")
			}
			requireClosed(t, inventoryStopped)
			requireClosed(t, runtimeStopped)
		})
	}
}

func TestRunControllerRejectsIncompleteComponents(t *testing.T) {
	for _, missing := range []string{"runtime", "store", "definitions"} {
		t.Run(missing, func(t *testing.T) {
			definitions := definitionRunner(func(context.Context) error {
				t.Error("inventory started with incomplete components")
				return nil
			})
			components := app.Components{Runtime: controller.NewMockRuntime(t), Store: store.New(), Definitions: definitions}
			switch missing {
			case "runtime":
				components.Runtime = nil
			case "store":
				components.Store = nil
			case "definitions":
				components.Definitions = nil
			}
			require.ErrorContains(t, app.RunController(context.Background(), components), "incomplete controller components")
		})
	}
}

func TestRunRegistersCollectorAroundControllerLifecycle(t *testing.T) {
	registered := make(chan struct{})
	inventoryStopped := make(chan struct{})
	definitions := definitionRunner(func(ctx context.Context) error {
		defer close(inventoryStopped)
		requireClosed(t, registered)
		<-ctx.Done()
		return nil
	})
	runtimeFailure := errors.New("runtime failed")
	cleanupFailure := errors.New("unregister failed")
	runtime := controller.NewMockRuntime(t)
	runtime.EXPECT().Run(mock.Anything).RunAndReturn(func(context.Context) error {
		requireClosed(t, registered)
		return runtimeFailure
	}).Once()
	publications := store.New()
	reg := &registration{err: cleanupFailure}
	collector := otel.NewMockCollector(t)
	collector.EXPECT().Register(mock.Anything, publications).Return(reg, nil).Run(func(_ metric.Meter, _ otel.Capturer) {
		close(registered)
	}).Once()
	components := app.Components{Runtime: runtime, Store: publications, Collector: collector, Definitions: definitions}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, components, noop.NewMeterProvider().Meter("test")) }()
	err := receiveLifecycle(t, done)
	require.ErrorIs(t, err, runtimeFailure)
	require.ErrorIs(t, err, cleanupFailure)
	require.Equal(t, 1, reg.calls)
	requireClosed(t, inventoryStopped)
}

func receiveLifecycle[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("lifecycle operation did not finish")
		var zero T
		return zero
	}
}

func requireClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	default:
		t.Error("lifecycle operation has not completed")
	}
}
