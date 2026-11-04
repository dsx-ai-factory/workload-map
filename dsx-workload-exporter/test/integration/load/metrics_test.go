// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package load_test

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	collector "github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/adapters/otel"
)

type resourceCollection struct {
	namespace string
	gvk       schema.GroupVersionKind
}

var phases = []string{"Initializing", "Running", "Completed", "Failed", "Degraded", "Suspended", "Suspending", "Resuming", "Undefined"}

func scrape(ctx context.Context, httpClient *http.Client, endpoint string) (map[string]*dto.MetricFamily, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scrape HTTP %d", response.StatusCode)
	}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	return parser.TextToMetricFamilies(io.LimitReader(response.Body, 128<<20))
}

func labels(metric *dto.Metric) map[string]string {
	result := make(map[string]string, len(metric.Label))
	for _, label := range metric.Label {
		result[label.GetName()] = label.GetValue()
	}
	return result
}

func checkSamples(families map[string]*dto.MetricFamily) error {
	for name, family := range families {
		if !strings.HasPrefix(name, "dsx_") {
			continue
		}
		if family.GetType() != dto.MetricType_GAUGE {
			return fmt.Errorf("%s is not a gauge", name)
		}
		seen := map[string]bool{}
		for _, sample := range family.Metric {
			keys := make([]string, 0, len(sample.Label))
			for _, pair := range sample.Label {
				keys = append(keys, pair.GetName()+"="+strconv.Quote(pair.GetValue()))
			}
			slices.Sort(keys)
			key := strings.Join(keys, ",")
			if seen[key] {
				return fmt.Errorf("duplicate %s series: %s", name, key)
			}
			seen[key] = true
			value := sample.GetGauge().GetValue()
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return fmt.Errorf("non-finite %s", name)
			}
			switch name {
			case collector.MetricPodWorkloadInfo:
				if value != 1 {
					return fmt.Errorf("Pod info value is %g", value)
				}
				l := labels(sample)
				for _, required := range []string{"namespace", "pod_name", "pod_uid", "workload_name", "workload_uid", "workload_kind"} {
					if l[required] == "" {
						return fmt.Errorf("Pod info missing %s", required)
					}
				}
			case collector.MetricWorkloadStatus, collector.MetricExporterObservationValid:
				if value != 0 && value != 1 {
					return fmt.Errorf("%s value is %g", name, value)
				}
			}
		}
	}
	return nil
}

