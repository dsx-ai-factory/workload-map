// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package core provides Karta operations shared by the browser and WASI doors,
// keeping both transports as thin input and output adapters.
package core

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/catalog"
	"github.com/dsx-ai-factory/workload-map/pkg/resource"
	"github.com/dsx-ai-factory/workload-map/pkg/tree"
)

// DecodeDefinition parses a Karta definition from JSON.
func DecodeDefinition(definitionJSON string) (*v1alpha1.Karta, error) {
	var definition v1alpha1.Karta
	if err := json.Unmarshal([]byte(definitionJSON), &definition); err != nil {
		return nil, fmt.Errorf("failed to unmarshal definition: %w", err)
	}
	return &definition, nil
}

// DecodeWorkload parses a workload from JSON into an unstructured object, so
// fields from any Kubernetes resource type survive.
func DecodeWorkload(workloadJSON string) (*unstructured.Unstructured, error) {
	var workload map[string]any
	if err := json.Unmarshal([]byte(workloadJSON), &workload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workload: %w", err)
	}
	return &unstructured.Unstructured{Object: workload}, nil
}

// BuildTree builds the workload tree from the definition and workload,
// including the root status. tree.Build validates the definition first.
func BuildTree(ctx context.Context, definitionJSON, workloadJSON string) (*tree.WorkloadTree, error) {
	definition, err := DecodeDefinition(definitionJSON)
	if err != nil {
		return nil, err
	}
	workload, err := DecodeWorkload(workloadJSON)
	if err != nil {
		return nil, err
	}
	componentFactory := resource.NewComponentFactoryFromObject(definition, workload)
	return tree.Build(ctx, componentFactory)
}

// ListCatalog returns the Karta definitions embedded at build time.
func ListCatalog() []*v1alpha1.Karta {
	return catalog.List()
}
