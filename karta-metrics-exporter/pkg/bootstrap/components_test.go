// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package bootstrap_test

import (
	"context"
	"net"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/app"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/bootstrap"
	inventory "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelresolver"
)

var _ = Describe("bootstrap stops on cancellation", func() {
	It("stops on cancellation without publishing data", func(specCtx SpecContext) {
		ctx, cancel := context.WithCancel(specCtx)
		defer cancel()
		components := bootstrap.New(&labelresolver.MockLabelResolver{}, &inventory.MockInventory{})
		application, err := app.New(components)
		Expect(err).NotTo(HaveOccurred())
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		done := make(chan error, 1)
		go func() { done <- application.ServeListener(ctx, listener) }()
		client := &http.Client{Timeout: time.Second}
		response, err := client.Get("http://" + listener.Addr().String() + "/readyz")
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusServiceUnavailable))
		Expect(response.Body.Close()).To(Succeed())
		cancel()
		select {
		case err := <-done:
			Expect(err).To(Succeed())
		case <-time.After(5 * time.Second):
			Fail("exporter application did not stop")
		}
		view := components.Store.Capture()
		Expect(view.Pods).To(BeEmpty())
		Expect(view.Workloads).To(BeEmpty())
	})
})
