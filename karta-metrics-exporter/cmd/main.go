// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Command karta-metrics-exporter starts the exporter scaffold.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/internal/config"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/submitterextractor"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/version"
)

const flagVersion = "version"

func main() {
	cmd := &cli.Command{
		Name:  "karta-metrics-exporter",
		Usage: "Export Karta workload metrics.",
		Flags: append([]cli.Flag{
			&cli.BoolFlag{Name: flagVersion, Usage: "Print the exporter version and exit."},
		}, config.Flags()...),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Bool(flagVersion) {
				fmt.Println(version.String())
				return nil
			}
			if cmd.NArg() != 0 {
				return fmt.Errorf("unexpected arguments: %v", cmd.Args().Slice())
			}

			cfg, err := config.Load(cmd)
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

			slog.Info("Karta metrics exporter scaffold started; metrics collection is not implemented",
				"version", version.String(), "submitterExtraction", submitterextractor.IsEnabled(extractor))
			<-ctx.Done()
			slog.Info("Karta metrics exporter stopped")
			return nil
		},
	}

	if err := run(cmd); err != nil {
		slog.Error("Karta metrics exporter failed", "error", err)
		os.Exit(1)
	}
}

// run keeps the signal handler's deferred cleanup out of main, since os.Exit skips defers.
func run(cmd *cli.Command) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return cmd.Run(ctx, os.Args)
}
