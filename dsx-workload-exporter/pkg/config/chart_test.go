// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v3"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelevaluator"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelresolver"
)

var _ = Describe("chart label configuration", func() {
	for _, tc := range []struct {
		name   string
		args   []string
		limits labelevaluator.Limits
	}{
		{name: "defaults", limits: defaultLabelEvaluationLimits},
		{
			name: "overrides",
			args: []string{
				"--set", "exporter.genericLabelEvaluationLimits.costLimit=500",
				"--set", "exporter.genericLabelEvaluationLimits.maxExpressionBytes=512",
				"--set", "exporter.genericLabelEvaluationLimits.maxResultBytes=32",
			},
			limits: labelevaluator.Limits{CostLimit: 500, MaxExpressionBytes: 512, MaxResultBytes: 32},
		},
	} {
		It("loads and compiles the configuration rendered by Helm with "+tc.name, func() {
			helm, err := exec.LookPath("helm")
			if err != nil {
				Skip("helm is needed for chart rendering")
			}
			chart := filepath.Join("..", "..", "..", "charts", "karta")
			rules := []labelresolver.LabelRule{{
				Name:       "team",
				Metrics:    []string{"dsx_workload_pod_info"},
				Sources:    []labelresolver.Source{labelresolver.SourceWorkload, labelresolver.SourcePod},
				Expression: `has(object.metadata.labels) && "team" in object.metadata.labels ? object.metadata.labels["team"] : ""`,
			}}
			raw, err := json.Marshal(rules)
			Expect(err).To(Succeed())
			args := []string{"template", "labels", chart,
				"--set", "exporter.enabled=true",
				"--set-json", "exporter.labelRules=" + string(raw),
			}
			out, err := exec.Command(helm, append(args, tc.args...)...).CombinedOutput()
			Expect(err).To(Succeed(), string(out))
			decoder := yaml.NewDecoder(bytes.NewReader(out))
			found := false
			for {
				var doc struct {
					Kind string            `yaml:"kind"`
					Data map[string]string `yaml:"data"`
				}
				err = decoder.Decode(&doc)
				if errors.Is(err, io.EOF) {
					break
				}
				Expect(err).To(Succeed())
				if doc.Kind != "ConfigMap" || doc.Data["config.yaml"] == "" {
					continue
				}
				found = true
				cfg, err := loadConfig(doc.Data["config.yaml"], nil)
				Expect(err).To(Succeed())
				Expect(cfg.Rules).To(Equal(rules))
				Expect(cfg.Limits).To(Equal(tc.limits))
				for _, rule := range cfg.Rules {
					_, err = labelevaluator.NewCompiler().Compile(labelevaluator.Config{Expression: rule.Expression, Limits: cfg.Limits})
					Expect(err).To(Succeed())
				}
			}
			Expect(found).To(BeTrue(), "rendered exporter config was not found")
		})
	}
})

var _ = Describe("chart definition source configuration", func() {
	for _, tc := range []struct {
		name         string
		values       string
		catalog      bool
		experimental bool
	}{
		{name: "defaults", catalog: true},
		{name: "catalog disabled", values: "exporter:\n  kartaDefinitionSource:\n    catalog:\n      enabled: false\n"},
		{name: "experimental enabled", values: "exporter:\n  kartaDefinitionSource:\n    enableExperimental: true\n", catalog: true, experimental: true},
	} {
		It("loads definition sources with "+tc.name, func() {
			out := renderExporterChart(GinkgoT(), tc.values)
			for _, raw := range chartDocuments(GinkgoT(), out) {
				var doc struct {
					Kind string            `yaml:"kind"`
					Data map[string]string `yaml:"data"`
				}
				Expect(yaml.Unmarshal(raw, &doc)).To(Succeed())
				if doc.Kind != "ConfigMap" || doc.Data["config.yaml"] == "" {
					continue
				}
				cfg, err := loadConfig(doc.Data["config.yaml"], nil)
				Expect(err).To(Succeed())
				Expect(cfg.KartaDefinitionSource.Catalog.Enabled).To(Equal(tc.catalog))
				Expect(cfg.KartaDefinitionSource.Cluster.Enabled).To(BeTrue())
				Expect(cfg.KartaDefinitionSource.EnableExperimental).To(Equal(tc.experimental))
				return
			}
			Fail("exporter config not found")
		})
	}

	It("refuses to render a cluster source enabled flag that is not a boolean", func() {
		helm, err := exec.LookPath("helm")
		if err != nil {
			Skip("helm is needed for chart rendering")
		}
		chart := filepath.Join("..", "..", "..", "charts", "karta")
		out, err := exec.Command(helm, "template", "sources", chart,
			"--set", "exporter.enabled=true",
			"--set", "exporter.kartaDefinitionSource.cluster.enabled=null",
		).CombinedOutput()
		Expect(err).To(HaveOccurred())
		Expect(string(out)).To(ContainSubstring("exporter.kartaDefinitionSource.cluster.enabled must be true or false"))
	})
})

