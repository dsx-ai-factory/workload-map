// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package load_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFleetValidation(t *testing.T) {
	fleet := []workload{{root: &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "root", Namespace: loadNamespace, UID: "root-uid", CreationTimestamp: metav1.NewTime(time.Unix(1000, 0))}}, pods: []*corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "pod", UID: "pod-uid"}}}, phase: "Running", revision: 2}}
	body := `# TYPE dsx_workload_pod_info gauge
 dsx_workload_pod_info{namespace="exporter-load",pod_name="pod",pod_uid="pod-uid",workload_name="root",workload_uid="root-uid",workload_group="apps",workload_version="v1",workload_kind="Deployment",root_revision="2",pod_revision="2",observation_valid="true",owner_resolution="resolved",component="deployment"} 1
# TYPE dsx_workload_created_timestamp_seconds gauge
 dsx_workload_created_timestamp_seconds{namespace="exporter-load",workload_name="root",workload_uid="root-uid"} 1000
# TYPE dsx_workload_status gauge
`
	for _, phase := range phases {
		value := 0
		if phase == "Running" {
			value = 1
		}
		body += fmt.Sprintf("dsx_workload_status{namespace=\"exporter-load\",workload_group=\"apps\",workload_version=\"v1\",workload_kind=\"Deployment\",workload_name=\"root\",workload_uid=\"root-uid\",phase=%q,root_revision=\"2\",observation_valid=\"true\"} %d\n", phase, value)
	}
	body += "# TYPE dsx_exporter_observation_valid gauge\n"
	for _, kind := range []string{"Pod", "Deployment", "ReplicaSet", "Namespace", "Karta"} {
		namespace, group, version := loadNamespace, "apps", "v1"
		if kind == "Pod" || kind == "Namespace" {
			group = ""
		}
		if kind == "Namespace" || kind == "Karta" {
			namespace = ""
		}
		if kind == "Karta" {
			group, version = "run.ai", "v1alpha1"
		}
		body += fmt.Sprintf("dsx_exporter_observation_valid{scope=\"resource_type\",namespace=%q,resource_group=%q,resource_version=%q,resource_kind=%q,reason=\"none\"} 1\n", namespace, group, version, kind)
	}
	namespaceHealth := "dsx_exporter_observation_valid{scope=\"resource_type\",namespace=\"\",resource_group=\"\",resource_version=\"v1\",resource_kind=\"Namespace\",reason=\"none\"} 1\n"
	kartaHealth := "dsx_exporter_observation_valid{scope=\"resource_type\",namespace=\"\",resource_group=\"run.ai\",resource_version=\"v1alpha1\",resource_kind=\"Karta\",reason=\"none\"} 1\n"
	tests := []struct {
		name, body string
		valid      bool
	}{
		{"valid", body, true},
		{"wrong namespace", strings.Replace(body, `namespace="exporter-load"`, `namespace="other"`, 1), false},
		{"wrong workload kind", strings.Replace(body, `workload_kind="Deployment"`, `workload_kind="Job"`, 1), false},
		{"wrong workload group", strings.Replace(body, `workload_group="apps"`, `workload_group="other"`, 1), false},
		{"wrong workload version", strings.Replace(body, `workload_version="v1"`, `workload_version="v2"`, 1), false},
		{"wrong status namespace", strings.ReplaceAll(body, `dsx_workload_status{namespace="exporter-load",workload_group="apps",workload_version="v1",workload_kind="Deployment"`, `dsx_workload_status{namespace="other",workload_group="apps",workload_version="v1",workload_kind="Deployment"`), false},
		{"wrong status workload_group", strings.ReplaceAll(body, `dsx_workload_status{namespace="exporter-load",workload_group="apps",workload_version="v1",workload_kind="Deployment"`, `dsx_workload_status{namespace="exporter-load",workload_group="other",workload_version="v1",workload_kind="Deployment"`), false},
		{"wrong status workload_version", strings.ReplaceAll(body, `dsx_workload_status{namespace="exporter-load",workload_group="apps",workload_version="v1",workload_kind="Deployment"`, `dsx_workload_status{namespace="exporter-load",workload_group="apps",workload_version="v2",workload_kind="Deployment"`), false},
		{"wrong status workload_kind", strings.ReplaceAll(body, `dsx_workload_status{namespace="exporter-load",workload_group="apps",workload_version="v1",workload_kind="Deployment"`, `dsx_workload_status{namespace="exporter-load",workload_group="apps",workload_version="v1",workload_kind="Job"`), false},
		{"wrong creation namespace", strings.ReplaceAll(body, `seconds{namespace="exporter-load"`, `seconds{namespace="other"`), false},
		{"missing Namespace health", strings.ReplaceAll(body, namespaceHealth, ""), false},
		{"missing Karta health", strings.ReplaceAll(body, kartaHealth, ""), false},
		{"duplicate health identity", strings.ReplaceAll(body, kartaHealth, strings.Replace(namespaceHealth, `reason="none"`, `reason="none",extra="duplicate"`, 1)), false},
		{"wrong Karta namespace", strings.ReplaceAll(body, kartaHealth, strings.Replace(kartaHealth, `namespace=""`, `namespace="exporter-load"`, 1)), false},
		{"wrong Karta version", strings.ReplaceAll(body, kartaHealth, strings.Replace(kartaHealth, `resource_version="v1alpha1"`, `resource_version="v1"`, 1)), false},
		{"unexpected health workload", strings.ReplaceAll(body, kartaHealth, strings.Replace(kartaHealth, `reason="none"`, `reason="none",workload_name="unexpected"`, 1)), false},
		{"wrong health namespace", strings.ReplaceAll(body, `scope="resource_type",namespace="exporter-load"`, `scope="resource_type",namespace="other"`), false},
		{"wrong health group", strings.ReplaceAll(body, `resource_group="apps"`, `resource_group="other"`), false},
		{"wrong health version", strings.ReplaceAll(body, `resource_version="v1"`, `resource_version="v2"`), false},
		{"wrong active phase value", strings.ReplaceAll(body, `phase="Running",root_revision="2",observation_valid="true"} 1`, `phase="Running",root_revision="2",observation_valid="true"} 0`), false},
		{"wrong inactive phase value", strings.ReplaceAll(body, `phase="Failed",root_revision="2",observation_valid="true"} 0`, `phase="Failed",root_revision="2",observation_valid="true"} 1`), false},
		{"missing Pod UID", strings.ReplaceAll(body, `pod_uid="pod-uid"`, `pod_uid=""`), false},
		{"stale root revision", strings.ReplaceAll(body, `root_revision="2"`, `root_revision="1"`), false},
		{"stale Pod revision", strings.ReplaceAll(body, `pod_revision="2"`, `pod_revision="1"`), false},
		{"wrong phase", strings.ReplaceAll(body, `phase="Running"`, `phase="Failed"`), false},
		{"wrong creation time", strings.ReplaceAll(body, "} 1000", "} 999"), false},
		{"resource failure", strings.ReplaceAll(body, `reason="none"`, `reason="forbidden"`), false},
		{"invalid observation", strings.ReplaceAll(body, `observation_valid="true"`, `observation_valid="false"`), false},
		{"missing family", strings.ReplaceAll(body, "dsx_workload_pod_info", "missing_pod_info"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.valid && tt.body == body {
				t.Fatal("negative case did not change fixture")
			}
			parser := expfmt.NewTextParser(model.UTF8Validation)
			families, err := parser.TextToMetricFamilies(strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if err := checkFleet(families, fleet); (err == nil) != tt.valid {
				t.Fatalf("valid=%t: %v", tt.valid, err)
			}
		})
	}
}
