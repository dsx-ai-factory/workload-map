// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package app_test

import (
	"context"
	"errors"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/app"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/controller"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
)

type definitionRunner struct {
	run        func(context.Context) error
	waitLoaded func(context.Context) error
}

func (d definitionRunner) Run(ctx context.Context) error {
	return d.run(ctx)
}

func (d definitionRunner) WaitLoaded(ctx context.Context) error {
	return d.waitLoaded(ctx)
}

var _ = Describe("Controller startup", func() {
	It("returns a sentinel when controller components are incomplete", func(ctx SpecContext) {
		err := app.RunController(ctx, app.Components{})
		Expect(errors.Is(err, app.ErrIncompleteControllerComponents)).To(BeTrue())
	})

	It("returns a sentinel when the metrics listener is missing", func(ctx SpecContext) {
		definitions := definitionRunner{
			run: func(ctx context.Context) error {
				<-ctx.Done()
				return nil
			},
			waitLoaded: func(ctx context.Context) error { return ctx.Err() },
		}
		application, err := app.New(app.Components{Runtime: controller.NewMockRuntime(GinkgoT()), Store: store.New(), Definitions: definitions})
		Expect(err).NotTo(HaveOccurred())
		err = application.ServeListener(ctx, nil)
		Expect(errors.Is(err, app.ErrMetricsListenerRequired)).To(BeTrue())
	})

	It("waits for the initial definition load before starting runtime workers", func(specCtx SpecContext) {
		ctx, cancel := context.WithCancel(specCtx)
		DeferCleanup(cancel)
		inventoryStarted := make(chan struct{})
		waiting := make(chan struct{})
		loaded := make(chan struct{})
		runtimeStarted := make(chan struct{})
		definitions := definitionRunner{
			run: func(ctx context.Context) error {
				close(inventoryStarted)
				<-ctx.Done()
				return nil
			},
			waitLoaded: func(ctx context.Context) error {
				close(waiting)
				select {
				case <-loaded:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
		}
		runtime := controller.NewMockRuntime(GinkgoT())
		runtime.EXPECT().Run(mock.Anything).RunAndReturn(func(ctx context.Context) error {
			close(runtimeStarted)
			<-ctx.Done()
			return nil
		}).Once()

		application, err := app.New(app.Components{Runtime: runtime, Store: store.New(), Definitions: definitions})
		Expect(err).NotTo(HaveOccurred())
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		result := make(chan error, 1)
		go func() { result <- application.ServeListener(ctx, listener) }()
		Eventually(inventoryStarted).Should(BeClosed())
		Eventually(waiting).Should(BeClosed())
		Consistently(runtimeStarted, "100ms").ShouldNot(BeClosed())
		close(loaded)
		Eventually(runtimeStarted).Should(BeClosed())
		cancel()
		Eventually(result).Should(Receive(Succeed()))
	})

	It("does not start the runtime when loading fails and joins the inventory", func(specCtx SpecContext) {
		loadFailure := errors.New("definition load failed")
		inventoryStopped := make(chan struct{})
		definitions := definitionRunner{
			run: func(ctx context.Context) error {
				<-ctx.Done()
				close(inventoryStopped)
				return nil
			},
			waitLoaded: func(context.Context) error { return loadFailure },
		}
		runtime := controller.NewMockRuntime(GinkgoT())

		result := runControllerAsync(specCtx, runtime, definitions)
		Eventually(result).Should(Receive(MatchError(loadFailure)))
		Expect(inventoryStopped).To(BeClosed())
	})

	It("joins the inventory when canceled while waiting for the initial load", func(specCtx SpecContext) {
		ctx, cancel := context.WithCancel(specCtx)
		DeferCleanup(cancel)
		waiting := make(chan struct{})
		inventoryCanceled := make(chan struct{})
		releaseInventory := make(chan struct{}, 1)
		defer close(releaseInventory)
		definitions := definitionRunner{
			run: func(ctx context.Context) error {
				<-ctx.Done()
				close(inventoryCanceled)
				<-releaseInventory
				return nil
			},
			waitLoaded: func(ctx context.Context) error {
				close(waiting)
				<-ctx.Done()
				return ctx.Err()
			},
		}
		runtime := controller.NewMockRuntime(GinkgoT())

		result := runControllerAsync(ctx, runtime, definitions)
		Eventually(waiting).Should(BeClosed())
		cancel()
		Eventually(inventoryCanceled).Should(BeClosed())
		Consistently(result, "100ms").ShouldNot(Receive())
		releaseInventory <- struct{}{}
		Eventually(result).Should(Receive(Succeed()))
	})

	DescribeTable("preserves independent failures when startup is canceled",
		func(specCtx SpecContext, loadError, inventoryError error, expected string) {
			ctx, cancel := context.WithCancel(specCtx)
			cancel()
			definitions := definitionRunner{
				run:        func(context.Context) error { return inventoryError },
				waitLoaded: func(context.Context) error { return loadError },
			}
			runtime := controller.NewMockRuntime(GinkgoT())

			result := runControllerAsync(ctx, runtime, definitions)
			Eventually(result).Should(Receive(MatchError(expected)))
		},
		Entry("load failure", errors.New("load failed"), nil, "load failed"),
		Entry("load failure with cancellation", errors.Join(context.Canceled, errors.New("load failed")), nil, "context canceled\nload failed"),
		Entry("inventory failure", context.Canceled, errors.New("watch failed"), "karta definition inventory: watch failed"),
	)

	runtimeFailure := errors.New("runtime failed")
	inventoryFailure := errors.New("inventory failed")
	DescribeTable("normalizes runtime cancellation without losing failures",
		func(specCtx SpecContext, runtimeError, inventoryError error, expected []error) {
			ctx, cancel := context.WithCancel(specCtx)
			DeferCleanup(cancel)
			runtimeStarted := make(chan struct{})
			definitions := definitionRunner{
				run: func(ctx context.Context) error {
					<-ctx.Done()
					return inventoryError
				},
				waitLoaded: func(context.Context) error { return nil },
			}
			runtime := controller.NewMockRuntime(GinkgoT())
			runtime.EXPECT().Run(mock.Anything).RunAndReturn(func(ctx context.Context) error {
				close(runtimeStarted)
				<-ctx.Done()
				return runtimeError
			}).Once()

			result := runControllerAsync(ctx, runtime, definitions)
			Eventually(runtimeStarted).Should(BeClosed())
			cancel()
			var err error
			Eventually(result).Should(Receive(&err))
			if len(expected) == 0 {
				Expect(err).NotTo(HaveOccurred())
			}
			for _, failure := range expected {
				Expect(errors.Is(err, failure)).To(BeTrue(), "missing failure %v in %v", failure, err)
			}
		},
		Entry("requested cancellation", context.Canceled, nil, []error{}),
		Entry("runtime failure with cancellation", errors.Join(context.Canceled, runtimeFailure), nil, []error{runtimeFailure}),
		Entry("inventory failure during cancellation", context.Canceled, inventoryFailure, []error{inventoryFailure}),
		Entry("runtime and inventory failures", errors.Join(context.Canceled, runtimeFailure), inventoryFailure, []error{runtimeFailure, inventoryFailure}),
	)

	DescribeTable("propagates an inventory stop before the initial load",
		func(specCtx SpecContext, inventoryError error) {
			waiting := make(chan struct{})
			definitions := definitionRunner{
				run: func(context.Context) error {
					<-waiting
					return inventoryError
				},
				waitLoaded: func(ctx context.Context) error {
					close(waiting)
					<-ctx.Done()
					return ctx.Err()
				},
			}
			runtime := controller.NewMockRuntime(GinkgoT())

			result := runControllerAsync(specCtx, runtime, definitions)
			var err error
			Eventually(result).Should(Receive(&err))
			expected := inventoryError
			if expected == nil {
				expected = app.ErrDefinitionInventoryStopped
			}
			Expect(errors.Is(err, expected)).To(BeTrue())
		},
		Entry("unexpected clean stop", nil),
		Entry("watch failure", errors.New("watch failed")),
	)

	It("joins runtime and inventory shutdown after loading succeeds", func(specCtx SpecContext) {
		ctx, cancel := context.WithCancel(specCtx)
		DeferCleanup(cancel)
		runtimeStarted := make(chan struct{})
		runtimeCanceled := make(chan struct{})
		inventoryCanceled := make(chan struct{})
		releaseRuntime := make(chan struct{}, 1)
		defer close(releaseRuntime)
		releaseInventory := make(chan struct{}, 1)
		defer close(releaseInventory)
		definitions := definitionRunner{
			run: func(ctx context.Context) error {
				<-ctx.Done()
				close(inventoryCanceled)
				<-releaseInventory
				return nil
			},
			waitLoaded: func(context.Context) error { return nil },
		}
		runtime := controller.NewMockRuntime(GinkgoT())
		runtime.EXPECT().Run(mock.Anything).RunAndReturn(func(ctx context.Context) error {
			close(runtimeStarted)
			<-ctx.Done()
			close(runtimeCanceled)
			<-releaseRuntime
			return nil
		}).Once()

		result := runControllerAsync(ctx, runtime, definitions)
		Eventually(runtimeStarted).Should(BeClosed())
		cancel()
		Eventually(runtimeCanceled).Should(BeClosed())
		Eventually(inventoryCanceled).Should(BeClosed())
		Consistently(result, "100ms").ShouldNot(Receive())
		releaseRuntime <- struct{}{}
		Consistently(result, "100ms").ShouldNot(Receive())
		releaseInventory <- struct{}{}
		Eventually(result).Should(Receive(Succeed()))
	})
})

func runControllerAsync(ctx context.Context, runtime controller.Runner, definitions app.DefinitionRunner) <-chan error {
	result := make(chan error, 1)
	go func() {
		defer GinkgoRecover()
		result <- app.RunController(ctx, app.Components{Runtime: runtime, Store: store.New(), Definitions: definitions})
	}()
	return result
}
