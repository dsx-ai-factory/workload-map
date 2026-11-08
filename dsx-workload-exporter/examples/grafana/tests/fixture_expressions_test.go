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
	"testing"

	"github.com/stretchr/testify/require"
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

func TestFixtureExpressions(t *testing.T) {
	dashboards := make(map[string]dashboard)
	for filename := range dashboardUIDs {
		dashboards[filename] = readDashboard(t, filename)
	}
	paths, err := filepath.Glob("*.test.yaml")
	require.NoError(t, err)
	require.NotEmpty(t, paths, "PromQL fixtures are missing")
	seen := make(map[fixtureKey]string)
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			bindings, err := fixtureBindings(data)
			require.NoError(t, err)
			for _, binding := range bindings {
				key := binding.key
				require.NotContains(t, seen, key, "%s:%d: duplicate fixture marker; first seen at %s", path, binding.line, seen[key])
				seen[key] = fmt.Sprintf("%s:%d", path, binding.line)
				t.Run(fmt.Sprintf("%s/panel-%d/%s", key.dashboard, key.panel, key.ref), func(t *testing.T) {
					require.Contains(t, dashboards, key.dashboard, "unknown dashboard in fixture marker")
					var expressions []string
					for _, obj := range dashboards[key.dashboard].objects {
						if obj["id"] != float64(key.panel) {
							continue
						}
						for _, target := range objects(obj["targets"]) {
							if target["refId"] == key.ref {
								expression, ok := target["expr"].(string)
								require.True(t, ok, "target needs a PromQL expression")
								expressions = append(expressions, expression)
							}
						}
					}
					require.Len(t, expressions, 1, "marker must identify exactly one dashboard query")
					actual := fixtureExpression(expressions[0])
					require.Equal(t, strings.Join(strings.Fields(binding.expression), " "), strings.Join(strings.Fields(actual), " "), "fixture query drift at %s:%d", path, binding.line)
				})
			}
		})
	}
	var keys []fixtureKey
	for key := range seen {
		keys = append(keys, key)
	}
	require.ElementsMatch(t, []fixtureKey{
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
	}, keys, "preserve all 21 core, resource, layout, GPU activity and observer query bindings")
}

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

func TestFixtureBindingsYAML(t *testing.T) {
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
	require.NoError(t, err)
	require.Len(t, bindings, 2, "unmarked aliases do not create bindings")
	require.Equal(t, fixtureKey{"workload-overview.json", 3, "A"}, bindings[0].key)
	require.Equal(t, fixtureKey{"workload-structure.json", 3, "A"}, bindings[1].key)
	require.Equal(t, "sum(\n  up{job=\"exporter\"}\n)", bindings[0].expression)
	require.Equal(t, bindings[0].expression, bindings[1].expression, "YAML aliases must resolve to their expression")

	for _, tt := range []struct {
		name string
		data string
	}{
		{"missing expression", "# dashboard-expression: workload-overview.json 3 A\n- eval_time: 0s\n"},
		{"invalid marker", "# dashboard-expression: workload-overview.json wrong A\n- expr: up\n"},
		{"two markers", "# dashboard-expression: workload-overview.json 3 A\n# dashboard-expression: workload-overview.json 4 A\n- expr: up\n"},
		{"misplaced marker", "- expr: up # dashboard-expression: workload-overview.json 3 A\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fixtureBindings([]byte(tt.data))
			require.Error(t, err)
		})
	}
}

func TestFixtureExpressionVariables(t *testing.T) {
	expression := `${__to:date:seconds} ${__to} $namespace ${namespace} ${namespace:regex} ${activity_view:raw} $1 $__interval $__interval_ms ${namespace:unknown}`
	require.Equal(t, `90 90000 ns ns ns 4 $1 30s $__interval_ms ${namespace:unknown}`, fixtureExpression(expression), "substitute supported Grafana variables without changing label replacements or longer names")
}
