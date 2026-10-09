// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Command kli is the Karta CLI: workload-aware visibility for any Kubernetes
// workload type. It builds on the Karta abstraction layer to list and describe
// workloads and the definitions Karta understands.
package main

import (
	"errors"
	"fmt"
	"os"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/dsx-ai-factory/workload-map/cli/cmd"
)

func main() {
	// client-go reports a failed discovery round through klog, duplicating a
	// diagnostic the CLI already surfaces in the middle of machine-readable stderr.
	utilruntime.ErrorHandlers = nil

	err := cmd.NewRootCommand().Execute()
	if err == nil {
		return
	}

	// The root sets SilenceErrors so the message is printed here instead, with
	// the lowercase prefix the rest of the CLI's diagnostics use.
	fmt.Fprintf(os.Stderr, "error: %v\n", err)

	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) {
		os.Exit(1)
	}
	// Silencing Cobra also silenced its usage hint, which is the whole value of
	// distinguishing a usage error.
	if coded.ExitCode() == cmd.ExitUsage {
		path := "kli"
		var located interface{ UsagePath() string }
		if errors.As(err, &located) && located.UsagePath() != "" {
			path = located.UsagePath()
		}
		fmt.Fprintf(os.Stderr, "Run '%s --help' for usage.\n", path)
	}
	os.Exit(coded.ExitCode())
}
