// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utiljson "k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/yaml"
)

func TestDynamoWorkerUpdatePatch(t *testing.T) {
	for _, manifest := range []string{"testdata/dynamo/updated.yaml", "testdata/dynamo/v1beta1/updated.yaml"} {
		t.Run(manifest, func(t *testing.T) {
			original, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			var before map[string]any
			if err := yaml.Unmarshal(original, &before); err != nil {
				t.Fatal(err)
			}
			data, err := dynamoWorkerUpdatePatch(manifest)
			if err != nil {
				t.Fatal(err)
			}
			var patch map[string]any
			if err := json.Unmarshal(data, &patch); err != nil {
				t.Fatal(err)
			}
			if len(patch) != 1 || patch["spec"] == nil {
				t.Fatalf("update must patch only spec: %s", data)
			}
			var container map[string]any
			spec := patch["spec"].(map[string]any)
			if before["apiVersion"] == "nvidia.com/v1alpha1" {
				container = spec["services"].(map[string]any)["decode"].(map[string]any)["extraPodSpec"].(map[string]any)["mainContainer"].(map[string]any)
			} else {
				for _, value := range spec["components"].([]any) {
					component := value.(map[string]any)
					if component["name"] == "decode" {
						container = component["podTemplate"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
					}
				}
			}
			wantEnv := []any{map[string]any{"name": "KARTA_E2E_UPDATE", "value": "updated"}}
			if !reflect.DeepEqual(container["env"], wantEnv) {
				t.Fatalf("worker update missing: %v", container)
			}
			delete(container, "env")
			if !reflect.DeepEqual(spec, before["spec"]) {
				t.Fatalf("update changed fields other than the worker environment")
			}
			metadata := before["metadata"].(map[string]any)
			if metadata["annotations"].(map[string]any)["nvidia.com/enable-grove"] != "false" {
				t.Fatal("managed rolling updates require the component deployment pathway")
			}
		})
	}
}

func TestDynamoRolloutComplete(t *testing.T) {
	for _, test := range []struct {
		name   string
		object string
		want   bool
	}{
		{"completed and ready", `{"metadata":{"generation":2},"status":{"observedGeneration":2,"state":"successful","rollingUpdate":{"phase":"Completed"}}}`, true},
		{"initial ready is not completed rollout", `{"metadata":{"generation":1},"status":{"observedGeneration":1,"state":"successful"}}`, false},
		{"still progressing", `{"metadata":{"generation":2},"status":{"observedGeneration":2,"state":"successful","rollingUpdate":{"phase":"InProgress"}}}`, false},
		{"completed but not ready", `{"metadata":{"generation":2},"status":{"observedGeneration":2,"state":"pending","rollingUpdate":{"phase":"Completed"}}}`, false},
		{"stale completed status", `{"metadata":{"generation":2},"status":{"observedGeneration":1,"state":"successful","rollingUpdate":{"phase":"Completed"}}}`, false},
		{"missing observed generation", `{"metadata":{"generation":2},"status":{"state":"successful","rollingUpdate":{"phase":"Completed"}}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var object unstructured.Unstructured
			if err := utiljson.Unmarshal([]byte(test.object), &object.Object); err != nil {
				t.Fatal(err)
			}
			if got := dynamoRolloutComplete(&object); got != test.want {
				t.Fatalf("dynamoRolloutComplete() = %t, want %t", got, test.want)
			}
		})
	}
}
