// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v3"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelevaluator"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelresolver"
)

var _ = Describe("chart runtime configuration", func() {
	DescribeTable("keeps workload observation permissions independent of cluster definitions", func(clusterEnabled string) {
		helm, err := exec.LookPath("helm")
		if err != nil {
			Skip("helm is needed for chart rendering")
		}
		out, err := exec.Command(helm, "template", "runtime", filepath.Join("..", "..", "..", "charts", "karta"),
			"--set", "exporter.enabled=true",
			"--set", "exporter.kartaDefinitionSource.cluster.enabled="+clusterEnabled,
		).CombinedOutput()
		Expect(err).To(Succeed(), string(out))
		decoder := yaml.NewDecoder(bytes.NewReader(out))
		var watchedResources []string
		bound := false
		for {
			var doc struct {
				Kind     string `yaml:"kind"`
				Metadata struct {
					Name string `yaml:"name"`
				} `yaml:"metadata"`
				Rules []struct {
					Resources []string `yaml:"resources"`
					Verbs     []string `yaml:"verbs"`
				} `yaml:"rules"`
			}
			err = decoder.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			Expect(err).To(Succeed())
			if doc.Metadata.Name != "karta-metrics-exporter-default" {
				continue
			}
			if doc.Kind == "ClusterRoleBinding" {
				bound = true
			}
			if doc.Kind != "ClusterRole" {
				continue
			}
			for _, rule := range doc.Rules {
				Expect(rule.Verbs).To(ConsistOf("list", "watch"))
				Expect(rule.Resources).NotTo(ContainElement("*"))
				watchedResources = append(watchedResources, rule.Resources...)
			}
		}
		Expect(bound).To(BeTrue())
		Expect(watchedResources).To(ContainElements("pods", "namespaces", "deployments", "replicasets"))
		if clusterEnabled == "true" {
			Expect(watchedResources).To(ContainElement("kartas"))
		} else {
			Expect(watchedResources).NotTo(ContainElement("kartas"))
		}
	}, Entry("cluster definitions enabled", "true"), Entry("cluster definitions disabled", "false"))

	It("loads and compiles the configuration rendered by Helm", func() {
		helm, err := exec.LookPath("helm")
		if err != nil {
			Skip("helm is needed for chart rendering")
		}
		chart := filepath.Join("..", "..", "..", "charts", "karta")
		rules := []labelresolver.LabelRule{{
			Name:       "team",
			Metrics:    []string{"karta_pod_workload_info"},
			Sources:    []labelresolver.Source{labelresolver.SourceWorkload, labelresolver.SourcePod},
			Expression: `has(object.metadata.labels) && "team" in object.metadata.labels ? object.metadata.labels["team"] : ""`,
		}}
		raw, err := json.Marshal(rules)
		Expect(err).To(Succeed())
		out, err := exec.Command(helm, "template", "labels", chart,
			"--set", "exporter.enabled=true",
			"--set-json", "exporter.labelRules="+string(raw),
			"--set", "exporter.genericLabelEvaluationLimits.maxResultBytes=32",
			"--set", "exporter.workers=3",
			"--set", "exporter.leafWorkers=4",
			"--set", "exporter.rootWorkers=5",
		).CombinedOutput()
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
			Expect(cfg.Limits.MaxResultBytes).To(Equal(32))
			Expect(cfg.Workers).To(Equal(3))
			Expect(cfg.LeafWorkers).To(Equal(4))
			Expect(cfg.RootWorkers).To(Equal(5))
			_, err = labelresolver.New(cfg.Config, labelresolver.Dependencies{Compiler: labelevaluator.NewCompiler()})
			Expect(err).To(Succeed())
		}
		Expect(found).To(BeTrue(), "rendered exporter config was not found")
	})
})
