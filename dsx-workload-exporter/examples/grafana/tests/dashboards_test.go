// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package grafana

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type dashboard struct {
	UID        string
	Version    int
	Refresh    string
	Templating struct{ List []variable }
	objects    []map[string]any
	root       map[string]any
	text       string
}

type variable struct {
	Name       string
	Type       string
	Hide       int
	Multi      bool
	IncludeAll bool
	Definition string
	Query      json.RawMessage
	Current    map[string]any
	Options    []variableOption
}

type variableOption struct {
	Text  string
	Value string
}

var dashboardUIDs = map[string]string{
	"workload-overview.json":  "dsx-example-workloads",
	"workload-structure.json": "dsx-example-structure",
	"metric-samples.json":     "dsx-example-metrics",
}

var metrics = []string{
	"dsx_workload_pod_info",
	"dsx_workload_status",
	"dsx_workload_created_timestamp_seconds",
	"dsx_exporter_observation_valid",
}

var recordingRules = []string{"dsx:workload:observed", "dsx:pod_attribution:unique"}

var observerMetrics = []string{
	"workload_gpu_observer_state",
	"workload_gpu_observer_assigned_devices",
	"workload_gpu_observer_utilization_max_ratio",
	"workload_gpu_observer_low_activity_observation_seconds",
	"workload_gpu_observer_observation_healthy",
}

var datasource = map[string]any{"type": "prometheus", "uid": "${datasource}"}

var grafanaVariablePattern = regexp.MustCompile(`\$\{([a-zA-Z_]\w*)(?::([^}]+))?\}|\$([a-zA-Z_]\w*)`)

// Quoted strings are consumed whole so braces in Grafana variables do not end selectors.
var observerSelectorPattern = regexp.MustCompile(`"(?:\\.|[^"\\])*"|\b(workload_gpu_observer_\w+)\b(?:\s*(\{(?:[^"{}]|"(?:\\.|[^"\\])*")*\}))?`)
var observerJobPattern = regexp.MustCompile(`(?:\{|,)\s*job\s*=~\s*"\$\{observer_job:regex\}"\s*(?:,|\})`)
var observerMetricPattern = regexp.MustCompile(`\bworkload_gpu_observer_\w+`)
var metricNameMatcherPattern = regexp.MustCompile(`\b__name__\s*(?:=|!=|!~)`)

