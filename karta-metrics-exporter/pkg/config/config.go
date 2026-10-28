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

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelevaluator"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelresolver"
)

// Loader resolves the configuration from the file, environment variables and flags.
type Loader struct {
	flags     map[string]*string
	lookupEnv func(string) (string, bool)
	flagSet   *flag.FlagSet
}

// Config is the exporter configuration. It is read from the YAML file named by
// --config. A flag, or its environment variable, overrides the file when set.
type Config struct {
	labelresolver.Config  `yaml:",inline"`
	KartaDefinitionSource KartaDefinitionSourceConfig `yaml:"kartaDefinitionSource" json:"kartaDefinitionSource"`
}

// KartaDefinitionSourceConfig is read from the file only: its lists do not
// fit a flag or an environment variable.
type KartaDefinitionSourceConfig struct {
	Catalog DefinitionSourceConfig `yaml:"catalog" json:"catalog"`
	Cluster DefinitionSourceConfig `yaml:"cluster" json:"cluster"`
}

type DefinitionSourceConfig struct {
	Enabled bool     `yaml:"enabled" json:"enabled"`
	Include []string `yaml:"include,omitempty" json:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
}

// setting is one configuration value with its flag and environment variable.
// apply parses the raw value into the label limits.
type setting struct {
	flag, env, usage string
	apply            func(*labelevaluator.Limits, string) error
}

const (
	flagConfig             = "config"
	flagCostLimit          = "generic-label-eval-cost-limit"
	flagMaxExpressionBytes = "generic-label-eval-max-expression-bytes"
	flagMaxResultBytes     = "generic-label-eval-max-result-bytes"
)

var errMultipleDocuments = errors.New("must contain a single YAML document")

var defaultLabelEvaluationLimits = labelevaluator.Limits{
	CostLimit:          labelevaluator.DefaultCostLimit,
	MaxExpressionBytes: labelevaluator.DefaultMaxExpressionBytes,
	MaxResultBytes:     labelevaluator.DefaultMaxResultBytes,
}

var configSetting = setting{flag: flagConfig, env: "KARTA_EXPORTER_CONFIG", usage: "Path to the exporter YAML config file."}

var labelSettings = []setting{
	{
		flag: flagCostLimit,
		env:  "KARTA_EXPORTER_GENERIC_LABEL_EVAL_COST_LIMIT",
		usage: fmt.Sprintf("Cost units one generic label evaluation may spend (default %d). Overrides genericLabelEvaluationLimits.costLimit.",
			defaultLabelEvaluationLimits.CostLimit),
		apply: func(limits *labelevaluator.Limits, v string) (err error) {
			limits.CostLimit, err = strconv.ParseUint(v, 10, 64)
			return err
		},
	},
	{
		flag: flagMaxExpressionBytes,
		env:  "KARTA_EXPORTER_GENERIC_LABEL_EVAL_MAX_EXPRESSION_BYTES",
		usage: fmt.Sprintf("Maximum length in bytes of a generic label expression (default %d). Overrides genericLabelEvaluationLimits.maxExpressionBytes.",
			defaultLabelEvaluationLimits.MaxExpressionBytes),
		apply: func(limits *labelevaluator.Limits, v string) (err error) {
			limits.MaxExpressionBytes, err = strconv.Atoi(v)
			return err
		},
	},
	{
		flag: flagMaxResultBytes,
		env:  "KARTA_EXPORTER_GENERIC_LABEL_EVAL_MAX_RESULT_BYTES",
		usage: fmt.Sprintf("Maximum length in bytes of a generic label evaluation result (default %d). Overrides genericLabelEvaluationLimits.maxResultBytes.",
			defaultLabelEvaluationLimits.MaxResultBytes),
		apply: func(limits *labelevaluator.Limits, v string) (err error) {
			limits.MaxResultBytes, err = strconv.Atoi(v)
			return err
		},
	},
}

// NewLoader registers the configuration flags on fs. Each flag can also be set
// from its KARTA_EXPORTER_* environment variable, read with lookupEnv.
func NewLoader(fs *flag.FlagSet, lookupEnv func(string) (string, bool)) *Loader {
	l := &Loader{flags: map[string]*string{}, lookupEnv: lookupEnv, flagSet: fs}
	for _, s := range append([]setting{configSetting}, labelSettings...) {
		l.flags[s.flag] = fs.String(s.flag, "", fmt.Sprintf("%s Environment variable: %s.", s.usage, s.env))
	}
	return l
}

// Load reads the config file, when set, and applies the environment variables
// and then the flags, so a flag wins. Explicit flags win even when empty. Unknown keys
// in the file are an error. Call it after fs is parsed.
func (l *Loader) Load() (Config, error) {
	cfg := Config{
		Config: labelresolver.Config{Limits: defaultLabelEvaluationLimits},
		// Cluster Karta definitions are read by default; the catalog is opt-in.
		KartaDefinitionSource: KartaDefinitionSourceConfig{Cluster: DefinitionSourceConfig{Enabled: true}},
	}

	if path, _ := l.value(configSetting); path != "" {
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

	for _, s := range labelSettings {
		if v, set := l.value(s); set {
			if err := s.apply(&cfg.Limits, v); err != nil {
				return Config{}, fmt.Errorf("--%s (%s): %w", s.flag, s.env, err)
			}
		}
	}

	if cfg.Limits.MaxExpressionBytes < 0 || cfg.Limits.MaxResultBytes < 0 {
		return Config{}, labelevaluator.ErrNegativeLimit
	}
	if err := labelresolver.ValidateConfig(cfg.Config); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// value distinguishes an explicit empty flag from an unset one.
func (l *Loader) value(s setting) (string, bool) {
	set := false
	l.flagSet.Visit(func(f *flag.Flag) {
		if f.Name == s.flag {
			set = true
		}
	})
	if set {
		return *l.flags[s.flag], true
	}
	return l.lookupEnv(s.env)
}

// InventoryConfig converts the file settings; the inventory validates them.
func (c KartaDefinitionSourceConfig) InventoryConfig() kartadefinitioninventory.Config {
	return kartadefinitioninventory.Config{
		Catalog: kartadefinitioninventory.CatalogConfig{Enabled: c.Catalog.Enabled, Include: c.Catalog.Include, Exclude: c.Catalog.Exclude},
		Cluster: kartadefinitioninventory.ClusterConfig{Enabled: c.Cluster.Enabled, Include: c.Cluster.Include, Exclude: c.Cluster.Exclude},
	}
}
