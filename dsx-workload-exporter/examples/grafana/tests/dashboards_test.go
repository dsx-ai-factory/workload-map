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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

var _ = Describe("Dashboard examples", func() {
	for _, filename := range []string{"workload-overview.json", "workload-structure.json", "metric-samples.json"} {
		Context(filename, func() {
			var d dashboard
			var variables map[string]variable
			var targets []map[string]any

			BeforeEach(func() {
				d = readDashboard(filename)
				variables = make(map[string]variable)
				for _, v := range d.Templating.List {
					Expect(variables).NotTo(HaveKey(v.Name), "duplicate variable")
					variables[v.Name] = v
				}
				targets = nil
				for _, obj := range d.objects {
					if _, ok := obj["expr"]; ok {
						targets = append(targets, obj)
					}
				}
			})

			It("imports without installation-specific metadata", func() {
				Expect(d.root).NotTo(HaveKey("dashboard"), "export the portable dashboard, not the API envelope")
				Expect(d.root).NotTo(HaveKey("meta"), "remove API metadata")
				Expect(d.root["id"]).To(BeNil(), "remove the Grafana installation ID")
				Expect(d.UID).To(Equal(dashboardUIDs[filename]))
				Expect(d.Version).To(BeNumerically("<=", 1), "remove the saved dashboard version")
				Expect(d.Refresh).To(Equal("30s"))
			})

			It("lets users choose their data source without saved query selections", func() {
				Expect(variables["datasource"].Type).To(Equal("datasource"))
				Expect(variables["datasource"].Hide).To(BeZero(), "datasource picker must be visible")
				Expect(variables).To(HaveKey("exporter_job"))
				Expect(variables["exporter_job"].Multi).To(BeFalse(), "select one exporter job")
				for _, v := range variables {
					if v.Type != "datasource" && v.Type != "query" {
						continue
					}
					if len(v.Current) != 0 {
						Expect(v.Current["value"]).To(Equal([]any{"$__all"}), "variable %s: remove the saved selection", v.Name)
					}
					Expect(v.Options).To(BeEmpty(), "variable %s: remove cached options", v.Name)
				}
			})

			It("uses unique panel IDs, portable data sources and relative links", func() {
				panelIDs := make(map[any]bool)
				for _, obj := range d.objects {
					if _, ok := obj["gridPos"]; ok {
						Expect(obj["id"]).NotTo(BeNil(), "panel %v needs an ID", obj["title"])
						Expect(panelIDs).NotTo(HaveKey(obj["id"]), "duplicate panel ID")
						panelIDs[obj["id"]] = true
					}
					if ds, ok := obj["datasource"]; ok {
						Expect(ds).To(Equal(datasource), "datasource on %v", obj["title"])
					}
					if obj["type"] == "query" {
						Expect(obj["datasource"]).To(Equal(datasource), "query variable %v", obj["name"])
					}
					if value, ok := obj["spanNulls"]; ok {
						Expect(value).To(Equal(false), "do not span missing observations")
					}
					if value, ok := obj["url"]; ok {
						Expect(value).NotTo(MatchRegexp(`^(?:https?://|/)`), "links must be portable")
					}
				}
				Expect(panelIDs).NotTo(BeEmpty(), "dashboard needs panels")
			})

			It("runs visible queries against the selected data source and observer job", func() {
				Expect(targets).NotTo(BeEmpty(), "dashboard needs queries")
				for i, target := range targets {
					By(fmt.Sprintf("checking query %d (%v)", i+1, target["refId"]))
					expr, ok := target["expr"].(string)
					Expect(ok).To(BeTrue(), "PromQL expression must be a string")
					Expect(target["datasource"]).To(Equal(datasource))
					Expect(target["hide"]).NotTo(Equal(true), "remove hidden queries")
					for _, name := range observerMetricPattern.FindAllString(expr, -1) {
						Expect(filename).To(Equal("workload-overview.json"), "observer queries belong in the overview")
						Expect(observerMetrics).To(ContainElement(name), "unknown observer metric")
					}
					selectors, err := observerSelectors(expr)
					Expect(err).NotTo(HaveOccurred())
					for _, selector := range selectors {
						Expect(selector).To(MatchRegexp(observerJobPattern.String()), "observer selector needs job=~\"${observer_job:regex}\"")
					}
				}
			})

			It("references supported metrics and declared variables", func() {
				for _, metric := range regexp.MustCompile(`\bdsx_[a-zA-Z0-9_]+`).FindAllString(d.text, -1) {
					if metric != "dsx_workload_" {
						Expect(metrics).To(ContainElement(metric), "unknown exporter metric")
					}
				}
				for _, rule := range regexp.MustCompile(`\bdsx:[a-zA-Z0-9_:]+`).FindAllString(d.text, -1) {
					Expect(recordingRules).To(ContainElement(rule), "unknown recording rule")
					Expect(filename).NotTo(Equal("metric-samples.json"), "raw samples must not use recording rules")
				}
				Expect(d.text).NotTo(MatchRegexp(`karta_demo:|\bgpu_observer_|simulator_|/karta/grafana`), "remove demo-specific references")
				for _, name := range variableReferences(d.text) {
					if !strings.HasPrefix(name, "__") {
						Expect(variables).To(HaveKey(name), "undeclared dashboard variable")
					}
				}
			})

			if filename != "metric-samples.json" {
				It("supports multiple namespaces and MIG profiling sources", func() {
					Expect(variables["namespace"].Multi).To(BeTrue())
					Expect(variables["dcgm_job"].Definition).To(ContainSubstring("DCGM_FI_PROF_GR_ENGINE_ACTIVE"))
				})
			}
			switch filename {
			case "workload-overview.json":
				It("offers GPU, active-only and all-workload activity views", func() {
					Expect(variables["gpu_device"].Definition).To(ContainSubstring("DCGM_FI_PROF_GR_ENGINE_ACTIVE"))
					activity := variables["activity_view"]
					Expect(activity.Type).To(Equal("custom"))
					Expect(activity.Multi).To(BeFalse())
					Expect(activity.IncludeAll).To(BeFalse())
					Expect(activity.Current).To(Equal(map[string]any{"text": "GPU workloads", "value": "4"}))
					Expect(activity.Options).To(ConsistOf(variableOption{"GPU workloads", "4"}, variableOption{"Active only", "1"}, variableOption{"All workloads", "5"}))
					var query string
					Expect(json.Unmarshal(activity.Query, &query)).To(Succeed())
					options := strings.Split(query, ",")
					for i, option := range options {
						text, value, ok := strings.Cut(option, ":")
						Expect(ok).To(BeTrue(), "activity option %q must have a value", option)
						options[i] = strings.TrimSpace(text) + ":" + strings.TrimSpace(value)
					}
					Expect(options).To(ConsistOf("GPU workloads:4", "Active only:1", "All workloads:5"), "activity query must match its options")
				})

				It("applies the activity filter only to its timeline", func() {
					for _, obj := range d.objects {
						if _, isPanel := obj["gridPos"]; !isPanel || obj["id"] == float64(308) {
							continue
						}
						// Nested panels have their own IDs and are checked separately.
						for key, value := range obj {
							if key == "panels" {
								continue
							}
							text, err := json.Marshal(value)
							Expect(err).NotTo(HaveOccurred())
							Expect(variableReferences(string(text))).NotTo(ContainElement("activity_view"), "panel %v: activity view applies only to panel 308", obj["id"])
						}
					}
				})
			case "workload-structure.json":
				It("shows a node graph for one selected workload", func() {
					Expect(variables).To(HaveKey("workload_uid"))
					Expect(variables["workload_uid"].Multi).To(BeFalse())
					Expect(variables["workload_uid"].IncludeAll).To(BeFalse())
					Expect(d.objects).To(ContainElement(HaveKeyWithValue("type", "nodeGraph")))
				})
			case "metric-samples.json":
				It("shows unaggregated instant samples of the four exporter metrics", func() {
					Expect(variables["metric"].Type).To(Equal("custom"))
					var query string
					Expect(json.Unmarshal(variables["metric"].Query, &query)).To(Succeed())
					Expect(strings.Split(query, ",")).To(ConsistOf(metrics))
					Expect(variables).NotTo(HaveKey("workload_uid"))
					Expect(targets).To(HaveLen(1))
					Expect(targets[0]["instant"]).To(Equal(true))
					expr, ok := targets[0]["expr"].(string)
					Expect(ok).To(BeTrue())
					Expect(expr).To(HavePrefix(`{__name__="${metric}",`))
					Expect(expr).To(HaveSuffix("}"))
					Expect(expr).To(ContainSubstring(`namespace=~"${namespace:regex}|^$"`), "include cluster-scoped samples")
					Expect(expr).NotTo(MatchRegexp(`\b(?:sum|count|max|min|rate|group)\s*\(`), "do not aggregate raw samples")
				})
			}
		})
	}
})

