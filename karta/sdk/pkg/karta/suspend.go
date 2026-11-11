// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package karta

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/dsx-ai-factory/workload-map/karta/sdk/internal/jq/execution"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

func (w *workload) CanSuspend() bool {
	return slices.ContainsFunc(w.def.components, func(c ComponentInfo) bool { return c.Suspendable })
}

func (w *workload) Suspend(ctx context.Context) error { return w.applySuspend(ctx, true) }

func (w *workload) Resume(ctx context.Context) error { return w.applySuspend(ctx, false) }

// applySuspend runs the actions of every suspendable component, root first,
// on a copy, and keeps the copy only when all of them succeed.
func (w *workload) applySuspend(ctx context.Context, suspend bool) error {
	if !w.CanSuspend() {
		return ErrSuspendNotSupported
	}
	accessor := resource.NewAccessor(execution.NewDefaultRunner(w.object))
	for _, info := range w.def.components {
		if !info.Suspendable {
			continue
		}
		apply := accessor.ApplyResumeActions
		if suspend {
			apply = accessor.ApplySuspendActions
		}
		if err := apply(ctx, w.def.byName[info.Name]); err != nil {
			return fmt.Errorf("%w: component %q: %w", ErrEvaluation, info.Name, err)
		}
	}
	mutated, err := accessor.GetObject()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrEvaluation, err)
	}
	// Round-trip through JSON so numbers are float64, like the rest of the object.
	encoded, err := json.Marshal(mutated)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrEvaluation, err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		return fmt.Errorf("%w: %w", ErrEvaluation, err)
	}
	if !reflect.DeepEqual(object, w.object) {
		w.object = object
		w.modified = true
	}
	return nil
}
