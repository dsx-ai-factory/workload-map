// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package config

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelevaluator"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelresolver"
)

var _ = Describe("configured evaluator bounds", func() {
	It("applies the configured value limit through the resolver", func() {
		cfg, err := loadConfig(`labelRules:
- name: team
  metrics: [karta_workload_status]
  sources: [workload]
  expression: object.metadata.labels.team
genericLabelEvaluationLimits:
  maxResultBytes: 3
`, nil)
		Expect(err).To(Succeed())
		resolver, err := labelresolver.New(cfg.Config, labelresolver.Dependencies{Compiler: labelevaluator.NewCompiler()})
		Expect(err).To(Succeed())
		object := &unstructured.Unstructured{}
		object.SetLabels(map[string]string{"team": "long"})
		results, err := resolver.Resolve(context.Background(), labelresolver.Input{
			Metrics: []string{labelresolver.MetricWorkloadStatus}, Workload: object,
		})
		Expect(err).To(Succeed())
		Expect(results[0].Labels).To(BeEmpty())
		Expect(results[0].Errors["team"]).To(MatchError(labelevaluator.ErrValueTooLong))
	})
})

var _ = Describe("runtime configuration", func() {
	It("keeps the shared worker default when queue overrides are unset or zero", func() {
		cfg, err := loadConfig("", nil)
		Expect(err).To(Succeed())
		Expect(cfg.Workers).To(Equal(2))
		Expect(cfg.LeafWorkers).To(BeZero())
		Expect(cfg.RootWorkers).To(BeZero())

		cfg, err = loadConfig("workers: 4\nleafWorkers: 7\nrootWorkers: 8", map[string]string{
			"KARTA_EXPORTER_LEAF_WORKERS": "0",
		}, "--root-workers=0")
		Expect(err).To(Succeed())
		Expect(cfg.Workers).To(Equal(4))
		Expect(cfg.LeafWorkers).To(BeZero())
		Expect(cfg.RootWorkers).To(BeZero())
	})
	It("loads queue worker overrides with flag, environment and file precedence", func() {
		cfg, err := loadConfig("workers: 3\nleafWorkers: 4\nrootWorkers: 5", nil)
		Expect(err).To(Succeed())
		Expect(cfg.Workers).To(Equal(3))
		Expect(cfg.LeafWorkers).To(Equal(4))
		Expect(cfg.RootWorkers).To(Equal(5))

		cfg, err = loadConfig("workers: 3\nleafWorkers: 4\nrootWorkers: 5", map[string]string{
			"KARTA_EXPORTER_LEAF_WORKERS": "6",
			"KARTA_EXPORTER_ROOT_WORKERS": "7",
		})
		Expect(err).To(Succeed())
		Expect(cfg.Workers).To(Equal(3))
		Expect(cfg.LeafWorkers).To(Equal(6))
		Expect(cfg.RootWorkers).To(Equal(7))

		cfg, err = loadConfig("workers: 3\nleafWorkers: 4\nrootWorkers: 5", map[string]string{
			"KARTA_EXPORTER_LEAF_WORKERS": "6",
			"KARTA_EXPORTER_ROOT_WORKERS": "7",
		}, "--leaf-workers=8", "--root-workers=9")
		Expect(err).To(Succeed())
		Expect(cfg.Workers).To(Equal(3))
		Expect(cfg.LeafWorkers).To(Equal(8))
		Expect(cfg.RootWorkers).To(Equal(9))
	})
	DescribeTable("rejects invalid queue worker overrides", func(key, flagName, envName string) {
		_, err := loadConfig(key+": -1", nil)
		Expect(err).To(MatchError("leafWorkers and rootWorkers must not be negative"))
		for _, value := range []string{"-1", "many", ""} {
			_, err = loadConfig("", nil, "--"+flagName+"="+value)
			Expect(err).To(HaveOccurred())
			_, err = loadConfig("", map[string]string{envName: value})
			Expect(err).To(HaveOccurred())
		}
	},
		Entry("leaf", "leafWorkers", "leaf-workers", "KARTA_EXPORTER_LEAF_WORKERS"),
		Entry("root", "rootWorkers", "root-workers", "KARTA_EXPORTER_ROOT_WORKERS"),
	)
	It("loads resource filters and bounded worker/API settings with flag precedence", func() {
		cfg, err := loadConfig("resourceRules:\n- apiGroups: [batch]\n  apiVersions: [v1]\n  resources: [jobs]\napiQPS: 30\nworkers: 3\n", map[string]string{"KARTA_EXPORTER_API_QPS": "40"}, "--api-qps=50", "--api-burst=100")
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.APIQPS).To(Equal(float32(50)))
		Expect(cfg.APIBurst).To(Equal(100))
		Expect(cfg.Workers).To(Equal(3))
		Expect(cfg.ResourceRules).To(HaveLen(1))
	})
	It("validates included rules before excluded rules with combined indices", func() {
		_, err := loadConfig("resourceRules: [{apiGroups: [batch], apiVersions: [v1], resources: [jobs]}]\nexcludeResourceRules: [{}]", nil)
		Expect(err).To(MatchError("resource rule 1 requires apiGroups, apiVersions and resources"))

		_, err = loadConfig("resourceRules: [{apiGroups: [batch], apiVersions: [v1], resources: [jobs/status]}]\nexcludeResourceRules: [{}]", nil)
		Expect(err).To(MatchError("resource rules must name whole resources or *"))
	})
	It("loads Kubernetes selectors through their JSON field names", func() {
		cfg, err := loadConfig("namespaceSelector:\n  matchLabels: {team: blue}\npodSelector:\n  matchExpressions:\n  - key: app\n    operator: In\n    values: [worker]", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.NamespaceSelector.MatchLabels).To(HaveKeyWithValue("team", "blue"))
		Expect(cfg.PodSelector.MatchExpressions).To(HaveLen(1))
		Expect(cfg.PodSelector.MatchExpressions[0].Values).To(Equal([]string{"worker"}))
	})
	DescribeTable("rejects non-finite API rates before creating clients", func(value string) {
		_, err := loadConfig("", nil, "--api-qps="+value)
		Expect(err).To(MatchError("apiQPS must be finite and positive"))
		_, err = loadConfig("", map[string]string{"KARTA_EXPORTER_API_QPS": value})
		Expect(err).To(MatchError("apiQPS must be finite and positive"))
	},
		Entry("NaN", "NaN"),
		Entry("positive infinity", "+Inf"),
		Entry("negative infinity", "-Inf"),
	)
	It("rejects malformed resources and unbounded runtime settings", func() {
		for _, body := range []string{"apiQPS: 0", "apiBurst: 0", "workers: 0", "maxAge: 1s", "resourceRules: [{apiGroups: [batch], apiVersions: [v1], resources: [jobs/status]}]", "requiredKinds: [{kind: Job}]"} {
			_, err := loadConfig(body, nil)
			Expect(err).To(HaveOccurred(), body)
		}
	})
})

var _ = Describe("public kind strings", func() {
	It("parses core and grouped slash strings and preserves their JSON representation", func() {
		cfg, err := loadConfig("ignoredOwnerKinds: [example.com/v1/Wrapper, v1/Node]\nrequiredKinds: [apps/v1/Deployment, v1/Node]", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.IgnoredOwnerKinds).To(Equal(Kinds{{Group: "example.com", Version: "v1", Kind: "Wrapper"}, {Version: "v1", Kind: "Node"}}))
		Expect(cfg.RequiredKinds[0]).To(Equal(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}))
		encoded, err := json.Marshal(cfg.IgnoredOwnerKinds)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(Equal(`["example.com/v1/Wrapper","v1/Node"]`))
	})
	It("rejects missing segments and wildcard kinds", func() {
		for _, value := range []string{"Node", "/v1/Node", "apps//Deployment", "apps/v1/", "apps/v1/Deployment/extra", "*/v1/Deployment", "apps/*/Deployment", "v1/*", "apps/v 1/Deployment"} {
			_, err := loadConfig("requiredKinds: ['"+value+"']", nil)
			Expect(err).To(HaveOccurred(), value)
		}
	})
})