func readDashboard(filename string) dashboard {
	GinkgoHelper()
	data, err := os.ReadFile(filepath.Join("..", filename))
	Expect(err).NotTo(HaveOccurred(), "%s", filename)
	var d dashboard
	Expect(json.Unmarshal(data, &d)).To(Succeed(), "%s", filename)
	Expect(json.Unmarshal(data, &d.root)).To(Succeed(), "%s", filename)
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

var _ = Describe("Grafana variable references", func() {
	DescribeTable("recognizes each activity variable spelling", func(text string) {
		Expect(variableReferences(text)).To(Equal([]string{"activity_view"}))
	},
		Entry("bare", "$activity_view"),
		Entry("braced", "${activity_view}"),
		Entry("raw", "${activity_view:raw}"),
		Entry("regex", "${activity_view:regex}"),
	)

	It("keeps longer variable names intact and ignores replacement captures", func() {
		Expect(variableReferences(`$1 $activity_view_extra ${__range_s}`)).To(Equal([]string{"activity_view_extra", "__range_s"}))
	})
})

var _ = Describe("Observer selectors", func() {
	DescribeTable("identifies selectors missing the selected observer job", func(expression string, expected []string) {
		var unscoped []string
		selectors, err := observerSelectors(expression)
		Expect(err).NotTo(HaveOccurred())
		for _, selector := range selectors {
			if !observerJobPattern.MatchString(selector) {
				unscoped = append(unscoped, selector)
			}
		}
		Expect(unscoped).To(Equal(expected))
	},
		Entry("scoped", `workload_gpu_observer_state{job=~"${observer_job:regex}", state="active"}`, []string(nil)),
		Entry("second selector unscoped", `workload_gpu_observer_state{job=~"${observer_job:regex}"} + workload_gpu_observer_state{state="active"}`, []string{`workload_gpu_observer_state{state="active"}`}),
		Entry("bare metric", `sum(workload_gpu_observer_state) + up{job=~"${observer_job:regex}"}`, []string{"workload_gpu_observer_state"}),
		Entry("wrong label", `workload_gpu_observer_state{other_job=~"${observer_job:regex}"}`, []string{`workload_gpu_observer_state{other_job=~"${observer_job:regex}"}`}),
		Entry("wrong matcher", `workload_gpu_observer_state{job!~"${observer_job:regex}"}`, []string{`workload_gpu_observer_state{job!~"${observer_job:regex}"}`}),
		Entry("quoted metric name", `label_replace(up, "name", "workload_gpu_observer_state", "", ".*")`, []string(nil)),
		Entry("quoted braces", `workload_gpu_observer_state{state="x}y", job =~ "${observer_job:regex}"}`, []string(nil)),
	)

	DescribeTable("requires explicit observer metric names", func(expression string) {
		_, err := observerSelectors(expression)
		Expect(err).To(MatchError(ContainSubstring("explicit metric names")))
	},
		Entry("exact name", `{__name__="workload_gpu_observer_state"}`),
		Entry("name regex", `{__name__=~"workload_gpu_observer_state|workload_gpu_observer_assigned_devices"}`),
		Entry("single-quoted name", `{__name__='workload_gpu_observer_state',job=~"${observer_job:regex}"}`),
	)
})