var _ = Describe("chart exporter ServiceMonitor", func() {
	for _, tc := range []struct {
		name     string
		values   string
		args     []string
		enabled  bool
		override string
		endpoint map[string]any
	}{
		{name: "disabled by default"},
		{name: "exporter disabled", args: []string{"--set", "exporter.enabled=false", "--set", "exporter.serviceMonitor.enabled=true"}},
		{name: "exporter null", args: []string{"--set", "exporter=null"}},
		{name: "monitor null", args: []string{"--set-json", "exporter.serviceMonitor=null"}},
		{
			name:     "enabled with inherited scrape settings",
			args:     []string{"--set", "exporter.serviceMonitor.enabled=true"},
			enabled:  true,
			endpoint: map[string]any{"port": "metrics", "path": "/metrics", "honorLabels": true},
		},
		{
			name: "enabled with overrides",
			values: `exporter:
  serviceMonitor:
    enabled: true
    additionalLabels:
      release: monitoring
      app.kubernetes.io/instance: forbidden
    interval: 30s
    scrapeTimeout: 10s
`,
			args:     []string{"--set", "fullnameOverride=custom"},
			enabled:  true,
			override: "custom-",
			endpoint: map[string]any{"port": "metrics", "path": "/metrics", "honorLabels": true, "interval": "30s", "scrapeTimeout": "10s"},
		},
	} {
		It(tc.name, func() {
			args := append([]string{"--namespace", "observability"}, tc.args...)
			out := renderExporterChart(GinkgoT(), tc.values, args...)
			found := false
			serviceLabels := map[string]string{}
			selector := map[string]any{
				"app.kubernetes.io/name":     "dsx-workload-exporter",
				"app.kubernetes.io/instance": "install",
			}
			for _, raw := range chartDocuments(GinkgoT(), out) {
				var doc struct {
					APIVersion string `yaml:"apiVersion"`
					Kind       string `yaml:"kind"`
					Metadata   struct {
						Name      string            `yaml:"name"`
						Namespace string            `yaml:"namespace"`
						Labels    map[string]string `yaml:"labels"`
					} `yaml:"metadata"`
					Spec map[string]any `yaml:"spec"`
				}
				Expect(yaml.Unmarshal(raw, &doc)).To(Succeed())
				if doc.Metadata.Name != tc.override+"dsx-workload-exporter" {
					continue
				}
				if doc.Kind == "Service" {
					serviceLabels = doc.Metadata.Labels
				}
				if doc.Kind != "ServiceMonitor" {
					continue
				}
				found = true
				Expect(doc.APIVersion).To(Equal("monitoring.coreos.com/v1"))
				Expect(doc.Metadata.Namespace).To(Equal("observability"))
				Expect(doc.Metadata.Labels["app.kubernetes.io/instance"]).To(Equal("install"))
				if tc.override != "" {
					Expect(doc.Metadata.Labels["release"]).To(Equal("monitoring"))
				}
				Expect(doc.Spec).To(Equal(map[string]any{
					"selector":  map[string]any{"matchLabels": selector},
					"endpoints": []any{tc.endpoint},
				}))
			}
			Expect(found).To(Equal(tc.enabled))
			if tc.enabled {
				for key, value := range selector {
					Expect(serviceLabels[key]).To(Equal(value), "ServiceMonitor must select the exporter Service")
				}
			}
		})
	}
})

