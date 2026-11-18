// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

//go:build promql

package grafana_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type queryFixture struct {
	EvaluationInterval string      `json:"evaluation_interval"`
	Tests              []queryTest `json:"tests"`
}

type queryTest struct {
	Name           string             `json:"name"`
	Interval       string             `json:"interval"`
	InputSeries    []queryInputSeries `json:"input_series"`
	PromQLExprTest []queryExpectation `json:"promql_expr_test"`
}

type queryInputSeries struct {
	Series string `json:"series"`
	Values string `json:"values"`
}

type queryExpectation struct {
	Expr       string        `json:"expr"`
	EvalTime   string        `json:"eval_time"`
	ExpSamples []querySample `json:"exp_samples"`
}

type querySample struct {
	Labels string  `json:"labels"`
	Value  float64 `json:"value"`
}

type queryDashboard struct {
	Panels     []queryPanel `json:"panels"`
	Templating struct {
		List []queryVariable `json:"list"`
	} `json:"templating"`
}

type queryVariable struct {
	Name       string `json:"name"`
	Multi      bool   `json:"multi"`
	IncludeAll bool   `json:"includeAll"`
	AllValue   string `json:"allValue"`
}

type queryPanel struct {
	Panels  []queryPanel `json:"panels"`
	ID      int          `json:"id"`
	Targets []struct {
		RefID string `json:"refId"`
		Expr  string `json:"expr"`
	} `json:"targets"`
}

