// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/stretchr/testify/mock"
	"go.opentelemetry.io/otel/metric"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/adapters/otel"
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
	_ otel.Collector           = (*otel.MockCollector)(nil)
	_ inventory.Reader         = (*inventory.MockInventory)(nil)
	_ discovery.Discoverer     = (*discovery.MockDiscovery)(nil)
	_ discovery.ObjectReader   = (*discovery.MockObjectReader)(nil)
	_ labelevaluator.Evaluator = (*labelevaluator.MockLabelEvaluator)(nil)
	_ labelresolver.Resolver   = (*labelresolver.MockLabelResolver)(nil)
	_ store.PublicationStore   = (*store.MockStore)(nil)
	_ controller.Runner        = (*controller.MockRuntime)(nil)
)

// New is the temporary composition root. Replace the factories here as components land.
// It returns an error when a label rule is invalid or fails to compile.
func New(labelConfig labelresolver.Config) (app.Components, error) {
	definitions := newInventory(inventory.Config{})
	reader := newObjectReader()
	ownership := newDiscovery(discovery.Dependencies{Reader: reader, Inventory: definitions})
	labels, err := labelresolver.New(labelConfig, labelresolver.Dependencies{Compiler: labelevaluator.NewCompiler()})
	if err != nil {
		return app.Components{}, fmt.Errorf("label resolver: %w", err)
	}
	publications := newStore(store.Options{})
	runtime := newRuntime(controller.Dependencies{
		Discovery:    ownership,
		Inventory:    definitions,
		Labels:       labels,
		Publications: publications,
	})
	return app.Components{Runtime: runtime, Store: publications, Collector: newCollector()}, nil
}

func newInventory(_ inventory.Config) inventory.Reader {
	m := &inventory.MockInventory{}
	m.EXPECT().Lookup(mock.Anything).Return(inventory.Selection{}, ErrNotImplemented)
	return m
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

func newStore(_ store.Options) store.PublicationStore {
	m := &store.MockStore{}
	m.EXPECT().Capture().Return(store.CapturedView{})
	// Writes are deliberately unconfigured: a real worker must use a real Store.
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

func newCollector() otel.Collector {
	m := &otel.MockCollector{}
	m.EXPECT().Register(mock.Anything, mock.Anything).RunAndReturn(
		func(meter metric.Meter, _ otel.Capturer) (metric.Registration, error) {
			return meter.RegisterCallback(func(context.Context, metric.Observer) error { return nil })
		})
	return m
}