var _ = Describe("chart exporter pprof", func() {
	for _, tc := range []struct {
		name string
		args []string
		flag string
	}{
		{name: "disabled by default"},
		{name: "enabled on the default port", args: []string{"--set", "exporter.pprof.enabled=true"}, flag: "--pprof-bind-address=127.0.0.1:6060"},
		{name: "enabled on a custom port", args: []string{"--set", "exporter.pprof.enabled=true", "--set", "exporter.pprof.port=7070"}, flag: "--pprof-bind-address=127.0.0.1:7070"},
		{name: "null", args: []string{"--set-json", "exporter.pprof=null"}},
	} {
		It(tc.name, func() {
			out := string(renderExporterChart(GinkgoT(), "", tc.args...))
			if tc.flag == "" {
				Expect(out).NotTo(ContainSubstring("--pprof-bind-address"))
				return
			}
			// Only on localhost: no Service exposes the profiles.
			Expect(out).To(ContainSubstring(tc.flag))
		})
	}
})

var _ = Describe("chart exporter resources", func() {
	for _, tc := range []struct {
		name  string
		args  []string
		kinds []string
	}{
		{name: "defaults"},
		{name: "disabled", args: []string{"--set", "exporter.enabled=false"}},
		{name: "null", args: []string{"--set-json", "exporter=null"}},
		{
			name:  "enabled",
			args:  []string{"--set", "exporter.enabled=true"},
			kinds: []string{"ServiceAccount", "ClusterRole", "ClusterRoleBinding", "ConfigMap", "Deployment", "Service"},
		},
	} {
		It("renders the expected resources with "+tc.name, func() {
			helm, err := exec.LookPath("helm")
			if err != nil {
				Skip("helm is needed for chart rendering")
			}
			chart := filepath.Join("..", "..", "..", "charts", "karta")
			args := append([]string{"template", "resources", chart}, tc.args...)
			out, err := exec.Command(helm, args...).CombinedOutput()
			Expect(err).To(Succeed(), string(out))
			decoder := yaml.NewDecoder(bytes.NewReader(out))
			var kinds []string
			for {
				var doc struct {
					Kind     string `yaml:"kind"`
					Metadata struct {
						Labels map[string]string `yaml:"labels"`
					} `yaml:"metadata"`
				}
				err = decoder.Decode(&doc)
				if errors.Is(err, io.EOF) {
					break
				}
				Expect(err).To(Succeed())
				if doc.Metadata.Labels["app.kubernetes.io/name"] == "dsx-workload-exporter" {
					kinds = append(kinds, doc.Kind)
				}
			}
			Expect(kinds).To(ConsistOf(tc.kinds))
		})
	}
})

var _ = Describe("chart exporter metrics Service", func() {
	for _, enabled := range []bool{true, false} {
		It(fmt.Sprintf("keeps diagnostics reachable with exporter.enabled=%t", enabled), func() {
			helm, err := exec.LookPath("helm")
			if err != nil {
				Skip("helm is needed for chart rendering")
			}
			chart := filepath.Join("..", "..", "..", "charts", "karta")
			out, err := exec.Command(helm, "template", "metrics", chart,
				"--set", fmt.Sprintf("exporter.enabled=%t", enabled),
			).CombinedOutput()
			Expect(err).To(Succeed(), string(out))
			decoder := yaml.NewDecoder(bytes.NewReader(out))
			found := false
			for {
				var doc struct {
					Kind     string `yaml:"kind"`
					Metadata struct {
						Name string `yaml:"name"`
					} `yaml:"metadata"`
					Spec yaml.Node `yaml:"spec"`
				}
				err = decoder.Decode(&doc)
				if errors.Is(err, io.EOF) {
					break
				}
				Expect(err).To(Succeed())
				if doc.Kind != "Service" || doc.Metadata.Name != "dsx-workload-exporter" {
					continue
				}
				found = true
				var spec struct {
					PublishNotReadyAddresses bool              `yaml:"publishNotReadyAddresses"`
					Selector                 map[string]string `yaml:"selector"`
					Ports                    []struct {
						Name       string `yaml:"name"`
						Port       int    `yaml:"port"`
						TargetPort string `yaml:"targetPort"`
					} `yaml:"ports"`
				}
				Expect(doc.Spec.Decode(&spec)).To(Succeed())
				Expect(spec.PublishNotReadyAddresses).To(BeTrue(), "diagnostic metrics must remain reachable when the exporter is unready")
				Expect(spec.Selector).To(Equal(map[string]string{
					"app.kubernetes.io/name":     "dsx-workload-exporter",
					"app.kubernetes.io/instance": "metrics",
				}))
				Expect(spec.Ports).To(HaveLen(1))
				Expect(spec.Ports[0].Name).To(Equal("metrics"))
				Expect(spec.Ports[0].Port).To(Equal(8080))
				Expect(spec.Ports[0].TargetPort).To(Equal("metrics"))
			}
			Expect(found).To(Equal(enabled), "exporter Service must only be rendered when enabled")
		})
	}
})

