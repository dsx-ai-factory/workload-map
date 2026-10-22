// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Command karta-metrics-exporter starts the exporter scaffold.
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

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/internal/config"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/submitterextractor"
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
	loader := config.NewLoader(fs, os.Getenv)
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
	attribution := cfg.Attribution
	extractor, err := submitterextractor.New(submitterextractor.Config{
		Expression:              attribution.SubmittedByExpression,
		CostLimit:               attribution.Limits.CostLimit,
		MaxExpressionBytes:      attribution.Limits.MaxExpressionBytes,
		MaxIdentityBytes:        attribution.Limits.MaxIdentityBytes,
		InterruptCheckFrequency: attribution.Limits.InterruptCheckFrequency,
	})
	if err != nil {
		return fmt.Errorf("invalid submitter configuration: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("Karta metrics exporter scaffold started; metrics collection is not implemented",
		"version", version.String(), "submitterExtraction", submitterextractor.IsEnabled(extractor))
	<-ctx.Done()
	slog.Info("Karta metrics exporter stopped")
	return nil
}
