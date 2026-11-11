// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package config loads the exporter configuration from a YAML file, flags and
// environment variables.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strconv"
	"time"

	"go.yaml.in/yaml/v4"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/controller"
	inventory "github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/kartadefinitioninventory"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelevaluator"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelresolver"
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
	ResourceRules         []controller.ResourceRule   `yaml:"resourceRules" json:"resourceRules"`
	ExcludeResourceRules  []controller.ResourceRule   `yaml:"excludeResourceRules" json:"excludeResourceRules"`
	NamespaceSelector     *metav1.LabelSelector       `yaml:"namespaceSelector" json:"namespaceSelector"`
	ObjectSelector        *metav1.LabelSelector       `yaml:"objectSelector" json:"objectSelector"`
	PodSelector           *metav1.LabelSelector       `yaml:"podSelector" json:"podSelector"`
	KartaDefinitionSource KartaDefinitionSourceConfig `yaml:"kartaDefinitionSource" json:"kartaDefinitionSource"`
	IgnoredOwnerKinds     Kinds                       `yaml:"ignoredOwnerKinds" json:"ignoredOwnerKinds"`
	RequiredKinds         Kinds                       `yaml:"requiredKinds" json:"requiredKinds"`
	RequestTimeout        string                      `yaml:"requestTimeout" json:"requestTimeout"`
	APIQPS                float32                     `yaml:"apiQPS" json:"apiQPS"`
	APIBurst              int                         `yaml:"apiBurst" json:"apiBurst"`
	Workers               int                         `yaml:"workers" json:"workers"`
	LeafWorkers           int                         `yaml:"leafWorkers" json:"leafWorkers"`
	RootWorkers           int                         `yaml:"rootWorkers" json:"rootWorkers"`
}

