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

	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/version"
)

func main() {
	printVersion := flag.Bool("version", false, "Print the exporter version and exit.")
	flag.Parse()
	if flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	if *printVersion {
		fmt.Println(version.String())
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("Karta metrics exporter scaffold started; metrics collection is not implemented", "version", version.String())
	<-ctx.Done()
	slog.Info("Karta metrics exporter stopped")
}
