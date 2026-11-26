//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	"fmt"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Each protocol has its own exporter release and receiver file, so a batch in
// push-http can only have come over OTLP/HTTP.
var pushFiles = map[string]string{"http/protobuf": "push-http", "grpc": "push-grpc"}

var _ = Describe("OTLP push", Serial, Ordered, func() {
	var uid string

	BeforeAll(func() {
		By("starting a Collector that writes every batch it receives to a file")
		submitWorkload("otel-receiver")
		command("", "rollout", "status", "-n", "exporter-otlp", "deployment/otel-receiver", "--timeout=120s")

		By("installing an exporter per protocol that pushes the shard a namespace every 2s")
		for release, settings := range map[string][]string{
			"exporter-e2e-otlp-http": {"protocol=http/protobuf", "endpoint=http://otel-receiver.exporter-otlp.svc:4318/v1/metrics"},
			"exporter-e2e-otlp-grpc": {"protocol=grpc", "endpoint=http://otel-receiver.exporter-otlp.svc:4317"},
		} {
			args := []string{
				"--set", "exporter.selection.namespaceSelector.matchLabels.exporter-e2e-shard=a",
				"--set", "exporter.metrics.delivery.mode=otlp",
				"--set", "exporter.metrics.delivery.otlp.interval=2s",
				"--set", "exporter.metrics.delivery.otlp.timeout=1s",
			}
			for _, setting := range settings {
				args = append(args, "--set", "exporter.metrics.delivery.otlp."+setting)
			}
			installExporter(release, args...)
		}

		By("running the lifecycle Pod")
		submitWorkload("pod-lifecycle")
		waitForRunningContainer("lifecycle", "initialize", true)
		command("", "exec", "-n", namespace, "lifecycle", "-c", "initialize", "--", "touch", "/gates/start")
		waitForWorkloadPhases("lifecycle", "Running")
		uid = strings.TrimSpace(command("", "get", "pod", "lifecycle", "-n", namespace, "-o", "jsonpath={.metadata.uid}"))
	})

	It("pushes all four gauge families and Running status in successive batches over both protocols", func() {
		for protocol, file := range pushFiles {
			By("reading the last two " + protocol + " batches")
			eventually(protocol+" batches with the Pod Running", func() bool {
				batches := receivedBatches(file, 2)
				if len(batches) < 2 {
					return false
				}
				for _, batch := range batches {
					if !hasSingleGauge(matching(batch, "dsx_workload_status", map[string]string{"workload_uid": uid, "phase": "Running"}), 1) {
						return false
					}
				}
				for _, family := range workloadFamilies {
					if len(batches[1][family].GetMetric()) == 0 {
						return false
					}
				}
				return true
			})
		}
	})

	It("keeps the Pod-to-workload join when a Collector scrapes /metrics and forwards over both protocols", func() {
		for _, file := range []string{"forward-http", "forward-grpc"} {
			By("joining the Pod to its workload status in " + file)
			eventually(file+" Pod-to-workload join", func() bool {
				batches := receivedBatches(file, 1)
				if len(batches) == 0 {
					return false
				}
				pod := matching(batches[0], "dsx_workload_pod_info", map[string]string{"namespace": namespace, "pod_uid": uid, "component": "pod"})
				if !hasSingleGauge(pod, 1) {
					return false
				}
				workload, _ := labelValue(pod[0], "workload_uid")
				return hasSingleGauge(matching(batches[0], "dsx_workload_status", map[string]string{"workload_uid": workload, "phase": "Running"}), 1)
			})
		}
	})

	It("stops including a deleted Pod in new batches", func() {
		By("finishing and deleting the lifecycle Pod")
		command("", "exec", "-n", namespace, "lifecycle", "-c", "work", "--", "touch", "/gates/finish")
		command("", "delete", "pod", "lifecycle", "-n", namespace, "--wait=true")
		for protocol, file := range pushFiles {
			eventually(protocol+" batches without the deleted Pod", func() bool {
				batches := receivedBatches(file, 2)
				if len(batches) < 2 {
					return false
				}
				for _, batch := range batches {
					if len(matching(batch, "dsx_workload_pod_info", map[string]string{"pod_uid": uid})) != 0 ||
						len(matching(batch, "dsx_workload_status", map[string]string{"workload_uid": uid})) != 0 {
						return false
					}
				}
				return true
			})
		}
	})
})

// receivedBatches returns the last n batches the receiver wrote to file, oldest
// first, as metric families the /metrics helpers can match.
func receivedBatches(file string, n int) []map[string]*dto.MetricFamily {
	GinkgoHelper()
	out, err := kubectl("", "exec", "-n", "exporter-otlp", "deployment/otel-receiver", "-c", "reader", "--",
		"tail", "-n", strconv.Itoa(n), "/data/"+file+".jsonl")
	if err != nil {
		fmt.Fprintf(GinkgoWriter, "retrying batch read: %v\n", err)
		return nil
	}
	var batches []map[string]*dto.MetricFamily
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var data metricpb.MetricsData
		Expect(protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal([]byte(line), &data)).To(Succeed(), "parse batch")
		families := map[string]*dto.MetricFamily{}
		for _, rm := range data.GetResourceMetrics() {
			for _, sm := range rm.GetScopeMetrics() {
				for _, m := range sm.GetMetrics() {
					for _, point := range m.GetGauge().GetDataPoints() {
						family := families[m.GetName()]
						if family == nil {
							family = &dto.MetricFamily{Name: proto.String(m.GetName()), Type: dto.MetricType_GAUGE.Enum()}
							families[m.GetName()] = family
						}
						metric := &dto.Metric{Gauge: &dto.Gauge{Value: proto.Float64(point.GetAsDouble())}}
						for _, kv := range point.GetAttributes() {
							metric.Label = append(metric.Label, &dto.LabelPair{Name: proto.String(kv.GetKey()), Value: proto.String(kv.GetValue().GetStringValue())})
						}
						family.Metric = append(family.Metric, metric)
					}
				}
			}
		}
		batches = append(batches, families)
	}
	return batches
}
