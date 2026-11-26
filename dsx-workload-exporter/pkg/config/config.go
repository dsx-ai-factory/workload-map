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
	"strconv"
	"time"

	"go.yaml.in/yaml/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/controller"
	inventory "github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"

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
	Selection             SelectionConfig             `yaml:"selection" json:"selection"`
	KartaDefinitionSource KartaDefinitionSourceConfig `yaml:"kartaDefinitionSource" json:"kartaDefinitionSource"`
	Metrics               MetricsConfig               `yaml:"metrics" json:"metrics"`
	Tuning                TuningConfig                `yaml:"tuning" json:"tuning"`
}

type SelectionConfig struct {
	NamespaceSelector    *metav1.LabelSelector       `yaml:"namespaceSelector" json:"namespaceSelector"`
	IncludeRootResources []controller.ResourceRule   `yaml:"includeRootResources" json:"includeRootResources"`
	ExcludeRootResources []controller.ResourceRule   `yaml:"excludeRootResources" json:"excludeRootResources"`
	RootSelectors        []controller.ObjectSelector `yaml:"rootSelectors" json:"rootSelectors"`
	PodSelector          *metav1.LabelSelector       `yaml:"podSelector" json:"podSelector"`
	IgnoredOwnerKinds    Kinds                       `yaml:"ignoredOwnerKinds" json:"ignoredOwnerKinds"`
	RequiredWatchKinds   Kinds                       `yaml:"requiredWatchKinds" json:"requiredWatchKinds"`
}

type MetricsConfig struct {
	labelresolver.Config   `yaml:",inline"`
	WorkloadSelector       *metav1.LabelSelector              `yaml:"workloadSelector" json:"workloadSelector"`
	ExcludeStatuses        []v1alpha1.ResourceStatus          `yaml:"excludeStatuses" json:"excludeStatuses"`
	PodWorkloadAssociation *controller.PodWorkloadAssociation `yaml:"podWorkloadAssociation" json:"podWorkloadAssociation"`
	Delivery               DeliveryConfig                     `yaml:"delivery" json:"delivery"`
}

type DeliveryConfig struct {
	Prometheus PrometheusDeliveryConfig `yaml:"prometheus" json:"prometheus"`
}

type PrometheusDeliveryConfig struct {
	TLS ServingTLSConfig `yaml:"tls" json:"tls"`
}

// ServingTLSConfig names the certificate and key the metrics listener serves.
// Leaving both empty keeps plain HTTP.
type ServingTLSConfig struct {
	CertFile string `yaml:"certFile" json:"certFile"`
	KeyFile  string `yaml:"keyFile" json:"keyFile"`
}

type TuningConfig struct {
	Workers   WorkerConfig    `yaml:"workers" json:"workers"`
	APIClient APIClientConfig `yaml:"apiClient" json:"apiClient"`
}

type WorkerConfig struct {
	Default   int `yaml:"default" json:"default"`
	Pods      int `yaml:"pods" json:"pods"`
	Workloads int `yaml:"workloads" json:"workloads"`
}

type APIClientConfig struct {
	RequestTimeout string  `yaml:"requestTimeout" json:"requestTimeout"`
	QPS            float32 `yaml:"qps" json:"qps"`
	Burst          int     `yaml:"burst" json:"burst"`
}

