// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package bootstrap_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/app"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/bootstrap"
	inventory "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelresolver"
)

var _ = Describe("bootstrap stops on cancellation", func() {
	It("stops on cancellation without publishing data", func(specCtx SpecContext) {
		ctx, cancel := context.WithCancel(specCtx)
		defer cancel()
		components := bootstrap.New(labelresolver.Config{}, &inventory.MockInventory{})
		done := make(chan error, 1)
		go func() { done <- app.Run(ctx, components, noop.NewMeterProvider().Meter("test")) }()
		cancel()
		select {
		case err := <-done:
			Expect(err).To(Succeed())
		case <-time.After(5 * time.Second):
			Fail("mock runtime did not stop")
		}
		Expect(components.Store.Capture().Records).To(BeEmpty())
	})
})
