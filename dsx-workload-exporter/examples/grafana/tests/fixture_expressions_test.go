// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package grafana

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v3"
)

type fixtureBinding struct {
	key        fixtureKey
	expression string
	line       int
}

type fixtureKey struct {
	dashboard string
	panel     int
	ref       string
}

var fixtureMarkerPattern = regexp.MustCompile(`(?m)^# dashboard-expression: (\S+) (\d+) (\S+)$`)

var fixtureVariables = map[string]string{
	"exporter_job": "exporter", "namespace": "ns", "workload_uid": "multi",
	"dcgm_job": "dcgm", "cadvisor_job": "cadvisor", "observer_job": "observer",
	"workload": ".*", "component": ".*", "component_instance": ".*", "gpu_device": ".*",
	"activity_view": "4", "__range_s": "300", "__interval": "30s", "__rate_interval": "5m", "__to": "90000",
}

var _ = Describe("Fixture expressions", func() {
	expectedKeys := []fixtureKey{
		{"workload-overview.json", 3, "A"},
		{"workload-overview.json", 4, "B"},
		{"workload-overview.json", 10, "A"},
		{"workload-overview.json", 17, "A"},
		{"workload-overview.json", 24, "A"},
		{"workload-overview.json", 102, "A"},
		{"workload-overview.json", 302, "A"},
		{"workload-overview.json", 303, "A"},
		{"workload-overview.json", 304, "A"},
		{"workload-overview.json", 305, "A"},
		{"workload-overview.json", 308, "A"},
		{"workload-overview.json", 310, "A"},
		{"workload-overview.json", 801, "A"},
		{"workload-structure.json", 3, "A"},
		{"workload-structure.json", 4, "gpu"},
		{"workload-structure.json", 4, "framebuffer"},
		{"workload-structure.json", 4, "cpu"},
		{"workload-structure.json", 4, "memory"},
		{"workload-structure.json", 4, "layoutLeaves"},
		{"workload-structure.json", 4, "layoutAncestors"},
		{"workload-structure.json", 9, "A"},
	}
	var bindings map[fixtureKey]fixtureBinding
	var locations map[fixtureKey]string

	BeforeEach(func() {
		paths, err := filepath.Glob("*.test.yaml")
		Expect(err).NotTo(HaveOccurred())
		Expect(paths).NotTo(BeEmpty(), "PromQL fixtures are missing")
		bindings = make(map[fixtureKey]fixtureBinding)
		locations = make(map[fixtureKey]string)
		for _, path := range paths {
			data, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred(), "read fixture %s", path)
			parsed, err := fixtureBindings(data)
			Expect(err).NotTo(HaveOccurred(), "parse fixture %s", path)
			for _, binding := range parsed {
				key := binding.key
				Expect(bindings).NotTo(HaveKey(key), "%s:%d: duplicate fixture marker; first seen at %s", path, binding.line, locations[key])
				Expect(dashboardUIDs).To(HaveKey(key.dashboard), "unknown dashboard in fixture marker at %s:%d", path, binding.line)
				bindings[key] = binding
				locations[key] = fmt.Sprintf("%s:%d", path, binding.line)
			}
		}
	})

	It("preserves all 21 core, resource, layout, GPU activity and observer query bindings", func() {
		var keys []fixtureKey
		for key := range bindings {
			keys = append(keys, key)
		}
		Expect(keys).To(ConsistOf(expectedKeys))
	})

	for _, key := range expectedKeys {
		It(fmt.Sprintf("matches %s/panel-%d/%s", key.dashboard, key.panel, key.ref), func() {
			Expect(bindings).To(HaveKey(key), "dashboard query needs a fixture binding")
			var expressions []string
			for _, obj := range readDashboard(key.dashboard).objects {
				if obj["id"] != float64(key.panel) {
					continue
				}
				for _, target := range objects(obj["targets"]) {
					if target["refId"] == key.ref {
						expression, ok := target["expr"].(string)
						Expect(ok).To(BeTrue(), "target needs a PromQL expression")
						expressions = append(expressions, expression)
					}
				}
			}
			Expect(expressions).To(HaveLen(1), "marker must identify exactly one dashboard query")
			actual := fixtureExpression(expressions[0])
			Expect(strings.Join(strings.Fields(actual), " ")).To(Equal(strings.Join(strings.Fields(bindings[key].expression), " ")), "fixture query drift at %s", locations[key])
		})
	}
})