func checkFleet(families map[string]*dto.MetricFamily, fleet []workload) error {
	if err := checkSamples(families); err != nil {
		return err
	}
	roots := make(map[string]*workload, len(fleet))
	pods := make(map[string]*workload, len(fleet)*2)
	names := map[string]string{}
	for i := range fleet {
		w := &fleet[i]
		roots[string(w.root.UID)] = w
		for _, pod := range w.pods {
			pods[string(pod.UID)] = w
			names[string(pod.UID)] = pod.Name
		}
	}
	podRows := families[collector.MetricPodWorkloadInfo].GetMetric()
	if len(podRows) != len(pods) {
		return fmt.Errorf("Pod rows=%d want=%d", len(podRows), len(pods))
	}
	seenPods := map[string]bool{}
	for _, sample := range podRows {
		l := labels(sample)
		w := pods[l["pod_uid"]]
		if w == nil || seenPods[l["pod_uid"]] {
			return fmt.Errorf("unexpected or repeated Pod UID %s", l["pod_uid"])
		}
		seenPods[l["pod_uid"]] = true
		if l["namespace"] != w.root.Namespace || l["workload_group"] != deploymentGVK.Group || l["workload_version"] != deploymentGVK.Version || l["workload_kind"] != deploymentGVK.Kind || l["workload_uid"] != string(w.root.UID) || l["workload_name"] != w.root.Name || l["pod_name"] != names[l["pod_uid"]] {
			return fmt.Errorf("incorrect Pod identity %v", l)
		}
		if l["root_revision"] != strconv.Itoa(w.revision) || l["pod_revision"] != strconv.Itoa(w.revision) {
			return fmt.Errorf("stale Pod revision for %s: %v", l["pod_name"], l)
		}
		if l["observation_valid"] != "true" || l["owner_resolution"] != "resolved" || l["component"] != "deployment" {
			return fmt.Errorf("invalid Pod attribution %v", l)
		}
	}
	statusRows := families[collector.MetricWorkloadStatus].GetMetric()
	if len(statusRows) != len(roots)*len(phases) {
		return fmt.Errorf("status rows=%d want=%d", len(statusRows), len(roots)*len(phases))
	}
	seenPhases := map[string]bool{}
	for _, sample := range statusRows {
		l := labels(sample)
		w := roots[l["workload_uid"]]
		key := l["workload_uid"] + "/" + l["phase"]
		if w == nil || seenPhases[key] || !slices.Contains(phases, l["phase"]) {
			return fmt.Errorf("unexpected status %v", l)
		}
		seenPhases[key] = true
		want := 0.0
		if l["phase"] == w.phase {
			want = 1
		}
		if l["namespace"] != w.root.Namespace || l["workload_group"] != deploymentGVK.Group || l["workload_version"] != deploymentGVK.Version || l["workload_kind"] != deploymentGVK.Kind || sample.GetGauge().GetValue() != want || l["root_revision"] != strconv.Itoa(w.revision) || l["observation_valid"] != "true" || l["workload_name"] != w.root.Name {
			return fmt.Errorf("stale status for %s phase %s revision %s", w.root.Name, l["phase"], l["root_revision"])
		}
	}
	creationRows := families[collector.MetricWorkloadCreatedTimestamp].GetMetric()
	if len(creationRows) != len(roots) {
		return fmt.Errorf("creation rows=%d want=%d", len(creationRows), len(roots))
	}
	seenCreated := map[string]bool{}
	for _, sample := range creationRows {
		l := labels(sample)
		w := roots[l["workload_uid"]]
		if w == nil || seenCreated[l["workload_uid"]] {
			return fmt.Errorf("unexpected creation identity %v", l)
		}
		seenCreated[l["workload_uid"]] = true
		if l["namespace"] != w.root.Namespace || sample.GetGauge().GetValue() != float64(w.root.CreationTimestamp.Unix()) || l["workload_name"] != w.root.Name {
			return fmt.Errorf("incorrect creation timestamp for %s", w.root.Name)
		}
	}
	healthy := map[resourceCollection]bool{
		{gvk: schema.GroupVersionKind{Version: "v1", Kind: "Namespace"}}:                                           false,
		{gvk: schema.GroupVersionKind{Group: "run.ai", Version: "v1alpha1", Kind: "Karta"}}:                        false,
		{namespace: loadNamespace, gvk: schema.GroupVersionKind{Version: "v1", Kind: "Pod"}}:                       false,
		{namespace: loadNamespace, gvk: deploymentGVK}:                                                             false,
		{namespace: loadNamespace, gvk: schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"}}: false,
	}
	healthRows := families[collector.MetricExporterObservationValid].GetMetric()
	if len(healthRows) != len(healthy) {
		return fmt.Errorf("health rows=%d want=%d", len(healthRows), len(healthy))
	}
	for _, sample := range healthRows {
		l := labels(sample)
		if l["scope"] != "resource_type" || sample.GetGauge().GetValue() != 1 || l["reason"] != "none" || l["workload_name"] != "" || l["workload_uid"] != "" {
			return fmt.Errorf("unexpected health row %v value=%g", l, sample.GetGauge().GetValue())
		}
		identity := resourceCollection{namespace: l["namespace"], gvk: schema.GroupVersionKind{Group: l["resource_group"], Version: l["resource_version"], Kind: l["resource_kind"]}}
		seen, expected := healthy[identity]
		if !expected || seen {
			return fmt.Errorf("unexpected or repeated health identity %v", l)
		}
		healthy[identity] = true
	}
	return nil
}
