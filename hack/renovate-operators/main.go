// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Command renovate-operators updates operators and adds CI recordings to Renovate draft PRs.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
)

type executor struct {
	dir            string
	env            []string
	stdout, stderr io.Writer
}

var (
	shaPattern    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	groupPattern  = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	authorPattern = regexp.MustCompile(`^([^<>\r\n]+) <([^<>\s]+)>$`)
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

func (e executor) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = e.dir
	cmd.Env = e.env
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func (e executor) output(ctx context.Context, args ...string) ([]byte, error) {
	cmd := e.command(ctx, args...)
	output, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("%s: %s: %w", args[0], bytes.TrimSpace(exit.Stderr), err)
		}
		return nil, fmt.Errorf("%s: %w", args[0], err)
	}
	return output, nil
}

func (e executor) outputJSON(ctx context.Context, value any, args ...string) error {
	output, err := e.output(ctx, args...)
	if err != nil {
		return err
	}
	return json.Unmarshal(output, value)
}

func (e executor) run(ctx context.Context, args ...string) error {
	cmd := e.command(ctx, args...)
	cmd.Stdout, cmd.Stderr = e.stdout, e.stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", args[0], err)
	}
	return nil
}

func (e executor) logged(ctx context.Context, log string, args ...string) error {
	fmt.Fprintf(e.stdout, "==> %s\n", strings.TrimSuffix(filepath.Base(log), ".log"))
	file, err := os.Create(log)
	if err != nil {
		return fmt.Errorf("create command log: %w", err)
	}
	cmd := e.command(ctx, args...)
	cmd.Stdout, cmd.Stderr = file, file
	runErr := cmd.Run()
	closeErr := file.Close()
	if runErr != nil {
		data, err := os.ReadFile(log)
		if err == nil {
			_, _ = e.stderr.Write(data[max(0, len(data)-16000):])
		}
		return fmt.Errorf("%s (see %s): %w", args[0], log, runErr)
	}
	return closeErr
}

func environment(values []string) map[string]string {
	env := make(map[string]string, len(values))
	for _, value := range values {
		key, value, ok := strings.Cut(value, "=")
		if ok {
			env[key] = value
		}
	}
	return env
}

func environmentList(values map[string]string) []string {
	env := make([]string, 0, len(values))
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	slices.Sort(env)
	return env
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func gitOutput(ctx context.Context, root string, args ...string) ([]byte, error) {
	output, err := (executor{dir: root, env: os.Environ()}).output(ctx, append([]string{"git"}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	return output, nil
}
