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
	Panels []struct {
		ID      int `json:"id"`
		Targets []struct {
			RefID string `json:"refId"`
			Expr  string `json:"expr"`
		} `json:"targets"`
	} `json:"panels"`
}

var _ = Describe("Dashboard query results", Label("promql"), func() {
	It("evaluates the shipped queries against synthetic telemetry", func() {
		variables := map[string]string{
			"cluster": "test", "exporter_job": "dsx", "cadvisor_job": "cadvisor", "dcgm_job": "dcgm",
			"namespace": "workloads", "workload": "training", "workload_uid": "workload-1",
			"component": ".*", "component_instance": ".*", "gpu_device": ".*", "activity_view": "4",
			"__range": "10m", "__rate_interval": "5m",
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
		variablePattern := regexp.MustCompile(`\$(?:\{([A-Za-z_]\w*)(?::\w+)?\}|([A-Za-z_]\w*))`)
		query := func(dashboard string, panelID int, ref, namespaceLabel string) string {
			name := fmt.Sprintf("workload-%s/%d/%s (%s)", dashboard, panelID, ref, namespaceLabel)
			data, err := os.ReadFile(filepath.Join(dashboardDirectory, "workload-"+dashboard+".json"))
			Expect(err).NotTo(HaveOccurred())
			var document queryDashboard
			Expect(json.Unmarshal(data, &document)).To(Succeed())
			var expression string
			for _, panel := range document.Panels {
				if panel.ID == panelID {
					for _, target := range panel.Targets {
						if target.RefID == ref {
							expression = target.Expr
						}
					}
				}
			}
			Expect(expression).NotTo(BeEmpty(), "missing query %s", name)
			variables["dcgm_namespace_label"] = namespaceLabel
			expression = variablePattern.ReplaceAllStringFunc(expression, func(match string) string {
				groups := variablePattern.FindStringSubmatch(match)
				variable := groups[1]
				if variable == "" {
					variable = groups[2]
				}
				value, exists := variables[variable]
				Expect(exists).To(BeTrue(), "unknown variable %s in %s", variable, name)
				return value
			})
			queryNames[expression] = name
			return expression
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
					stateExpectation(stateQuery, "4m", "low_activity"),
					stateExpectation(stateQuery, "6m", "unknown"),
				},
			})
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
		}
		for i := range fixture.Tests {
			fixture.Tests[i].Interval = "30s"
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
