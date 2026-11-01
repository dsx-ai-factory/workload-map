// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Command karta-metrics-exporter starts the exporter.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"go.opentelemetry.io/otel/metric/noop"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/app"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/bootstrap"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/config"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelevaluator"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/version"
)

func main() {
	if err := run(); err != nil {
		slog.Error("Karta metrics exporter failed", "error", err)
		os.Exit(1)
	}
}

// run keeps the signal handler's deferred cleanup out of main, since os.Exit skips defers.
func run() error {
	fs := flag.NewFlagSet("karta-metrics-exporter", flag.ContinueOnError)
	showVersion := fs.Bool("version", false, "Print the exporter version and exit.")
	loader := config.NewLoader(fs, os.LookupEnv)
	switch err := fs.Parse(os.Args[1:]); {
	case errors.Is(err, flag.ErrHelp):
		return nil
	case err != nil:
		return err
	}
	if *showVersion {
		fmt.Println(version.String())
		return nil
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}

	cfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("invalid exporter configuration: %w", err)
	}
	labels, err := labelresolver.New(cfg.Config, labelresolver.Dependencies{Compiler: labelevaluator.NewCompiler()})
	if err != nil {
		return fmt.Errorf("invalid exporter configuration: %w", err)
	}

	// Falls back to the in-cluster service account when no kubeconfig is found.
	restConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return fmt.Errorf("kubernetes client configuration: %w", err)
	}
	client, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}
	definitions := cfg.KartaDefinitionSource.InventoryConfig()
	inventory, err := kartadefinitioninventory.New(client, definitions)
	if err != nil {
		return fmt.Errorf("invalid Karta definition sources: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Once shutdown begins, a second signal ends the process.
	context.AfterFunc(ctx, stop)
	inventoryDone := make(chan error, 1)
	go func() { inventoryDone <- inventory.Run(ctx) }()
	if err := inventory.WaitLoaded(ctx); err != nil && ctx.Err() == nil {
		stop()
		return fmt.Errorf("initial Karta definition load: %w", errors.Join(err, <-inventoryDone))
	}
	components := bootstrap.New(labels, inventory)
	if ctx.Err() == nil {
		slog.Info("Karta metrics exporter started; metrics collection is not implemented",
			"version", version.String(), "labelRules", len(cfg.Rules),
			"catalogDefinitions", definitions.Catalog.Enabled, "clusterDefinitions", definitions.Cluster.Enabled)
	}
	err = app.Run(ctx, components, noop.NewMeterProvider().Meter("karta-metrics-exporter"))
	stop()
	if inventoryErr := <-inventoryDone; inventoryErr != nil {
		err = errors.Join(err, fmt.Errorf("karta definition inventory: %w", inventoryErr))
	}
	if err != nil {
		return err
	}
	slog.Info("Karta metrics exporter stopped")
	return nil
}
