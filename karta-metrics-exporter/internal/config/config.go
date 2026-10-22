// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package config loads the exporter configuration from a YAML file, flags and
// environment variables.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/urfave/cli/v3"
	"go.yaml.in/yaml/v3"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/submitterextractor"
)

const (
	flagConfig                  = "config"
	flagSubmittedByExpression   = "submitted-by-expression"
	flagCostLimit               = "submitter-cost-limit"
	flagMaxExpressionBytes      = "submitter-max-expression-bytes"
	flagMaxIdentityBytes        = "submitter-max-identity-bytes"
	flagInterruptCheckFrequency = "submitter-interrupt-check-frequency"
)

// errZeroInterruptCheckFrequency rejects a setting that would stop CEL from
// checking for cancellation during evaluation.
var errZeroInterruptCheckFrequency = errors.New("attribution.limits.interruptCheckFrequency must be greater than 0")

var errMultipleDocuments = errors.New("must contain a single YAML document")

// Config is the exporter configuration. It is read from the YAML file named by
// --config. A flag, or its environment variable, overrides the file when set.
type Config struct {
	Attribution AttributionConfig `yaml:"attribution" json:"attribution"`
}

type AttributionConfig struct {
	// SubmittedByExpression is the CEL expression returning the submitter. Empty disables extraction.
	SubmittedByExpression string            `yaml:"submittedByExpression" json:"submittedByExpression"`
	Limits                AttributionLimits `yaml:"limits" json:"limits"`
}

// AttributionLimits bounds submitter extraction. A zero value selects the
// default, except InterruptCheckFrequency, which must be greater than 0.
type AttributionLimits struct {
	CostLimit               uint64 `yaml:"costLimit" json:"costLimit"`
	MaxExpressionBytes      int    `yaml:"maxExpressionBytes" json:"maxExpressionBytes"`
	MaxIdentityBytes        int    `yaml:"maxIdentityBytes" json:"maxIdentityBytes"`
	InterruptCheckFrequency uint   `yaml:"interruptCheckFrequency" json:"interruptCheckFrequency"`
}

var defaultAttributionLimits = AttributionLimits{
	CostLimit:               submitterextractor.DefaultCostLimit,
	MaxExpressionBytes:      submitterextractor.DefaultMaxExpressionBytes,
	MaxIdentityBytes:        submitterextractor.DefaultMaxIdentityBytes,
	InterruptCheckFrequency: submitterextractor.DefaultInterruptCheckFrequency,
}

// Flags returns the configuration flags. Each one can also be set from
// its KARTA_EXPORTER_* environment variable; the command line wins.
func Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    flagConfig,
			Usage:   "Path to the exporter YAML config file.",
			Sources: cli.EnvVars("KARTA_EXPORTER_CONFIG"),
		},
		&cli.StringFlag{
			Name:    flagSubmittedByExpression,
			Usage:   "CEL expression returning the submitter identity. Overrides attribution.submittedByExpression.",
			Sources: cli.EnvVars("KARTA_EXPORTER_SUBMITTED_BY_EXPRESSION"),
		},
		&cli.Uint64Flag{
			Name:    flagCostLimit,
			Value:   defaultAttributionLimits.CostLimit,
			Usage:   "CEL cost units one submitter evaluation may spend. Overrides attribution.limits.costLimit.",
			Sources: cli.EnvVars("KARTA_EXPORTER_SUBMITTER_COST_LIMIT"),
		},
		&cli.IntFlag{
			Name:    flagMaxExpressionBytes,
			Value:   defaultAttributionLimits.MaxExpressionBytes,
			Usage:   "Maximum length of the submitter expression. Overrides attribution.limits.maxExpressionBytes.",
			Sources: cli.EnvVars("KARTA_EXPORTER_SUBMITTER_MAX_EXPRESSION_BYTES"),
		},
		&cli.IntFlag{
			Name:    flagMaxIdentityBytes,
			Value:   defaultAttributionLimits.MaxIdentityBytes,
			Usage:   "Maximum length of an extracted submitter identity. Overrides attribution.limits.maxIdentityBytes.",
			Sources: cli.EnvVars("KARTA_EXPORTER_SUBMITTER_MAX_IDENTITY_BYTES"),
		},
		&cli.UintFlag{
			Name:    flagInterruptCheckFrequency,
			Value:   defaultAttributionLimits.InterruptCheckFrequency,
			Usage:   "Comprehension iterations between cancellation checks; must be greater than 0. Overrides attribution.limits.interruptCheckFrequency.",
			Sources: cli.EnvVars("KARTA_EXPORTER_SUBMITTER_INTERRUPT_CHECK_FREQUENCY"),
		},
	}
}

// Load reads the config file, when set, and applies the flags and
// environment variables set on cmd. Unknown keys in the file are an error.
func Load(cmd *cli.Command) (Config, error) {
	cfg := Config{Attribution: AttributionConfig{Limits: defaultAttributionLimits}}

	if path := cmd.String(flagConfig); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("config file %s: %w", path, err)
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		// An empty file decodes to io.EOF and keeps the defaults.
		switch err := dec.Decode(&cfg); {
		case errors.Is(err, io.EOF):
		case err != nil:
			return Config{}, fmt.Errorf("config file %s: %w", path, err)
		default:
			// A trailing "---" yields an empty document, which is not a second config.
			for {
				var extra any
				err := dec.Decode(&extra)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil || extra != nil {
					return Config{}, fmt.Errorf("config file %s: %w", path, errMultipleDocuments)
				}
			}
		}
	}

	// An empty value counts as unset, so an empty environment variable does not
	// clear the expression from the file.
	if expression := cmd.String(flagSubmittedByExpression); expression != "" {
		cfg.Attribution.SubmittedByExpression = expression
	}
	if cmd.IsSet(flagCostLimit) {
		cfg.Attribution.Limits.CostLimit = cmd.Uint64(flagCostLimit)
	}
	if cmd.IsSet(flagMaxExpressionBytes) {
		cfg.Attribution.Limits.MaxExpressionBytes = cmd.Int(flagMaxExpressionBytes)
	}
	if cmd.IsSet(flagMaxIdentityBytes) {
		cfg.Attribution.Limits.MaxIdentityBytes = cmd.Int(flagMaxIdentityBytes)
	}
	if cmd.IsSet(flagInterruptCheckFrequency) {
		cfg.Attribution.Limits.InterruptCheckFrequency = cmd.Uint(flagInterruptCheckFrequency)
	}

	if cfg.Attribution.Limits.InterruptCheckFrequency == 0 {
		return Config{}, errZeroInterruptCheckFrequency
	}
	return cfg, nil
}
