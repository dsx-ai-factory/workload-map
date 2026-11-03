// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Command dsx-workload-exporter starts the exporter.
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

	"k8s.io/client-go/tools/clientcmd"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/app"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/bootstrap"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/config"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelevaluator"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/version"
)

func main() {
	if err := run(); err != nil {
		slog.Error("DSX workload exporter failed", "error", err)
		os.Exit(1)
	}
}

// run keeps the signal handler's deferred cleanup out of main, since os.Exit skips defers.
func run() error {
	fs := flag.NewFlagSet("dsx-workload-exporter", flag.ContinueOnError)
	address := fs.String("metrics-bind-address", ":8080", "Listen address for metrics and health probes.")
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
	_, err = labelresolver.New(cfg.Config, labelresolver.Dependencies{Compiler: labelevaluator.NewCompiler()})
	if err != nil {
		return fmt.Errorf("invalid exporter configuration: %w", err)
	}

	// Falls back to the in-cluster service account when no kubeconfig is found.
	restConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return fmt.Errorf("kubernetes client configuration: %w", err)
	}
	components, err := bootstrap.New(cfg, restConfig)
	if err != nil {
		return fmt.Errorf("construct exporter: %w", err)
	}
	application, err := app.New(components)
	if err != nil {
		return fmt.Errorf("construct exporter application: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Once shutdown begins, a second signal ends the process.
	context.AfterFunc(ctx, stop)
	if ctx.Err() == nil {
		slog.Info("DSX workload exporter starting",
			"version", version.String(), "address", *address, "labelRules", len(cfg.Rules),
			"catalogDefinitions", cfg.KartaDefinitionSource.Catalog.Enabled, "clusterDefinitions", cfg.KartaDefinitionSource.Cluster.Enabled)
	}
	err = application.Serve(ctx, *address)
	if err != nil {
		return err
	}
	slog.Info("DSX workload exporter stopped")
	return nil
}