var _ = Describe("Dashboard query results", Label("promql"), func() {
	It("matches Grafana's interpolation for supported selections", func() {
		multi := queryVariable{Multi: true, IncludeAll: true, AllValue: ".*"}
		// Expected values come from Grafana 12.2.1 and Scenes 6.34.0 interpolation.
		for _, test := range []struct {
			values   []string
			variable queryVariable
			format   string
			expected string
		}{
			{[]string{"foo.bar"}, multi, "regex", `foo\.bar`},
			{[]string{"foo.bar"}, multi, "", `foo\\.bar`},
			{[]string{"foo.bar", "other.job"}, multi, "regex", `(foo\.bar|other\.job)`},
			{[]string{"foo.bar", "other.job"}, multi, "", `(foo\\.bar|other\\.job)`},
			{[]string{"$__all"}, multi, "regex", `.*`},
			{[]string{"$__all"}, multi, "", `.*`},
			{[]string{"cadvisor.prod+(gpu)"}, queryVariable{}, "regex", `cadvisor\.prod\+\(gpu\)`},
			{[]string{"cadvisor.prod+(gpu)"}, queryVariable{}, "", `cadvisor.prod+(gpu)`},
			{[]string{"cadvisor.o'hare"}, queryVariable{}, "raw", `cadvisor.o'hare`},
			{[]string{`cadvisor.path\job`}, queryVariable{}, "raw", `cadvisor.path\job`},
		} {
			Expect(interpolateQueryVariable(test.values, test.variable, test.format)).To(Equal(test.expected))
		}
	})
	It("evaluates the shipped queries against synthetic telemetry", func() {
		variables := map[string][]string{
			"cluster": {"test"}, "exporter_job": {"dsx"}, "cadvisor_job": {"cadvisor"}, "dcgm_job": {"dcgm"},
			"namespace": {"workloads"}, "workload": {"training"}, "workload_uid": {"workload-1"},
			"component": {"$__all"}, "component_instance": {"$__all"}, "gpu_device": {"$__all"}, "activity_view": {"4"},
			"__range": {"10m"}, "__rate_interval": {"5m"},
		}
		workload := map[string]string{
			"cluster": "test", "namespace": "workloads", "workload_name": "training", "workload_uid": "workload-1",
		}
		pod := queryIdentity(workload, map[string]string{
			"job": "dsx", "pod_name": "worker", "pod_uid": "pod-1", "observation_valid": "true",
		})
		reasons := map[string]string{
			"active": "measured_above_5pct", "low_activity": "reported_devices_quiet_3m",
			"warming": "collecting_quiet_window", "unknown": "missing_or_conflicting_telemetry",
		}
		queryNames := map[string]string{}
		// Named Grafana variables must not consume PromQL's literal $1 replacements.
		variablePattern := regexp.MustCompile(`\$(?:\{([A-Za-z_]\w*)(?::(\w+))?\}|([A-Za-z_]\w*))`)
		query := func(dashboard string, panelID int, ref, namespaceLabel string) string {
			name := fmt.Sprintf("workload-%s/%d/%s (%s)", dashboard, panelID, ref, namespaceLabel)
			data, err := os.ReadFile(filepath.Join(dashboardDirectory, "workload-"+dashboard+".json"))
			Expect(err).NotTo(HaveOccurred())
			var document queryDashboard
			Expect(json.Unmarshal(data, &document)).To(Succeed())
			var expression string
			panels := slices.Clone(document.Panels)
			for len(panels) > 0 {
				panel := panels[0]
				panels = append(panels[1:], panel.Panels...)
				if panel.ID == panelID {
					for _, target := range panel.Targets {
						if target.RefID == ref {
							expression = target.Expr
						}
					}
				}
			}
			if panelID == 0 {
				for _, value := range readDashboard("workload-" + dashboard + ".json")["templating"].(map[string]any)["list"].([]any) {
					variable := value.(map[string]any)
					if variable["name"] == ref {
						expression = variable["query"].(map[string]any)["query"].(string)
						if strings.HasPrefix(expression, "label_values(") {
							parts := strings.TrimSuffix(strings.TrimPrefix(expression, "label_values("), ")")
							separator := strings.LastIndex(parts, ",")
							Expect(separator).To(BeNumerically(">", 0))
							expression = "group by (" + strings.TrimSpace(parts[separator+1:]) + ") (" + parts[:separator] + ")"
						} else {
							expression = strings.TrimSuffix(strings.TrimPrefix(expression, "query_result("), ")")
						}
					}
				}
			}
			Expect(expression).NotTo(BeEmpty(), "missing query %s", name)
			variables["dcgm_namespace_label"] = []string{namespaceLabel}
			expression = variablePattern.ReplaceAllStringFunc(expression, func(match string) string {
				groups := variablePattern.FindStringSubmatch(match)
				variable := groups[1]
				if variable == "" {
					variable = groups[3]
				}
				value, exists := variables[variable]
				Expect(exists).To(BeTrue(), "unknown variable %s in %s", variable, name)
				definition := queryVariable{}
				for _, candidate := range document.Templating.List {
					if candidate.Name == variable {
						definition = candidate
						break
					}
				}
				return interpolateQueryVariable(value, definition, groups[2])
			})
			queryNames[expression] = name
			return expression
		}
		maximumQuery := func(dashboard string, panelID int, ref, namespaceLabel string) string {
			expression := query(dashboard, panelID, ref, namespaceLabel)
			maximum := "max(" + expression + ")"
			queryNames[maximum] = "maximum of " + queryNames[expression]
			return maximum
		}
		stateExpectation := func(expression, evalTime, state string) queryExpectation {
			return queryExpected(expression, evalTime, querySample{
				Labels: queryLabels(queryIdentity(workload, map[string]string{
					"state": state, "reason": reasons[state], "workload": "training", "workload_key": "test/workloads/workload-1",
				})),
				Value: 1,
			})
		}

		fixture := queryFixture{EvaluationInterval: "30s"}
		for _, namespaceLabel := range []string{"namespace", "exported_namespace"} {
			stateQuery := query("overview", 306, "A", namespaceLabel)
			gpu := map[string]string{
				"cluster": "test", "job": "dcgm", namespaceLabel: "workloads", "pod_uid": "pod-1", "UUID": "GPU-1",
			}
			if namespaceLabel == "exported_namespace" {
				gpu["namespace"] = "monitoring"
			}
			attribution := querySeries("dsx_workload_pod_info", "1+0x30", pod)
			fixture.Tests = append(fixture.Tests, queryTest{
				Name: "GPU active, warming, low and stale: " + namespaceLabel,
				InputSeries: []queryInputSeries{
					attribution,
					querySeries("DCGM_FI_DEV_GPU_UTIL", "10 0+0x7 _x20", gpu),
				},
				PromQLExprTest: []queryExpectation{
					stateExpectation(stateQuery, "0s", "active"),
					stateExpectation(stateQuery, "30s", "warming"),
					stateExpectation(stateQuery, "3m", "warming"),
					stateExpectation(stateQuery, "3m30s", "low_activity"),
					stateExpectation(stateQuery, "4m", "low_activity"),
					stateExpectation(stateQuery, "6m", "unknown"),
				},
			}, queryTest{
				Name: "Six quiet points are warming and seven are low activity: " + namespaceLabel,
				InputSeries: []queryInputSeries{
					attribution,
					querySeries("DCGM_FI_DEV_GPU_UTIL", "0+0x30", gpu),
				},
				PromQLExprTest: []queryExpectation{
					stateExpectation(stateQuery, "2m30s", "warming"),
					stateExpectation(stateQuery, "3m", "low_activity"),
				},
			})
			activeQuery := query("overview", 304, "A", namespaceLabel)
			observedQuery := query("overview", 302, "A", namespaceLabel)
			lowQuery := query("overview", 305, "A", namespaceLabel)
			for _, test := range []struct {
				name     string
				values   string
				peer     string
				expected []querySample
			}{
				{"Missing GPU readings leave active count unavailable", "", "", nil},
				{"Quiet GPU readings produce zero active workloads", "2+0x30", "", []querySample{{"{}", 0}}},
				{"Active GPU readings produce one active workload", "80+0x30", "", []querySample{{"{}", 1}}},
				{"Out-of-range GPU readings leave active count unavailable", "101+0x30", "", nil},
				{"Conflicting GPU readings leave active count unavailable", "80+0x30", "2+0x30", nil},
				{"Stale GPU readings leave active count unavailable", "80 _x30", "", nil},
			} {
				input := []queryInputSeries{attribution}
				if test.values != "" {
					input = append(input, querySeries("DCGM_FI_DEV_GPU_UTIL", test.values, gpu))
				}
				if test.peer != "" {
					input = append(input, querySeries("DCGM_FI_DEV_GPU_UTIL", test.peer, queryIdentity(gpu, map[string]string{"instance": "replica-b"})))
				}
				var observed, low []querySample
				if len(test.expected) > 0 {
					observed = []querySample{{"{}", 1}}
					low = []querySample{{"{}", 1 - test.expected[0].Value}}
				}
				fixture.Tests = append(fixture.Tests, queryTest{
					Name:        test.name + ": " + namespaceLabel,
					InputSeries: input,
					PromQLExprTest: []queryExpectation{
						queryExpected(activeQuery, "2m", test.expected...),
						queryExpected(observedQuery, "3m", observed...),
						queryExpected(lowQuery, "3m", low...),
					},
				})
			}
			timelineQuery := maximumQuery("overview", 308, "A", namespaceLabel)
			fixture.Tests = append(fixture.Tests, queryTest{
				Name: "GPU timeline preserves the seven-point quiet boundary: " + namespaceLabel,
				InputSeries: []queryInputSeries{
					attribution,
					querySeries("DCGM_FI_DEV_GPU_UTIL", "0+0x30", gpu),
				},
				PromQLExprTest: []queryExpectation{
					queryExpected(timelineQuery, "2m30s", querySample{"{}", 3}),
					queryExpected(timelineQuery, "3m", querySample{"{}", 2}),
				},
			}, queryTest{
				Name: "GPU timeline retains a device last reported 120 seconds ago: " + namespaceLabel,
				InputSeries: []queryInputSeries{
					attribution,
					querySeries("DCGM_FI_DEV_GPU_UTIL", "0+0x30", gpu),
					querySeries("DCGM_FI_DEV_GPU_UTIL", "0+0x2 _x28", queryIdentity(gpu, map[string]string{"UUID": "GPU-2"})),
				},
				PromQLExprTest: []queryExpectation{
					queryExpected(timelineQuery, "3m", querySample{"{}", 4}),
					queryExpected(timelineQuery, "6m", querySample{"{}", 2}),
				},
			})
			for _, device := range []struct {
				name   string
				labels map[string]string
			}{
				{"full GPU", map[string]string{"UUID": "GPU-2"}},
				{"MIG device", map[string]string{"UUID": "GPU-2", "GPU_I_ID": "7", "GPU_CI_ID": "0"}},
			} {
				unknownQuery := query("overview", 310, "A", namespaceLabel)
				quietWindowQuery := maximumQuery("overview", 306, "D", namespaceLabel)
				// Unavailable quiet windows are NaN, which JSON fixtures cannot encode.
				queryNames[quietWindowQuery+" >= 0"] = queryNames[quietWindowQuery] + " (available)"
				quietWindowQuery += " >= 0"
				fixture.Tests = append(fixture.Tests, queryTest{
					Name: "Framebuffer-only device remains in recent history: " + device.name + "/" + namespaceLabel,
					InputSeries: []queryInputSeries{
						attribution,
						querySeries("DCGM_FI_DEV_GPU_UTIL", "0+0x30", gpu),
						querySeries("DCGM_FI_DEV_FB_USED", "1024+0x2 _x28", queryIdentity(gpu, device.labels)),
					},
					PromQLExprTest: []queryExpectation{
						stateExpectation(stateQuery, "3m", "unknown"),
						queryExpected(timelineQuery, "3m", querySample{"{}", 4}),
						queryExpected(lowQuery, "3m", querySample{"{}", 0}),
						queryExpected(unknownQuery, "3m", querySample{"{}", 1}),
						queryExpected(quietWindowQuery, "3m"),
						stateExpectation(stateQuery, "6m", "low_activity"),
						queryExpected(timelineQuery, "6m", querySample{"{}", 2}),
						queryExpected(lowQuery, "6m", querySample{"{}", 1}),
						queryExpected(unknownQuery, "6m", querySample{"{}", 0}),
						queryExpected(quietWindowQuery, "6m", querySample{"{}", 180}),
					},
				})
			}
			staleMIG := []queryInputSeries{
				attribution,
				querySeries("DCGM_FI_DEV_GPU_UTIL", "_x8 10+0x12", gpu),
			}
			for _, metric := range []string{"DCGM_FI_DEV_GPU_UTIL", "DCGM_FI_PROF_GR_ENGINE_ACTIVE", "DCGM_FI_DEV_FB_USED"} {
				staleMIG = append(staleMIG, querySeries(metric, "1 _x30", queryIdentity(gpu, map[string]string{
					"GPU_I_ID": "7", "GPU_CI_ID": "0",
				})))
			}
			fixture.Tests = append(fixture.Tests, queryTest{
				Name:           "Stale MIG evidence does not hide a fresh full GPU: " + namespaceLabel,
				InputSeries:    staleMIG,
				PromQLExprTest: []queryExpectation{stateExpectation(stateQuery, "4m", "active")},
			}, queryTest{
				Name: "A missing second GPU prevents low activity: " + namespaceLabel,
				InputSeries: []queryInputSeries{
					attribution,
					querySeries("DCGM_FI_DEV_GPU_UTIL", "0+0x30", gpu),
					querySeries("DCGM_FI_DEV_GPU_UTIL", "0+0x2 _x28", queryIdentity(gpu, map[string]string{"UUID": "GPU-2"})),
				},
				PromQLExprTest: []queryExpectation{stateExpectation(stateQuery, "4m", "unknown")},
			})
			for _, test := range []struct {
				dashboard string
				panel     int
				ref       string
				metric    string
				value     string
				expected  float64
			}{
				{"overview", 801, "A", "DCGM_FI_DEV_GPU_UTIL", "10", 1},
				{"overview", 801, "A", "DCGM_FI_PROF_GR_ENGINE_ACTIVE", "0.1", 1},
				{"overview", 16, "A", "DCGM_FI_DEV_GPU_UTIL", "10", 10},
				{"overview", 16, "A", "DCGM_FI_PROF_GR_ENGINE_ACTIVE", "0.1", 10},
				{"overview", 10, "A", "DCGM_FI_DEV_GPU_UTIL", "10", 10},
				{"overview", 10, "A", "DCGM_FI_PROF_GR_ENGINE_ACTIVE", "0.1", 10},
				{"overview", 11, "A", "DCGM_FI_DEV_FB_USED", "1024", 1024 * 1048576},
				{"overview", 17, "A", "DCGM_FI_DEV_FB_USED", "1024", 1024 * 1048576},
				{"overview", 24, "A", "DCGM_FI_DEV_GPU_UTIL", "10", 10},
				{"overview", 24, "A", "DCGM_FI_PROF_GR_ENGINE_ACTIVE", "0.1", 10},
				{"overview", 25, "A", "DCGM_FI_DEV_FB_USED", "1024", 1024 * 1048576},
				{"structure", 9, "A", "DCGM_FI_DEV_GPU_UTIL", "10", 10},
				{"structure", 9, "A", "DCGM_FI_PROF_GR_ENGINE_ACTIVE", "0.1", 10},
				{"structure", 10, "A", "DCGM_FI_DEV_FB_USED", "1024", 1024 * 1048576 / 1e9},
				{"structure", 4, "gpu", "DCGM_FI_DEV_GPU_UTIL", "10", 10},
				{"structure", 4, "gpu", "DCGM_FI_PROF_GR_ENGINE_ACTIVE", "0.1", 10},
				{"structure", 4, "framebuffer", "DCGM_FI_DEV_FB_USED", "1024", 1024 * 1048576},
			} {
				expression := maximumQuery(test.dashboard, test.panel, test.ref, namespaceLabel)
				device := queryIdentity(gpu)
				if test.metric == "DCGM_FI_PROF_GR_ENGINE_ACTIVE" {
					device["GPU_I_ID"], device["GPU_CI_ID"] = "7", "0"
				}
				zeroExpected := 0.0
				if test.panel == 801 {
					zeroExpected = 1
				}
				fixture.Tests = append(fixture.Tests, queryTest{
					Name: fmt.Sprintf("GPU measurements require fresh attribution: %s/%d/%s/%s", test.dashboard, test.panel, test.ref, namespaceLabel),
					InputSeries: []queryInputSeries{
						querySeries("dsx_workload_pod_info", "1 _x30", pod),
						querySeries(test.metric, test.value+"+0x30", device),
					},
					PromQLExprTest: []queryExpectation{
						queryExpected(expression, "0s", querySample{"{}", test.expected}),
						queryExpected(expression, "2m"),
					},
				}, queryTest{
					Name: fmt.Sprintf("NaN replica rejects a GPU measurement: %s/%d/%s/%s", test.dashboard, test.panel, test.ref, namespaceLabel),
					InputSeries: []queryInputSeries{
						attribution,
						querySeries(test.metric, "0+0x30", device),
						querySeries(test.metric, "_ NaN+0x30", queryIdentity(device, map[string]string{"instance": "nan-peer"})),
					},
					PromQLExprTest: []queryExpectation{
						queryExpected(expression, "0s", querySample{"{}", zeroExpected}),
						queryExpected(expression, "30s"),
					},
				})
				if namespaceLabel == "exported_namespace" {
					savedNamespace := variables["namespace"]
					variables["namespace"] = []string{"$__all"}
					allNamespaces := maximumQuery(test.dashboard, test.panel, test.ref, namespaceLabel)
					variables["namespace"] = savedNamespace
					withoutNamespace := queryIdentity(device, map[string]string{"namespace": "workloads"})
					delete(withoutNamespace, namespaceLabel)
					fixture.Tests = append(fixture.Tests, queryTest{
						Name: fmt.Sprintf("All namespaces still requires the selected DCGM namespace label: %s/%d/%s", test.dashboard, test.panel, test.ref),
						InputSeries: []queryInputSeries{
							attribution,
							querySeries(test.metric, test.value+" stale", device),
							querySeries(test.metric, "_ "+test.value+"+0x30", withoutNamespace),
						},
						PromQLExprTest: []queryExpectation{
							queryExpected(allNamespaces, "0s", querySample{"{}", test.expected}),
							queryExpected(allNamespaces, "30s"),
						},
					})
					if test.metric != "DCGM_FI_PROF_GR_ENGINE_ACTIVE" {
						fixture.Tests = append(fixture.Tests, queryTest{
							Name: fmt.Sprintf("MIG evidence without the selected namespace cannot hide its parent: %s/%d/%s", test.dashboard, test.panel, test.ref),
							InputSeries: []queryInputSeries{
								attribution,
								querySeries(test.metric, test.value+"+0x30", device),
								querySeries("DCGM_FI_DEV_FB_USED", "0+0x30", queryIdentity(withoutNamespace, map[string]string{"GPU_I_ID": "7", "GPU_CI_ID": "0"})),
							},
							PromQLExprTest: []queryExpectation{queryExpected(allNamespaces, "1m", querySample{"{}", test.expected})},
						})
					}
				}
				fixture.Tests = append(fixture.Tests, queryTest{
					Name: fmt.Sprintf("Raw GPU measurements expire after 90 seconds: %s/%d/%s/%s/%s", test.dashboard, test.panel, test.ref, test.metric, namespaceLabel),
					InputSeries: []queryInputSeries{
						attribution, querySeries(test.metric, test.value+" _x30", device),
					},
					PromQLExprTest: []queryExpectation{
						queryExpected(expression, "0s", querySample{"{}", test.expected}),
						queryExpected(expression, "90s", querySample{"{}", test.expected}),
						queryExpected(expression, "2m"),
					},
				})
				if test.metric == "DCGM_FI_PROF_GR_ENGINE_ACTIVE" {
					fixture.Tests = append(fixture.Tests, queryTest{
						Name: fmt.Sprintf("Fresh MIG profiling survives stale utilization evidence: %s/%d/%s/%s", test.dashboard, test.panel, test.ref, namespaceLabel),
						InputSeries: []queryInputSeries{
							attribution,
							querySeries("DCGM_FI_DEV_GPU_UTIL", "80+0x30", gpu),
							querySeries("DCGM_FI_DEV_GPU_UTIL", "0 _x30", device),
							querySeries("DCGM_FI_PROF_GR_ENGINE_ACTIVE", "0.1+0x30", device),
							querySeries("DCGM_FI_DEV_FB_USED", "1024+0x30", device),
						},
						PromQLExprTest: []queryExpectation{
							queryExpected(expression, "0s", querySample{"{}", test.expected}),
							queryExpected(expression, "2m", querySample{"{}", test.expected}),
						},
					})
					continue
				}
				input := []queryInputSeries{attribution, querySeries(test.metric, test.value+"+0x30", gpu)}
				for _, metric := range []string{"DCGM_FI_DEV_GPU_UTIL", "DCGM_FI_PROF_GR_ENGINE_ACTIVE", "DCGM_FI_DEV_FB_USED"} {
					input = append(input, querySeries(metric, "0 _x30", queryIdentity(gpu, map[string]string{
						"GPU_I_ID": "7", "GPU_CI_ID": "0",
					})))
				}
				fixture.Tests = append(fixture.Tests, queryTest{
					Name:           fmt.Sprintf("Raw GPU measurements ignore stale MIG evidence: %s/%d/%s/%s", test.dashboard, test.panel, test.ref, namespaceLabel),
					InputSeries:    input,
					PromQLExprTest: []queryExpectation{queryExpected(expression, "2m", querySample{"{}", test.expected})},
				})
			}
			for _, test := range []struct {
				dashboard string
				panel     int
				metric    string
				value     string
				conflict  string
				expected  float64
				mig       bool
			}{
				{"overview", 16, "DCGM_FI_DEV_GPU_UTIL", "10", "80", 10, false},
				{"structure", 9, "DCGM_FI_DEV_GPU_UTIL", "10", "80", 10, false},
				{"overview", 16, "DCGM_FI_PROF_GR_ENGINE_ACTIVE", "0.1", "0.8", 10, true},
				{"structure", 9, "DCGM_FI_PROF_GR_ENGINE_ACTIVE", "0.1", "0.8", 10, true},
				{"overview", 11, "DCGM_FI_DEV_FB_USED", "1024", "2048", 1024 * 1048576, false},
				{"structure", 10, "DCGM_FI_DEV_FB_USED", "1024", "2048", 1024 * 1048576 / 1e9, false},
			} {
				expression := query(test.dashboard, test.panel, "A", namespaceLabel)
				device := queryIdentity(gpu)
				if test.mig {
					device["GPU_I_ID"], device["GPU_CI_ID"] = "7", "0"
				}
				first := querySeries(test.metric, test.value+"+0x2", queryIdentity(device, map[string]string{
					"gpu": "0", "instance": "replica-a",
				}))
				second := queryIdentity(device, map[string]string{"gpu": "1", "instance": "replica-b"})
				fixture.Tests = append(fixture.Tests, queryTest{
					Name: fmt.Sprintf("Agreeing GPU indexes retain one measurement: %s/%d/%s/%s", test.dashboard, test.panel, test.metric, namespaceLabel),
					InputSeries: []queryInputSeries{
						attribution, first, querySeries(test.metric, test.value+"+0x2", second),
					},
					PromQLExprTest: []queryExpectation{queryExpected(expression, "1m", querySample{queryLabels(workload), test.expected})},
				}, queryTest{
					Name: fmt.Sprintf("Conflicting GPU indexes reject the measurement: %s/%d/%s/%s", test.dashboard, test.panel, test.metric, namespaceLabel),
					InputSeries: []queryInputSeries{
						attribution, first, querySeries(test.metric, test.conflict+"+0x2", second),
					},
					PromQLExprTest: []queryExpectation{queryExpected(expression, "1m")},
				})
			}
			fixture.Tests = append(fixture.Tests, queryTest{
				Name: "NaN activity replica prevents a quiet classification: " + namespaceLabel,
				InputSeries: []queryInputSeries{
					attribution,
					querySeries("DCGM_FI_DEV_GPU_UTIL", "0+0x30", gpu),
					querySeries("DCGM_FI_DEV_GPU_UTIL", "_ NaN+0x30", queryIdentity(gpu, map[string]string{"instance": "nan-peer"})),
				},
				PromQLExprTest: []queryExpectation{
					stateExpectation(stateQuery, "3m", "unknown"),
					queryExpected(timelineQuery, "3m", querySample{"{}", 4}),
					queryExpected(observedQuery, "3m"),
					queryExpected(lowQuery, "3m"),
				},
			})
			if namespaceLabel == "exported_namespace" {
				savedNamespace, savedView := variables["namespace"], variables["activity_view"]
				variables["namespace"], variables["activity_view"] = []string{"$__all"}, []string{"5"}
				missingNamespace := queryIdentity(gpu, map[string]string{"namespace": "workloads"})
				delete(missingNamespace, namespaceLabel)
				fixture.Tests = append(fixture.Tests, queryTest{
					Name:        "GPU states reject scrape-target namespace without the selected DCGM label",
					InputSeries: []queryInputSeries{attribution, querySeries("DCGM_FI_DEV_GPU_UTIL", "0+0x30", missingNamespace)},
					PromQLExprTest: []queryExpectation{
						stateExpectation(query("overview", 306, "A", namespaceLabel), "3m", "unknown"),
						queryExpected(maximumQuery("overview", 308, "A", namespaceLabel), "3m", querySample{"{}", 4}),
						queryExpected(query("overview", 302, "A", namespaceLabel), "3m"),
						queryExpected(query("overview", 305, "A", namespaceLabel), "3m"),
						queryExpected(query("overview", 310, "A", namespaceLabel), "3m", querySample{"{}", 1}),
					},
				})
				variables["namespace"], variables["activity_view"] = savedNamespace, savedView
			}
			for _, test := range []struct {
				dashboard string
				panel     int
				ref       string
				divisor   float64
			}{{"overview", 11, "A", 1}, {"overview", 17, "A", 1}, {"overview", 25, "A", 1}, {"structure", 10, "A", 1e9}, {"structure", 4, "framebuffer", 1}} {
				expression := maximumQuery(test.dashboard, test.panel, test.ref, namespaceLabel)
				var podExpression string
				if test.panel == 4 {
					measurement := query(test.dashboard, test.panel, test.ref, namespaceLabel)
					expression = "max((" + measurement + `) and on (id) label_replace(vector(1), "id", "workload|workloads|workload-1", "", ""))`
					queryNames[expression] = "shared workload VRAM (" + namespaceLabel + ")"
					podExpression = "max((" + measurement + `) and on (id) label_replace(vector(1), "id", "pod|workloads|pod-2", "", ""))`
					queryNames[podExpression] = "unaffected Pod VRAM (" + namespaceLabel + ")"
				}
				expectations := []queryExpectation{
					queryExpected(expression, "0s", querySample{"{}", 300 * 1048576 / test.divisor}),
					queryExpected(expression, "1m"),
				}
				if podExpression != "" {
					expectations = append(expectations, queryExpected(podExpression, "1m", querySample{"{}", 300 * 1048576}))
				}
				fixture.Tests = append(fixture.Tests, queryTest{
					Name: fmt.Sprintf("Conflicting Pod VRAM cannot be hidden by another Pod: %s/%d/%s/%s", test.dashboard, test.panel, test.ref, namespaceLabel),
					InputSeries: []queryInputSeries{
						attribution,
						querySeries("dsx_workload_pod_info", "1+0x30", queryIdentity(pod, map[string]string{"pod_name": "worker-b", "pod_uid": "pod-2"})),
						querySeries("DCGM_FI_DEV_FB_USED", "300 100+0x30", queryIdentity(gpu, map[string]string{"instance": "replica-a"})),
						querySeries("DCGM_FI_DEV_FB_USED", "300 200+0x30", queryIdentity(gpu, map[string]string{"instance": "replica-b"})),
						querySeries("DCGM_FI_DEV_FB_USED", "300+0x30", queryIdentity(gpu, map[string]string{"pod_uid": "pod-2"})),
					},
					PromQLExprTest: expectations,
				})
			}
			for _, test := range []struct {
				dashboard string
				panel     int
				divisor   float64
			}{{"overview", 11, 1}, {"structure", 10, 1e9}} {
				expression := query(test.dashboard, test.panel, "A", namespaceLabel)
				parents := []queryInputSeries{
					attribution,
					querySeries("DCGM_FI_DEV_FB_USED", "8192+0x2", gpu),
					querySeries("DCGM_FI_DEV_FB_USED", "4096+0x2", queryIdentity(gpu, map[string]string{"UUID": "GPU-2"})),
				}
				fixture.Tests = append(fixture.Tests, queryTest{
					Name:           "Parent-only GPU memory is retained: " + test.dashboard + "/" + namespaceLabel,
					InputSeries:    parents,
					PromQLExprTest: []queryExpectation{queryExpected(expression, "1m", querySample{queryLabels(workload), 12288 * 1048576 / test.divisor})},
				}, queryTest{
					Name: "MIG memory replaces only its matching parent: " + test.dashboard + "/" + namespaceLabel,
					InputSeries: append(slices.Clone(parents),
						querySeries("DCGM_FI_DEV_FB_USED", "2048+0x2", queryIdentity(gpu, map[string]string{"GPU_I_ID": "7", "GPU_CI_ID": "0"})),
						querySeries("DCGM_FI_DEV_FB_USED", "1024+0x2", queryIdentity(gpu, map[string]string{"GPU_I_ID": "8", "GPU_CI_ID": "0"})),
					),
					PromQLExprTest: []queryExpectation{queryExpected(expression, "1m", querySample{queryLabels(workload), 7168 * 1048576 / test.divisor})},
				})
			}
		}

		for _, test := range []struct {
			dashboard string
			panel     int
			metric    string
			values    string
			expected  float64
		}{
			{"overview", 104, "container_cpu_usage_seconds_total", "0+30x20", 1000},
			{"overview", 105, "container_memory_working_set_bytes", "4096+0x20", 4096},
			{"structure", 11, "container_cpu_usage_seconds_total", "0+30x20", 1000},
			{"structure", 12, "container_memory_working_set_bytes", "4096+0x20", 4096},
		} {
			expression := query(test.dashboard, test.panel, "A", "namespace")
			resultLabels := map[string]string{}
			if test.dashboard == "structure" {
				resultLabels = workload
			}
			container := map[string]string{
				"cluster": "test", "job": "cadvisor", "namespace": "workloads", "pod": "worker", "container": "main", "id": "first",
			}
			valid := []queryInputSeries{
				querySeries("dsx_workload_pod_info", "1+0x20", pod),
				querySeries(test.metric, test.values, queryIdentity(container, map[string]string{"instance": "replica-a"})),
				querySeries(test.metric, test.values, queryIdentity(container, map[string]string{"instance": "replica-b"})),
			}
			fixture.Tests = append(fixture.Tests, queryTest{
				Name:           fmt.Sprintf("Agreeing replicas of one container ID: %s/%d", test.dashboard, test.panel),
				InputSeries:    valid,
				PromQLExprTest: []queryExpectation{queryExpected(expression, "5m", querySample{queryLabels(resultLabels), test.expected})},
			}, queryTest{
				Name: fmt.Sprintf("A conflicting second container ID rejects the container: %s/%d", test.dashboard, test.panel),
				InputSeries: append(slices.Clone(valid),
					querySeries(test.metric, test.values, queryIdentity(container, map[string]string{"id": "second", "instance": "replica-a"})),
					querySeries(test.metric, "0+60x20", queryIdentity(container, map[string]string{"id": "second", "instance": "replica-b"})),
				),
				PromQLExprTest: []queryExpectation{queryExpected(expression, "5m")},
			})
		}

		for _, test := range []struct {
			dashboard string
			panel     int
		}{{"overview", 4}, {"structure", 2}} {
			expression := query(test.dashboard, test.panel, "D", "namespace")
			status := queryIdentity(workload, map[string]string{"job": "dsx", "observation_valid": "true"})
			var valid []queryInputSeries
			for _, phase := range []struct{ name, value string }{{"Running", "1+0x2"}, {"Completed", "0+0x2"}} {
				for _, replica := range []string{"replica-a", "replica-b"} {
					valid = append(valid, querySeries("dsx_workload_status", phase.value, queryIdentity(status, map[string]string{
						"phase": phase.name, "instance": replica,
					})))
				}
			}
			fixture.Tests = append(fixture.Tests, queryTest{
				Name:        "Two phase names need no fixed phase count: " + test.dashboard,
				InputSeries: valid,
				PromQLExprTest: []queryExpectation{queryExpected(expression, "1m", querySample{
					Labels: queryLabels(queryIdentity(workload, map[string]string{"job": "dsx"})), Value: 1,
				})},
			}, queryTest{
				Name: "Any phase disagreement rejects the workload: " + test.dashboard,
				InputSeries: append(slices.Clone(valid),
					querySeries("dsx_workload_status", "0+0x2", queryIdentity(status, map[string]string{"phase": "Pending", "instance": "replica-a"})),
					querySeries("dsx_workload_status", "1+0x2", queryIdentity(status, map[string]string{"phase": "Pending", "instance": "replica-b"})),
				),
				PromQLExprTest: []queryExpectation{queryExpected(expression, "1m")},
			})
			for _, label := range []string{"karta", "karta_source", "workload_group", "workload_version", "workload_kind", "owner_resolution", "team"} {
				fixture.Tests = append(fixture.Tests, queryTest{
					Name: "Conflicting phase identity rejects the count: " + test.dashboard + "/" + label,
					InputSeries: append(slices.Clone(valid), querySeries("dsx_workload_status", "1+0x2", queryIdentity(status, map[string]string{
						"phase": "Running", "instance": "replica-c", label: "other",
					}))),
					PromQLExprTest: []queryExpectation{queryExpected(expression, "1m")},
				})
			}
		}
		for _, test := range []struct {
			dashboard string
			panel     int
			ref       string
			expected  float64
		}{
			{"overview", 104, "A", 1000},
			{"overview", 8, "A", 1000},
			{"overview", 19, "A", 1000},
			{"overview", 109, "A", 1000},
			{"overview", 111, "A", 1000},
			{"structure", 11, "A", 1000},
			{"structure", 4, "cpu", 1},
		} {
			expression := maximumQuery(test.dashboard, test.panel, test.ref, "namespace")
			container := map[string]string{
				"cluster": "test", "job": "cadvisor", "namespace": "workloads", "pod": "worker", "container": "main", "id": "first",
			}
			fixture.Tests = append(fixture.Tests, queryTest{
				Name: fmt.Sprintf("A stale CPU replica does not hide a healthy peer: %s/%d/%s", test.dashboard, test.panel, test.ref),
				InputSeries: []queryInputSeries{
					querySeries("dsx_workload_pod_info", "1+0x20", pod),
					querySeries("container_cpu_usage_seconds_total", "0+30x20", queryIdentity(container, map[string]string{"instance": "replica-a"})),
					querySeries("container_cpu_usage_seconds_total", "0+30x2 stale _x17", queryIdentity(container, map[string]string{"instance": "replica-b"})),
				},
				PromQLExprTest: []queryExpectation{queryExpected(expression, "5m", querySample{"{}", test.expected})},
			})
		}

		failureQuery := query("overview", 103, "A", "namespace")
		for _, test := range []struct {
			name        string
			peerUp      string
			observation string
			expected    []querySample
		}{
			{"Healthy replicas with successful observations return zero", "1", "1", []querySample{{"{}", 0}}},
			{"A successful observation cannot bypass a down replica", "0", "1", nil},
			{"Explicit failures remain visible with a down replica", "0", "0", []querySample{{"{}", 1}}},
		} {
			fixture.Tests = append(fixture.Tests, queryTest{
				Name: test.name,
				InputSeries: []queryInputSeries{
					querySeries("up", "1+0x2", map[string]string{"cluster": "test", "job": "dsx", "instance": "replica-a"}),
					querySeries("up", test.peerUp+"+0x2", map[string]string{"cluster": "test", "job": "dsx", "instance": "replica-b"}),
					querySeries("dsx_exporter_observation_valid", test.observation+"+0x2", queryIdentity(workload, map[string]string{
						"job": "dsx", "instance": "replica-a", "scope": "workload",
					})),
				},
				PromQLExprTest: []queryExpectation{queryExpected(failureQuery, "1m", test.expected...)},
			})
		}

		workloadQuery := query("structure", 0, "workload_uid", "namespace")
		for _, metric := range []string{"dsx_workload_status", "dsx_workload_created_timestamp_seconds", "dsx_workload_pod_info", "dsx_exporter_observation_valid"} {
			fixture.Tests = append(fixture.Tests, queryTest{
				Name:     "Workload observed between subquery boundaries is discoverable: " + metric,
				Interval: "10s",
				InputSeries: []queryInputSeries{querySeries(metric, "_ 1 stale", queryIdentity(workload, map[string]string{
					"job": "dsx", "scope": "workload",
				}))},
				PromQLExprTest: []queryExpectation{queryExpected(workloadQuery, "30s", querySample{
					queryLabels(queryIdentity(workload, map[string]string{"job": "dsx", "selection": "training / workload-1"})), 1,
				})},
			})
		}
		defaults := maps.Clone(variables)
		for _, test := range []struct {
			name      string
			selection []string
			expected  float64
		}{
			{"Dotted workload selection excludes regex-like names", []string{"train.one"}, 1},
			{"Multiple dotted workloads exclude regex-like names", []string{"train.one", "train.two"}, 2},
			{"All workloads preserves the configured wildcard", []string{"$__all"}, 3},
		} {
			variables = maps.Clone(defaults)
			variables["workload"] = test.selection
			var input []queryInputSeries
			for i, name := range []string{"train.one", "train.two", "trainXone"} {
				input = append(input, querySeries("dsx_workload_status", "1+0x2", queryIdentity(workload, map[string]string{
					"job": "dsx", "workload_name": name, "workload_uid": fmt.Sprintf("workload-%d", i+1),
				})))
			}
			fixture.Tests = append(fixture.Tests, queryTest{
				Name:           test.name,
				InputSeries:    input,
				PromQLExprTest: []queryExpectation{queryExpected(query("overview", 2, "A", "namespace"), "1m", querySample{"{}", test.expected})},
			})
		}
		for _, test := range []struct {
			dashboard string
			panel     int
			ref       string
			expected  float64
		}{{"overview", 104, "A", 1000}, {"structure", 11, "A", 1000}, {"structure", 4, "cpu", 1}} {
			variables = maps.Clone(defaults)
			variables["cadvisor_job"] = []string{"cadvisor.prod+(gpu)"}
			container := map[string]string{
				"cluster": "test", "namespace": "workloads", "pod": "worker", "container": "main", "id": "first",
			}
			fixture.Tests = append(fixture.Tests, queryTest{
				Name: fmt.Sprintf("CPU job selection is literal: %s/%d/%s", test.dashboard, test.panel, test.ref),
				InputSeries: []queryInputSeries{
					querySeries("dsx_workload_pod_info", "1+0x20", pod),
					querySeries("container_cpu_usage_seconds_total", "0+30x20", queryIdentity(container, map[string]string{"job": "cadvisor.prod+(gpu)"})),
					querySeries("container_cpu_usage_seconds_total", "0+60x20", queryIdentity(container, map[string]string{"job": "cadvisorXproddgpu"})),
				},
				PromQLExprTest: []queryExpectation{queryExpected(maximumQuery(test.dashboard, test.panel, test.ref, "namespace"), "5m", querySample{"{}", test.expected})},
			})
		}
		for _, test := range []struct {
			dashboard string
			panel     int
		}{{"overview", 16}, {"structure", 9}} {
			for _, selectedJob := range []string{"exporter_job", "dcgm_job"} {
				variables = maps.Clone(defaults)
				variables[selectedJob] = []string{"source.prod+(gpu)"}
				attributionJob, gpuJob := "dsx", "dcgm"
				if selectedJob == "exporter_job" {
					attributionJob = variables[selectedJob][0]
				} else {
					gpuJob = variables[selectedJob][0]
				}
				gpu := map[string]string{"cluster": "test", "namespace": "workloads", "pod_uid": "pod-1", "UUID": "GPU-1"}
				fixture.Tests = append(fixture.Tests, queryTest{
					Name: fmt.Sprintf("GPU source job selection is literal: %s/%d/%s", test.dashboard, test.panel, selectedJob),
					InputSeries: []queryInputSeries{
						querySeries("dsx_workload_pod_info", "1+0x2", queryIdentity(pod, map[string]string{"job": attributionJob})),
						querySeries("dsx_workload_pod_info", "1+0x2", queryIdentity(pod, map[string]string{"job": "sourceXproddgpu", "workload_name": "wrong"})),
						querySeries("DCGM_FI_DEV_GPU_UTIL", "10+0x2", queryIdentity(gpu, map[string]string{"job": gpuJob})),
						querySeries("DCGM_FI_DEV_GPU_UTIL", "80+0x2", queryIdentity(gpu, map[string]string{"job": "sourceXproddgpu"})),
					},
					PromQLExprTest: []queryExpectation{queryExpected(query(test.dashboard, test.panel, "A", "namespace"), "1m", querySample{queryLabels(workload), 10})},
				})
			}
		}
		variables = maps.Clone(defaults)
		variables["workload"] = []string{"training"}
		fixture.Tests = append(fixture.Tests, queryTest{
			Name:        "Failure-only workloads can be selected and retain their inventory row",
			InputSeries: []queryInputSeries{querySeries("dsx_exporter_observation_valid", "0+0x2", queryIdentity(workload, map[string]string{"job": "dsx", "scope": "workload"}))},
			PromQLExprTest: []queryExpectation{
				queryExpected(query("overview", 0, "workload", "namespace"), "1m", querySample{`{workload_name="training"}`, 1}),
				queryExpected(query("overview", 4, "A", "namespace"), "1m", querySample{queryLabels(queryIdentity(workload, map[string]string{"job": "dsx"})), 1}),
			},
		})
		for _, job := range []string{"cadvisor.o'hare", `cadvisor.path\job`} {
			for _, test := range []struct {
				dashboard string
				panel     int
				metric    string
				values    string
				expected  float64
			}{{"overview", 104, "container_cpu_usage_seconds_total", "0+30x20", 1000}, {"overview", 105, "container_memory_working_set_bytes", "4096+0x20", 4096}, {"structure", 11, "container_cpu_usage_seconds_total", "0+30x20", 1000}, {"structure", 12, "container_memory_working_set_bytes", "4096+0x20", 4096}} {
				variables = maps.Clone(defaults)
				variables["cadvisor_job"] = []string{job}
				fixture.Tests = append(fixture.Tests, queryTest{
					Name: fmt.Sprintf("Quoted and escaped job names retain their literal value: %s/%d/%s", test.dashboard, test.panel, job),
					InputSeries: []queryInputSeries{
						querySeries("dsx_workload_pod_info", "1+0x20", pod),
						querySeries(test.metric, test.values, map[string]string{"cluster": "test", "job": job, "namespace": "workloads", "pod": "worker", "container": "main", "id": "first"}),
					},
					PromQLExprTest: []queryExpectation{queryExpected(maximumQuery(test.dashboard, test.panel, "A", "namespace"), "5m", querySample{"{}", test.expected})},
				})
			}
		}
		for _, job := range []string{"team's-dsx", "team-dsx"} {
			variables = maps.Clone(defaults)
			variables["exporter_job"] = []string{job}
			fixture.Tests = append(fixture.Tests, queryTest{
				Name:        "Exporter job names remain literal during discovery: " + job,
				InputSeries: []queryInputSeries{querySeries("dsx_workload_status", "1+0x2", queryIdentity(workload, map[string]string{"job": job}))},
				PromQLExprTest: []queryExpectation{
					queryExpected(query("structure", 0, "namespace", "namespace"), "1m", querySample{`{namespace="workloads"}`, 1}),
					queryExpected(query("structure", 0, "workload_uid", "namespace"), "1m", querySample{queryLabels(queryIdentity(workload, map[string]string{"job": job, "selection": "training / workload-1"})), 1}),
				},
			})
		}
		variables = maps.Clone(defaults)
		variables["cluster"] = []string{"team's-cluster"}
		fixture.Tests = append(fixture.Tests, queryTest{
			Name:           "Cluster labels containing apostrophes remain literal",
			InputSeries:    []queryInputSeries{querySeries("dsx_workload_status", "1+0x2", queryIdentity(workload, map[string]string{"cluster": "team's-cluster", "job": "dsx"}))},
			PromQLExprTest: []queryExpectation{queryExpected(query("overview", 2, "A", "namespace"), "1m", querySample{"{}", 1})},
		})
		variables = maps.Clone(defaults)
		agreementQuery := maximumQuery("overview", 16, "A", "namespace")
		gpu := map[string]string{"cluster": "test", "job": "dcgm", "namespace": "workloads", "pod_uid": "pod-1", "UUID": "GPU-1"}
		fixture.Tests = append(fixture.Tests, queryTest{
			Name: "GPU agreement is exact and rejects non-finite samples",
			InputSeries: []queryInputSeries{
				querySeries("dsx_workload_pod_info", "1+0x30", pod),
				querySeries("DCGM_FI_DEV_GPU_UTIL", "0 0 NaN +Inf NaN 0", gpu),
				querySeries("DCGM_FI_DEV_GPU_UTIL", "-0 1e-200 NaN +Inf stale 0", queryIdentity(gpu, map[string]string{"instance": "replica-b"})),
			},
			PromQLExprTest: []queryExpectation{
				queryExpected(agreementQuery, "0s", querySample{"{}", 0}),
				queryExpected(agreementQuery, "30s"),
				queryExpected(agreementQuery, "1m"),
				queryExpected(agreementQuery, "90s"),
				queryExpected(agreementQuery, "2m"),
				queryExpected(agreementQuery, "2m30s", querySample{"{}", 0}),
			},
		})
		variables = maps.Clone(defaults)
		variables["activity_view"] = []string{"2"}
		variables["workload"] = []string{"$__all"}
		variables["__range"] = []string{"4m"}
		timelineExpression := query("overview", 308, "A", "namespace")
		activeWorkload := map[string]string{
			"cluster": "test", "namespace": "workloads", "workload_name": "active-at-end", "workload_uid": "workload-1",
		}
		quietWorkload := queryIdentity(activeWorkload, map[string]string{"workload_name": "quiet-at-end", "workload_uid": "workload-2"})
		rangePod := map[string]string{"job": "dsx", "observation_valid": "true"}
		rangeGPU := map[string]string{"cluster": "test", "namespace": "workloads", "job": "dcgm"}
		rangeLabels := queryLabels(queryIdentity(activeWorkload, map[string]string{"uid_short": "workload"}))
		rangeTest := queryTest{
			Name: "Active-only timeline retains earlier states only for workloads active at the range end",
			InputSeries: []queryInputSeries{
				querySeries("dsx_workload_pod_info", "1+0x8", queryIdentity(activeWorkload, rangePod, map[string]string{"pod_uid": "pod-1"})),
				querySeries("dsx_workload_pod_info", "1+0x8", queryIdentity(quietWorkload, rangePod, map[string]string{"pod_uid": "pod-2"})),
				querySeries("DCGM_FI_DEV_GPU_UTIL", "0+0x3 80+0x4", queryIdentity(rangeGPU, map[string]string{"pod_uid": "pod-1", "UUID": "GPU-1"})),
				querySeries("DCGM_FI_DEV_GPU_UTIL", "80+0x3 0+0x4", queryIdentity(rangeGPU, map[string]string{"pod_uid": "pod-2", "UUID": "GPU-2"})),
			},
			// The outer evaluation fixes end() while the subquery evaluates each earlier point.
			PromQLExprTest: []queryExpectation{
				queryExpected("count_over_time(("+timelineExpression+")[4m:30s])", "4m", querySample{rangeLabels, 8}),
				queryExpected("count_over_time((("+timelineExpression+") == 3)[4m:30s])", "4m", querySample{rangeLabels, 3}),
				queryExpected("count_over_time((("+timelineExpression+") == 1)[4m:30s])", "4m", querySample{rangeLabels, 5}),
			},
		}
		for i, expectation := range rangeTest.PromQLExprTest {
			queryNames[expectation.Expr] = fmt.Sprintf("activity timeline range assertion %d", i)
		}
		fixture.Tests = append(fixture.Tests, rangeTest)
		variables = defaults
		for i := range fixture.Tests {
			if fixture.Tests[i].Interval == "" {
				fixture.Tests[i].Interval = "30s"
			}
		}

		directory, err := os.MkdirTemp("", "dashboard-queries-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, directory)
		// The Prometheus image runs as nobody and only reads synthetic fixtures.
		Expect(os.Chmod(directory, 0o755)).To(Succeed())
		data, err := json.Marshal(fixture)
		Expect(err).NotTo(HaveOccurred())
		path := filepath.Join(directory, "queries.json")
		Expect(os.WriteFile(path, data, 0o644)).To(Succeed())
		Expect(os.Chmod(path, 0o644)).To(Succeed())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		var command *exec.Cmd
		if promtool := os.Getenv("PROMTOOL"); promtool != "" {
			command = exec.CommandContext(ctx, promtool, "test", "rules", path)
		} else {
			command = exec.CommandContext(ctx, "docker", "run", "--rm", "--entrypoint=/bin/promtool",
				"-v", directory+":/tests:ro", "quay.io/prometheus/prometheus:v3.14.0", "test", "rules", "/tests/queries.json")
		}
		output, err := command.CombinedOutput()
		message := string(output)
		for expression, name := range queryNames {
			message = strings.ReplaceAll(message, strconv.Quote(expression), strconv.Quote(name))
			message = strings.ReplaceAll(message, expression, name)
		}
		Expect(ctx.Err()).NotTo(HaveOccurred(), "%s", message)
		if err != nil {
			Fail(fmt.Sprintf("promtool failed: %v\n%s", err, message))
		}
		fmt.Fprintf(GinkgoWriter, "%sPassed %d dashboard query cases.\n", message, len(fixture.Tests))
	})
})

func queryIdentity(identities ...map[string]string) map[string]string {
	identity := map[string]string{}
	for _, values := range identities {
		maps.Copy(identity, values)
	}
	return identity
}

func queryLabels(identity map[string]string) string {
	pairs := make([]string, 0, len(identity))
	for _, key := range slices.Sorted(maps.Keys(identity)) {
		value, err := json.Marshal(identity[key])
		Expect(err).NotTo(HaveOccurred())
		pairs = append(pairs, key+"="+string(value))
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

func querySeries(metric, values string, identity map[string]string) queryInputSeries {
	return queryInputSeries{Series: metric + queryLabels(identity), Values: values}
}

func queryExpected(expression, evalTime string, samples ...querySample) queryExpectation {
	return queryExpectation{Expr: expression, EvalTime: evalTime, ExpSamples: append([]querySample{}, samples...)}
}

// Grafana 12.2.1's default Prometheus formatting differs from the explicit regex formatter.
func interpolateQueryVariable(values []string, variable queryVariable, format string) string {
	if len(values) == 1 && values[0] == "$__all" && variable.AllValue != "" {
		return variable.AllValue
	}
	escaped := make([]string, len(values))
	for i, value := range values {
		switch {
		case format == "raw":
			escaped[i] = value
		case format == "regex":
			escaped[i] = strings.ReplaceAll(regexp.QuoteMeta(value), "/", `\/`)
		case variable.Multi || variable.IncludeAll:
			escaped[i] = strings.ReplaceAll(regexp.QuoteMeta(value), `\`, `\\`)
		default:
			escaped[i] = strings.NewReplacer(`\`, `\\`, "'", `\\'`).Replace(value)
		}
	}
	if len(escaped) == 1 {
		return escaped[0]
	}
	return "(" + strings.Join(escaped, "|") + ")"
}