var _ = Describe("chart exporter GOMEMLIMIT", func() {
	for _, tc := range []struct {
		name    string
		args    []string
		want    string
		failure string
	}{
		{name: "auto with the default 4Gi limit", want: "3865470566"},
		{name: "auto with a binary limit", args: []string{"--set", "exporter.resources.limits.memory=2.5Gi"}, want: "2415919104"},
		{name: "auto with a decimal limit", args: []string{"--set", "exporter.resources.limits.memory=1500M"}, want: "1350000000"},
		{name: "auto with a byte limit", args: []string{"--set-json", "exporter.resources.limits.memory=4294967296"}, want: "3865470566"},
		{name: "auto with a decimal exponent limit", args: []string{"--set-string", "exporter.resources.limits.memory=4e9"}, want: "3600000000"},
		{name: "auto with a padded exponent limit", args: []string{"--set-string", "exporter.resources.limits.memory=4e009"}, want: "3600000000"},
		{name: "auto with a micro limit", args: []string{"--set-string", "exporter.resources.limits.memory=4294967296000000u"}, want: "3865470566"},
		{name: "auto with a fractional limit", args: []string{"--set-string", "exporter.resources.limits.memory=.5Gi"}, want: "483183820"},
		{name: "auto without a memory limit", args: []string{"--set-json", "exporter.resources.limits=null"}},
		// Zero means unlimited; GOMEMLIMIT=0 would be a zero-byte Go limit.
		{name: "auto with a zero request and limit", args: []string{"--set-string", "exporter.resources.requests.memory=0",
			"--set-string", "exporter.resources.limits.memory=0"}},
		{name: "auto with a numeric zero limit", args: []string{"--set-json", "exporter.resources.limits.memory=0"}},
		{name: "an explicit value", args: []string{"--set", "exporter.goMemLimit=3600MiB"}, want: "3600MiB"},
		{name: "an empty value", args: []string{"--set", "exporter.goMemLimit="}},
		{name: "an unreadable limit", args: []string{"--set", "exporter.resources.limits.memory=4Gx"},
			failure: `cannot read exporter.resources.limits.memory "4Gx"`},
		{name: "an out-of-range exponent", args: []string{"--set-string", "exporter.resources.limits.memory=4e99"},
			failure: `cannot read exporter.resources.limits.memory "4e99"`},
	} {
		It("renders "+tc.name, func() {
			helm, err := exec.LookPath("helm")
			if err != nil {
				Skip("helm is needed for chart rendering")
			}
			chart := filepath.Join("..", "..", "..", "charts", "karta")
			args := append([]string{"template", "memlimit", chart, "--set", "exporter.enabled=true",
				"--show-only", "templates/exporter-deployment.yaml"}, tc.args...)
			out, err := exec.Command(helm, args...).CombinedOutput()
			if tc.failure != "" {
				Expect(err).To(HaveOccurred())
				Expect(string(out)).To(ContainSubstring(tc.failure))
				return
			}
			Expect(err).To(Succeed(), string(out))
			var deployment struct {
				Spec struct {
					Template struct {
						Spec struct {
							Containers []struct {
								Env []struct {
									Name  string `yaml:"name"`
									Value string `yaml:"value"`
								} `yaml:"env"`
							} `yaml:"containers"`
						} `yaml:"spec"`
					} `yaml:"template"`
				} `yaml:"spec"`
			}
			Expect(yaml.Unmarshal(out, &deployment)).To(Succeed())
			Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
			got := ""
			for _, env := range deployment.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "GOMEMLIMIT" {
					got = env.Value
				}
			}
			Expect(got).To(Equal(tc.want))
		})
	}
})