func TestDashboardContract(t *testing.T) {
	for filename, uid := range dashboardUIDs {
		t.Run(filename, func(t *testing.T) {
			d := readDashboard(t, filename)
			require.NotContains(t, d.root, "dashboard", "export the portable dashboard, not the API envelope")
			require.NotContains(t, d.root, "meta", "export the portable dashboard, not API metadata")
			require.Nil(t, d.root["id"], "remove the Grafana installation ID")
			require.Equal(t, uid, d.UID, "dashboard UID")
			require.LessOrEqual(t, d.Version, 1, "remove the saved dashboard version")
			require.Equal(t, "30s", d.Refresh, "refresh interval")

			variables := make(map[string]variable)
			for _, v := range d.Templating.List {
				require.NotContains(t, variables, v.Name, "duplicate variable")
				variables[v.Name] = v
				t.Run("variable/"+v.Name, func(t *testing.T) {
					if v.Type == "datasource" || v.Type == "query" {
						if len(v.Current) != 0 {
							require.Equal(t, []any{"$__all"}, v.Current["value"], "remove the saved selection")
						}
						require.Empty(t, v.Options, "remove cached options")
					}
				})
			}
			require.Equal(t, "datasource", variables["datasource"].Type)
			require.Zero(t, variables["datasource"].Hide, "datasource picker must be visible")
			require.Contains(t, variables, "exporter_job")
			require.False(t, variables["exporter_job"].Multi, "select one exporter job")
			if filename != "metric-samples.json" {
				require.True(t, variables["namespace"].Multi, "namespace must allow multiple selections")
				require.Contains(t, variables["dcgm_job"].Definition, "DCGM_FI_PROF_GR_ENGINE_ACTIVE")
			}
			switch filename {
			case "workload-overview.json":
				require.Contains(t, variables["gpu_device"].Definition, "DCGM_FI_PROF_GR_ENGINE_ACTIVE")
				activity := variables["activity_view"]
				require.Equal(t, "custom", activity.Type)
				require.False(t, activity.Multi, "select one activity view")
				require.False(t, activity.IncludeAll, "activity view has explicit options")
				require.Equal(t, map[string]any{"text": "GPU workloads", "value": "4"}, activity.Current)
				require.ElementsMatch(t, []variableOption{{"GPU workloads", "4"}, {"Active only", "1"}, {"All workloads", "5"}}, activity.Options)
				var query string
				require.NoError(t, json.Unmarshal(activity.Query, &query))
				options := strings.Split(query, ",")
				for i, option := range options {
					text, value, ok := strings.Cut(option, ":")
					require.True(t, ok, "activity option %q must have a value", option)
					options[i] = strings.TrimSpace(text) + ":" + strings.TrimSpace(value)
				}
				require.ElementsMatch(t, []string{"GPU workloads:4", "Active only:1", "All workloads:5"}, options, "activity query must match its options")
			case "workload-structure.json":
				require.Contains(t, variables, "workload_uid")
				require.False(t, variables["workload_uid"].Multi, "select one workload")
				require.False(t, variables["workload_uid"].IncludeAll, "select one workload")
			case "metric-samples.json":
				require.Equal(t, "custom", variables["metric"].Type)
				var query string
				require.NoError(t, json.Unmarshal(variables["metric"].Query, &query))
				require.ElementsMatch(t, metrics, strings.Split(query, ","), "raw metric choices")
				require.NotContains(t, variables, "workload_uid")
			}

			panelIDs := make(map[any]bool)
			var targets []map[string]any
			hasNodeGraph := false
			for _, obj := range d.objects {
				if _, ok := obj["gridPos"]; ok {
					require.NotNil(t, obj["id"], "panel %v needs an ID", obj["title"])
					require.NotContains(t, panelIDs, obj["id"], "duplicate panel ID")
					panelIDs[obj["id"]] = true
					if filename == "workload-overview.json" && obj["id"] != float64(308) {
						// Nested panels have their own IDs and are checked separately.
						for key, value := range obj {
							if key == "panels" {
								continue
							}
							text, err := json.Marshal(value)
							require.NoError(t, err)
							require.NotContains(t, variableReferences(string(text)), "activity_view", "panel %v: activity view applies only to panel 308", obj["id"])
						}
					}
				}
				if obj["type"] == "nodeGraph" {
					hasNodeGraph = true
				}
				if ds, ok := obj["datasource"]; ok {
					require.Equal(t, datasource, ds, "datasource on %v", obj["title"])
				}
				if obj["type"] == "query" {
					require.Equal(t, datasource, obj["datasource"], "query variable %v datasource", obj["name"])
				}
				if raw, ok := obj["expr"]; ok {
					targets = append(targets, obj)
					t.Run(fmt.Sprintf("query/%d-%v", len(targets), obj["refId"]), func(t *testing.T) {
						require.IsType(t, "", raw, "PromQL expression")
						expr := raw.(string)
						require.Equal(t, datasource, obj["datasource"], "query datasource")
						require.NotEqual(t, true, obj["hide"], "remove hidden queries")
						for _, name := range observerMetricPattern.FindAllString(expr, -1) {
							require.Equal(t, "workload-overview.json", filename, "observer queries belong in the overview")
							require.Contains(t, observerMetrics, name, "unknown observer metric")
						}
						selectors, err := observerSelectors(expr)
						require.NoError(t, err)
						for _, selector := range selectors {
							require.True(t, observerJobPattern.MatchString(selector), "observer selector %q needs job=~\"${observer_job:regex}\"", selector)
						}
					})
				}
				if value, ok := obj["spanNulls"]; ok {
					require.Equal(t, false, value, "do not span missing observations")
				}
				if value, ok := obj["url"]; ok {
					require.NotRegexp(t, `^(?:https?://|/)`, value, "links must be portable")
				}
			}
			require.NotEmpty(t, panelIDs, "dashboard needs panels")
			require.NotEmpty(t, targets, "dashboard needs queries")
			if filename == "workload-structure.json" {
				require.True(t, hasNodeGraph, "structure dashboard needs a node graph")
			}
			for _, metric := range regexp.MustCompile(`\bdsx_[a-zA-Z0-9_]+`).FindAllString(d.text, -1) {
				if metric != "dsx_workload_" {
					require.Contains(t, metrics, metric, "unknown exporter metric")
				}
			}
			for _, rule := range regexp.MustCompile(`\bdsx:[a-zA-Z0-9_:]+`).FindAllString(d.text, -1) {
				require.Contains(t, recordingRules, rule, "unknown recording rule")
				require.NotEqual(t, "metric-samples.json", filename, "raw samples must not use recording rules")
			}
			require.NotRegexp(t, `karta_demo:|\bgpu_observer_|simulator_|/karta/grafana`, d.text, "remove demo-specific references")
			for _, name := range variableReferences(d.text) {
				if !strings.HasPrefix(name, "__") {
					require.Contains(t, variables, name, "undeclared dashboard variable")
				}
			}
			if filename == "metric-samples.json" {
				require.Len(t, targets, 1, "raw samples need one query")
				require.Equal(t, true, targets[0]["instant"], "raw samples need an instant query")
				expr, ok := targets[0]["expr"].(string)
				require.True(t, ok)
				require.True(t, strings.HasPrefix(expr, `{__name__="${metric}",`), "select the raw metric")
				require.True(t, strings.HasSuffix(expr, "}"), "use a raw metric selector")
				require.Contains(t, expr, `namespace=~"${namespace:regex}|^$"`, "include cluster-scoped samples")
				require.NotRegexp(t, `\b(?:sum|count|max|min|rate|group)\s*\(`, expr, "do not aggregate raw samples")
			}
		})
	}
}

