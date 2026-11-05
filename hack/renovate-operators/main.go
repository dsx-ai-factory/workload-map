// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Command renovate-operators updates operators and adds CI recordings to Renovate draft PRs.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if os.Getenv("E2E_UPDATE_CAPTURE") == "true" {
		return json.NewEncoder(stdout).Encode(args)
	}
	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	if len(args) == 0 {
		return errors.New("usage: renovate-operators matrix|config|run")
	}
	command := args[0]
	args = args[1:]
	if command == "run" {
		if len(args) != 0 {
			return fmt.Errorf("%s takes configuration from the CI environment", command)
		}
		opts, err := optionsFromEnvironment(ctx)
		if err != nil {
			return err
		}
		exec := executor{dir: opts.Root, env: os.Environ(), stdout: stdout, stderr: stderr}
		return runUpdates(ctx, opts, binary, exec)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	group := flags.String("operator", os.Getenv("E2E_UPDATE_GROUP"), "operator group or all for matrix")
	root := flags.String("repo", ".", "repository root")
	base := flags.String("base-branch", os.Getenv("E2E_UPDATE_BASE_BRANCH"), "base branch")
	output := flags.String("output", "", "write JSON to this file instead of stdout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if command == "matrix" {
		if *group == "" {
			*group = "all"
		}
		groups, err := operatorGroups(*group)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(groups)
	}
	if command != "config" {
		return fmt.Errorf("unknown command %q", command)
	}
	if *base == "" {
		*base = "main"
	}
	plan, err := makePlan(ctx, planOptions{Root: *root, Binary: binary, Group: *group, BaseBranch: *base})
	if err != nil {
		return err
	}
	env := environment(os.Environ())
	env["E2E_UPDATE_BASE_BRANCH"] = *base
	value, err := renovateConfig(plan, env)
	if err != nil {
		return err
	}
	if *output != "" {
		return writeJSONFile(*output, value)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