// KartaDefinitionSourceConfig selects definitions from the configured sources.
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
type setting struct {
	flag, env, usage string
	apply            func(*Config, string) error
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

var configSetting = setting{flag: flagConfig, env: "DSX_WORKLOAD_EXPORTER_CONFIG", usage: "Path to the exporter YAML config file."}

var settings = []setting{
	{
		flag: flagCostLimit,
		env:  "DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_COST_LIMIT",
		usage: fmt.Sprintf("CEL cost units one label evaluation may spend (default %d). Overrides genericLabelEvaluationLimits.costLimit.",
			defaultLabelEvaluationLimits.CostLimit),
		apply: func(c *Config, v string) (err error) {
			c.Limits.CostLimit, err = strconv.ParseUint(v, 10, 64)
			return err
		},
	},
	{
		flag: flagMaxExpressionBytes,
		env:  "DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_MAX_EXPRESSION_BYTES",
		usage: fmt.Sprintf("Maximum length of the label expression (default %d). Overrides genericLabelEvaluationLimits.maxExpressionBytes.",
			defaultLabelEvaluationLimits.MaxExpressionBytes),
		apply: func(c *Config, v string) (err error) {
			c.Limits.MaxExpressionBytes, err = strconv.Atoi(v)
			return err
		},
	},
	{
		flag: flagMaxResultBytes,
		env:  "DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_MAX_RESULT_BYTES",
		usage: fmt.Sprintf("Maximum length of an extracted label value (default %d). Overrides genericLabelEvaluationLimits.maxResultBytes.",
			defaultLabelEvaluationLimits.MaxResultBytes),
		apply: func(c *Config, v string) (err error) {
			c.Limits.MaxResultBytes, err = strconv.Atoi(v)
			return err
		},
	},
	{
		flag:  "api-qps",
		env:   "DSX_WORKLOAD_EXPORTER_API_QPS",
		usage: "Shared Kubernetes API request rate (default 20).",
		apply: func(c *Config, v string) error {
			n, err := strconv.ParseFloat(v, 32)
			c.APIQPS = float32(n)
			return err
		},
	},
	{
		flag:  "api-burst",
		env:   "DSX_WORKLOAD_EXPORTER_API_BURST",
		usage: "Shared Kubernetes API request burst (default 40).",
		apply: func(c *Config, v string) (err error) {
			c.APIBurst, err = strconv.Atoi(v)
			return err
		},
	},
	{
		flag:  "workers",
		env:   "DSX_WORKLOAD_EXPORTER_WORKERS",
		usage: "Default workers per Pod and workload queue (default 4).",
		apply: func(c *Config, v string) (err error) {
			c.Workers, err = strconv.Atoi(v)
			return err
		},
	},
	{
		flag:  "leaf-workers",
		env:   "DSX_WORKLOAD_EXPORTER_LEAF_WORKERS",
		usage: "Pod discovery and publication workers; zero inherits workers (default 0).",
		apply: func(c *Config, v string) (err error) {
			c.LeafWorkers, err = strconv.Atoi(v)
			return err
		},
	},
	{
		flag:  "root-workers",
		env:   "DSX_WORKLOAD_EXPORTER_ROOT_WORKERS",
		usage: "Candidate discovery and workload publication workers; zero inherits workers (default 0).",
		apply: func(c *Config, v string) (err error) {
			c.RootWorkers, err = strconv.Atoi(v)
			return err
		},
	},
	{
		flag:  "request-timeout",
		env:   "DSX_WORKLOAD_EXPORTER_REQUEST_TIMEOUT",
		usage: "Timeout for resource discovery and each LIST page (default 30s).",
		apply: func(c *Config, v string) error {
			c.RequestTimeout = v
			return nil
		},
	},
}

// NewLoader registers the configuration flags on fs. Each flag can also be set
// from its DSX_WORKLOAD_EXPORTER_* environment variable, read with lookupEnv.
func NewLoader(fs *flag.FlagSet, lookupEnv func(string) (string, bool)) *Loader {
	l := &Loader{flags: map[string]*string{}, lookupEnv: lookupEnv, flagSet: fs}
	for _, s := range append([]setting{configSetting}, settings...) {
		l.flags[s.flag] = fs.String(s.flag, "", fmt.Sprintf("%s Environment variable: %s.", s.usage, s.env))
	}
	return l
}

// Load reads the config file, when set, and applies the environment variables
// and then the flags, so a flag wins. Explicit flags win even when empty. Unknown keys
// in the file are an error. Call it after fs is parsed.
func (l *Loader) Load() (Config, error) {
	cfg := DefaultConfig()

	if path, _ := l.value(configSetting); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("config file %s: %w", path, err)
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		var document yaml.Node
		// An empty file decodes to io.EOF and keeps the defaults.
		switch err := dec.Decode(&document); {
		case errors.Is(err, io.EOF):
		case err != nil:
			return Config{}, fmt.Errorf("config file %s: %w", path, err)
		default:
			var value any
			if err := document.Decode(&value); err != nil {
				return Config{}, fmt.Errorf("config file %s: %w", path, err)
			}
			jsonData, err := json.Marshal(value)
			if err != nil {
				return Config{}, fmt.Errorf("config file %s: %w", path, err)
			}
			jsonDecoder := json.NewDecoder(bytes.NewReader(jsonData))
			jsonDecoder.DisallowUnknownFields()
			if err := jsonDecoder.Decode(&cfg); err != nil {
				return Config{}, fmt.Errorf("config file %s: %w", path, err)
			}
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

	for _, s := range settings {
		if v, set := l.value(s); set {
			if err := s.apply(&cfg, v); err != nil {
				return Config{}, fmt.Errorf("--%s (%s): %w", s.flag, s.env, err)
			}
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// DefaultConfig selects bundled and cluster definitions and bounds API requests and worker concurrency.
func DefaultConfig() Config {
	return Config{
		Config: labelresolver.Config{Limits: defaultLabelEvaluationLimits},
		KartaDefinitionSource: KartaDefinitionSourceConfig{
			Catalog: DefinitionSourceConfig{Enabled: true},
			Cluster: DefinitionSourceConfig{Enabled: true},
		},
		RequestTimeout: "30s",
		Workers:        4,
		APIQPS:         20,
		APIBurst:       40,
	}
}

// Validate rejects runtime settings before any clients start.
func (cfg Config) Validate() error {
	if cfg.Limits.MaxExpressionBytes < 0 || cfg.Limits.MaxResultBytes < 0 {
		return labelevaluator.ErrNegativeLimit
	}
	if err := labelresolver.ValidateConfig(cfg.Config); err != nil {
		return err
	}
	for _, selector := range []*metav1.LabelSelector{cfg.NamespaceSelector, cfg.ObjectSelector, cfg.PodSelector} {
		if selector != nil {
			if _, err := metav1.LabelSelectorAsSelector(selector); err != nil {
				return err
			}
		}
	}
	if err := controller.ValidateResourceRules(slices.Concat(cfg.ResourceRules, cfg.ExcludeResourceRules)); err != nil {
		return err
	}
	for _, kinds := range [][]schema.GroupVersionKind{cfg.IgnoredOwnerKinds, cfg.RequiredKinds} {
		seen := map[schema.GroupVersionKind]bool{}
		for _, kind := range kinds {
			if kind.Kind == "" || kind.Version == "" || seen[kind] {
				return fmt.Errorf("invalid or repeated kind %s", kind)
			}
			seen[kind] = true
		}
	}
	timeout, err := time.ParseDuration(cfg.RequestTimeout)
	if err != nil || timeout <= 0 {
		return errors.New("requestTimeout must be a positive duration")
	}
	if cfg.APIQPS <= 0 || math.IsNaN(float64(cfg.APIQPS)) || math.IsInf(float64(cfg.APIQPS), 0) {
		return errors.New("apiQPS must be finite and positive")
	}
	if cfg.APIBurst <= 0 {
		return errors.New("apiBurst must be positive")
	}
	if cfg.Workers <= 0 {
		return errors.New("workers must be positive")
	}
	if cfg.LeafWorkers < 0 || cfg.RootWorkers < 0 {
		return errors.New("leafWorkers and rootWorkers must not be negative")
	}
	return nil
}

// InventoryConfig converts the file settings; the inventory validates them.
func (c KartaDefinitionSourceConfig) InventoryConfig() inventory.Config {
	convert := func(s DefinitionSourceConfig) inventory.SourceConfig {
		return inventory.SourceConfig{Enabled: s.Enabled, Include: s.Include, Exclude: s.Exclude}
	}
	return inventory.Config{Catalog: convert(c.Catalog), Cluster: convert(c.Cluster)}
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
