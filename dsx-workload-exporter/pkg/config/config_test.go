// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package config

import (
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v3"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/kartadefinitioninventory"
)

var _ = Describe("config defaults and rules", func() {
	It("rejects the removed singular object selector key", func() {
		_, err := loadConfig("objectSelector: {matchLabels: {team: platform}}", nil)
		Expect(err).To(MatchError(ContainSubstring(`unknown field "objectSelector"`)))
	})

	It("loads independent workload metric selection and an optional Pod association", func() {
		defaults, err := loadConfig("", nil)
		Expect(err).To(Succeed())
		Expect(defaults.WorkloadMetricSelector).To(BeNil())
		Expect(defaults.PodWorkloadAssociation).To(BeNil())
		cfg, err := loadConfig(`workloadMetricSelector:
  matchExpressions:
  - key: example.org/group
    operator: DoesNotExist
podWorkloadAssociation:
  podSelector:
    matchLabels:
      example.org/managed: "true"
  target:
    apiVersion: scheduling.example.org/v1
    kind: Workload
    nameFromLabel: example.org/group
`, nil)
		Expect(err).To(Succeed())
		Expect(cfg.WorkloadMetricSelector.MatchExpressions).To(HaveLen(1))
		Expect(cfg.PodWorkloadAssociation.Target.APIVersion).To(Equal("scheduling.example.org/v1"))
		Expect(cfg.PodWorkloadAssociation.Target.NameFromLabel).To(Equal("example.org/group"))
		Expect(cfg.PodWorkloadAssociation.PodSelector.MatchLabels).To(HaveKeyWithValue("example.org/managed", "true"))
	})

	for _, invalid := range []string{
		`workloadMetricSelector: {matchExpressions: [{key: group, operator: Invalid}]}`,
		`podWorkloadAssociation: {target: {apiVersion: "", kind: Workload, nameFromLabel: group}}`,
		`podWorkloadAssociation: {target: {apiVersion: example.org/, kind: Workload, nameFromLabel: group}}`,
		`podWorkloadAssociation: {target: {apiVersion: example.org/v1, kind: "*", nameFromLabel: group}}`,
		`podWorkloadAssociation: {target: {apiVersion: example.org/v1, kind: Workload, nameFromLabel: ""}}`,
		`podWorkloadAssociation: {target: {apiVersion: example.org/v1, kind: Workload, nameFromLabel: "bad key"}}`,
		`podWorkloadAssociation: {target: {apiVersion: example.org/v1, kind: Workload, nameFromLabel: group}, podSelector: {matchExpressions: [{key: group, operator: Invalid}]}}`,
	} {
		It("rejects invalid association and metric selection configuration: "+invalid, func() {
			var parsed map[string]any
			Expect(yaml.Unmarshal([]byte(invalid), &parsed)).To(Succeed())
			_, err := loadConfig(invalid, nil)
			Expect(err).To(HaveOccurred())
		})
	}

	It("preserves flat configuration keys when encoding embedded resolver settings", func() {
		cfg, err := loadConfig("labelRules: []\ngenericLabelEvaluationLimits:\n  maxResultBytes: 32\n", nil)
		Expect(err).To(Succeed())
		for _, format := range []struct {
			encode func(any) ([]byte, error)
			decode func([]byte, any) error
		}{
			{json.Marshal, json.Unmarshal},
			{yaml.Marshal, yaml.Unmarshal},
		} {
			data, err := format.encode(cfg)
			Expect(err).To(Succeed())
			var keys map[string]any
			Expect(format.decode(data, &keys)).To(Succeed())
			Expect(keys).To(HaveKey("kartaDefinitionSource"))
			Expect(keys).To(HaveKey("labelRules"))
			Expect(keys).To(HaveKey("genericLabelEvaluationLimits"))
			Expect(keys["genericLabelEvaluationLimits"]).To(HaveLen(3))
			for _, name := range []string{"costLimit", "maxExpressionBytes", "maxResultBytes"} {
				Expect(keys["genericLabelEvaluationLimits"]).To(HaveKey(name))
			}
			var decoded Config
			Expect(format.decode(data, &decoded)).To(Succeed())
			Expect(decoded.Config).To(Equal(cfg.Config))
			Expect(decoded.KartaDefinitionSource).To(Equal(cfg.KartaDefinitionSource))
		}
	})

	It("loads label rules while retaining unspecified defaults", func() {
		cfg, err := loadConfig("", nil)
		Expect(err).To(Succeed())
		Expect(cfg.Limits).To(Equal(defaultLabelEvaluationLimits))
		Expect(cfg.Rules).To(BeEmpty())
		for _, contents := range []string{"\n", "labelRules: []\n---\n"} {
			cfg, err = loadConfig(contents, nil)
			Expect(err).To(Succeed())
			Expect(cfg.Limits).To(Equal(defaultLabelEvaluationLimits))
		}
		cfg, err = loadConfig(`labelRules:
- name: team
  metrics: [dsx_workload_pod_info]
  sources: [workload, pod]
  expression: |
    has(object.metadata.labels) ? object.metadata.labels["team"] : ""
genericLabelEvaluationLimits:
  maxResultBytes: 32
`, nil)
		Expect(err).To(Succeed())
		Expect(cfg.Rules).To(HaveLen(1))
		Expect(cfg.Rules[0].Name).To(Equal("team"))
		Expect(cfg.Rules[0].Expression).To(ContainSubstring("object.metadata.labels"))
		Expect(cfg.Limits.MaxResultBytes).To(Equal(32))
		Expect(cfg.Limits.CostLimit).To(Equal(defaultLabelEvaluationLimits.CostLimit))
		Expect(cfg.Limits.MaxExpressionBytes).To(Equal(defaultLabelEvaluationLimits.MaxExpressionBytes))
	})
})

