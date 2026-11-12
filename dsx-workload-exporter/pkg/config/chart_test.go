// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/controller"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelevaluator"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

var _ = Describe("chart label configuration", func() {
	It("loads the documented Pod association Helm example", func() {
		document, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "dsx-workload-exporter", "configuration.md"))
		Expect(err).To(Succeed())
		_, example, found := bytes.Cut(document, []byte("<summary>Complete Helm values for Pod association</summary>"))
		Expect(found).To(BeTrue(), "Pod association Helm example summary not found in configuration.md")
		example, _, found = bytes.Cut(example, []byte("</details>"))
		Expect(found).To(BeTrue(), "Pod association Helm example closing details tag not found")
		_, values, found := bytes.Cut(example, []byte("```yaml\n"))
		Expect(found).To(BeTrue(), "Pod association Helm example opening YAML fence not found")
		values, _, found = bytes.Cut(values, []byte("\n```"))
		Expect(found).To(BeTrue(), "Pod association Helm example closing YAML fence not found")
		for _, raw := range chartDocuments(GinkgoT(), renderExporterChart(GinkgoT(), string(values))) {
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
			Expect(cfg.PodWorkloadAssociation).NotTo(BeNil())
			Expect(cfg.PodWorkloadAssociation.Target.NameFromLabel).To(Equal("platform.example.com/workload-name"))
			Expect(cfg.Rules).To(HaveLen(1))
			_, err = labelevaluator.NewCompiler().Compile(labelevaluator.Config{Expression: cfg.Rules[0].Expression, Limits: cfg.Limits})
			Expect(err).To(Succeed())
			return
		}
		Fail("exporter config not found")
	})

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
				"--set-json", `exporter.objectSelectors=[{"selector":{"matchLabels":{"environment":"production"}}},{"apiVersion":"scheduling.example.org/v1","kind":"Workload","selector":{"matchLabels":{"app.kubernetes.io/managed-by":"example-platform"}}}]`,
				"--set-json", "exporter.labelRules=" + string(raw),
				"--set-json", `exporter.workloadMetricSelector={"matchExpressions":[{"key":"example.org/group","operator":"DoesNotExist"}]}`,
				"--set-json", `exporter.podWorkloadAssociation={"target":{"apiVersion":"scheduling.example.org/v1","kind":"Workload","nameFromLabel":"example.org/group"}}`,
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
				Expect(cfg.ObjectSelectors).To(Equal([]controller.ObjectSelector{{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"environment": "production"}}}, {APIVersion: "scheduling.example.org/v1", Kind: "Workload", Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/managed-by": "example-platform"}}}}))
				Expect(cfg.Limits).To(Equal(tc.limits))
				Expect(cfg.WorkloadMetricSelector).To(Equal(&metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "example.org/group", Operator: metav1.LabelSelectorOpDoesNotExist}}}))
				Expect(cfg.PodWorkloadAssociation).To(Equal(&controller.PodWorkloadAssociation{Target: controller.AssociationTarget{APIVersion: "scheduling.example.org/v1", Kind: "Workload", NameFromLabel: "example.org/group"}}))
				for _, rule := range cfg.Rules {
					_, err = labelevaluator.NewCompiler().Compile(labelevaluator.Config{Expression: rule.Expression, Limits: cfg.Limits})
					Expect(err).To(Succeed())
				}
			}
			Expect(found).To(BeTrue(), "rendered exporter config was not found")
		})
	}
})

var _ = Describe("chart status exclusions", func() {
	DescribeTable("preserves default and explicit lists",
		func(values string, want []v1alpha1.ResourceStatus) {
			out := renderExporterChart(GinkgoT(), values)
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
				Expect(cfg.ExcludeStatuses).To(Equal(want))
				return
			}
			Fail("exporter config not found")
		},
		Entry("default", "", []v1alpha1.ResourceStatus{v1alpha1.SuspendingStatus, v1alpha1.ResumingStatus}),
		Entry("all phases", "exporter:\n  excludeStatuses: []\n", []v1alpha1.ResourceStatus{}),
		Entry("custom exclusions", "exporter:\n  excludeStatuses: [Running]\n", []v1alpha1.ResourceStatus{v1alpha1.RunningStatus}),
	)
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
			"--set-json", `exporter.objectSelectors=[{"selector":{"matchLabels":{"environment":"production"}}},{"apiVersion":"scheduling.example.org/v1","kind":"Workload","selector":{"matchLabels":{"app.kubernetes.io/managed-by":"example-platform"}}}]`,
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
