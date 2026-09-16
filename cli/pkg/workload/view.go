// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package workload resolves a workload object into the view the CLI renders,
// reading it through its Karta definition without contacting the cluster.
package workload

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/definitions"
	"github.com/dsx-ai-factory/workload-map/pkg/resource"
	"github.com/dsx-ai-factory/workload-map/pkg/tree"
)

// View is one workload as Karta resolves it: the root object's identity, the
// definition that covers it, and its normalized phase.
type View struct {
	Name       string    `json:"name"`
	Namespace  string    `json:"namespace"`
	Kind       string    `json:"kind"`
	APIVersion string    `json:"apiVersion"`
	CreatedAt  time.Time `json:"createdAt"`
	Definition string    `json:"definition"`
	Origin     string    `json:"origin"`
	Phases     []string  `json:"phases"`
}

// undefinedPhase covers both an absent status mapping and one that matched nothing.
const undefinedPhase = "Undefined"

// Resolve reads obj through def and returns its view.
func Resolve(ctx context.Context, obj *unstructured.Unstructured, def definitions.Definition) (*View, error) {
	factory := resource.NewComponentFactoryFromObject(def.Karta, obj)

	workloadTree, err := tree.Build(ctx, factory)
	if err != nil {
		return nil, fmt.Errorf("build tree: %w", err)
	}

	view := viewOf(obj, def, workloadTree)
	return &view, nil
}

// viewOf reads the identity every rendering of a workload shares.
func viewOf(obj *unstructured.Unstructured, def definitions.Definition, workloadTree *tree.WorkloadTree) View {
	return View{
		Name:       obj.GetName(),
		Namespace:  obj.GetNamespace(),
		Kind:       obj.GetKind(),
		APIVersion: obj.GetAPIVersion(),
		CreatedAt:  obj.GetCreationTimestamp().Time,
		Definition: def.Karta.Name,
		Origin:     string(def.Origin),
		Phases:     phases(workloadTree),
	}
}

// phases folds "no status mapping" and "mapping matched nothing" into one value.
func phases(t *tree.WorkloadTree) []string {
	if t.Status == nil || len(t.Status.Phases) == 0 {
		return []string{undefinedPhase}
	}
	return t.Status.Phases
}