var _ = Describe("config precedence", func() {
	for _, tc := range []struct {
		name           string
		env            map[string]string
		args           []string
		wantCost       uint64
		wantExpression int
		wantResult     int
	}{
		{name: "file", wantCost: 5, wantExpression: 50, wantResult: 500},
		{
			name: "environment",
			env: map[string]string{
				"DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_COST_LIMIT":           "7",
				"DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_MAX_EXPRESSION_BYTES": "70",
				"DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_MAX_RESULT_BYTES":     "700",
			},
			wantCost: 7, wantExpression: 70, wantResult: 700,
		},
		{
			name: "flag",
			env: map[string]string{
				"DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_COST_LIMIT":           "7",
				"DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_MAX_EXPRESSION_BYTES": "70",
				"DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_MAX_RESULT_BYTES":     "700",
			},
			args: []string{
				"--generic-label-eval-cost-limit", "9",
				"--generic-label-eval-max-expression-bytes", "90",
				"--generic-label-eval-max-result-bytes", "900",
			},
			wantCost: 9, wantExpression: 90, wantResult: 900,
		},
		{
			name: "explicit zero",
			args: []string{
				"--generic-label-eval-cost-limit", "0",
				"--generic-label-eval-max-expression-bytes", "0",
				"--generic-label-eval-max-result-bytes", "0",
			},
		},
	} {
		It(tc.name, func() {
			cfg, err := loadConfig("genericLabelEvaluationLimits:\n  costLimit: 5\n  maxExpressionBytes: 50\n  maxResultBytes: 500\n", tc.env, tc.args...)
			Expect(err).To(Succeed())
			Expect(cfg.Limits.CostLimit).To(Equal(tc.wantCost))
			Expect(cfg.Limits.MaxExpressionBytes).To(Equal(tc.wantExpression))
			Expect(cfg.Limits.MaxResultBytes).To(Equal(tc.wantResult))
		})
	}
})

