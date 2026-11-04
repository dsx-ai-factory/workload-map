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
	fleet := []workload{{root: &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "root", UID: "root-uid", CreationTimestamp: metav1.NewTime(time.Unix(1000, 0))}}, pods: []*corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "pod", UID: "pod-uid"}}}, phase: "Running", revision: 2}}
	body := `# TYPE dsx_workload_pod_info gauge
 dsx_workload_pod_info{namespace="load",pod_name="pod",pod_uid="pod-uid",workload_name="root",workload_uid="root-uid",workload_kind="Deployment",root_revision="2",pod_revision="2",observation_valid="true",owner_resolution="resolved",component="deployment"} 1
# TYPE dsx_workload_created_timestamp_seconds gauge
 dsx_workload_created_timestamp_seconds{workload_name="root",workload_uid="root-uid"} 1000
# TYPE dsx_workload_status gauge
`
	for _, phase := range phases {
		value := 0
		if phase == "Running" {
			value = 1
		}
		body += fmt.Sprintf("dsx_workload_status{workload_name=\"root\",workload_uid=\"root-uid\",phase=%q,root_revision=\"2\",observation_valid=\"true\"} %d\n", phase, value)
	}
	body += "# TYPE dsx_exporter_observation_valid gauge\n"
	for _, kind := range []string{"Pod", "Deployment", "ReplicaSet"} {
		body += fmt.Sprintf("dsx_exporter_observation_valid{scope=\"resource_type\",resource_kind=%q,reason=\"none\"} 1\n", kind)
	}
	tests := []struct {
		name, body string
		valid      bool
	}{
		{"valid", body, true},
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