// KartaDefinitionSourceConfig selects definitions from the configured sources.
type KartaDefinitionSourceConfig struct {
	EnableExperimental bool                   `yaml:"enableExperimental" json:"enableExperimental"`
	Catalog            DefinitionSourceConfig `yaml:"catalog" json:"catalog"`
	Cluster            DefinitionSourceConfig `yaml:"cluster" json:"cluster"`
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
	flagCostLimit          = "label-eval-cost-limit"
	flagMaxExpressionBytes = "label-eval-max-expression-bytes"
	flagMaxResultBytes     = "label-eval-max-result-bytes"
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
		env:  "DSX_WORKLOAD_EXPORTER_LABEL_EVAL_COST_LIMIT",
		usage: fmt.Sprintf("CEL cost units one label evaluation may spend (default %d). Overrides metrics.labelEvaluationLimits.costLimit.",
			defaultLabelEvaluationLimits.CostLimit),
		apply: func(c *Config, v string) (err error) {
			c.Metrics.Limits.CostLimit, err = strconv.ParseUint(v, 10, 64)
			return err
		},
	},
	{
		flag: flagMaxExpressionBytes,
		env:  "DSX_WORKLOAD_EXPORTER_LABEL_EVAL_MAX_EXPRESSION_BYTES",
		usage: fmt.Sprintf("Maximum length of the label expression (default %d). Overrides metrics.labelEvaluationLimits.maxExpressionBytes.",
			defaultLabelEvaluationLimits.MaxExpressionBytes),
		apply: func(c *Config, v string) (err error) {
			c.Metrics.Limits.MaxExpressionBytes, err = strconv.Atoi(v)
			return err
		},
	},
	{
		flag: flagMaxResultBytes,
		env:  "DSX_WORKLOAD_EXPORTER_LABEL_EVAL_MAX_RESULT_BYTES",
		usage: fmt.Sprintf("Maximum length of an extracted label value (default %d). Overrides metrics.labelEvaluationLimits.maxResultBytes.",
			defaultLabelEvaluationLimits.MaxResultBytes),
		apply: func(c *Config, v string) (err error) {
			c.Metrics.Limits.MaxResultBytes, err = strconv.Atoi(v)
			return err
		},
	},
	{
		flag:  "api-qps",
		env:   "DSX_WORKLOAD_EXPORTER_API_QPS",
		usage: "Shared Kubernetes API request rate (default 20).",
		apply: func(c *Config, v string) error {
			n, err := strconv.ParseFloat(v, 32)
			c.Tuning.APIClient.QPS = float32(n)
			return err
		},
	},
	{
		flag:  "api-burst",
		env:   "DSX_WORKLOAD_EXPORTER_API_BURST",
		usage: "Shared Kubernetes API request burst (default 40).",
		apply: func(c *Config, v string) (err error) {
			c.Tuning.APIClient.Burst, err = strconv.Atoi(v)
			return err
		},
	},
	{
		flag:  "workers",
		env:   "DSX_WORKLOAD_EXPORTER_WORKERS",
		usage: "Default workers per Pod and workload queue (default 4).",
		apply: func(c *Config, v string) (err error) {
			c.Tuning.Workers.Default, err = strconv.Atoi(v)
			return err
		},
	},
	{
		flag:  "pod-workers",
		env:   "DSX_WORKLOAD_EXPORTER_POD_WORKERS",
		usage: "Pod discovery and publication workers; zero inherits workers (default 0).",
		apply: func(c *Config, v string) (err error) {
			c.Tuning.Workers.Pods, err = strconv.Atoi(v)
			return err
		},
	},
	{
		flag:  "workload-workers",
		env:   "DSX_WORKLOAD_EXPORTER_WORKLOAD_WORKERS",
		usage: "Candidate discovery and workload publication workers; zero inherits workers (default 0).",
		apply: func(c *Config, v string) (err error) {
			c.Tuning.Workers.Workloads, err = strconv.Atoi(v)
			return err
		},
	},
	{
		flag:  "request-timeout",
		env:   "DSX_WORKLOAD_EXPORTER_REQUEST_TIMEOUT",
		usage: "Timeout for resource discovery and each LIST page (default 30s).",
		apply: func(c *Config, v string) error {
			c.Tuning.APIClient.RequestTimeout = v
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
		Metrics: MetricsConfig{
			Config:          labelresolver.Config{Limits: defaultLabelEvaluationLimits},
			ExcludeStatuses: []v1alpha1.ResourceStatus{v1alpha1.SuspendingStatus, v1alpha1.ResumingStatus},
		},
		KartaDefinitionSource: KartaDefinitionSourceConfig{
			Catalog: DefinitionSourceConfig{Enabled: true},
			Cluster: DefinitionSourceConfig{Enabled: true},
		},
		Tuning: TuningConfig{
			Workers:   WorkerConfig{Default: 4},
			APIClient: APIClientConfig{RequestTimeout: "30s", QPS: 20, Burst: 40},
		},
	}
}