var _ = Describe("config path and empty override", func() {
	It("lets explicit empty flags override environment values", func() {
		path := filepath.Join(GinkgoT().TempDir(), "config.yaml")
		Expect(os.WriteFile(path, []byte("genericLabelEvaluationLimits:\n  maxResultBytes: 32\n"), 0o600)).To(Succeed())
		cfg, err := loadConfig("", map[string]string{"DSX_WORKLOAD_EXPORTER_CONFIG": path})
		Expect(err).To(Succeed())
		Expect(cfg.Limits.MaxResultBytes).To(Equal(32))
		cfg, err = loadConfig("", map[string]string{"DSX_WORKLOAD_EXPORTER_CONFIG": path}, "--config=")
		Expect(err).To(Succeed())
		Expect(cfg.Limits).To(Equal(defaultLabelEvaluationLimits))
		for _, setting := range []struct{ flag, env string }{
			{"--generic-label-eval-cost-limit", "DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_COST_LIMIT"},
			{"--generic-label-eval-max-expression-bytes", "DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_MAX_EXPRESSION_BYTES"},
			{"--generic-label-eval-max-result-bytes", "DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_MAX_RESULT_BYTES"},
		} {
			_, err = loadConfig("", map[string]string{setting.env: "7"}, setting.flag+"=")
			Expect(err).To(HaveOccurred())
		}
	})
})

var _ = Describe("config rejects invalid input", func() {
	It("rejects invalid files, flags and environment values", func() {
		for _, contents := range []string{
			"genericLabelEvaluationLimits:\n  costLimit: lots",
			"genericLabelEvaluationLimits:\n  maxResultBytes: -1", "genericLabelEvaluationLimits:\n  maxExpressionBytes: -1",
			"genericLabelEvaluationLimits:\n  interruptCheckFrequency: 100",
			"genericLabelEvaluationLimits:\n  maxValueBytes: 32",
			"labelLimits:\n  costLimit: 5",
			"labelRules: [", "labelRules: []\n---\nlabelRules: []", "labelRules: []\n---\n---\nlabelRules: []",
			"labelRules: []\n---\nlabelRules: [", "unknown: true",
			"labelRules:\n- name: team\n  metrics: [dsx_workload_status]\n  sources: [pod]\n  expression: object.kind",
		} {
			By(contents, func() {
				_, err := loadConfig(contents, nil)
				Expect(err).To(HaveOccurred())
			})
		}
		for _, args := range [][]string{
			{"--unknown-option", "1"},
			{"--config", "/does-not-exist/config.yaml"}, {"--generic-label-eval-cost-limit", "lots"},
			{"--generic-label-eval-max-result-bytes", "-1"}, {"--generic-label-eval-max-expression-bytes", "-1"},
			{"--generic-label-eval-interrupt-check-frequency", "100"},
			{"--label-cost-limit", "5"}, {"--label-max-expression-bytes", "50"},
			{"--label-max-value-bytes", "32"}, {"--label-interrupt-check-frequency", "100"},
		} {
			_, err := loadConfig("", nil, args...)
			Expect(err).To(HaveOccurred())
		}
		for _, env := range []string{
			"DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_COST_LIMIT",
			"DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_MAX_EXPRESSION_BYTES",
			"DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_MAX_RESULT_BYTES",
		} {
			for _, value := range []string{"lots", "-1", ""} {
				_, err := loadConfig("", map[string]string{env: value})
				Expect(err).To(HaveOccurred())
			}
		}
	})

	It("registers canonical label flags and runtime flags", func() {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		NewLoader(fs, func(string) (string, bool) { return "", false })
		var names []string
		fs.VisitAll(func(f *flag.Flag) { names = append(names, f.Name) })
		Expect(names).To(ConsistOf("config", "generic-label-eval-cost-limit", "generic-label-eval-max-expression-bytes", "generic-label-eval-max-result-bytes", "api-qps", "api-burst", "workers", "leaf-workers", "root-workers", "request-timeout"))
	})

	It("ignores removed environment settings", func() {
		cfg, err := loadConfig("", map[string]string{
			"DSX_WORKLOAD_EXPORTER_LABEL_COST_LIMIT":                             "1",
			"DSX_WORKLOAD_EXPORTER_LABEL_MAX_EXPRESSION_BYTES":                   "1",
			"DSX_WORKLOAD_EXPORTER_LABEL_MAX_VALUE_BYTES":                        "1",
			"DSX_WORKLOAD_EXPORTER_LABEL_INTERRUPT_CHECK_FREQUENCY":              "0",
			"DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_INTERRUPT_CHECK_FREQUENCY": "0",
		})
		Expect(err).To(Succeed())
		Expect(cfg.Limits).To(Equal(defaultLabelEvaluationLimits))
	})
})

