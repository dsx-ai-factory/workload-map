// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/viper"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/submitterextractor"
)

const envPrefix = "KARTA"

// errZeroInterruptCheckFrequency rejects a setting that would stop CEL from
// checking for cancellation during evaluation.
var errZeroInterruptCheckFrequency = errors.New("attribution.limits.interruptCheckFrequency must be greater than 0")

// Config is the exporter configuration, read from an optional YAML file and
// KARTA_* environment variables. A nested key maps to an environment variable
// by joining its path with underscores, for example
// KARTA_ATTRIBUTION_SUBMITTEDBYEXPRESSION.
type Config struct {
	Attribution AttributionConfig `mapstructure:"attribution" yaml:"attribution" json:"attribution"`
}

type AttributionConfig struct {
	// SubmittedByExpression is the CEL expression returning the submitter. Empty disables extraction.
	SubmittedByExpression string            `mapstructure:"submittedByExpression" yaml:"submittedByExpression" json:"submittedByExpression"`
	Limits                AttributionLimits `mapstructure:"limits" yaml:"limits" json:"limits"`
}

// AttributionLimits bounds submitter extraction. A zero value selects the
// default, except InterruptCheckFrequency, which must be greater than 0.
type AttributionLimits struct {
	CostLimit               uint64 `mapstructure:"costLimit" yaml:"costLimit" json:"costLimit"`
	MaxExpressionBytes      int    `mapstructure:"maxExpressionBytes" yaml:"maxExpressionBytes" json:"maxExpressionBytes"`
	MaxIdentityBytes        int    `mapstructure:"maxIdentityBytes" yaml:"maxIdentityBytes" json:"maxIdentityBytes"`
	InterruptCheckFrequency uint   `mapstructure:"interruptCheckFrequency" yaml:"interruptCheckFrequency" json:"interruptCheckFrequency"`
}

// loadConfig reads the config file at path, when set, and applies environment
// overrides. Unknown keys are an error.
func loadConfig(path string) (*Config, error) {
	v := viper.New()
	v.SetEnvPrefix(envPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	// Unmarshal only sees environment values for keys viper already knows.
	v.SetDefault("attribution.submittedByExpression", "")
	v.SetDefault("attribution.limits.costLimit", submitterextractor.DefaultCostLimit)
	v.SetDefault("attribution.limits.maxExpressionBytes", submitterextractor.DefaultMaxExpressionBytes)
	v.SetDefault("attribution.limits.maxIdentityBytes", submitterextractor.DefaultMaxIdentityBytes)
	v.SetDefault("attribution.limits.interruptCheckFrequency", submitterextractor.DefaultInterruptCheckFrequency)

	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("config file %s: %w", path, err)
		}
	}

	var cfg Config
	if err := v.UnmarshalExact(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if cfg.Attribution.Limits.InterruptCheckFrequency == 0 {
		return nil, errZeroInterruptCheckFrequency
	}
	return &cfg, nil
}
