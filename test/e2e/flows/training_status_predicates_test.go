// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/json"

	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog/kartas"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

func TestTrainingPartialCapacityPredicates(t *testing.T) {
	tests := []struct {
		name, kind, object string
		want               []string
	}{
		{"running master with waiting worker", "pytorch", `{"spec":{"pytorchReplicaSpecs":{"Worker":{"replicas":1}}},"status":{"conditions":[{"type":"Created","status":"True"},{"type":"Running","status":"True"}],"replicaStatuses":{"Master":{"active":1},"Worker":{}}}}`, []string{"Pending", "Running"}},
		{"all workers active", "pytorch", `{"spec":{"pytorchReplicaSpecs":{"Worker":{"replicas":1}}},"status":{"conditions":[{"type":"Running","status":"True"}],"replicaStatuses":{"Master":{"active":1},"Worker":{"active":1}}}}`, []string{"Running"}},
		{"succeeded worker is accounted for", "pytorch", `{"spec":{"pytorchReplicaSpecs":{"Worker":{"replicas":1}}},"status":{"conditions":[{"type":"Running","status":"True"}],"replicaStatuses":{"Master":{"active":1},"Worker":{"succeeded":1}}}}`, []string{"Running"}},
		{"zero requested workers", "pytorch", `{"spec":{"pytorchReplicaSpecs":{"Worker":{"replicas":0}}},"status":{"conditions":[{"type":"Running","status":"True"}],"replicaStatuses":{"Master":{"active":1},"Worker":{}}}}`, []string{"Running"}},
		{"elastic capacity is not a fixed shortfall", "pytorch", `{"spec":{"elasticPolicy":{},"pytorchReplicaSpecs":{"Worker":{"replicas":2}}},"status":{"conditions":[{"type":"Running","status":"True"}],"replicaStatuses":{"Master":{"active":1},"Worker":{}}}}`, []string{"Running"}},
		{"missing worker observation", "pytorch", `{"spec":{"pytorchReplicaSpecs":{"Worker":{"replicas":1}}},"status":{"conditions":[{"type":"Running","status":"True"}],"replicaStatuses":{"Master":{"active":1}}}}`, []string{"Running"}},
		{"worker failure is not a startup wait", "pytorch", `{"spec":{"pytorchReplicaSpecs":{"Worker":{"replicas":1}}},"status":{"conditions":[{"type":"Running","status":"True"}],"replicaStatuses":{"Master":{"active":1},"Worker":{"failed":1}}}}`, []string{"Running"}},
		{"completion suppresses worker wait", "pytorch", `{"spec":{"pytorchReplicaSpecs":{"Worker":{"replicas":1}}},"status":{"conditions":[{"type":"Running","status":"False"},{"type":"Succeeded","status":"True"}],"replicaStatuses":{"Master":{"succeeded":1},"Worker":{}}}}`, []string{"Completed"}},
		{"nonretryable launcher failure with active worker", "mpi", `{"spec":{"runPolicy":{"backoffLimit":0}},"status":{"conditions":[{"type":"Running","status":"True"}],"replicaStatuses":{"Launcher":{"failed":1},"Worker":{"active":1}}}}`, []string{"Degraded", "Running"}},
		{"nonretryable failure while launcher still counted active", "mpi", `{"spec":{"runPolicy":{"backoffLimit":0}},"status":{"conditions":[{"type":"Running","status":"True"}],"replicaStatuses":{"Launcher":{"failed":1,"active":1},"Worker":{"active":1}}}}`, []string{"Degraded", "Running"}},
		{"retry allowance does not imply current degradation", "mpi", `{"spec":{"runPolicy":{"backoffLimit":3}},"status":{"conditions":[{"type":"Running","status":"True"}],"replicaStatuses":{"Launcher":{"failed":1},"Worker":{"active":1}}}}`, []string{"Running"}},
		{"unknown retry allowance", "mpi", `{"status":{"conditions":[{"type":"Running","status":"True"}],"replicaStatuses":{"Launcher":{"failed":1},"Worker":{"active":1}}}}`, []string{"Running"}},
		{"terminal failure suppresses degradation", "mpi", `{"spec":{"runPolicy":{"backoffLimit":0}},"status":{"conditions":[{"type":"Running","status":"False"},{"type":"Failed","status":"True"}],"replicaStatuses":{"Launcher":{"failed":1},"Worker":{}}}}`, []string{"Failed"}},
		{"no active replicas does not establish degraded execution", "mpi", `{"spec":{"runPolicy":{"backoffLimit":0}},"status":{"conditions":[{"type":"Running","status":"True"}],"replicaStatuses":{"Launcher":{"failed":1},"Worker":{}}}}`, []string{"Running"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &unstructured.Unstructured{}
			if err := json.Unmarshal([]byte(tt.object), &u.Object); err != nil {
				t.Fatal(err)
			}
			definition := kartas.Pytorch()
			pending := PyTorchPending()(u)
			degraded := false
			if tt.kind == "mpi" {
				definition = kartas.Mpijob()
				pending = TrainingPending()(u)
				degraded = MPIJobDegraded()(u)
			}
			var native []string
			for _, phase := range []struct {
				name    string
				matches bool
			}{
				{"Pending", pending}, {"Progressing", TrainingProgressing()(u)}, {"Running", CondTrue("Running")(u)},
				{"Degraded", degraded}, {"Completed", CondTrue("Succeeded")(u)}, {"Failed", CondTrue("Failed")(u)}, {"Suspended", CondTrue("Suspended")(u)},
			} {
				if phase.matches {
					native = append(native, phase.name)
				}
			}
			if len(native) == 0 {
				native = []string{"Undefined"}
			}
			slices.Sort(native)
			if !slices.Equal(native, tt.want) {
				t.Fatalf("native=%v, want %v", native, tt.want)
			}
			root, err := resource.NewComponentFactoryFromObject(definition, u).GetRootComponent()
			if err != nil {
				t.Fatal(err)
			}
			status, err := root.GetStatus(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var actual []string
			for _, phase := range status.MatchedStatuses {
				actual = append(actual, string(phase))
			}
			slices.Sort(actual)
			if !slices.Equal(actual, tt.want) {
				t.Fatalf("SDK=%v, want %v", actual, tt.want)
			}
		})
	}
}
