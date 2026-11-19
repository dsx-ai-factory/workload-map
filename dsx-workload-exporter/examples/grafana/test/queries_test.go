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
							// This checks selector interpolation, not Grafana's range-based metadata API.
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
			for _, shared := range []struct {
				name, metric, value string
				labels              map[string]string
			}{
				{"full GPU", "DCGM_FI_DEV_GPU_UTIL", "2", nil},
				{"MIG instance", "DCGM_FI_PROF_GR_ENGINE_ACTIVE", "0.02", map[string]string{"GPU_I_ID": "7", "GPU_CI_ID": "0"}},
			} {
				device := queryIdentity(gpu, shared.labels)
				for _, replacement := range []struct {
					name, prefix, state, device string
					low, timeline               float64
				}{{"new device", "_x13 ", "warming", "GPU-2", 0, 3}, {"unchanged devices", "", "low_activity", "GPU-2", 1, 2}, {"new reporter on a quiet device", "_x13 ", "warming", "GPU-1", 0, 3}} {
					quietWindow := maximumQuery("overview", 306, "D", namespaceLabel) + " >= 0"
					queryNames[quietWindow] = "quiet duration after device replacement (" + namespaceLabel + ")"
					var duration []querySample
					if replacement.low == 1 {
						duration = []querySample{{"{}", 180}}
					}
					fixture.Tests = append(fixture.Tests, queryTest{
						Interval: "15s",
						Name:     "Each current device needs its own quiet history: " + shared.name + "/" + replacement.name + "/" + namespaceLabel,
						InputSeries: []queryInputSeries{
							querySeries("dsx_workload_pod_info", "1+0x12 stale", pod),
							querySeries("dsx_workload_pod_info", "1+0x50", queryIdentity(pod, map[string]string{"pod_uid": "pod-2", "pod_name": "survivor"})),
							querySeries("dsx_workload_pod_info", replacement.prefix+"1+0x50", queryIdentity(pod, map[string]string{"pod_uid": "pod-3", "pod_name": "third"})),
							querySeries(shared.metric, shared.value+"+0x12 stale", device),
							querySeries(shared.metric, shared.value+"+0x50", queryIdentity(device, map[string]string{"pod_uid": "pod-2"})),
							querySeries(shared.metric, replacement.prefix+shared.value+"+0x50", queryIdentity(device, map[string]string{"pod_uid": "pod-3", "UUID": replacement.device})),
						},
						PromQLExprTest: []queryExpectation{
							stateExpectation(stateQuery, "3m15s", replacement.state),
							queryExpected(query("overview", 305, "A", namespaceLabel), "3m15s", querySample{"{}", replacement.low}),
							queryExpected(query("overview", 310, "A", namespaceLabel), "3m15s", querySample{"{}", 0}),
							queryExpected(query("overview", 308, "A", namespaceLabel), "3m15s", querySample{queryLabels(queryIdentity(workload, map[string]string{"uid_short": "workload"})), replacement.timeline}),
							queryExpected(quietWindow, "3m15s", duration...),
							stateExpectation(stateQuery, "6m30s", "low_activity"),
						},
					})
				}
				for _, survivor := range []struct {
					name, prefix, state string
					timeline, low       float64
				}{{"continuous reporter", "", "low_activity", 2, 1}, {"new reporter", "_x3 ", "warming", 3, 0}} {
					fixture.Tests = append(fixture.Tests, queryTest{
						Name: "A shared device remains present after one Pod departs: " + shared.name + "/" + survivor.name + "/" + namespaceLabel,
						InputSeries: []queryInputSeries{
							querySeries("dsx_workload_pod_info", "1+0x2 stale", pod),
							querySeries("dsx_workload_pod_info", survivor.prefix+"1+0x30", queryIdentity(pod, map[string]string{"pod_uid": "pod-2", "pod_name": "survivor"})),
							querySeries(shared.metric, shared.value+"+0x2 stale", device),
							querySeries(shared.metric, survivor.prefix+shared.value+"+0x30", queryIdentity(device, map[string]string{"pod_uid": "pod-2"})),
						},
						PromQLExprTest: []queryExpectation{
							stateExpectation(stateQuery, "3m", survivor.state),
							queryExpected(query("overview", 305, "A", namespaceLabel), "3m", querySample{"{}", survivor.low}),
							queryExpected(query("overview", 310, "A", namespaceLabel), "3m", querySample{"{}", 0}),
							queryExpected(query("overview", 308, "A", namespaceLabel), "3m", querySample{queryLabels(queryIdentity(workload, map[string]string{"uid_short": "workload"})), survivor.timeline}),
						},
					})
				}
			}
			variables["activity_view"] = []string{"5"}
			for _, device := range []struct {
				name, metric, active string
				labels               map[string]string
			}{{"full GPU", "DCGM_FI_DEV_GPU_UTIL", "80", nil}, {"MIG instance", "DCGM_FI_PROF_GR_ENGINE_ACTIVE", "0.8", map[string]string{"GPU_I_ID": "7", "GPU_CI_ID": "0"}}} {
				for _, peer := range []struct {
					name, state string
					labels      map[string]string
					timeline    float64
				}{{"same device", "unknown", nil, 4}, {"different device", "active", map[string]string{"UUID": "GPU-2"}, 1}, {"different MIG entity", "active", map[string]string{"GPU_I_ID": "8"}, 1}} {
					if device.name == "full GPU" && peer.name == "different MIG entity" {
						continue
					}
					var active []querySample
					unknown := 1.0
					if peer.state == "active" {
						active = []querySample{{"{}", 1}}
						unknown = 0
					}
					var graphValues, historyValues []querySample
					agreedGraph := []querySample{
						{queryLabels(map[string]string{"cluster": "test", "id": "workload|workloads|workload-1"}), 80},
						{queryLabels(map[string]string{"cluster": "test", "id": "pod|workloads|pod-1"}), 80},
						{queryLabels(map[string]string{"cluster": "test", "id": "pod|workloads|pod-2"}), 80},
					}
					if peer.state == "active" {
						graphValues = slices.Clone(agreedGraph)
						graphValues[2].Value = 0
						historyValues = []querySample{{queryLabels(workload), 80}}
					}
					fixture.Tests = append(fixture.Tests, queryTest{
						Name: "Shared device reporters must agree: " + device.name + "/" + peer.name + "/" + namespaceLabel,
						InputSeries: []queryInputSeries{
							attribution,
							querySeries("dsx_workload_pod_info", "1+0x30", queryIdentity(pod, map[string]string{"pod_uid": "pod-2", "pod_name": "peer"})),
							querySeries(device.metric, device.active+"+0x30", queryIdentity(gpu, device.labels)),
							querySeries(device.metric, "0 "+device.active+"+0x29", queryIdentity(gpu, device.labels, peer.labels, map[string]string{"pod_uid": "pod-2"})),
						},
						PromQLExprTest: []queryExpectation{
							stateExpectation(query("overview", 306, "A", namespaceLabel), "0s", peer.state),
							queryExpected(query("structure", 4, "gpu", namespaceLabel), "0s", graphValues...),
							queryExpected(query("structure", 9, "A", namespaceLabel), "0s", historyValues...),
							queryExpected(query("structure", 4, "gpu", namespaceLabel), "30s", agreedGraph...),
							queryExpected(query("structure", 9, "A", namespaceLabel), "30s", querySample{queryLabels(workload), 80}),
							queryExpected(query("overview", 304, "A", namespaceLabel), "0s", active...),
							queryExpected(query("overview", 310, "A", namespaceLabel), "0s", querySample{"{}", unknown}),
							queryExpected(query("overview", 308, "A", namespaceLabel), "0s", querySample{queryLabels(queryIdentity(workload, map[string]string{"uid_short": "workload"})), peer.timeline}),
							stateExpectation(query("overview", 306, "A", namespaceLabel), "30s", "active"),
							queryExpected(query("overview", 304, "A", namespaceLabel), "30s", querySample{"{}", 1}),
							queryExpected(query("overview", 310, "A", namespaceLabel), "30s", querySample{"{}", 0}),
							queryExpected(query("overview", 308, "A", namespaceLabel), "30s", querySample{queryLabels(queryIdentity(workload, map[string]string{"uid_short": "workload"})), 1}),
						},
					})
				}
			}
			for _, entity := range []struct {
				name   string
				labels map[string]string
			}{{"full GPU", nil}, {"MIG instance", map[string]string{"GPU_I_ID": "7", "GPU_CI_ID": "0"}}} {
				var agreedGraph []querySample
				for _, id := range []string{
					"workload|workloads|workload-1", "component|workloads|workload-1|trainer", "component|workloads|workload-1|evaluator",
					"pod|workloads|pod-1", "pod|workloads|pod-2",
				} {
					agreedGraph = append(agreedGraph, querySample{queryLabels(map[string]string{"cluster": "test", "id": id}), 64 * 1048576})
				}
				fixture.Tests = append(fixture.Tests, queryTest{
					Name: "Device framebuffer readings agree across components: " + entity.name + "/" + namespaceLabel,
					InputSeries: []queryInputSeries{
						querySeries("dsx_workload_pod_info", "1+0x30", queryIdentity(pod, map[string]string{"component": "trainer"})),
						querySeries("dsx_workload_pod_info", "1+0x30", queryIdentity(pod, map[string]string{"pod_uid": "pod-2", "pod_name": "peer", "component": "evaluator"})),
						querySeries("DCGM_FI_DEV_FB_USED", "64+0x30", queryIdentity(gpu, entity.labels)),
						querySeries("DCGM_FI_DEV_FB_USED", "32 64+0x29", queryIdentity(gpu, entity.labels, map[string]string{"pod_uid": "pod-2"})),
					},
					PromQLExprTest: []queryExpectation{
						queryExpected(query("structure", 4, "framebuffer", namespaceLabel), "0s"),
						queryExpected(query("structure", 4, "framebuffer", namespaceLabel), "30s", agreedGraph...),
						queryExpected(query("structure", 10, "A", namespaceLabel), "0s"),
						queryExpected(query("structure", 10, "A", namespaceLabel), "30s", querySample{queryLabels(workload), 64 * 1048576 / 1e9}),
						queryExpected(query("overview", 11, "A", namespaceLabel), "0s"),
						queryExpected(query("overview", 11, "A", namespaceLabel), "30s", querySample{queryLabels(workload), 64 * 1048576}),
						queryExpected(query("overview", 17, "A", namespaceLabel), "0s"),
						queryExpected(query("overview", 17, "A", namespaceLabel), "30s", querySample{queryLabels(workload), 64 * 1048576}),
						queryExpected(maximumQuery("overview", 25, "A", namespaceLabel), "0s"),
						queryExpected(maximumQuery("overview", 25, "A", namespaceLabel), "30s", querySample{"{}", 64 * 1048576}),
					},
				})
			}
			for _, entity := range []struct {
				name   string
				labels map[string]string
			}{{"full GPU", nil}, {"MIG instance", map[string]string{"GPU_I_ID": "7", "GPU_CI_ID": "0"}}} {
				selectedPod := queryIdentity(pod, map[string]string{"component": "trainer", "component_instance": "replica", "replica_group_id": "group-a"})
				for _, selection := range []struct {
					name       string
					peer       map[string]string
					components bool
				}{
					{"workload selection", map[string]string{"workload_name": "other", "workload_uid": "workload-2"}, false},
					{"component selection", map[string]string{"component": "evaluator"}, true},
					{"instance selection", map[string]string{"component_instance": "other"}, true},
					{"invalid mapping", map[string]string{"observation_valid": "false"}, false},
				} {
					variables["component"], variables["component_instance"] = []string{"trainer"}, []string{"replica"}
					for _, target := range []struct {
						dashboard string
						panel     int
						divisor   float64
					}{{"overview", 11, 1}, {"overview", 17, 1}, {"overview", 25, 1}, {"structure", 10, 1e9}} {
						if selection.components && (target.dashboard != "overview" || target.panel != 25) {
							continue
						}
						expression := query(target.dashboard, target.panel, "A", namespaceLabel)
						labels := queryIdentity(workload)
						if target.panel == 25 {
							labels["component"], labels["component_instance"] = "trainer", "replica"
						}
						expected := querySample{queryLabels(labels), 64 * 1048576 / target.divisor}
						fixture.Tests = append(fixture.Tests, queryTest{
							Name: fmt.Sprintf("Framebuffer agreement precedes %s: %s/%d/%s/%s", selection.name, target.dashboard, target.panel, entity.name, namespaceLabel),
							InputSeries: []queryInputSeries{
								querySeries("dsx_workload_pod_info", "1+0x30", selectedPod),
								querySeries("dsx_workload_pod_info", "1+0x30", queryIdentity(selectedPod, selection.peer, map[string]string{"pod_uid": "pod-2", "pod_name": "peer"})),
								querySeries("DCGM_FI_DEV_FB_USED", "64+0x30", queryIdentity(gpu, entity.labels)),
								querySeries("DCGM_FI_DEV_FB_USED", "_ 32 64 stale", queryIdentity(gpu, entity.labels, map[string]string{"pod_uid": "pod-2"})),
							},
							PromQLExprTest: []queryExpectation{
								queryExpected(expression, "0s", expected),
								queryExpected(expression, "30s"),
								queryExpected(expression, "1m", expected),
								queryExpected(expression, "1m30s", expected),
							},
						})
					}
				}
				variables["component"], variables["component_instance"] = []string{"$__all"}, []string{"$__all"}
				for _, missing := range []string{"component", "component_instance", "replica_group_id"} {
					peer := map[string]string{"pod_uid": "pod-2", "pod_name": "peer", missing: ""}
					if missing == "component" {
						peer["component_instance"] = ""
					}
					if missing != "replica_group_id" {
						peer["replica_group_id"] = ""
					}
					var single, shared []querySample
					for _, id := range []string{
						"workload|workloads|workload-1", "component|workloads|workload-1|trainer", "instance|workloads|workload-1|trainer|replica",
						"group|workloads|workload-1|trainer|replica|group-a", "pod|workloads|pod-1",
					} {
						single = append(single, querySample{queryLabels(map[string]string{"cluster": "test", "id": id}), 64 * 1048576})
					}
					shared = append(slices.Clone(single), querySample{queryLabels(map[string]string{"cluster": "test", "id": "pod|workloads|pod-2"}), 64 * 1048576})
					expression := query("structure", 4, "framebuffer", namespaceLabel)
					fixture.Tests = append(fixture.Tests, queryTest{
						Name: "Framebuffer agreement precedes graph metadata filtering: " + missing + "/" + entity.name + "/" + namespaceLabel,
						InputSeries: []queryInputSeries{
							querySeries("dsx_workload_pod_info", "1+0x30", selectedPod),
							querySeries("dsx_workload_pod_info", "1+0x30", queryIdentity(selectedPod, peer)),
							querySeries("DCGM_FI_DEV_FB_USED", "64+0x30", queryIdentity(gpu, entity.labels)),
							querySeries("DCGM_FI_DEV_FB_USED", "_ 32 64 stale", queryIdentity(gpu, entity.labels, map[string]string{"pod_uid": "pod-2"})),
						},
						PromQLExprTest: []queryExpectation{
							queryExpected(expression, "0s", single...),
							queryExpected(expression, "30s"),
							queryExpected(expression, "1m", shared...),
							queryExpected(expression, "1m30s", single...),
						},
					})
				}
			}
			for _, device := range []struct {
				name, activity, value string
				labels                map[string]string
			}{{"full GPU", "DCGM_FI_DEV_GPU_UTIL", "80", nil}, {"MIG instance", "DCGM_FI_PROF_GR_ENGINE_ACTIVE", "0.8", map[string]string{"GPU_I_ID": "7", "GPU_CI_ID": "0"}}} {
				for _, namespace := range []string{"workloads", "$__all"} {
					variables["namespace"], variables["activity_view"] = []string{namespace}, []string{"5"}
					for _, peer := range []struct {
						name     string
						labels   map[string]string
						conflict bool
					}{
						{"same device", nil, true},
						{"different device", map[string]string{"UUID": "GPU-2"}, false},
						{"different cluster", map[string]string{"cluster": "other"}, false},
						{"empty workload namespace", map[string]string{namespaceLabel: ""}, false},
						{"different MIG entity", map[string]string{"GPU_I_ID": "8"}, false},
					} {
						if device.name == "full GPU" && peer.name == "different MIG entity" {
							continue
						}
						input := []queryInputSeries{
							attribution,
							querySeries("dsx_workload_pod_info", "1+0x30", queryIdentity(pod, map[string]string{"namespace": "peer-workloads", "pod_uid": "pod-2", "pod_name": "peer", "workload_name": "other", "workload_uid": "workload-2"})),
							querySeries(device.activity, device.value+"+0x30", queryIdentity(gpu, device.labels)),
							querySeries("DCGM_FI_DEV_FB_USED", "64+0x30", queryIdentity(gpu, device.labels)),
							querySeries(device.activity, "_ 0 "+device.value+" stale", queryIdentity(gpu, device.labels, map[string]string{namespaceLabel: "peer-workloads", "pod_uid": "pod-2"}, peer.labels)),
							querySeries("DCGM_FI_DEV_FB_USED", "_ 32 64 stale", queryIdentity(gpu, device.labels, map[string]string{namespaceLabel: "peer-workloads", "pod_uid": "pod-2"}, peer.labels)),
						}
						times := []string{"30s"}
						if peer.conflict {
							times = []string{"0s", "30s", "1m", "1m30s"}
						}
						var expectations []queryExpectation
						for _, at := range times {
							state, timeline, unknown := "active", 1.0, 0.0
							active := []querySample{{"{}", 1}}
							accepted := !peer.conflict || at != "30s"
							if !accepted {
								state, timeline, unknown, active = "unknown", 4, 1, nil
							}
							expectations = append(expectations,
								stateExpectation(query("overview", 306, "A", namespaceLabel), at, state),
								queryExpected(query("overview", 308, "A", namespaceLabel), at, querySample{queryLabels(queryIdentity(workload, map[string]string{"uid_short": "workload"})), timeline}),
								queryExpected(query("overview", 304, "A", namespaceLabel), at, active...),
								queryExpected(query("overview", 310, "A", namespaceLabel), at, querySample{"{}", unknown}),
							)
							for _, target := range []struct {
								dashboard string
								panel     int
								ref       string
								value     float64
							}{
								{"overview", 16, "A", 80}, {"structure", 9, "A", 80}, {"structure", 4, "gpu", 80},
								{"overview", 11, "A", 64 * 1048576}, {"overview", 17, "A", 64 * 1048576}, {"overview", 25, "A", 64 * 1048576},
								{"structure", 10, "A", 64 * 1048576 / 1e9}, {"structure", 4, "framebuffer", 64 * 1048576},
							} {
								var samples []querySample
								if accepted {
									samples = []querySample{{queryLabels(workload), target.value}}
									if target.panel == 4 {
										samples = []querySample{
											{queryLabels(map[string]string{"cluster": "test", "id": "workload|workloads|workload-1"}), target.value},
											{queryLabels(map[string]string{"cluster": "test", "id": "pod|workloads|pod-1"}), target.value},
										}
									}
								}
								expectations = append(expectations, queryExpected(query(target.dashboard, target.panel, target.ref, namespaceLabel), at, samples...))
							}
						}
						fixture.Tests = append(fixture.Tests, queryTest{
							Name:        "Device agreement crosses workload namespaces: " + device.name + "/" + namespace + "/" + peer.name + "/" + namespaceLabel,
							InputSeries: input, PromQLExprTest: expectations,
						})
					}
				}
			}
			variables["namespace"] = []string{"workloads"}
			variables["activity_view"] = []string{"4"}
			variables["workload"] = []string{"$__all"}
			isolatedState := "(" + query("overview", 306, "A", namespaceLabel) + `) and on (workload_uid) label_replace(vector(1), "workload_uid", "workload-1", "", "")`
			queryNames[isolatedState] = "disappearing device remains scoped to its workload (" + namespaceLabel + ")"
			fixture.Tests = append(fixture.Tests, queryTest{
				Name: "Another workload cannot hide a disappearing device: " + namespaceLabel,
				InputSeries: []queryInputSeries{
					attribution,
					querySeries("dsx_workload_pod_info", "1+0x30", queryIdentity(pod, map[string]string{"pod_uid": "pod-2", "pod_name": "other", "workload_name": "other", "workload_uid": "workload-2"})),
					querySeries("DCGM_FI_DEV_GPU_UTIL", "2+0x2 stale", gpu),
					querySeries("DCGM_FI_DEV_GPU_UTIL", "2+0x30", queryIdentity(gpu, map[string]string{"UUID": "GPU-2"})),
					querySeries("DCGM_FI_DEV_GPU_UTIL", "2+0x30", queryIdentity(gpu, map[string]string{"pod_uid": "pod-2"})),
				},
				PromQLExprTest: []queryExpectation{stateExpectation(isolatedState, "3m", "unknown")},
			})
			variables["workload"] = []string{"training"}
			variables["activity_view"] = []string{"5"}
			if namespaceLabel == "exported_namespace" {
				fixture.Tests = append(fixture.Tests, queryTest{
					Name: "Activity agreement preserves the original scrape namespace",
					InputSeries: []queryInputSeries{
						attribution,
						querySeries("DCGM_FI_DEV_GPU_UTIL", "80+0x30", gpu),
						querySeries("DCGM_FI_DEV_GPU_UTIL", "80 0", queryIdentity(gpu, map[string]string{"namespace": "monitoring-peer"})),
					},
					PromQLExprTest: []queryExpectation{
						stateExpectation(query("overview", 306, "A", namespaceLabel), "0s", "active"),
						queryExpected(query("overview", 304, "A", namespaceLabel), "0s", querySample{"{}", 1}),
						queryExpected(maximumQuery("overview", 308, "A", namespaceLabel), "0s", querySample{"{}", 1}),
						stateExpectation(query("overview", 306, "A", namespaceLabel), "30s", "unknown"),
						queryExpected(query("overview", 310, "A", namespaceLabel), "30s", querySample{"{}", 1}),
						queryExpected(maximumQuery("overview", 308, "A", namespaceLabel), "30s", querySample{"{}", 4}),
					},
				})
			}
			fixture.Tests = append(fixture.Tests, queryTest{
				Name: "Activity rejects conflicting components until mappings agree: " + namespaceLabel,
				InputSeries: []queryInputSeries{
					attribution,
					querySeries("dsx_workload_pod_info", "1 stale", queryIdentity(pod, map[string]string{"component": "conflicting"})),
					querySeries("DCGM_FI_DEV_GPU_UTIL", "80+0x30", gpu),
				},
				PromQLExprTest: []queryExpectation{
					stateExpectation(query("overview", 306, "A", namespaceLabel), "0s", "unknown"),
					queryExpected(query("overview", 304, "A", namespaceLabel), "0s"),
					queryExpected(query("overview", 302, "A", namespaceLabel), "0s"),
					queryExpected(query("overview", 310, "A", namespaceLabel), "0s", querySample{"{}", 1}),
					queryExpected(maximumQuery("overview", 308, "A", namespaceLabel), "0s", querySample{"{}", 4}),
					queryExpected(maximumQuery("overview", 16, "A", namespaceLabel), "0s"),
					stateExpectation(query("overview", 306, "A", namespaceLabel), "30s", "active"),
					queryExpected(query("overview", 304, "A", namespaceLabel), "30s", querySample{"{}", 1}),
					queryExpected(query("overview", 302, "A", namespaceLabel), "30s", querySample{"{}", 1}),
					queryExpected(query("overview", 310, "A", namespaceLabel), "30s", querySample{"{}", 0}),
					queryExpected(maximumQuery("overview", 308, "A", namespaceLabel), "30s", querySample{"{}", 1}),
					queryExpected(maximumQuery("overview", 16, "A", namespaceLabel), "30s", querySample{"{}", 80}),
				},
			})
			for _, measurement := range []struct {
				name, values, state        string
				gpu, active, low, timeline float64
			}{
				{"active", "80+0x30", "active", 80, 1, 0, 1},
				{"quiet", "2+0x30", "low_activity", 2, 0, 1, 2},
			} {
				// Empty workload UIDs model malformed downstream identities, not native exporter output.
				expectations := []queryExpectation{
					stateExpectation(stateQuery, "3m", "unknown"),
					stateExpectation(stateQuery, "6m30s", measurement.state),
					queryExpected(query("overview", 310, "A", namespaceLabel), "3m", querySample{"{}", 1}),
					queryExpected(query("overview", 310, "A", namespaceLabel), "6m30s", querySample{"{}", 0}),
					queryExpected(query("overview", 308, "A", namespaceLabel), "3m", querySample{queryLabels(queryIdentity(workload, map[string]string{"uid_short": "workload"})), 4}),
					queryExpected(query("overview", 308, "A", namespaceLabel), "6m30s", querySample{queryLabels(queryIdentity(workload, map[string]string{"uid_short": "workload"})), measurement.timeline}),
				}
				for _, target := range []struct {
					dashboard string
					panel     int
					ref       string
					value     float64
				}{
					{"overview", 801, "A", 1}, {"overview", 302, "A", 1}, {"overview", 303, "A", 1},
					{"overview", 304, "A", measurement.active}, {"overview", 305, "A", measurement.low},
					{"overview", 306, "B", measurement.gpu / 100}, {"overview", 306, "C", 1},
					{"overview", 307, "A", measurement.gpu / 100}, {"overview", 16, "A", measurement.gpu},
					{"structure", 4, "gpu", measurement.gpu},
				} {
					expression := maximumQuery(target.dashboard, target.panel, target.ref, namespaceLabel)
					expectations = append(expectations,
						queryExpected(expression, "3m"),
						queryExpected(expression, "6m30s", querySample{"{}", target.value}),
					)
				}
				if measurement.name == "quiet" {
					quietWindow := maximumQuery("overview", 306, "D", namespaceLabel) + " >= 0"
					expectations = append(expectations,
						stateExpectation(stateQuery, "3m30s", "warming"),
						queryExpected(quietWindow, "3m"),
						queryExpected(quietWindow, "6m30s", querySample{"{}", 180}),
					)
				}
				fixture.Tests = append(fixture.Tests, queryTest{
					Name: "Defensive activity checks reject a conflicting empty workload UID: " + measurement.name + "/" + namespaceLabel,
					InputSeries: []queryInputSeries{
						attribution,
						querySeries("dsx_workload_pod_info", "1+0x6 stale", queryIdentity(pod, map[string]string{"workload_uid": ""})),
						querySeries("DCGM_FI_DEV_GPU_UTIL", measurement.values, gpu),
					},
					PromQLExprTest: expectations,
				})
			}
			variables["namespace"] = []string{"$__all"}
			fixture.Tests = append(fixture.Tests, queryTest{
				Name: "Only same-namespace MIG evidence suppresses a full GPU: " + namespaceLabel,
				InputSeries: []queryInputSeries{
					attribution,
					querySeries("DCGM_FI_DEV_GPU_UTIL", "80+0x30", gpu),
					querySeries("DCGM_FI_DEV_FB_USED", "100+0x30", queryIdentity(gpu, map[string]string{namespaceLabel: "other", "GPU_I_ID": "7", "GPU_CI_ID": "0"})),
					querySeries("DCGM_FI_DEV_FB_USED", "_ 100+0x30", queryIdentity(gpu, map[string]string{"GPU_I_ID": "7", "GPU_CI_ID": "0"})),
				},
				PromQLExprTest: []queryExpectation{
					stateExpectation(query("overview", 306, "A", namespaceLabel), "0s", "active"),
					queryExpected(query("overview", 310, "A", namespaceLabel), "0s", querySample{"{}", 0}),
					queryExpected(maximumQuery("overview", 308, "A", namespaceLabel), "0s", querySample{"{}", 1}),
					stateExpectation(query("overview", 306, "A", namespaceLabel), "30s", "unknown"),
					queryExpected(query("overview", 310, "A", namespaceLabel), "30s", querySample{"{}", 1}),
					queryExpected(maximumQuery("overview", 308, "A", namespaceLabel), "30s", querySample{"{}", 4}),
				},
			})
			variables["namespace"], variables["activity_view"] = []string{"workloads"}, []string{"4"}
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
					Name: fmt.Sprintf("GPU attribution uses UID across Pod name reuse: %s/%d/%s/%s", test.dashboard, test.panel, test.ref, namespaceLabel),
					InputSeries: []queryInputSeries{
						attribution,
						querySeries("dsx_workload_pod_info", "1+0x30", queryIdentity(pod, map[string]string{"pod_uid": "old-pod"})),
						querySeries("dsx_workload_pod_info", "_ 1+0x30", queryIdentity(pod, map[string]string{"component": "conflicting"})),
						querySeries(test.metric, test.value+"+0x30", device),
					},
					PromQLExprTest: []queryExpectation{
						queryExpected(expression, "0s", querySample{"{}", test.expected}),
						queryExpected(expression, "30s"),
					},
				}, queryTest{
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
					half, err := strconv.ParseFloat(test.value, 64)
					Expect(err).NotTo(HaveOccurred())
					initial, conflicting := test.expected, test.expected/2
					if test.metric == "DCGM_FI_DEV_FB_USED" {
						initial, conflicting = half*1.5*1048576, half/2*1048576
						if test.dashboard == "structure" && test.panel == 10 {
							initial, conflicting = initial/1e9, conflicting/1e9
						}
					}
					input := []queryInputSeries{
						attribution,
						querySeries(test.metric, test.value+"+0x30", device),
						querySeries(test.metric, test.value+" 0", queryIdentity(device, map[string]string{"namespace": "monitoring-peer"})),
					}
					var remaining []querySample
					if test.panel != 801 {
						input = append(input, querySeries(test.metric, strconv.FormatFloat(half/2, 'g', -1, 64)+"+0x30", queryIdentity(device, map[string]string{"UUID": "GPU-2"})))
						remaining = []querySample{{"{}", conflicting}}
					}
					fixture.Tests = append(fixture.Tests, queryTest{
						Name:        fmt.Sprintf("Scrape namespaces remain distinct until GPU agreement: %s/%d/%s/%s", test.dashboard, test.panel, test.ref, test.metric),
						InputSeries: input,
						PromQLExprTest: []queryExpectation{
							queryExpected(expression, "0s", querySample{"{}", initial}),
							queryExpected(expression, "30s", remaining...),
						},
					})
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
					queryNames[podExpression] = "conflicted shared device Pod VRAM (" + namespaceLabel + ")"
				}
				expectations := []queryExpectation{
					queryExpected(expression, "0s", querySample{"{}", 300 * 1048576 / test.divisor}),
					queryExpected(expression, "1m"),
				}
				if podExpression != "" {
					expectations = append(expectations, queryExpected(podExpression, "1m"))
					fixture.Tests = append(fixture.Tests, queryTest{
						Name: "A different GPU is unaffected by framebuffer disagreement: " + namespaceLabel,
						InputSeries: []queryInputSeries{
							attribution,
							querySeries("dsx_workload_pod_info", "1+0x30", queryIdentity(pod, map[string]string{"pod_name": "worker-b", "pod_uid": "pod-2"})),
							querySeries("DCGM_FI_DEV_FB_USED", "100+0x30", queryIdentity(gpu, map[string]string{"instance": "replica-a"})),
							querySeries("DCGM_FI_DEV_FB_USED", "200+0x30", queryIdentity(gpu, map[string]string{"instance": "replica-b"})),
							querySeries("DCGM_FI_DEV_FB_USED", "300+0x30", queryIdentity(gpu, map[string]string{"pod_uid": "pod-2", "UUID": "GPU-2"})),
						},
						PromQLExprTest: []queryExpectation{queryExpected(query("structure", 4, "framebuffer", namespaceLabel), "1m",
							querySample{queryLabels(map[string]string{"cluster": "test", "id": "workload|workloads|workload-1"}), 300 * 1048576},
							querySample{queryLabels(map[string]string{"cluster": "test", "id": "pod|workloads|pod-2"}), 300 * 1048576},
						)},
					})
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

		nodes := query("structure", 4, "nodes", "namespace")
		edges := query("structure", 4, "edges", "namespace")
		ancestors := query("structure", 4, "layoutAncestors", "namespace")
		leaves := query("structure", 4, "layoutLeaves", "namespace")
		graphPod := queryIdentity(pod, map[string]string{"component": "trainer", "component_instance": "replica", "replica_group_id": "group-a"})
		for _, test := range []struct {
			name      string
			values    string
			peer      map[string]string
			peerValue string
			leaves    float64
			accepted  bool
		}{
			{"Stale component conflicts expire", "1+0x20", map[string]string{"component": "old"}, "1 _x20", 2, true},
			{"Recreated Pods retain both UID identities", "1+0x20", map[string]string{"pod_uid": "old-pod"}, "1+0x20", 3, true},
			{"One UID with conflicting names is rejected", "1+0x20", map[string]string{"pod_name": "conflicting"}, "1+0x20", 1, false},
			{"One UID with conflicting components is rejected", "1+0x20", map[string]string{"component": "conflicting"}, "1+0x20", 1, false},
			{"Stale Pod mappings do not retain measurements", "1 _x20", nil, "", 1, false},
		} {
			input := []queryInputSeries{
				querySeries("dsx_workload_pod_info", test.values, graphPod),
				querySeries("dsx_workload_pod_info", "1+0x20", queryIdentity(graphPod, map[string]string{"pod_name": "healthy", "pod_uid": "pod-2"})),
			}
			if test.peer != nil {
				input = append(input, querySeries("dsx_workload_pod_info", test.peerValue, queryIdentity(graphPod, test.peer)))
			}
			for _, identity := range []struct{ name, uid, value string }{{"worker", "pod-1", "80"}, {"healthy", "pod-2", "10"}} {
				device := map[string]string{"cluster": "test", "namespace": "workloads", "job": "dcgm", "pod_uid": identity.uid, "UUID": "GPU-" + identity.uid}
				container := map[string]string{"cluster": "test", "namespace": "workloads", "job": "cadvisor", "pod": identity.name, "container": "main", "id": identity.uid}
				input = append(input,
					querySeries("DCGM_FI_DEV_GPU_UTIL", identity.value+"+0x20", device),
					querySeries("DCGM_FI_DEV_FB_USED", "300+0x20", device),
					querySeries("container_cpu_usage_seconds_total", "0+30x20", container),
					querySeries("container_memory_working_set_bytes", "4096+0x20", container),
				)
			}
			expectations := []queryExpectation{
				queryExpected("count("+nodes+")", "2m", querySample{"{}", test.leaves + 4}),
				queryExpected("count("+leaves+")", "2m", querySample{"{}", test.leaves}),
				queryExpected("count("+query("structure", 5, "A", "namespace")+")", "2m", querySample{"{}", test.leaves}),
				queryExpected("("+ancestors+") unless on (cluster, id) ("+nodes+")", "2m"),
				queryExpected("("+nodes+") unless on (cluster, id) ("+ancestors+")", "2m"),
			}
			for _, endpoint := range []string{"source", "target"} {
				expectations = append(expectations, queryExpected("label_replace(("+edges+"), \"id\", \"$1\", \""+endpoint+"\", \"(.+)\") unless on (cluster, id) ("+nodes+")", "2m"))
			}
			for _, resource := range []struct {
				ref   string
				value float64
			}{{"gpu", 10}, {"framebuffer", 300 * 1048576}, {"cpu", 0.4}, {"memory", 4096}} {
				expression := query("structure", 4, resource.ref, "namespace")
				expectations = append(expectations,
					queryExpected("("+expression+") unless on (cluster, id) ("+nodes+")", "2m"),
					queryExpected("max(("+expression+`) and on (id) label_replace(vector(1), "id", "pod|workloads|pod-2", "", ""))`, "2m", querySample{"{}", resource.value}),
				)
			}
			var accepted []querySample
			if test.accepted {
				accepted = []querySample{{"{}", 80}}
			}
			expectations = append(expectations, queryExpected("max(("+query("structure", 4, "gpu", "namespace")+`) and on (id) label_replace(vector(1), "id", "pod|workloads|pod-1", "", ""))`, "2m", accepted...))
			if test.name == "Recreated Pods retain both UID identities" {
				expectations = append(expectations, queryExpected("max(("+query("structure", 4, "gpu", "namespace")+`) and on (id) label_replace(vector(1), "id", "pod|workloads|old-pod", "", ""))`, "2m"))
			}
			if test.name == "Stale component conflicts expire" {
				var pairs []querySample
				for _, edge := range []struct{ source, target string }{
					{"workload|workloads|workload-1", "component|workloads|workload-1|trainer"},
					{"component|workloads|workload-1|trainer", "instance|workloads|workload-1|trainer|replica"},
					{"instance|workloads|workload-1|trainer|replica", "group|workloads|workload-1|trainer|replica|group-a"},
					{"instance|workloads|workload-1|trainer|replica", "pod|workloads|pod-1"},
					{"instance|workloads|workload-1|trainer|replica", "pod|workloads|pod-2"},
					{"group|workloads|workload-1|trainer|replica|group-a", "pod|workloads|pod-1"},
					{"group|workloads|workload-1|trainer|replica|group-a", "pod|workloads|pod-2"},
				} {
					pairs = append(pairs, querySample{queryLabels(map[string]string{"source": edge.source, "target": edge.target}), 1})
				}
				expectations = append(expectations, queryExpected("group by (source, target) ("+edges+")", "2m", pairs...))
			}
			for i, expectation := range expectations {
				queryNames[expectation.Expr] = fmt.Sprintf("graph identity assertion %d", i)
			}
			fixture.Tests = append(fixture.Tests, queryTest{Name: test.name, InputSeries: input, PromQLExprTest: expectations})
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
			if test.metric == "container_cpu_usage_seconds_total" {
				container["cpu"] = "total"
			}
			valid := []queryInputSeries{
				querySeries("dsx_workload_pod_info", "1+0x20", pod),
				querySeries(test.metric, test.values, queryIdentity(container, map[string]string{"instance": "replica-a"})),
				querySeries(test.metric, test.values, queryIdentity(container, map[string]string{"instance": "replica-b"})),
			}
			for _, mapping := range []struct {
				name     string
				values   string
				fresh    bool
				expected []querySample
			}{
				{"Stale mappings cannot attribute current container usage", "1+0x6 _x20", false, nil},
				{"Stale old UID does not reject a fresh replacement", "1+0x6 _x20", true, []querySample{{queryLabels(resultLabels), test.expected}}},
				{"Fresh same-name UIDs remain ambiguous for containers", "1+0x20", true, nil},
			} {
				input := []queryInputSeries{
					querySeries("dsx_workload_pod_info", mapping.values, queryIdentity(pod, map[string]string{"pod_uid": "old-pod", "workload_name": "retired", "workload_uid": "old-workload"})),
					querySeries(test.metric, test.values, container),
				}
				if mapping.fresh {
					input = append(input, querySeries("dsx_workload_pod_info", "1+0x20", pod))
				}
				expectations := []queryExpectation{queryExpected(expression, "5m", mapping.expected...)}
				if test.dashboard == "overview" {
					panel := 19
					if test.metric == "container_memory_working_set_bytes" {
						panel = 20
					}
					selectedWorkloads := variables["workload"]
					variables["workload"] = []string{"$__all"}
					var attributed []querySample
					if len(mapping.expected) > 0 {
						attributed = []querySample{{queryLabels(workload), test.expected}}
					}
					expectations = append(expectations, queryExpected(query("overview", panel, "A", "namespace"), "5m", attributed...))
					variables["workload"] = selectedWorkloads
				}
				fixture.Tests = append(fixture.Tests, queryTest{
					Name:           fmt.Sprintf("%s: %s/%d", mapping.name, test.dashboard, test.panel),
					InputSeries:    input,
					PromQLExprTest: expectations,
				})
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
				"cpu": "total",
			}

			fixture.Tests = append(fixture.Tests, queryTest{
				Name: fmt.Sprintf("A first-sample replacement ID blocks the old container rate: %s/%d/%s", test.dashboard, test.panel, test.ref),
				InputSeries: []queryInputSeries{
					querySeries("dsx_workload_pod_info", "1+0x30", pod),
					querySeries("container_cpu_usage_seconds_total", "0+30x10 stale", container),
					querySeries("container_cpu_usage_seconds_total", "_x10 30+30x20", queryIdentity(container, map[string]string{"id": "replacement"})),
				},
				PromQLExprTest: []queryExpectation{
					queryExpected(expression, "5m"),
					queryExpected(expression, "10m", querySample{"{}", test.expected}),
				},
			})
			for _, cpu := range []string{"total", "cpu00"} {
				fixture.Tests = append(fixture.Tests, queryTest{
					Name: fmt.Sprintf("A first-sample peer does not hide an available CPU rate: %s/%d/%s/%s", test.dashboard, test.panel, test.ref, cpu),
					InputSeries: []queryInputSeries{
						querySeries("dsx_workload_pod_info", "1+0x30", pod),
						querySeries("container_cpu_usage_seconds_total", "0+30x20", queryIdentity(container, map[string]string{"cpu": cpu, "instance": "ready"})),
						querySeries("container_cpu_usage_seconds_total", "_x10 300", queryIdentity(container, map[string]string{"cpu": cpu, "instance": "new"})),
					},
					PromQLExprTest: []queryExpectation{queryExpected(expression, "5m", querySample{"{}", test.expected})},
				})
			}

			firstCore, secondCore := 0.2, 0.4
			for _, shape := range []struct {
				name, core, peer, total, totalPeer, aggregate string
				cores                                         float64
				unavailable                                   bool
			}{
				{name: "Unequal cores sum after replica agreement", core: "12+12x20", cores: firstCore + secondCore},
				{name: "Equal cores are distinct CPU measurements", core: "6+6x20", cores: firstCore + firstCore},
				{name: "Unreported zero-cumulative cores need no invented sample", cores: firstCore},
				{name: "A total stream wins over per-core streams", core: "12+12x20", total: "0+30x20", cores: 1},
				{name: "One conflicting core rejects the entire container", core: "12+12x20", peer: "18+18x20", unavailable: true},
				{name: "Conflicting total streams cannot fall back to cores", core: "12+12x20", total: "0+30x20", totalPeer: "0+60x20", unavailable: true},
				{name: "Verified normalized and explicit totals agree", core: "12+12x20", total: "0+30x20", aggregate: "0+30x20", cores: 1},
				{name: "Conflicting normalized and explicit totals are unavailable", core: "12+12x20", total: "0+30x20", aggregate: "0+60x20", unavailable: true},
				{name: "A new total without rate history blocks core fallback", core: "12+12x20", total: "_x10 30", unavailable: true},
				{name: "A new reported core without rate history prevents a partial sum", core: "_x10 6", unavailable: true},
			} {
				input := []queryInputSeries{querySeries("dsx_workload_pod_info", "1+0x20", pod)}
				for _, replica := range []string{"replica-a", "replica-b"} {
					input = append(input, querySeries("container_cpu_usage_seconds_total", "6+6x20", queryIdentity(container, map[string]string{"cpu": "cpu00", "instance": replica})))
					if shape.core != "" {
						values := shape.core
						if replica == "replica-b" && shape.peer != "" {
							values = shape.peer
						}
						input = append(input, querySeries("container_cpu_usage_seconds_total", values, queryIdentity(container, map[string]string{"cpu": "cpu01", "instance": replica})))
					}
					if shape.total != "" && replica == "replica-a" {
						input = append(input, querySeries("container_cpu_usage_seconds_total", shape.total, queryIdentity(container, map[string]string{"instance": "total-a"})))
					}
				}
				if shape.totalPeer != "" {
					input = append(input, querySeries("container_cpu_usage_seconds_total", shape.totalPeer, queryIdentity(container, map[string]string{"instance": "total-b"})))
				}
				if shape.aggregate != "" {
					input = append(input, querySeries("container_cpu_usage_seconds_total", shape.aggregate, queryIdentity(container, map[string]string{"cpu": "", "instance": "normalized"})))
				}
				var expected []querySample
				if !shape.unavailable {
					expected = []querySample{{"{}", shape.cores * test.expected}}
				}
				fixture.Tests = append(fixture.Tests, queryTest{
					Name:           fmt.Sprintf("%s: %s/%d/%s", shape.name, test.dashboard, test.panel, test.ref),
					InputSeries:    input,
					PromQLExprTest: []queryExpectation{queryExpected(expression, "5m", expected...)},
				})
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

		for _, test := range []struct {
			dashboard           string
			panel               int
			ref, metric, values string
			expected            float64
		}{
			{"overview", 104, "A", "container_cpu_usage_seconds_total", "0+30x30", 1000},
			{"overview", 8, "A", "container_cpu_usage_seconds_total", "0+30x30", 1000},
			{"overview", 19, "A", "container_cpu_usage_seconds_total", "0+30x30", 1000},
			{"overview", 109, "A", "container_cpu_usage_seconds_total", "0+30x30", 1000},
			{"overview", 111, "A", "container_cpu_usage_seconds_total", "0+30x30", 1000},
			{"structure", 11, "A", "container_cpu_usage_seconds_total", "0+30x30", 1000},
			{"structure", 4, "cpu", "container_cpu_usage_seconds_total", "0+30x30", 1},
			{"overview", 105, "A", "container_memory_working_set_bytes", "4096+0x30", 4096},
			{"overview", 9, "A", "container_memory_working_set_bytes", "4096+0x30", 4096},
			{"overview", 20, "A", "container_memory_working_set_bytes", "4096+0x30", 4096},
			{"overview", 110, "A", "container_memory_working_set_bytes", "4096+0x30", 4096},
			{"overview", 112, "A", "container_memory_working_set_bytes", "4096+0x30", 4096},
			{"structure", 12, "A", "container_memory_working_set_bytes", "4096+0x30", 4096},
			{"structure", 4, "memory", "container_memory_working_set_bytes", "4096+0x30", 4096},
		} {
			expression := maximumQuery(test.dashboard, test.panel, test.ref, "namespace")
			container := map[string]string{
				"cluster": "test", "job": "cadvisor", "namespace": "workloads", "pod": "worker", "container": "main", "id": "first",
			}
			if test.metric == "container_cpu_usage_seconds_total" {
				container["cpu"] = "total"
			}
			fixture.Tests = append(fixture.Tests, queryTest{
				Name: fmt.Sprintf("Defensive malformed Pod UID mappings reject resource telemetry: %s/%d/%s", test.dashboard, test.panel, test.ref),
				InputSeries: []queryInputSeries{
					querySeries("dsx_workload_pod_info", "1+0x30", pod),
					querySeries("dsx_workload_pod_info", "1+0x10 stale", queryIdentity(pod, map[string]string{"pod_name": "other", "workload_name": "other", "workload_uid": "workload-2"})),
					querySeries(test.metric, test.values, container),
				},
				PromQLExprTest: []queryExpectation{
					queryExpected(expression, "5m"),
					queryExpected(expression, "6m", querySample{"{}", test.expected}),
				},
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
			variables["__range"] = []string{"15s"}
			fixture.Tests = append(fixture.Tests, queryTest{
				Name: "Workload remains selectable between scrapes: " + metric,
				InputSeries: []queryInputSeries{querySeries(metric, "1 1", queryIdentity(workload, map[string]string{
					"job": "dsx", "scope": "workload",
				}))},
				PromQLExprTest: []queryExpectation{queryExpected(query("structure", 0, "workload_uid", "namespace"), "20s", querySample{
					queryLabels(queryIdentity(workload, map[string]string{"job": "dsx", "selection": "training / workload-1"})), 1,
				})},
			})
			variables["__range"] = []string{"10m"}
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
		gpuOnlyTimeline := maximumQuery("overview", 308, "A", "namespace")
		variables["activity_view"] = []string{"5"}
		fixture.Tests = append(fixture.Tests, queryTest{
			Name:        "Failure-only workloads can be selected and retain their inventory row",
			InputSeries: []queryInputSeries{querySeries("dsx_exporter_observation_valid", "0+0x2 _x8", queryIdentity(workload, map[string]string{"job": "dsx", "scope": "workload"}))},
			PromQLExprTest: []queryExpectation{
				queryExpected(query("overview", 0, "workload", "namespace"), "1m", querySample{`{workload_name="training"}`, 1}),
				queryExpected(query("overview", 4, "A", "namespace"), "1m", querySample{queryLabels(queryIdentity(workload, map[string]string{"job": "dsx"})), 1}),
				queryExpected(query("overview", 310, "A", "namespace"), "1m", querySample{"{}", 1}),
				stateExpectation(query("overview", 306, "A", "namespace"), "1m", "unknown"),
				queryExpected(maximumQuery("overview", 308, "A", "namespace"), "1m", querySample{"{}", 4}),
				queryExpected(gpuOnlyTimeline, "1m"),
				queryExpected(query("overview", 310, "A", "namespace"), "3m"),
				queryExpected(query("overview", 306, "A", "namespace"), "3m"),
				queryExpected(maximumQuery("overview", 308, "A", "namespace"), "3m"),
			},
		})
		fixture.Tests = append(fixture.Tests, queryTest{
			Name: "Activity identities require workload scope and a UID",
			InputSeries: []queryInputSeries{
				querySeries("dsx_exporter_observation_valid", "0+0x2", queryIdentity(workload, map[string]string{"job": "dsx", "scope": "collection"})),
				querySeries("dsx_exporter_observation_valid", "0+0x2", queryIdentity(workload, map[string]string{"job": "dsx", "scope": "workload", "workload_uid": ""})),
			},
			PromQLExprTest: []queryExpectation{
				queryExpected(query("overview", 310, "A", "namespace"), "1m"),
				queryExpected(query("overview", 306, "A", "namespace"), "1m"),
				queryExpected(maximumQuery("overview", 308, "A", "namespace"), "1m"),
			},
		})
		variables = maps.Clone(defaults)
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
				Name:        "Exporter job selectors preserve literal names: " + job,
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
			// The selected end stays fixed while the subquery evaluates earlier points.
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
		variables["__range"] = []string{"11m"}
		// Grafana's selected end is 650s, while its 60s display step rounds the query end to 600s.
		selectedEnd := strings.ReplaceAll(query("overview", 308, "A", "namespace"), "${__to:date:seconds}", "650")
		queryNames[selectedEnd] = "activity timeline with selected end 650s"
		fixture.Tests = append(fixture.Tests, queryTest{
			Name: "Timeline row selection uses the selected end instead of the rounded display end",
			InputSeries: []queryInputSeries{
				querySeries("dsx_workload_pod_info", "1+0x22", queryIdentity(activeWorkload, rangePod, map[string]string{"pod_uid": "pod-1"})),
				querySeries("dsx_workload_pod_info", "1+0x22", queryIdentity(quietWorkload, rangePod, map[string]string{"pod_uid": "pod-2"})),
				querySeries("DCGM_FI_DEV_GPU_UTIL", "0+0x20 80+0x1", queryIdentity(rangeGPU, map[string]string{"pod_uid": "pod-1", "UUID": "GPU-1"})),
				querySeries("DCGM_FI_DEV_GPU_UTIL", "80+0x20 0+0x1", queryIdentity(rangeGPU, map[string]string{"pod_uid": "pod-2", "UUID": "GPU-2"})),
			},
			PromQLExprTest: []queryExpectation{
				queryExpected(selectedEnd, "2m", querySample{rangeLabels, 3}),
				queryExpected(selectedEnd, "10m", querySample{rangeLabels, 2}),
				queryExpected(selectedEnd, "10m50s", querySample{rangeLabels, 1}),
			},
		})
		variables = defaults
		for i := range fixture.Tests {
			if fixture.Tests[i].Interval == "" {
				fixture.Tests[i].Interval = "30s"
			}
			for j := range fixture.Tests[i].PromQLExprTest {
				expectation := &fixture.Tests[i].PromQLExprTest[j]
				if strings.Contains(expectation.Expr, "${__to:date:seconds}") {
					// Existing fixtures select a range ending at their evaluation time.
					end, err := time.ParseDuration(expectation.EvalTime)
					Expect(err).NotTo(HaveOccurred())
					resolved := strings.ReplaceAll(expectation.Expr, "${__to:date:seconds}", strconv.FormatInt(int64(end.Round(time.Second)/time.Second), 10))
					if name, ok := queryNames[expectation.Expr]; ok {
						queryNames[resolved] = name
					}
					expectation.Expr = resolved
				}
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
