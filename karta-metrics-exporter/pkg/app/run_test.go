// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package app_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/adapters/otel"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/app"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/controller"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

type registration struct {
	metric.Registration
	calls int
	err   error
}

func (r *registration) Unregister() error {
	r.calls++
	return r.err
}

var _ = Describe("App lifecycle", func() {
	runtimeFailure := errors.New("runtime failed")
	cleanupFailure := errors.New("unregister failed")
	for _, tc := range []struct {
		name              string
		registrationError error
		runtimeError      error
		cleanupError      error
		nilRegistration   bool
	}{
		{name: "success"},
		{name: "registration failure", registrationError: errors.New("registration failed")},
		{name: "runtime failure", runtimeError: runtimeFailure},
		{name: "cleanup failure", cleanupError: cleanupFailure},
		{name: "both failures", runtimeError: runtimeFailure, cleanupError: cleanupFailure},
		{name: "missing registration", nilRegistration: true},
	} {
		It(tc.name, func(ctx SpecContext) {
			publications := store.NewMockStore(GinkgoT())
			runtime := controller.NewMockRuntime(GinkgoT())

			reg := &registration{err: tc.cleanupError}
			registered := false
			if tc.registrationError == nil && !tc.nilRegistration {
				runtime.EXPECT().Run(ctx).Run(func(context.Context) {
					Expect(registered).To(BeTrue())
				}).Return(tc.runtimeError).Once()
			}
			collector := otel.NewMockCollector(GinkgoT())
			collector.EXPECT().Register(mock.Anything, publications).RunAndReturn(
				func(_ metric.Meter, source otel.Capturer) (metric.Registration, error) {
					Expect(source).To(BeIdenticalTo(publications))
					registered = true
					if tc.registrationError != nil || tc.nilRegistration {
						return nil, tc.registrationError
					}
					return reg, nil
				}).Once()
			components := app.Components{Runtime: runtime, Store: publications, Collector: collector}

			err := app.Run(ctx, components, noop.NewMeterProvider().Meter("test"))
			switch {
			case tc.registrationError != nil:
				Expect(err).To(MatchError(tc.registrationError))
				Expect(reg.calls).To(BeZero())
			case tc.nilRegistration:
				Expect(err).To(MatchError(ContainSubstring("no registration")))
				Expect(reg.calls).To(BeZero())
			default:
				Expect(reg.calls).To(Equal(1))
				if tc.runtimeError != nil {
					Expect(err).To(MatchError(tc.runtimeError))
				}
				if tc.cleanupError != nil {
					Expect(err).To(MatchError(tc.cleanupError))
				}
				if tc.runtimeError == nil && tc.cleanupError == nil {
					Expect(err).To(Succeed())
				}
			}
		})
	}
})

var _ = Describe("run rejects incomplete components", func() {
	It("rejects missing dependencies before registering collection", func(specCtx SpecContext) {
		Expect(app.Run(specCtx, app.Components{}, noop.NewMeterProvider().Meter("test"))).To(MatchError(ContainSubstring("incomplete")))
	})
})
