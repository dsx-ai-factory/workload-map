// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Command gen-samples regenerates the built-in Karta YAML catalog under
// docs/catalog/ from the typed Go definitions in pkg/catalog. It is wired into
// `make generate-samples` and the `validate` chain so drift between the Go
// definitions and the committed YAML fails CI via `git diff --exit-code`.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dsx-ai-factory/workload-map/pkg/catalog"
)

// outputDir is the catalog directory relative to the repository root, which is
// the working directory when run via `go run ./hack/gen-samples`.
var outputDir = filepath.Join("docs", "catalog")

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", outputDir, err)
	}

	// Clear existing catalog files first so removed definitions do not linger.
	existing, err := filepath.Glob(filepath.Join(outputDir, "*.yaml"))
	if err != nil {
		return fmt.Errorf("list %s: %w", outputDir, err)
	}
	for _, path := range existing {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}

	for _, k := range catalog.List() {
		slug, err := catalog.Slug(k)
		if err != nil {
			return fmt.Errorf("slug for %q: %w", k.Name, err)
		}
		data, err := catalog.MarshalYAML(k)
		if err != nil {
			return err
		}
		path := filepath.Join(outputDir, slug+".yaml")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}
