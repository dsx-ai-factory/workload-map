// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"go.yaml.in/yaml/v3"
	"k8s.io/utils/ptr"

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
// --config, and flags given explicitly on the command line override it.
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

// configFlags holds the configuration flags until the file is loaded.
type configFlags struct {
	path                    *string
	submittedByExpression   *string
	costLimit               *uint64
	maxExpressionBytes      *int
	maxIdentityBytes        *int
	interruptCheckFrequency *uint
}

func bindConfigFlags(fs *flag.FlagSet) *configFlags {
	return &configFlags{
		path: fs.String(flagConfig, "", "Path to the exporter YAML config file."),
		submittedByExpression: fs.String(flagSubmittedByExpression, "",
			"CEL expression returning the submitter identity. Overrides attribution.submittedByExpression."),
		costLimit: fs.Uint64(flagCostLimit, defaultAttributionLimits.CostLimit,
			"CEL cost units one submitter evaluation may spend. Overrides attribution.limits.costLimit."),
		maxExpressionBytes: fs.Int(flagMaxExpressionBytes, defaultAttributionLimits.MaxExpressionBytes,
			"Maximum length of the submitter expression. Overrides attribution.limits.maxExpressionBytes."),
		maxIdentityBytes: fs.Int(flagMaxIdentityBytes, defaultAttributionLimits.MaxIdentityBytes,
			"Maximum length of an extracted submitter identity. Overrides attribution.limits.maxIdentityBytes."),
		interruptCheckFrequency: fs.Uint(flagInterruptCheckFrequency, defaultAttributionLimits.InterruptCheckFrequency,
			"Comprehension iterations between cancellation checks; must be greater than 0. Overrides attribution.limits.interruptCheckFrequency."),
	}
}

// load reads the config file, when set, and applies the flags set on fs, which
// must already be parsed. Unknown keys in the file are an error.
func (f *configFlags) load(fs *flag.FlagSet) (*Config, error) {
	cfg := Config{Attribution: AttributionConfig{Limits: defaultAttributionLimits}}

	if path := ptr.Deref(f.path, ""); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("config file %s: %w", path, err)
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		// An empty file decodes to io.EOF and keeps the defaults.
		switch err := dec.Decode(&cfg); {
		case errors.Is(err, io.EOF):
		case err != nil:
			return nil, fmt.Errorf("config file %s: %w", path, err)
		default:
			var extra any
			if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("config file %s: %w", path, errMultipleDocuments)
			}
		}
	}

	limits := &cfg.Attribution.Limits
	fs.Visit(func(fl *flag.Flag) {
		switch fl.Name {
		case flagSubmittedByExpression:
			cfg.Attribution.SubmittedByExpression = ptr.Deref(f.submittedByExpression, "")
		case flagCostLimit:
			limits.CostLimit = ptr.Deref(f.costLimit, 0)
		case flagMaxExpressionBytes:
			limits.MaxExpressionBytes = ptr.Deref(f.maxExpressionBytes, 0)
		case flagMaxIdentityBytes:
			limits.MaxIdentityBytes = ptr.Deref(f.maxIdentityBytes, 0)
		case flagInterruptCheckFrequency:
			limits.InterruptCheckFrequency = ptr.Deref(f.interruptCheckFrequency, 0)
		}
	})

	if limits.InterruptCheckFrequency == 0 {
		return nil, errZeroInterruptCheckFrequency
	}
	return &cfg, nil
}
