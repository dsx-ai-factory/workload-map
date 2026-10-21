// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Command karta-metrics-exporter starts the exporter scaffold.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/submitterextractor"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/version"
)

func main() {
	printVersion := flag.Bool("version", false, "Print the exporter version and exit.")
	configPath := flag.String("config", "", "Path to the exporter config file. KARTA_* environment variables override it.")
	flag.Parse()
	if flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	if *printVersion {
		fmt.Println(version.String())
		return
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		slog.Error("Invalid exporter configuration", "error", err)
		os.Exit(1)
	}
	attribution := cfg.Attribution
	if _, err := submitterextractor.New(submitterextractor.Config{
		Expression:              attribution.SubmittedByExpression,
		CostLimit:               attribution.Limits.CostLimit,
		MaxExpressionBytes:      attribution.Limits.MaxExpressionBytes,
		MaxIdentityBytes:        attribution.Limits.MaxIdentityBytes,
		InterruptCheckFrequency: attribution.Limits.InterruptCheckFrequency,
	}); err != nil {
		slog.Error("Invalid submitter configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("Karta metrics exporter scaffold started; metrics collection is not implemented",
		"version", version.String(), "submitterExtraction", cfg.Attribution.SubmittedByExpression != "")
	<-ctx.Done()
	slog.Info("Karta metrics exporter stopped")
}