var _ = Describe("Karta definition sources", func() {
	It("enables bundled and cluster definitions by default", func() {
		cfg, err := loadConfig("", nil)
		Expect(err).NotTo(HaveOccurred())

		Expect(cfg.KartaDefinitionSource.InventoryConfig()).To(Equal(kartadefinitioninventory.Config{
			Catalog: kartadefinitioninventory.SourceConfig{Enabled: true},
			Cluster: kartadefinitioninventory.SourceConfig{Enabled: true},
		}))
	})

	DescribeTable("loads experimental opt-in for both sources",
		func(content string, enabled bool) {
			cfg, err := loadConfig(content, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.KartaDefinitionSource.EnableExperimental).To(Equal(enabled))
			Expect(cfg.KartaDefinitionSource.InventoryConfig().EnableExperimental).To(Equal(enabled))
		},
		Entry("omitted", "", false),
		Entry("enabled", "kartaDefinitionSource:\n  enableExperimental: true\n", true),
		Entry("disabled", "kartaDefinitionSource:\n  enableExperimental: false\n", false),
	)

	DescribeTable("preserves explicit source settings",
		func(content string, catalogEnabled, clusterEnabled bool) {
			cfg, err := loadConfig(content, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.KartaDefinitionSource.Catalog.Enabled).To(Equal(catalogEnabled))
			Expect(cfg.KartaDefinitionSource.Cluster.Enabled).To(Equal(clusterEnabled))
		},
		Entry("catalog disabled", "kartaDefinitionSource:\n  catalog:\n    enabled: false\n", false, true),
		Entry("cluster disabled", "kartaDefinitionSource:\n  cluster:\n    enabled: false\n", true, false),
		Entry("both disabled", "kartaDefinitionSource:\n  catalog:\n    enabled: false\n  cluster:\n    enabled: false\n", false, false),
		Entry("filters without enabled flags", "kartaDefinitionSource:\n  catalog:\n    include: [batch-job-v1]\n  cluster:\n    exclude: [team-job]\n", true, true),
	)

	It("keeps catalog name filters empty when the file only enables the catalog", func() {
		cfg, err := loadConfig("kartaDefinitionSource:\n  catalog:\n    enabled: true\n", nil)
		Expect(err).NotTo(HaveOccurred())

		Expect(cfg.KartaDefinitionSource.Catalog).To(Equal(DefinitionSourceConfig{Enabled: true}))
		Expect(cfg.KartaDefinitionSource.Cluster.Enabled).To(BeTrue())
	})

	It("reads the file as Helm renders it", func() {
		cfg, err := loadConfig(`kartaDefinitionSource:
  catalog:
    enabled: true
    include: []
    exclude:
    - core-pod-v1
  cluster:
    enabled: true
    include:
    - platform-job
    exclude: []
`, nil)
		Expect(err).NotTo(HaveOccurred())

		Expect(cfg.KartaDefinitionSource.InventoryConfig()).To(Equal(kartadefinitioninventory.Config{
			Catalog: kartadefinitioninventory.SourceConfig{Enabled: true, Include: []string{}, Exclude: []string{"core-pod-v1"}},
			Cluster: kartadefinitioninventory.SourceConfig{Enabled: true, Include: []string{"platform-job"}, Exclude: []string{}},
		}))
	})

	DescribeTable("rejects an unknown key",
		func(content string) {
			_, err := loadConfig(content, nil)
			Expect(err).To(MatchError(ContainSubstring("unknown field")))
		},
		Entry("a mistyped one in a source", "kartaDefinitionSource:\n  cluster:\n    exlude: [a]\n"),
		Entry("labelSelector in the catalog source", "kartaDefinitionSource:\n  catalog:\n    labelSelector: {}\n"),
		Entry("labelSelector in the cluster source", "kartaDefinitionSource:\n  cluster:\n    labelSelector: {}\n"),
	)
})

func loadConfig(content string, env map[string]string, args ...string) (Config, error) {
	GinkgoHelper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	loader := NewLoader(fs, func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	})
	if content != "" {
		path := filepath.Join(GinkgoT().TempDir(), "config.yaml")
		Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
		args = append([]string{"--config", path}, args...)
	}
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	return loader.Load()
}
