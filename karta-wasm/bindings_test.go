// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"
	"testing"

	"github.com/dsx-ai-factory/workload-map/test/types"
)

func mustMarshalJSON(t *testing.T, value any) string {
	t.Helper()
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("failed to marshal %T: %v", value, err)
	}
	return string(jsonBytes)
}

func TestJSBuildTree(t *testing.T) {
	definitionJSON := mustMarshalJSON(t, types.ReactorKarta())
	workloadJSON := mustMarshalJSON(t, types.NewReactorObject())

	resultEnvelope := jsBuildTree(js.Value{}, []js.Value{js.ValueOf(definitionJSON), js.ValueOf(workloadJSON)}).(js.Value)
	if !resultEnvelope.Get("error").IsNull() {
		t.Fatalf("unexpected error: %s", resultEnvelope.Get("error").String())
	}

	var workloadTree struct {
		Status   *struct{ Phases []string }
		Children []struct{ Name string }
	}
	if err := json.Unmarshal([]byte(resultEnvelope.Get("data").String()), &workloadTree); err != nil {
		t.Fatalf("failed to unmarshal tree: %v", err)
	}
	if workloadTree.Status == nil || len(workloadTree.Status.Phases) != 1 || workloadTree.Status.Phases[0] != "Running" {
		t.Fatalf("expected Status.Phases = [Running], got %#v", workloadTree.Status)
	}
	if len(workloadTree.Children) != 1 || workloadTree.Children[0].Name != "service" {
		t.Fatalf("expected a single %q component, got %#v", "service", workloadTree.Children)
	}
}

func TestJSBuildTreeRejectsWrongArgumentCount(t *testing.T) {
	resultEnvelope := jsBuildTree(js.Value{}, []js.Value{js.ValueOf("{}")}).(js.Value)

	if resultEnvelope.Get("error").IsNull() {
		t.Fatal("expected an error for a missing argument")
	}
}

func TestJSListCatalog(t *testing.T) {
	resultEnvelope := jsListCatalog(js.Value{}, nil).(js.Value)
	if !resultEnvelope.Get("error").IsNull() {
		t.Fatalf("unexpected error: %s", resultEnvelope.Get("error").String())
	}

	var catalogDefinitions []map[string]any
	if err := json.Unmarshal([]byte(resultEnvelope.Get("data").String()), &catalogDefinitions); err != nil {
		t.Fatalf("failed to unmarshal catalog: %v", err)
	}
	if len(catalogDefinitions) == 0 {
		t.Fatal("expected the embedded catalog to be non-empty")
	}
}
