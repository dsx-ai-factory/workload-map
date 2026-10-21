// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/submitterextractor"
)

const multiLineExpression = `("karta.run.ai/submitted-by" in annotations)
  ? annotations["karta.run.ai/submitted-by"]
  : ""
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := loadConfig("")
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	want := AttributionConfig{Limits: AttributionLimits{
		CostLimit:               submitterextractor.DefaultCostLimit,
		MaxExpressionBytes:      submitterextractor.DefaultMaxExpressionBytes,
		MaxIdentityBytes:        submitterextractor.DefaultMaxIdentityBytes,
		InterruptCheckFrequency: submitterextractor.DefaultInterruptCheckFrequency,
	}}
	if cfg.Attribution != want {
		t.Errorf("Attribution = %+v, want %+v", cfg.Attribution, want)
	}
}

func TestLoadConfigFile(t *testing.T) {
	path := writeConfig(t, `attribution:
  submittedByExpression: |
    ("karta.run.ai/submitted-by" in annotations)
      ? annotations["karta.run.ai/submitted-by"]
      : ""
  limits:
    costLimit: 2000000
    maxIdentityBytes: 64
    interruptCheckFrequency: 10
`)

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if got := cfg.Attribution.SubmittedByExpression; got != multiLineExpression {
		t.Errorf("SubmittedByExpression = %q, want %q", got, multiLineExpression)
	}
	want := AttributionLimits{
		CostLimit:               2000000,
		MaxExpressionBytes:      submitterextractor.DefaultMaxExpressionBytes,
		MaxIdentityBytes:        64,
		InterruptCheckFrequency: 10,
	}
	if cfg.Attribution.Limits != want {
		t.Errorf("Limits = %+v, want %+v", cfg.Attribution.Limits, want)
	}
}

func TestLoadConfigEnvOverridesFile(t *testing.T) {
	path := writeConfig(t, `attribution:
  submittedByExpression: '"from-file"'
  limits:
    costLimit: 5
`)
	t.Setenv("KARTA_ATTRIBUTION_SUBMITTEDBYEXPRESSION", `"from-env"`)
	t.Setenv("KARTA_ATTRIBUTION_LIMITS_COSTLIMIT", "7")

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if got := cfg.Attribution.SubmittedByExpression; got != `"from-env"` {
		t.Errorf("SubmittedByExpression = %q, want env value", got)
	}
	if got := cfg.Attribution.Limits.CostLimit; got != 7 {
		t.Errorf("CostLimit = %d, want 7", got)
	}
}

func TestLoadConfigZeroInterruptCheckFrequency(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		path := writeConfig(t, "attribution:\n  limits:\n    interruptCheckFrequency: 0\n")
		if _, err := loadConfig(path); !errors.Is(err, errZeroInterruptCheckFrequency) {
			t.Errorf("loadConfig() error = %v, want errZeroInterruptCheckFrequency", err)
		}
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("KARTA_ATTRIBUTION_LIMITS_INTERRUPTCHECKFREQUENCY", "0")
		if _, err := loadConfig(""); !errors.Is(err, errZeroInterruptCheckFrequency) {
			t.Errorf("loadConfig() error = %v, want errZeroInterruptCheckFrequency", err)
		}
	})

	t.Run("negative", func(t *testing.T) {
		t.Setenv("KARTA_ATTRIBUTION_LIMITS_INTERRUPTCHECKFREQUENCY", "-1")
		if _, err := loadConfig(""); err == nil {
			t.Error("loadConfig() error = nil, want error")
		}
	})
}

func TestLoadConfigErrors(t *testing.T) {
	tests := []struct {
		name string
		path func(t *testing.T) string
	}{
		{name: "missing file", path: func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent.yaml") }},
		{name: "unknown key", path: func(t *testing.T) string {
			return writeConfig(t, "attribution:\n  submittedBy: x\n")
		}},
		{name: "wrong type", path: func(t *testing.T) string {
			return writeConfig(t, "attribution:\n  limits:\n    costLimit: lots\n")
		}},
		{name: "malformed yaml", path: func(t *testing.T) string { return writeConfig(t, "attribution: [\n") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := loadConfig(tt.path(t)); err == nil {
				t.Error("loadConfig() error = nil, want error")
			}
		})
	}
}
