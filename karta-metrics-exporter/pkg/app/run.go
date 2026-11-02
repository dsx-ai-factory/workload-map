// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package app

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/metric"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/adapters/otel"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/controller"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

// Components supplies the runtime, publication store and metric registration.
type Components struct {
	Runtime     controller.Runner
	Store       store.PublicationStore
	Collector   otel.Collector
	Definitions DefinitionRunner
}

// DefinitionRunner owns the definition watch.
type DefinitionRunner interface {
	Run(context.Context) error
	WaitLoaded(context.Context) error
}

var (
	ErrIncompleteControllerComponents = errors.New("incomplete controller components")
	ErrDefinitionInventoryStopped     = errors.New("definition inventory stopped before cancellation")
)

// Run registers collection before starting workers, then unregisters on shutdown.
func Run(ctx context.Context, components Components, meter metric.Meter) (err error) {
	if components.Runtime == nil || components.Store == nil || components.Collector == nil || meter == nil {
		return errors.New("incomplete exporter components")
	}
	registration, err := components.Collector.Register(meter, components.Store)
	if err != nil {
		return fmt.Errorf("register collector: %w", err)
	}
	if registration == nil {
		return errors.New("collector returned no registration")
	}
	defer func() { err = errors.Join(err, registration.Unregister()) }()
	if components.Definitions != nil {
		return RunController(ctx, components)
	}
	return components.Runtime.Run(ctx)
}

// RunController runs the inventory and workers and joins both on shutdown.
// It maintains publication records without registering a metrics collector.
func RunController(ctx context.Context, components Components) error {
	if components.Runtime == nil || components.Store == nil || components.Definitions == nil {
		return ErrIncompleteControllerComponents
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	inventoryDone := make(chan error, 1)
	go func() {
		err := components.Definitions.Run(runCtx)
		if err == nil && runCtx.Err() == nil {
			err = ErrDefinitionInventoryStopped
		}
		inventoryDone <- err
		cancel()
	}()
	err := components.Definitions.WaitLoaded(runCtx)
	if err == nil {
		err = components.Runtime.Run(runCtx)
	} else if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		err = nil
	}
	cancel()
	if inventoryErr := <-inventoryDone; inventoryErr != nil {
		err = errors.Join(err, fmt.Errorf("karta definition inventory: %w", inventoryErr))
	}
	return err
}
