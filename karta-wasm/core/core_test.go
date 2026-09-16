// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package core

import (
	"context"
	"encoding/json"
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

func TestDecodeDefinition(t *testing.T) {
	definition, err := DecodeDefinition(mustMarshalJSON(t, types.ReactorKarta()))
	if err != nil {
		t.Fatalf("DecodeDefinition() error = %v", err)
	}
	if definition.Name != "reactor" {
		t.Errorf("expected definition name = %q, got %q", "reactor", definition.Name)
	}
}

func TestDecodeDefinitionRejectsInvalidJSON(t *testing.T) {
	if _, err := DecodeDefinition("not json"); err == nil {
		t.Fatal("expected an error for malformed definition JSON")
	}
}

func TestDecodeWorkload(t *testing.T) {
	workload, err := DecodeWorkload(mustMarshalJSON(t, types.NewReactorObject()))
	if err != nil {
		t.Fatalf("DecodeWorkload() error = %v", err)
	}
	if workload.GetKind() == "" {
		t.Error("expected the decoded workload to carry a kind")
	}
}

func TestDecodeWorkloadRejectsInvalidJSON(t *testing.T) {
	if _, err := DecodeWorkload("not json"); err == nil {
		t.Fatal("expected an error for malformed workload JSON")
	}
}

func TestBuildTree(t *testing.T) {
	workloadTree, err := BuildTree(context.Background(),
		mustMarshalJSON(t, types.ReactorKarta()), mustMarshalJSON(t, types.NewReactorObject()))
	if err != nil {
		t.Fatalf("BuildTree() error = %v", err)
	}
	if workloadTree == nil {
		t.Fatal("expected a tree")
	}
}

func TestListCatalog(t *testing.T) {
	if len(ListCatalog()) == 0 {
		t.Error("expected the built-in catalog to be non-empty")
	}
}
