// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package instructions

import (
	"context"
	"fmt"

	"github.com/dsx-ai-factory/workload-map/pkg/resource"
)

// InferPodComponent infers the component name for the given pod based on component type selectors
func InferPodComponent(ctx context.Context, podQuerier *resource.PodQuerier, summary *StructureSummary) (string, error) {
	// If there is only one leaf component, the pod must match it
	if len(summary.leafComponents) == 1 {
		return summary.leafComponents[0], nil
	}

	// Only check leaf components (have pod definitions)
	for _, componentName := range summary.leafComponents {
		leafDefinition := summary.componentDefinitionsByName[componentName]

		// Check if pod matches this component type's selector
		if leafDefinition.PodSelector != nil {
			matches, err := podQuerier.MatchesComponentType(ctx, leafDefinition.PodSelector.ComponentTypeSelector)
			if err != nil {
				return "", fmt.Errorf("failed to check if pod matches component %s: %w", componentName, err)
			}
			if matches {
				return componentName, nil
			}
		}

	}

	return "", fmt.Errorf("no component found for pod %s", podQuerier.GetPodName())
}

// InferPodComponentInstance infers the component instance for the given pod based on component instance selectors
// It returns the component instance if found, nil if not found
func InferPodComponentInstance(ctx context.Context, podQuerier *resource.PodQuerier, componentName string, factory *resource.ComponentFactory) (*string, error) {
	component, err := factory.GetComponent(componentName)
	if err != nil {
		return nil, err
	}

	instanceIds, err := component.GetInstanceIds(ctx)
	if err != nil {
		return nil, err
	}

	var podComponentInstance *string
	// If there are instance ids (non-empty) and the component has a pod selector, try to match the instance id
	if len(instanceIds) > 0 && len(instanceIds[0]) > 0 && component.GetPodSelector() != nil {
		podComponentInstanceName, err := podQuerier.GetMatchingInstanceId(ctx, component.GetPodSelector().ComponentInstanceSelector, instanceIds)
		if err != nil {
			return nil, err
		}

		if podComponentInstanceName != "" {
			podComponentInstance = &podComponentInstanceName
		}
	}

	return podComponentInstance, nil
}
