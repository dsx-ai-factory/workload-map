// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type executor struct {
	dir            string
	env            []string
	stdout, stderr io.Writer
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

func readJSONFile(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
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

func copyExecutable(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o755)
}

func removeCheckDirectory(path string) error {
	if err := filepath.WalkDir(path, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o755)
		}
		return nil
	}); err != nil {
		return err
	}
	return os.RemoveAll(path)
}
