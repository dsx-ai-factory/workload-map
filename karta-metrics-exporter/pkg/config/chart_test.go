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
				Metrics:    []string{"karta_pod_workload_info"},
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
