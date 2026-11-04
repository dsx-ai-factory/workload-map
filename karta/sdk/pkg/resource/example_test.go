// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package resource_test

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"

	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/jq/execution"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

func ExampleComponent_GetScale() {
	ctx := context.Background()
	workload := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata":   map[string]interface{}{"name": "demo"},
			"spec":       map[string]interface{}{"replicas": int64(3)},
		},
	}

	// An absent ScaleDefinition is an unsupported capability, not a scale of zero.
	// This differs from a definition whose configured path does not match the workload:
	// in that case the path is evaluated and the resulting extraction/data-shape error
	// is returned rather than DefinitionNotFoundError.
	withoutScale := &v1alpha1.Karta{
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{Name: "deployment"},
			},
		},
	}
	factory := resource.NewComponentFactoryFromObject(withoutScale, workload)
	component, err := factory.GetRootComponent()
	if err != nil {
		panic(err)
	}

	scales, err := component.GetScale(ctx)
	if err != nil {
		panic(err)
	}
	if scales == nil {
		fmt.Println("no scale definition: scaling not supported")
	}

	// A malformed JQ expression is returned as a typed execution error.
	withMalformedScale := &v1alpha1.Karta{
		Spec: v1alpha1.KartaSpec{
			StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{
					Name: "deployment",
					ScaleDefinition: &v1alpha1.ScaleDefinition{
						ReplicasPath: ptr.To(".spec.replicas["),
					},
				},
			},
		},
	}
	factory = resource.NewComponentFactoryFromObject(withMalformedScale, workload)
	component, err = factory.GetRootComponent()
	if err != nil {
		panic(err)
	}

	_, err = component.GetScale(ctx)
	var parseErr *execution.JQParseError
	if errors.As(err, &parseErr) {
		fmt.Println("malformed replicas expression: parse error")
	}

	// Output:
	// no scale definition: scaling not supported
	// malformed replicas expression: parse error
}
