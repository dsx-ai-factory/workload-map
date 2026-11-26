// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package config

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v3"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

var _ = Describe("config defaults and rules", func() {
	It("loads independent workload metric selection and an optional Pod association", func() {
		defaults, err := loadConfig("", nil)
		Expect(err).To(Succeed())
		Expect(defaults.Metrics.WorkloadSelector).To(BeNil())
		Expect(defaults.Metrics.PodWorkloadAssociation).To(BeNil())
		cfg, err := loadConfig(`metrics:
  workloadSelector:
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
		Expect(cfg.Metrics.WorkloadSelector.MatchExpressions).To(HaveLen(1))
		Expect(cfg.Metrics.PodWorkloadAssociation.Target.APIVersion).To(Equal("scheduling.example.org/v1"))
		Expect(cfg.Metrics.PodWorkloadAssociation.Target.NameFromLabel).To(Equal("example.org/group"))
		Expect(cfg.Metrics.PodWorkloadAssociation.PodSelector.MatchLabels).To(HaveKeyWithValue("example.org/managed", "true"))
	})

	for _, invalid := range []string{
		`metrics: {workloadSelector: {matchExpressions: [{key: group, operator: Invalid}]}}`,
		`metrics: {podWorkloadAssociation: {target: {apiVersion: "", kind: Workload, nameFromLabel: group}}}`,
		`metrics: {podWorkloadAssociation: {target: {apiVersion: example.org/, kind: Workload, nameFromLabel: group}}}`,
		`metrics: {podWorkloadAssociation: {target: {apiVersion: example.org/v1, kind: "*", nameFromLabel: group}}}`,
		`metrics: {podWorkloadAssociation: {target: {apiVersion: example.org/v1, kind: Workload, nameFromLabel: ""}}}`,
		`metrics: {podWorkloadAssociation: {target: {apiVersion: example.org/v1, kind: Workload, nameFromLabel: "bad key"}}}`,
		`metrics: {podWorkloadAssociation: {target: {apiVersion: example.org/v1, kind: Workload, nameFromLabel: group}, podSelector: {matchExpressions: [{key: group, operator: Invalid}]}}}`,
	} {
		It("rejects invalid association and metric selection configuration: "+invalid, func() {
			var parsed map[string]any
			Expect(yaml.Unmarshal([]byte(invalid), &parsed)).To(Succeed())
			_, err := loadConfig(invalid, nil)
			Expect(err).To(HaveOccurred())
		})
	}

	It("encodes label settings under metrics in both JSON and YAML", func() {
		cfg, err := loadConfig("metrics: {labelRules: [], labelEvaluationLimits: {maxResultBytes: 32}}", nil)
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
			metrics := keys["metrics"]
			Expect(metrics).To(HaveKey("labelRules"))
			Expect(metrics).To(HaveKeyWithValue("labelEvaluationLimits", HaveLen(3)))
			for _, name := range []string{"costLimit", "maxExpressionBytes", "maxResultBytes"} {
				Expect(metrics).To(HaveKeyWithValue("labelEvaluationLimits", HaveKey(name)))
			}
			var decoded Config
			Expect(format.decode(data, &decoded)).To(Succeed())
			Expect(decoded.Metrics).To(Equal(cfg.Metrics))
			Expect(decoded.KartaDefinitionSource).To(Equal(cfg.KartaDefinitionSource))
		}
	})

	It("loads label rules while retaining unspecified defaults", func() {
		cfg, err := loadConfig("", nil)
		Expect(err).To(Succeed())
		Expect(cfg.Metrics.Limits).To(Equal(defaultLabelEvaluationLimits))
		Expect(cfg.Metrics.Rules).To(BeEmpty())
		for _, contents := range []string{"\n", "metrics: {labelRules: []}\n---\n"} {
			cfg, err = loadConfig(contents, nil)
			Expect(err).To(Succeed())
			Expect(cfg.Metrics.Limits).To(Equal(defaultLabelEvaluationLimits))
		}
		cfg, err = loadConfig(`metrics:
  labelRules:
  - name: team
    metrics: [dsx_workload_pod_info]
    sources: [workload, pod]
    expression: |
      has(object.metadata.labels) ? object.metadata.labels["team"] : ""
  labelEvaluationLimits:
    maxResultBytes: 32
`, nil)
		Expect(err).To(Succeed())
		Expect(cfg.Metrics.Rules).To(HaveLen(1))
		Expect(cfg.Metrics.Rules[0].Name).To(Equal("team"))
		Expect(cfg.Metrics.Rules[0].Expression).To(ContainSubstring("object.metadata.labels"))
		Expect(cfg.Metrics.Limits.MaxResultBytes).To(Equal(32))
		Expect(cfg.Metrics.Limits.CostLimit).To(Equal(defaultLabelEvaluationLimits.CostLimit))
		Expect(cfg.Metrics.Limits.MaxExpressionBytes).To(Equal(defaultLabelEvaluationLimits.MaxExpressionBytes))
	})
})

var _ = Describe("grouped configuration", func() {
	It("loads namespace, root and Pod selection and additional watch kinds", func() {
		cfg, err := loadConfig(`selection:
  namespaceSelector: {matchLabels: {team: platform}}
  rootSelectors: [{selector: {matchLabels: {tier: batch}}}]
  podSelector: {matchLabels: {role: worker}}
  includeRootResources: [{apiGroups: [apps], apiVersions: [v1], resources: [deployments]}]
  excludeRootResources: [{apiGroups: [""], apiVersions: [v1], resources: [secrets]}]
  ignoredOwnerKinds: [example.org/v1/Wrapper]
  requiredWatchKinds: [apps/v1/Deployment]`, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Selection.NamespaceSelector.MatchLabels).To(HaveKeyWithValue("team", "platform"))
		Expect(cfg.Selection.RootSelectors).To(HaveLen(1))
		Expect(cfg.Selection.RootSelectors[0].Selector.MatchLabels).To(HaveKeyWithValue("tier", "batch"))
		Expect(cfg.Selection.PodSelector.MatchLabels).To(HaveKeyWithValue("role", "worker"))
		Expect(cfg.Selection.IncludeRootResources).To(HaveLen(1))
		Expect(cfg.Selection.IncludeRootResources[0].Resources).To(ConsistOf("deployments"))
		Expect(cfg.Selection.ExcludeRootResources).To(HaveLen(1))
		Expect(cfg.Selection.ExcludeRootResources[0].Resources).To(ConsistOf("secrets"))
		Expect(cfg.Selection.IgnoredOwnerKinds).To(HaveLen(1))
		Expect(cfg.Selection.IgnoredOwnerKinds[0].Kind).To(Equal("Wrapper"))
		Expect(cfg.Selection.RequiredWatchKinds).To(HaveLen(1))
		Expect(cfg.Selection.RequiredWatchKinds[0].Kind).To(Equal("Deployment"))
	})

	It("applies flags and environment variables after grouped values", func() {
		cfg, err := loadConfig(`tuning: {workers: {default: 6, pods: 2, workloads: 3}, apiClient: {qps: 2.5, burst: 10, requestTimeout: 12s}}`, map[string]string{
			"DSX_WORKLOAD_EXPORTER_API_QPS":          "5.5",
			"DSX_WORKLOAD_EXPORTER_WORKERS":          "7",
			"DSX_WORKLOAD_EXPORTER_POD_WORKERS":      "4",
			"DSX_WORKLOAD_EXPORTER_WORKLOAD_WORKERS": "5",
		}, "--workers=8", "--workload-workers=6")
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Tuning.Workers.Default).To(Equal(8))
		Expect(cfg.Tuning.Workers.Pods).To(Equal(4))
		Expect(cfg.Tuning.Workers.Workloads).To(Equal(6))
		Expect(cfg.Tuning.APIClient.RequestTimeout).To(Equal("12s"))
		Expect(cfg.Tuning.APIClient.QPS).To(Equal(float32(5.5)))
		Expect(cfg.Tuning.APIClient.Burst).To(Equal(10))
	})

	It("rejects unknown fields, wrong types and Helm-only tuning settings", func() {
		for _, content := range []string{
			"workers: 3", "selection: {typo: []}", "metrics: {workloadSelector: {label: {}}}",
			"metrics: {labelRules: [{unknown: 1}]}",
			"tuning: {workers: 3}", "tuning: {workers: {default: invalid}}",
			"tuning: {apiClient: {rate: 2}}", "tuning: {pprof: {enabled: true}}",
			"tuning: {goMemoryLimit: auto}", "tuning: {workers: {pods: -1}}",
		} {
			By(content, func() {
				_, err := loadConfig(content, nil)
				Expect(err).To(HaveOccurred())
			})
		}
	})
})

var _ = Describe("status exclusions", func() {
	DescribeTable("loads defaults and explicit replacements",
		func(content string, want []v1alpha1.ResourceStatus) {
			cfg, err := loadConfig(content, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Metrics.ExcludeStatuses).To(Equal(want))
		},
		Entry("default", "", []v1alpha1.ResourceStatus{v1alpha1.SuspendingStatus, v1alpha1.ResumingStatus}),
		Entry("unrelated setting", "tuning: {workers: {default: 2}}", []v1alpha1.ResourceStatus{v1alpha1.SuspendingStatus, v1alpha1.ResumingStatus}),
		Entry("all phases", "metrics: {excludeStatuses: []}", []v1alpha1.ResourceStatus{}),
		Entry("custom exclusions replace defaults", "metrics: {excludeStatuses: [Running]}", []v1alpha1.ResourceStatus{v1alpha1.RunningStatus}),
	)

	It("rejects unknown or incorrectly cased phase names", func() {
		for _, value := range []string{"unknown", "running", ""} {
			_, err := loadConfig("metrics: {excludeStatuses: [\""+value+"\"]}", nil)
			Expect(err).To(MatchError(ContainSubstring("unknown excluded status")))
		}
	})
})

var _ = Describe("Prometheus delivery TLS", func() {
	It("serves plain HTTP by default", func() {
		cfg, err := loadConfig("", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Metrics.Delivery.Prometheus.TLS).To(BeZero())
	})

	It("loads the certificate and key paths", func() {
		cfg, err := loadConfig("metrics: {delivery: {prometheus: {tls: {certFile: /tls/tls.crt, keyFile: /tls/tls.key}}}}", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Metrics.Delivery.Prometheus.TLS).To(Equal(ServingTLSConfig{CertFile: "/tls/tls.crt", KeyFile: "/tls/tls.key"}))
	})

	DescribeTable("rejects a certificate without its key and the reverse",
		func(content string) {
			_, err := loadConfig(content, nil)
			Expect(err).To(MatchError(ContainSubstring("certFile and keyFile must be set together")))
		},
		Entry("certificate only", "metrics: {delivery: {prometheus: {tls: {certFile: /tls/tls.crt}}}}"),
		Entry("key only", "metrics: {delivery: {prometheus: {tls: {keyFile: /tls/tls.key}}}}"),
	)

	It("rejects an unknown key", func() {
		_, err := loadConfig("metrics: {delivery: {prometheus: {tls: {existingSecret: dsx-exporter-tls}}}}", nil)
		Expect(err).To(MatchError(ContainSubstring(`unknown field "existingSecret"`)))
	})
})

var _ = Describe("OTLP delivery", func() {
	otlp := func(settings string) string {
		return "metrics:\n  delivery:\n    mode: otlp\n    otlp: {" + settings + "}\n"
	}

	It("keeps Prometheus pull by default", func() {
		cfg, err := loadConfig("", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Metrics.Delivery.Mode).To(Equal(DeliveryPrometheus))
		Expect(cfg.Metrics.Delivery.OTLP).To(Equal(OTLPDeliveryConfig{Protocol: ProtocolHTTPProtobuf, Interval: "30s", Timeout: "10s"}))
	})

	It("treats an unset mode as Prometheus pull in a config built without defaults", func() {
		cfg := DefaultConfig()
		cfg.Metrics.Delivery = DeliveryConfig{}
		Expect(cfg.Validate()).To(Succeed())
	})

	DescribeTable("loads a receiver for each protocol",
		func(settings string, want OTLPDeliveryConfig) {
			cfg, err := loadConfig(otlp(settings), nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Metrics.Delivery.Mode).To(Equal(DeliveryOTLP))
			Expect(cfg.Metrics.Delivery.OTLP).To(Equal(want))
		},
		Entry("http/protobuf with defaults", "endpoint: https://collector.example.com:4318/v1/metrics",
			OTLPDeliveryConfig{Protocol: ProtocolHTTPProtobuf, Endpoint: "https://collector.example.com:4318/v1/metrics", Interval: "30s", Timeout: "10s"}),
		Entry("grpc with mutual TLS", "protocol: grpc, endpoint: 'https://collector.example.com:4317', interval: 1m, timeout: 5s, tls: {caFile: /tls/ca.crt, certFile: /tls/tls.crt, keyFile: /tls/tls.key}",
			OTLPDeliveryConfig{Protocol: ProtocolGRPC, Endpoint: "https://collector.example.com:4317", Interval: "1m", Timeout: "5s",
				TLS: OTLPTLSConfig{CAFile: "/tls/ca.crt", CertFile: "/tls/tls.crt", KeyFile: "/tls/tls.key"}}),
		Entry("grpc in plain text", "protocol: grpc, endpoint: 'http://collector:4317'",
			OTLPDeliveryConfig{Protocol: ProtocolGRPC, Endpoint: "http://collector:4317", Interval: "30s", Timeout: "10s"}),
	)

	DescribeTable("rejects invalid settings",
		func(content, message string) {
			_, err := loadConfig(content, nil)
			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("an unknown mode", "metrics: {delivery: {mode: push}}", `metrics.delivery.mode must be prometheus or otlp, not "push"`),
		Entry("an unknown protocol", otlp("protocol: http/json, endpoint: 'https://c:4318/v1/metrics'"), `metrics.delivery.otlp.protocol must be http/protobuf or grpc, not "http/json"`),
		Entry("no endpoint", otlp(""), `metrics.delivery.otlp.endpoint must be an http or https URL, not ""`),
		Entry("an endpoint without a scheme", otlp("endpoint: 'collector:4318'"), "metrics.delivery.otlp.endpoint must be an http or https URL"),
		Entry("an endpoint with another scheme", otlp("endpoint: 'ftp://collector:4318/v1/metrics'"), "metrics.delivery.otlp.endpoint must be an http or https URL"),
		Entry("an http/protobuf endpoint without its path", otlp("endpoint: 'https://collector:4318'"), "metrics.delivery.otlp.endpoint needs the receiver path for http/protobuf"),
		Entry("a grpc endpoint with a path", otlp("protocol: grpc, endpoint: 'https://collector:4317/v1/metrics'"), "metrics.delivery.otlp.endpoint takes no path for grpc"),
		Entry("an interval that is not a duration", otlp("endpoint: 'https://c:4318/v1/metrics', interval: soon"), `metrics.delivery.otlp.interval must be a positive duration, not "soon"`),
		Entry("a zero interval", otlp("endpoint: 'https://c:4318/v1/metrics', interval: 0s"), "metrics.delivery.otlp.interval must be a positive duration"),
		Entry("a negative timeout", otlp("endpoint: 'https://c:4318/v1/metrics', timeout: -1s"), "metrics.delivery.otlp.timeout must be a positive duration"),
		Entry("a timeout longer than the interval", otlp("endpoint: 'https://c:4318/v1/metrics', interval: 5s, timeout: 10s"), "metrics.delivery.otlp.timeout 10s must not exceed interval 5s"),
		Entry("a client certificate without its key", otlp("endpoint: 'https://c:4318/v1/metrics', tls: {certFile: /tls/tls.crt}"), "metrics.delivery.otlp.tls: certFile and keyFile must be set together"),
		Entry("TLS files for a plain-text endpoint", otlp("endpoint: 'http://c:4318/v1/metrics', tls: {caFile: /tls/ca.crt}"), "metrics.delivery.otlp.tls needs an https endpoint"),
		Entry("a serving certificate in otlp mode",
			"metrics:\n  delivery:\n    mode: otlp\n    prometheus: {tls: {certFile: /tls/tls.crt, keyFile: /tls/tls.key}}\n    otlp: {endpoint: 'https://c:4318/v1/metrics'}\n",
			"metrics.delivery.prometheus.tls does not apply in otlp mode"),
	)
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
				"DSX_WORKLOAD_EXPORTER_LABEL_EVAL_COST_LIMIT":           "7",
				"DSX_WORKLOAD_EXPORTER_LABEL_EVAL_MAX_EXPRESSION_BYTES": "70",
				"DSX_WORKLOAD_EXPORTER_LABEL_EVAL_MAX_RESULT_BYTES":     "700",
			},
			wantCost: 7, wantExpression: 70, wantResult: 700,
		},
		{
			name: "flag",
			env: map[string]string{
				"DSX_WORKLOAD_EXPORTER_LABEL_EVAL_COST_LIMIT":           "7",
				"DSX_WORKLOAD_EXPORTER_LABEL_EVAL_MAX_EXPRESSION_BYTES": "70",
				"DSX_WORKLOAD_EXPORTER_LABEL_EVAL_MAX_RESULT_BYTES":     "700",
			},
			args: []string{
				"--label-eval-cost-limit", "9",
				"--label-eval-max-expression-bytes", "90",
				"--label-eval-max-result-bytes", "900",
			},
			wantCost: 9, wantExpression: 90, wantResult: 900,
		},
		{
			name: "explicit zero",
			args: []string{
				"--label-eval-cost-limit", "0",
				"--label-eval-max-expression-bytes", "0",
				"--label-eval-max-result-bytes", "0",
			},
		},
	} {
		It(tc.name, func() {
			cfg, err := loadConfig("metrics: {labelEvaluationLimits: {costLimit: 5, maxExpressionBytes: 50, maxResultBytes: 500}}", tc.env, tc.args...)
			Expect(err).To(Succeed())
			Expect(cfg.Metrics.Limits.CostLimit).To(Equal(tc.wantCost))
			Expect(cfg.Metrics.Limits.MaxExpressionBytes).To(Equal(tc.wantExpression))
			Expect(cfg.Metrics.Limits.MaxResultBytes).To(Equal(tc.wantResult))
		})
	}
})

var _ = Describe("config path and empty override", func() {
	It("lets explicit empty flags override environment values", func() {
		path := filepath.Join(GinkgoT().TempDir(), "config.yaml")
		Expect(os.WriteFile(path, []byte("metrics: {labelEvaluationLimits: {maxResultBytes: 32}}"), 0o600)).To(Succeed())
		cfg, err := loadConfig("", map[string]string{"DSX_WORKLOAD_EXPORTER_CONFIG": path})
		Expect(err).To(Succeed())
		Expect(cfg.Metrics.Limits.MaxResultBytes).To(Equal(32))
		cfg, err = loadConfig("", map[string]string{"DSX_WORKLOAD_EXPORTER_CONFIG": path}, "--config=")
		Expect(err).To(Succeed())
		Expect(cfg.Metrics.Limits).To(Equal(defaultLabelEvaluationLimits))
		for _, setting := range []struct{ flag, env string }{
			{"--label-eval-cost-limit", "DSX_WORKLOAD_EXPORTER_LABEL_EVAL_COST_LIMIT"},
			{"--label-eval-max-expression-bytes", "DSX_WORKLOAD_EXPORTER_LABEL_EVAL_MAX_EXPRESSION_BYTES"},
			{"--label-eval-max-result-bytes", "DSX_WORKLOAD_EXPORTER_LABEL_EVAL_MAX_RESULT_BYTES"},
		} {
			_, err = loadConfig("", map[string]string{setting.env: "7"}, setting.flag+"=")
			Expect(err).To(HaveOccurred())
		}
	})
})

var _ = Describe("config rejects invalid input", func() {
	It("identifies the invalid label rule by its grouped path", func() {
		_, err := loadConfig(`metrics: {labelRules: [{name: bad-name}]}`, nil)
		Expect(err).To(MatchError(`metrics.labelRules[0]: invalid label name "bad-name"`))
	})

	It("distinguishes malformed and nonpositive API timeouts", func() {
		cfg := DefaultConfig()
		cfg.Tuning.APIClient.RequestTimeout = "30"
		err := cfg.Validate()
		Expect(err).To(MatchError(`tuning.apiClient.requestTimeout: time: missing unit in duration "30"`))
		Expect(errors.Unwrap(err)).To(MatchError(`time: missing unit in duration "30"`))

		for _, timeout := range []string{"0s", "-1s"} {
			cfg.Tuning.APIClient.RequestTimeout = timeout
			Expect(cfg.Validate()).To(MatchError("tuning.apiClient.requestTimeout must be a positive duration"))
		}
	})

	It("rejects invalid files, flags and environment values", func() {
		for _, contents := range []string{
			"metrics: {labelEvaluationLimits: {costLimit: lots}}",
			"metrics: {labelEvaluationLimits: {maxResultBytes: -1}}", "metrics: {labelEvaluationLimits: {maxExpressionBytes: -1}}",
			"metrics: {labelEvaluationLimits: {interruptCheckFrequency: 100}}",
			"metrics: {labelEvaluationLimits: {maxValueBytes: 32}}",
			"metrics: {unknown: {costLimit: 5}}",
			"metrics: {labelRules: [", "metrics: {labelRules: []}\n---\nmetrics: {labelRules: []}", "metrics: {labelRules: []}\n---\n---\nmetrics: {labelRules: []}",
			"metrics: {labelRules: []}\n---\nmetrics: {labelRules: [", "unknown: true",
			"metrics: {labelRules: [{name: team, metrics: [dsx_workload_status], sources: [pod], expression: object.kind}]}",
		} {
			By(contents, func() {
				_, err := loadConfig(contents, nil)
				Expect(err).To(HaveOccurred())
			})
		}
		for _, args := range [][]string{
			{"--unknown-option", "1"},
			{"--config", "/does-not-exist/config.yaml"}, {"--label-eval-cost-limit", "lots"},
			{"--label-eval-max-result-bytes", "-1"}, {"--label-eval-max-expression-bytes", "-1"},
			{"--label-eval-interrupt-check-frequency", "100"},
			{"--label-cost-limit", "5"}, {"--label-max-expression-bytes", "50"},
			{"--label-max-value-bytes", "32"}, {"--label-interrupt-check-frequency", "100"},
		} {
			_, err := loadConfig("", nil, args...)
			Expect(err).To(HaveOccurred())
		}
		for _, env := range []string{
			"DSX_WORKLOAD_EXPORTER_LABEL_EVAL_COST_LIMIT",
			"DSX_WORKLOAD_EXPORTER_LABEL_EVAL_MAX_EXPRESSION_BYTES",
			"DSX_WORKLOAD_EXPORTER_LABEL_EVAL_MAX_RESULT_BYTES",
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
		Expect(names).To(ConsistOf("config", "label-eval-cost-limit", "label-eval-max-expression-bytes", "label-eval-max-result-bytes", "api-qps", "api-burst", "workers", "pod-workers", "workload-workers", "request-timeout"))
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
