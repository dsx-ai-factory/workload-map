// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"reflect"
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/json"

	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog/kartas"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

func TestRayServiceRecordedPhases(t *testing.T) {
	tests := []struct {
		name string
		json string
		want []string
	}{
		{"absent status", `{}`, []string{"Undefined"}},
		{"first observation wait", `{"metadata":{"generation":1},"spec":{"rayClusterConfig":{},"serveConfigV2":"applications: []"},"status":{}}`, []string{"Pending"}},
		{"uncreated desired service", `{"spec":{"rayClusterConfig":{},"serveConfigV2":"applications: []"}}`, []string{"Undefined"}},
		{"zero generation desired service", `{"metadata":{"generation":0},"spec":{"rayClusterConfig":{},"serveConfigV2":"applications: []"}}`, []string{"Undefined"}},
		{"missing service intent", `{"metadata":{"generation":1},"spec":{"rayClusterConfig":{}}}`, []string{"Undefined"}},
		{"unknown nonempty first status", `{"metadata":{"generation":1},"spec":{"rayClusterConfig":{},"serveConfigV2":"applications: []"},"status":{"message":"unrecognized problem"}}`, []string{"Undefined"}},
		{"deleting unobserved service", `{"metadata":{"generation":1,"deletionTimestamp":"2026-10-19T00:00:00Z"},"spec":{"rayClusterConfig":{},"serveConfigV2":"applications: []"}}`, []string{"Undefined"}},
		{"waiting", `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"Initializing"}]}}`, []string{"Pending"}},
		{"initial cluster starting", `{"status":{"pendingServiceStatus":{"rayClusterName":"new"},"conditions":[{"type":"Ready","status":"False","reason":"Initializing"}]}}`, []string{"Pending", "Progressing"}},
		{"promoted cluster awaiting endpoints", `{"status":{"activeServiceStatus":{"rayClusterName":"promoted"},"conditions":[{"type":"Ready","status":"False","reason":"Initializing"},{"type":"UpgradeInProgress","status":"False","reason":"NoPendingCluster"}]}}`, []string{"Pending", "Progressing"}},
		{"active name after readiness is not progress", `{"status":{"activeServiceStatus":{"rayClusterName":"active"},"conditions":[{"type":"Ready","status":"True"}]}}`, []string{"Running"}},
		{"active name with lost endpoints", `{"status":{"activeServiceStatus":{"rayClusterName":"active"},"conditions":[{"type":"Ready","status":"False","reason":"ZeroServeEndpoints"}]}}`, []string{"Degraded"}},
		{"deleting initialization", `{"metadata":{"deletionTimestamp":"2026-10-19T00:00:00Z"},"status":{"activeServiceStatus":{"rayClusterName":"active"},"conditions":[{"type":"Ready","status":"False","reason":"Initializing"}]}}`, []string{"Undefined"}},
		{"running", `{"status":{"conditions":[{"type":"Ready","status":"True"}]}}`, []string{"Running"}},
		{"serving during replacement", `{"status":{"conditions":[{"type":"Ready","status":"True"},{"type":"UpgradeInProgress","status":"True"}]}}`, []string{"Progressing", "Running"}},
		{"serving during rollback", `{"status":{"conditions":[{"type":"Ready","status":"True"},{"type":"RollbackInProgress","status":"True"}]}}`, []string{"Progressing", "Running"}},
		{"pending name alone does not imply upgrade", `{"status":{"pendingServiceStatus":{"rayClusterName":"stale"},"conditions":[{"type":"Ready","status":"True"}]}}`, []string{"Running"}},
		{"lost endpoints", `{"status":{"pendingServiceStatus":{"rayClusterName":"stale"},"conditions":[{"type":"Ready","status":"False","reason":"ZeroServeEndpoints"}]}}`, []string{"Degraded"}},
		{"initialization failed with sticky upgrade", `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"InitializingTimeout"},{"type":"UpgradeInProgress","status":"True"}]}}`, []string{"Failed"}},
		{"validation failed with sticky upgrade", `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"ValidationFailed"},{"type":"UpgradeInProgress","status":"True"}]}}`, []string{"Failed"}},
		{"suspended with sticky startup", `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"Initializing"},{"type":"Suspended","status":"True"}]}}`, []string{"Undefined"}},
		{"requested suspension", `{"spec":{"suspend":true},"status":{"conditions":[{"type":"Ready","status":"False","reason":"Initializing"}]}}`, []string{"Undefined"}},
		{"stale observation", `{"metadata":{"generation":2},"status":{"observedGeneration":1,"pendingServiceStatus":{"rayClusterName":"old"},"conditions":[{"type":"Ready","status":"False","reason":"Initializing"}]}}`, []string{"Undefined"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			u := &unstructured.Unstructured{}
			if err := json.Unmarshal([]byte(test.json), &u.Object); err != nil {
				t.Fatal(err)
			}
			var native []string
			for _, phase := range []struct {
				name    string
				matches bool
			}{
				{"Progressing", rayServiceProgressing(u)},
				{"Pending", rayServicePending(u)},
				{"Running", CondTrue("Ready")(u)},
				{"Degraded", operatorConditionReason("Ready", "False", "ZeroServeEndpoints")(u)},
				{"Failed", rayServiceFailed(u)},
			} {
				if phase.matches {
					native = append(native, phase.name)
				}
			}
			if len(native) == 0 {
				native = []string{"Undefined"}
			}
			slices.Sort(native)
			if !reflect.DeepEqual(native, test.want) {
				t.Fatalf("native phases = %v; want %v", native, test.want)
			}
			root, err := resource.NewComponentFactoryFromObject(kartas.RayService(), u).GetRootComponent()
			if err != nil {
				t.Fatal(err)
			}
			status, err := root.GetStatus(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var sdk []string
			for _, phase := range status.MatchedStatuses {
				sdk = append(sdk, string(phase))
			}
			slices.Sort(sdk)
			if !reflect.DeepEqual(sdk, test.want) {
				t.Fatalf("SDK phases = %v; want %v", sdk, test.want)
			}
		})
	}
}
