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
	Runtime   controller.Runner
	Store     store.PublicationStore
	Collector otel.Collector
}

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
	return components.Runtime.Run(ctx)
}
