// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package grafana_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const dashboardDirectory = "../../../docs/tutorials/grafana"

// Grafana v12.2.1 registers these in public/app/features/transformers/standardTransformers.ts.
// Internal helpers such as "order" belong under their public transformation ("organize").
var nativeTransformations = strings.Fields(`
reduce filterFieldsByName renameByRegex filterByRefId filterByValue organize
joinByField seriesToRows concatenate calculateField labelsToFields groupBy sortBy
merge histogram rowsToFields configFromData prepareTimeSeries convertFieldType
spatial fieldLookup extractFields heatmap groupingToMatrix limit joinByLabels
regression partitionByValues formatString groupToNestedTable formatTime
timeSeriesTable transpose
`)

var _ = Describe("Dashboard portability", func() {
	for _, example := range []struct {
		name string
		uid  string
	}{
		{"workload-overview.json", "dsx-example-workloads"},
		{"workload-structure.json", "dsx-example-structure"},
	} {
		Describe(example.name, func() {
			var dashboard map[string]any
			var variables map[string]map[string]any

			BeforeEach(func() {
				dashboard = readDashboard(example.name)
				variables = make(map[string]map[string]any)
				for _, value := range dashboard["templating"].(map[string]any)["list"].([]any) {
					variable := value.(map[string]any)
					variables[variable["name"].(string)] = variable
				}
			})

			It("uses native transformations", func() {
				visitJSON(dashboard["panels"], func(value any) {
					panel, ok := value.(map[string]any)
					if !ok || panel["transformations"] == nil {
						return
					}
					for _, value := range panel["transformations"].([]any) {
						transform := value.(map[string]any)
						Expect(nativeTransformations).To(ContainElement(transform["id"]),
							"panel %v: transformation %v is not registered in Grafana 12.2.1", panel["id"], transform["id"])
					}
				})
			})

			It("fits in one ConfigMap", func() {
				info, err := os.Stat(filepath.Join(dashboardDirectory, example.name))
				Expect(err).NotTo(HaveOccurred())
				Expect(info.Size()).To(BeNumerically("<=", 1024*1024), "demo provisions one ConfigMap per dashboard")
			})

			It("has stable import IDs and unique panel IDs", func() {
				Expect(dashboard["uid"]).To(Equal(example.uid))
				Expect(dashboard["id"]).To(BeNil())
				ids := make(map[any]bool)
				visitJSON(dashboard["panels"], func(value any) {
					panel, ok := value.(map[string]any)
					if !ok {
						return
					}
					if _, exists := panel["gridPos"]; exists {
						Expect(panel).To(HaveKey("id"), "panel with gridPos must have an ID")
						Expect(ids).NotTo(HaveKey(panel["id"]), "duplicate panel ID %v", panel["id"])
						ids[panel["id"]] = true
					}
				})
			})

			It("discovers the datasource and jobs without saved selections", func() {
				Expect(variables).To(HaveKey("datasource"))
				Expect(variables["datasource"]["type"]).To(Equal("datasource"))
				Expect(variables["datasource"]["query"]).To(Equal("prometheus"))
				for _, name := range []string{"datasource", "exporter_job", "cadvisor_job", "dcgm_job", "cluster"} {
					Expect(variables).To(HaveKey(name))
					variable := variables[name]
					current, _ := variable["current"].(map[string]any)
					Expect(current["value"]).To(Or(BeNil(), BeZero(), BeEmpty()), "%s has a saved selection", name)
					if name != "cluster" {
						Expect(variable["options"]).To(Or(BeNil(), BeZero(), BeEmpty()), "%s has saved options", name)
					}
				}
				for _, name := range []string{"exporter_job", "cadvisor_job", "dcgm_job"} {
					Expect(variables[name]["type"]).To(Equal("query"), name)
					Expect(variables[name]["includeAll"]).To(Or(BeNil(), BeZero(), BeEmpty()), name)
				}
				Expect(variables["cluster"]["type"]).To(Equal("textbox"))
				Expect(variables["cluster"]["query"]).To(Or(BeNil(), BeZero(), BeEmpty()))
			})

			It("refreshes history variables when the time range changes", func() {
				for _, value := range dashboard["templating"].(map[string]any)["list"].([]any) {
					variable := value.(map[string]any)
					name := variable["name"]
					query, err := json.Marshal(variable["query"])
					Expect(err).NotTo(HaveOccurred(), name)
					if strings.Contains(string(query), "$__range") {
						Expect(variable["refresh"]).To(BeNumerically("==", 2), "variable %s", name)
						Expect(variable["definition"]).To(Equal(variable["query"].(map[string]any)["query"]), "variable %s", name)
					}
				}
			})

			It("queries raw metrics through the selected datasource", func() {
				visitJSON(dashboard, func(value any) {
					item, ok := value.(map[string]any)
					if !ok {
						return
					}
					if expression, ok := item["expr"].(string); ok && expression != "" {
						Expect(expression).NotTo(ContainSubstring("dsx:"), "query %v", item["refId"])
						Expect(expression).NotTo(ContainSubstring("workload_gpu_observer_"), "query %v", item["refId"])
						Expect(expression).NotTo(MatchRegexp(`==\s*(?:7|9)\b`),
							"query %v: phase availability must not assume an exporter version", item["refId"])
					}
					if source, ok := item["datasource"].(map[string]any); ok && source["type"] == "prometheus" {
						Expect(source["uid"]).To(BeElementOf("$datasource", "${datasource}"), "datasource %v", source)
					}
				})
			})

			It("resolves every template reference", func() {
				reference := regexp.MustCompile(`\$\{([A-Za-z_]\w*)(?::[^}]+)?\}|\$([A-Za-z_]\w*)`)
				visitJSON(dashboard, func(value any) {
					text, ok := value.(string)
					if !ok {
						return
					}
					for _, match := range reference.FindAllStringSubmatch(text, -1) {
						name := match[1]
						if name == "" {
							name = match[2]
						}
						if !strings.HasPrefix(name, "__") {
							Expect(variables).To(HaveKey(name), "undeclared variable %s in %q", name, text)
						}
					}
				})
			})

			It("has no saved environment or external dashboard links", func() {
				knownUIDs := make(map[string]bool)
				files, err := filepath.Glob(filepath.Join(dashboardDirectory, "*.json"))
				Expect(err).NotTo(HaveOccurred())
				for _, file := range files {
					data, err := os.ReadFile(file)
					Expect(err).NotTo(HaveOccurred(), file)
					var value any
					Expect(json.Unmarshal(data, &value)).To(Succeed(), file)
					if object, ok := value.(map[string]any); ok {
						if uid, ok := object["uid"].(string); ok {
							knownUIDs[uid] = true
						}
					}
				}
				content, err := json.Marshal(dashboard)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(content)).NotTo(MatchRegexp(`https?://`))
				link := regexp.MustCompile(`(?:^|/)d/([^/?]+)`)
				visitJSON(dashboard, func(value any) {
					item, ok := value.(map[string]any)
					if !ok {
						return
					}
					url, _ := item["url"].(string)
					if match := link.FindStringSubmatch(url); match != nil {
						Expect(knownUIDs).To(HaveKey(match[1]), "unknown dashboard in link %q", url)
						Expect(url).To(ContainSubstring("${datasource:queryparam}"), url)
						Expect(url).To(ContainSubstring("${cluster:queryparam}"), url)
						Expect(url).To(ContainSubstring("${namespace:queryparam}"), url)
					}
				})
			})
		})
	}
})

func readDashboard(name string) map[string]any {
	GinkgoHelper()
	data, err := os.ReadFile(filepath.Join(dashboardDirectory, name))
	Expect(err).NotTo(HaveOccurred(), name)
	var dashboard map[string]any
	Expect(json.Unmarshal(data, &dashboard)).To(Succeed(), name)
	return dashboard
}

func visitJSON(value any, visit func(any)) {
	visit(value)
	switch value := value.(type) {
	case map[string]any:
		for _, child := range value {
			visitJSON(child, visit)
		}
	case []any:
		for _, child := range value {
			visitJSON(child, visit)
		}
	}
}
