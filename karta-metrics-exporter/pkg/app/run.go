// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package app

import (
	"context"
	"errors"
	"fmt"
)

// DefinitionRunner owns the definition watch.
type DefinitionRunner interface {
	Run(context.Context) error
	WaitLoaded(context.Context) error
}

var (
	ErrIncompleteControllerComponents = errors.New("incomplete controller components")
	ErrDefinitionInventoryStopped     = errors.New("definition inventory stopped before cancellation")
)

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
		if err == runCtx.Err() {
			err = nil
		}
	} else if err == ctx.Err() {
		err = nil
	}
	cancel()
	if inventoryErr := <-inventoryDone; inventoryErr != nil {
		err = errors.Join(err, fmt.Errorf("karta definition inventory: %w", inventoryErr))
	}
	return err
}
