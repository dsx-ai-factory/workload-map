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

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/app"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/bootstrap"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/config"
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
	components, err := bootstrap.New(cfg.Config)
	if err != nil {
		return fmt.Errorf("invalid exporter configuration: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("Karta metrics exporter started; metrics collection is not implemented",
		"version", version.String(), "labelRules", len(cfg.Rules))
	if err := app.Run(ctx, components, noop.NewMeterProvider().Meter("karta-metrics-exporter")); err != nil {
		return err
	}

	slog.Info("Karta metrics exporter stopped")
	return nil
}