func readDashboard(t *testing.T, filename string) dashboard {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", filename))
	require.NoError(t, err)
	var d dashboard
	require.NoError(t, json.Unmarshal(data, &d), "%s", filename)
	require.NoError(t, json.Unmarshal(data, &d.root), "%s", filename)
	d.text = string(data)
	d.objects = objects(d.root)
	return d
}

func objects(value any) []map[string]any {
	var result []map[string]any
	switch value := value.(type) {
	case map[string]any:
		result = append(result, value)
		for _, child := range value {
			result = append(result, objects(child)...)
		}
	case []any:
		for _, child := range value {
			result = append(result, objects(child)...)
		}
	}
	return result
}

func variableReferences(text string) []string {
	var names []string
	for _, match := range grafanaVariablePattern.FindAllStringSubmatch(text, -1) {
		name := match[1]
		if name == "" {
			name = match[3]
		}
		names = append(names, name)
	}
	return names
}

func observerSelectors(expression string) ([]string, error) {
	if observerMetricPattern.MatchString(expression) && metricNameMatcherPattern.MatchString(expression) {
		return nil, fmt.Errorf("observer queries must use explicit metric names instead of __name__ selectors")
	}
	var selectors []string
	for _, match := range observerSelectorPattern.FindAllStringSubmatch(expression, -1) {
		if match[1] != "" {
			selectors = append(selectors, match[0])
		}
	}
	return selectors, nil
}

func TestVariableReferences(t *testing.T) {
	for _, text := range []string{"$activity_view", "${activity_view}", "${activity_view:raw}", "${activity_view:regex}"} {
		t.Run(text, func(t *testing.T) {
			require.Equal(t, []string{"activity_view"}, variableReferences(text))
		})
	}
	require.Equal(t, []string{"activity_view_extra", "__range_s"}, variableReferences(`$1 $activity_view_extra ${__range_s}`), "replacement captures are not dashboard variables")
}

func TestObserverSelectorScoping(t *testing.T) {
	for _, tt := range []struct {
		name       string
		expression string
		unscoped   []string
	}{
		{"scoped", `workload_gpu_observer_state{job=~"${observer_job:regex}", state="active"}`, nil},
		{"second selector unscoped", `workload_gpu_observer_state{job=~"${observer_job:regex}"} + workload_gpu_observer_state{state="active"}`, []string{`workload_gpu_observer_state{state="active"}`}},
		{"bare metric", `sum(workload_gpu_observer_state) + up{job=~"${observer_job:regex}"}`, []string{"workload_gpu_observer_state"}},
		{"wrong label", `workload_gpu_observer_state{other_job=~"${observer_job:regex}"}`, []string{`workload_gpu_observer_state{other_job=~"${observer_job:regex}"}`}},
		{"wrong matcher", `workload_gpu_observer_state{job!~"${observer_job:regex}"}`, []string{`workload_gpu_observer_state{job!~"${observer_job:regex}"}`}},
		{"quoted metric name", `label_replace(up, "name", "workload_gpu_observer_state", "", ".*")`, nil},
		{"quoted braces", `workload_gpu_observer_state{state="x}y", job =~ "${observer_job:regex}"}`, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var unscoped []string
			selectors, err := observerSelectors(tt.expression)
			require.NoError(t, err)
			for _, selector := range selectors {
				if !observerJobPattern.MatchString(selector) {
					unscoped = append(unscoped, selector)
				}
			}
			require.Equal(t, tt.unscoped, unscoped)
		})
	}
	for _, expression := range []string{
		`{__name__="workload_gpu_observer_state"}`,
		`{__name__=~"workload_gpu_observer_state|workload_gpu_observer_assigned_devices"}`,
		`{__name__='workload_gpu_observer_state',job=~"${observer_job:regex}"}`,
	} {
		t.Run(expression, func(t *testing.T) {
			_, err := observerSelectors(expression)
			require.ErrorContains(t, err, "explicit metric names")
		})
	}
}
