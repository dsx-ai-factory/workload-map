// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package otel

import (
	"go.opentelemetry.io/otel/metric"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

// Capturer supplies one immutable publication snapshot per collection.
type Capturer interface {
	Capture() store.CapturedView
}

// Collector registers a collection callback and returns its registration.
// After the runtime stops, the caller must call Unregister on that registration
// to remove the callback from the meter.
type Collector interface {
	Register(metric.Meter, Capturer) (metric.Registration, error)
}