var _ = Describe("Fixture bindings YAML", func() {
	It("resolves marked aliases without binding unmarked aliases", func() {
		data := []byte(`tests:
  - promql_expr_test:
      # dashboard-expression: workload-overview.json 3 A
      - expr: &query |-
          sum(
            up{job="exporter"}
          )
      - expr: *query
      # dashboard-expression: workload-structure.json 3 A
      - expr: *query
`)
		bindings, err := fixtureBindings(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(bindings).To(HaveLen(2), "unmarked aliases do not create bindings")
		Expect(bindings[0].key).To(Equal(fixtureKey{"workload-overview.json", 3, "A"}))
		Expect(bindings[1].key).To(Equal(fixtureKey{"workload-structure.json", 3, "A"}))
		Expect(bindings[0].expression).To(Equal("sum(\n  up{job=\"exporter\"}\n)"))
		Expect(bindings[1].expression).To(Equal(bindings[0].expression), "YAML aliases must resolve to their expression")
	})

	DescribeTable("rejects invalid markers", func(data string) {
		_, err := fixtureBindings([]byte(data))
		Expect(err).To(HaveOccurred())
	},
		Entry("missing expression", "# dashboard-expression: workload-overview.json 3 A\n- eval_time: 0s\n"),
		Entry("invalid marker", "# dashboard-expression: workload-overview.json wrong A\n- expr: up\n"),
		Entry("two markers", "# dashboard-expression: workload-overview.json 3 A\n# dashboard-expression: workload-overview.json 4 A\n- expr: up\n"),
		Entry("misplaced marker", "- expr: up # dashboard-expression: workload-overview.json 3 A\n"),
	)
})

var _ = Describe("Fixture expression variables", func() {
	It("substitutes supported Grafana variables without changing label replacements or longer names", func() {
		expression := `${__to:date:seconds} ${__to} $namespace ${namespace} ${namespace:regex} ${activity_view:raw} $1 $__interval $__interval_ms ${namespace:unknown}`
		Expect(fixtureExpression(expression)).To(Equal(`90 90000 ns ns ns 4 $1 30s $__interval_ms ${namespace:unknown}`))
	})
})

func fixtureBindings(data []byte) ([]fixtureBinding, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse fixture: %w", err)
	}
	var bindings []fixtureBinding
	var visit func(*yaml.Node) error
	visit = func(node *yaml.Node) error {
		comments := node.HeadComment + node.LineComment + node.FootComment
		if strings.Contains(comments, "dashboard-expression:") {
			matches := fixtureMarkerPattern.FindAllStringSubmatch(node.HeadComment, -1)
			if node.Kind != yaml.MappingNode || len(matches) != 1 || strings.Count(comments, "dashboard-expression:") != 1 {
				return fmt.Errorf("line %d: put one dashboard-expression marker immediately above its expression entry", node.Line)
			}
			match := matches[0]
			panel, err := strconv.Atoi(match[2])
			if err != nil {
				return fmt.Errorf("line %d: invalid panel ID: %w", node.Line, err)
			}
			var entry struct {
				Expr string `yaml:"expr"`
			}
			if err := node.Decode(&entry); err != nil {
				return fmt.Errorf("line %d: decode expression: %w", node.Line, err)
			}
			if strings.TrimSpace(entry.Expr) == "" {
				return fmt.Errorf("line %d: dashboard-expression marker needs a nonempty expr", node.Line)
			}
			bindings = append(bindings, fixtureBinding{fixtureKey{match[1], panel, match[3]}, entry.Expr, node.Line})
		}
		for _, child := range node.Content {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(&document); err != nil {
		return nil, err
	}
	return bindings, nil
}

func fixtureExpression(expression string) string {
	return grafanaVariablePattern.ReplaceAllStringFunc(expression, func(reference string) string {
		if reference == "${__to:date:seconds}" {
			return "90"
		}
		match := grafanaVariablePattern.FindStringSubmatch(reference)
		name := match[1]
		if name == "" {
			name = match[3]
		}
		if value, ok := fixtureVariables[name]; ok && (match[2] == "" || match[2] == "regex" || match[2] == "raw") {
			return value
		}
		return reference
	})
}
