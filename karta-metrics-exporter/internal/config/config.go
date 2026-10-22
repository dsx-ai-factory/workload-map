// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package config loads the exporter configuration from a YAML file, flags and
// environment variables.
package config

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

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

// setting is one configuration value with its flag and environment variable.
// apply parses the raw value into the attribution config.
type setting struct {
	flag, env, usage string
	apply            func(*AttributionConfig, string) error
}

var configSetting = setting{flag: flagConfig, env: "KARTA_EXPORTER_CONFIG", usage: "Path to the exporter YAML config file."}

var attributionSettings = []setting{
	{
		flag:  flagSubmittedByExpression,
		env:   "KARTA_EXPORTER_SUBMITTED_BY_EXPRESSION",
		usage: "CEL expression returning the submitter identity. Overrides attribution.submittedByExpression.",
		apply: func(a *AttributionConfig, v string) error {
			a.SubmittedByExpression = v
			return nil
		},
	},
	{
		flag: flagCostLimit,
		env:  "KARTA_EXPORTER_SUBMITTER_COST_LIMIT",
		usage: fmt.Sprintf("CEL cost units one submitter evaluation may spend (default %d). Overrides attribution.limits.costLimit.",
			defaultAttributionLimits.CostLimit),
		apply: func(a *AttributionConfig, v string) (err error) {
			a.Limits.CostLimit, err = strconv.ParseUint(v, 10, 64)
			return err
		},
	},
	{
		flag: flagMaxExpressionBytes,
		env:  "KARTA_EXPORTER_SUBMITTER_MAX_EXPRESSION_BYTES",
		usage: fmt.Sprintf("Maximum length of the submitter expression (default %d). Overrides attribution.limits.maxExpressionBytes.",
			defaultAttributionLimits.MaxExpressionBytes),
		apply: func(a *AttributionConfig, v string) (err error) {
			a.Limits.MaxExpressionBytes, err = strconv.Atoi(v)
			return err
		},
	},
	{
		flag: flagMaxIdentityBytes,
		env:  "KARTA_EXPORTER_SUBMITTER_MAX_IDENTITY_BYTES",
		usage: fmt.Sprintf("Maximum length of an extracted submitter identity (default %d). Overrides attribution.limits.maxIdentityBytes.",
			defaultAttributionLimits.MaxIdentityBytes),
		apply: func(a *AttributionConfig, v string) (err error) {
			a.Limits.MaxIdentityBytes, err = strconv.Atoi(v)
			return err
		},
	},
	{
		flag: flagInterruptCheckFrequency,
		env:  "KARTA_EXPORTER_SUBMITTER_INTERRUPT_CHECK_FREQUENCY",
		usage: fmt.Sprintf("Comprehension iterations between cancellation checks; must be greater than 0 (default %d). Overrides attribution.limits.interruptCheckFrequency.",
			defaultAttributionLimits.InterruptCheckFrequency),
		apply: func(a *AttributionConfig, v string) error {
			n, err := strconv.ParseUint(v, 10, 0)
			a.Limits.InterruptCheckFrequency = uint(n)
			return err
		},
	},
}

// Loader resolves the configuration from the file, environment variables and flags.
type Loader struct {
	flags  map[string]*string
	getenv func(string) string
}

// NewLoader registers the configuration flags on fs. Each flag can also be set
// from its KARTA_EXPORTER_* environment variable, read with getenv.
func NewLoader(fs *flag.FlagSet, getenv func(string) string) *Loader {
	l := &Loader{flags: map[string]*string{}, getenv: getenv}
	for _, s := range append([]setting{configSetting}, attributionSettings...) {
		l.flags[s.flag] = fs.String(s.flag, "", fmt.Sprintf("%s Environment variable: %s.", s.usage, s.env))
	}
	return l
}

// Load reads the config file, when set, and applies the environment variables
// and then the flags, so a flag wins. Empty values count as unset. Unknown keys
// in the file are an error. Call it after fs is parsed.
func (l *Loader) Load() (Config, error) {
	cfg := Config{Attribution: AttributionConfig{Limits: defaultAttributionLimits}}

	if path := l.value(configSetting); path != "" {
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

	for _, s := range attributionSettings {
		if v := l.value(s); v != "" {
			if err := s.apply(&cfg.Attribution, v); err != nil {
				return Config{}, fmt.Errorf("--%s (%s): %w", s.flag, s.env, err)
			}
		}
	}

	if cfg.Attribution.Limits.InterruptCheckFrequency == 0 {
		return Config{}, errZeroInterruptCheckFrequency
	}
	return cfg, nil
}

// value returns the setting's flag value, or its environment variable when the
// flag is empty.
func (l *Loader) value(s setting) string {
	if v := *l.flags[s.flag]; v != "" {
		return v
	}
	return l.getenv(s.env)
}
