// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package bootstrap

import (
	"context"
	"errors"

	"github.com/stretchr/testify/mock"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/app"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/controller"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/discovery"
	inventory "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelevaluator"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

var ErrNotImplemented = errors.New("exporter component is not implemented")

// Keep generated mocks checked against their contracts even before implementations exist.
var (
	_ labelevaluator.Compiler  = (*labelevaluator.MockLabelCompiler)(nil)
	_ inventory.Reader         = (*inventory.MockInventory)(nil)
	_ discovery.Discoverer     = (*discovery.MockDiscovery)(nil)
	_ discovery.ObjectReader   = (*discovery.MockObjectReader)(nil)
	_ labelevaluator.Evaluator = (*labelevaluator.MockLabelEvaluator)(nil)
	_ labelresolver.Resolver   = (*labelresolver.MockLabelResolver)(nil)
	_ controller.Runner        = (*controller.MockRuntime)(nil)
)

// New is the temporary composition root. Replace the mock factories here as components land.
// The caller builds the label resolver and runs the definition inventory.
func New(labels labelresolver.Resolver, definitions inventory.Reader) app.Components {
	reader := newObjectReader()
	ownership := newDiscovery(discovery.Dependencies{Reader: reader, Inventory: definitions})
	publications := store.New()
	runtime := newRuntime(controller.Dependencies{
		Discovery:    ownership,
		Inventory:    definitions,
		Labels:       labels,
		Publications: publications,
	})
	return app.Components{Runtime: runtime, Store: publications}
}

func newObjectReader() discovery.ObjectReader {
	m := &discovery.MockObjectReader{}
	m.EXPECT().Cached(mock.Anything).Return(nil, false)
	m.EXPECT().GetMetadata(mock.Anything, mock.Anything).Return(nil, ErrNotImplemented)
	return m
}

func newDiscovery(_ discovery.Dependencies) discovery.Discoverer {
	m := &discovery.MockDiscovery{}
	m.EXPECT().Get(mock.Anything, mock.Anything).Return(discovery.Result{}, ErrNotImplemented)
	return m
}

func newRuntime(_ controller.Dependencies) controller.Runner {
	m := &controller.MockRuntime{}
	m.EXPECT().Run(mock.Anything).RunAndReturn(func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})
	return m
}
