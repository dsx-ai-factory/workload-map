// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package bootstrap_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/rest"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/bootstrap"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/config"
)

var _ = Describe("exporter construction", func() {
	It("constructs real components without starting API requests or mutating client settings", func() {
		cfg := config.DefaultConfig()
		client := &rest.Config{Host: "https://example.test", QPS: 3, Burst: 5}
		components, err := bootstrap.New(cfg, client)
		Expect(err).NotTo(HaveOccurred())
		Expect(components.Runtime).NotTo(BeNil())
		Expect(components.Definitions).NotTo(BeNil())
		Expect(components.Store).NotTo(BeNil())
		Expect(components.Runtime.Ready()).To(BeFalse())
		Expect(client.QPS).To(Equal(float32(3)))
		Expect(client.Burst).To(Equal(5))
		Expect(client.RateLimiter).To(BeNil())
	})

	It("rejects missing client configuration and invalid runtime settings", func() {
		cfg := config.DefaultConfig()
		_, err := bootstrap.New(cfg, nil)
		Expect(err).To(MatchError("kubernetes configuration is required"))
		cfg.RequestTimeout = "-1s"
		_, err = bootstrap.New(cfg, &rest.Config{Host: "https://example.test"})
		Expect(err).To(HaveOccurred())
	})
})
