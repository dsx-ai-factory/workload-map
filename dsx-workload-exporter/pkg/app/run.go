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
func RunController(ctx context.Context, components Components) (err error) {
	if components.Runtime == nil || components.Store == nil || components.Definitions == nil {
		return ErrIncompleteControllerComponents
	}
	runCtx, cancel := context.WithCancel(ctx)
	inventoryDone := make(chan error, 1)
	defer func() {
		cancel()
		if inventoryErr := <-inventoryDone; inventoryErr != nil {
			err = errors.Join(err, fmt.Errorf("karta definition inventory: %w", inventoryErr))
		}
	}()
	go func() {
		err := components.Definitions.Run(runCtx)
		if err == nil && runCtx.Err() == nil {
			err = ErrDefinitionInventoryStopped
		}
		inventoryDone <- err
		cancel()
	}()
	err = components.Definitions.WaitLoaded(runCtx)
	if err != nil {
		if err == ctx.Err() {
			return nil
		}
		return err
	}

	err = components.Runtime.Run(runCtx)
	if err == runCtx.Err() {
		return nil
	}
	return err
}