// Validate rejects runtime settings before any clients start.
func (cfg Config) Validate() error {
	if cfg.Metrics.Limits.MaxExpressionBytes < 0 || cfg.Metrics.Limits.MaxResultBytes < 0 {
		return fmt.Errorf("metrics.labelEvaluationLimits: %w", labelevaluator.ErrNegativeLimit)
	}
	if err := labelresolver.ValidateConfig(cfg.Metrics.Config); err != nil {
		return fmt.Errorf("metrics.%w", err)
	}
	for _, field := range []struct {
		path     string
		selector *metav1.LabelSelector
	}{
		{"selection.namespaceSelector", cfg.Selection.NamespaceSelector},
		{"selection.podSelector", cfg.Selection.PodSelector},
		{"metrics.workloadSelector", cfg.Metrics.WorkloadSelector},
	} {
		if field.selector != nil {
			if _, err := metav1.LabelSelectorAsSelector(field.selector); err != nil {
				return fmt.Errorf("%s: %w", field.path, err)
			}
		}
	}
	if err := controller.ValidateResourceRules(cfg.Selection.IncludeRootResources); err != nil {
		return fmt.Errorf("selection.includeRootResources: %w", err)
	}
	if err := controller.ValidateResourceRules(cfg.Selection.ExcludeRootResources); err != nil {
		return fmt.Errorf("selection.excludeRootResources: %w", err)
	}
	for _, status := range cfg.Metrics.ExcludeStatuses {
		switch status {
		case v1alpha1.InitializingStatus, v1alpha1.RunningStatus, v1alpha1.CompletedStatus,
			v1alpha1.FailedStatus, v1alpha1.DegradedStatus, v1alpha1.SuspendedStatus,
			v1alpha1.SuspendingStatus, v1alpha1.ResumingStatus, v1alpha1.UndefinedStatus:
		default:
			return fmt.Errorf("metrics.excludeStatuses: unknown excluded status %q", status)
		}
	}
	if err := controller.ValidateObjectSelectors(cfg.Selection.RootSelectors); err != nil {
		return err
	}
	if err := controller.ValidatePodWorkloadAssociation(cfg.Metrics.PodWorkloadAssociation); err != nil {
		return err
	}
	if tls := cfg.Metrics.Delivery.Prometheus.TLS; (tls.CertFile == "") != (tls.KeyFile == "") {
		return errors.New("metrics.delivery.prometheus.tls: certFile and keyFile must be set together")
	}
	for _, kinds := range [][]schema.GroupVersionKind{cfg.Selection.IgnoredOwnerKinds, cfg.Selection.RequiredWatchKinds} {
		seen := map[schema.GroupVersionKind]bool{}
		for _, kind := range kinds {
			if kind.Kind == "" || kind.Version == "" || seen[kind] {
				return fmt.Errorf("invalid or repeated kind %s", kind)
			}
			seen[kind] = true
		}
	}
	timeout, err := time.ParseDuration(cfg.Tuning.APIClient.RequestTimeout)
	if err != nil {
		return fmt.Errorf("tuning.apiClient.requestTimeout: %w", err)
	}
	if timeout <= 0 {
		return errors.New("tuning.apiClient.requestTimeout must be a positive duration")
	}
	if cfg.Tuning.APIClient.QPS <= 0 || math.IsNaN(float64(cfg.Tuning.APIClient.QPS)) || math.IsInf(float64(cfg.Tuning.APIClient.QPS), 0) {
		return errors.New("tuning.apiClient.qps must be finite and positive")
	}
	if cfg.Tuning.APIClient.Burst <= 0 {
		return errors.New("tuning.apiClient.burst must be positive")
	}
	if cfg.Tuning.Workers.Default <= 0 {
		return errors.New("tuning.workers.default must be positive")
	}
	if cfg.Tuning.Workers.Pods < 0 {
		return errors.New("tuning.workers.pods must not be negative")
	}
	if cfg.Tuning.Workers.Workloads < 0 {
		return errors.New("tuning.workers.workloads must not be negative")
	}
	return nil
}

// InventoryConfig converts the file settings; the inventory validates them.
func (c KartaDefinitionSourceConfig) InventoryConfig() inventory.Config {
	convert := func(s DefinitionSourceConfig) inventory.SourceConfig {
		return inventory.SourceConfig{Enabled: s.Enabled, Include: s.Include, Exclude: s.Exclude}
	}
	return inventory.Config{Catalog: convert(c.Catalog), Cluster: convert(c.Cluster), EnableExperimental: c.EnableExperimental}
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
