// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// This CPU stand-in exercises the NIMCache controller's real PVC and Job lifecycle.
// It writes a marker instead of downloading model weights.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

var errSimulatedFailure = errors.New("simulated model cache failure")

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("create-model-store", flag.ContinueOnError)
	modelRepo := flags.String("model-repo", "", "model identifier written to the marker")
	modelStore := flags.String("model-store", "", "PVC directory for the marker")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse cache arguments: %w", err)
	}
	if *modelRepo == "" || *modelStore == "" || flags.NArg() != 0 {
		return errors.New("model-repo and model-store are required; positional arguments are unsupported")
	}
	if value := os.Getenv("NIM_CACHE_TEST_DELAY"); value != "" {
		delay, err := time.ParseDuration(value)
		if err != nil || delay < 0 {
			return fmt.Errorf("invalid NIM_CACHE_TEST_DELAY %q", value)
		}
		time.Sleep(delay)
	}
	if os.Getenv("NIM_CACHE_TEST_FAIL") == "true" {
		return errSimulatedFailure
	}
	if err := os.MkdirAll(*modelStore, 0o750); err != nil {
		return fmt.Errorf("create model store: %w", err)
	}
	if err := os.WriteFile(filepath.Join(*modelStore, "fictive-model.txt"), []byte(*modelRepo+"\n"), 0o640); err != nil {
		return fmt.Errorf("write model marker: %w", err)
	}
	return nil
}
