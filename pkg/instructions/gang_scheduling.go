// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package instructions

import (
	"context"
	"errors"
	"fmt"

	"github.com/samber/lo"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/resource"
)

// PodGroupingEffectiveComponent contains the effective component information for pod grouping
type PodGroupingEffectiveComponent struct {
	EffectiveComponent string                             // the actual effective component name
	PodGroupName       string                             // the pod group name this belongs to
	MemberDefinition   *v1alpha1.PodGroupMemberDefinition // the specific member definition with filters
}

// GetPodGroupingEffectiveComponent returns all the information about the effective component for the given pod's component
func GetPodGroupingEffectiveComponent(ctx context.Context, podQuerier *resource.PodQuerier, podComponentName string, summary *StructureSummary) (*PodGroupingEffectiveComponent, error) {
	// Get effective component candidate for gang scheduling (if any)
	candidate, err := getEffectiveComponentForPod(ctx, podQuerier, podComponentName, summary)
	if err != nil {
		return nil, err
	}

	// candidate can be nil if none was found
	return candidate, nil
}

// getEffectiveComponentForPod dynamically determines the effective component for a specific pod
func getEffectiveComponentForPod(ctx context.Context, podQuerier *resource.PodQuerier, componentName string, summary *StructureSummary) (*PodGroupingEffectiveComponent, error) {
	if summary.gangSchedulingSummary == nil {
		return nil, nil
	}

	// Get pre-computed and pre-sorted candidates for this component
	candidates, exists := summary.gangSchedulingSummary.effectiveComponentCandidates[componentName]
	if !exists || len(candidates) == 0 {
		return nil, nil
	}

	// Try each candidate (already sorted by priority), return first match
	for _, candidate := range candidates {
		// If no filters, always matches
		if len(candidate.member.Filters) == 0 {
			return &PodGroupingEffectiveComponent{
				EffectiveComponent: candidate.effectiveComponent,
				PodGroupName:       candidate.podGroupName,
				MemberDefinition:   candidate.member,
			}, nil
		}

		// Check if pod passed all filters (ANDed)
		passed, err := podQuerier.PassesFilters(ctx, candidate.member.Filters)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate filters for component %s in group %s: %w", candidate.effectiveComponent, candidate.podGroupName, err)
		}

		if passed {
			return &PodGroupingEffectiveComponent{
				EffectiveComponent: candidate.effectiveComponent,
				PodGroupName:       candidate.podGroupName,
				MemberDefinition:   candidate.member,
			}, nil
		}
	}

	// No matches found
	return nil, nil
}

type SubtreeRoot struct {
	ComponentName string
	InstanceId    *string
}

// CalculateSubtreeScale calculates the aggregated scale for a component subtree
func CalculateSubtreeScale(ctx context.Context, componentName string, instanceId *string, factory *resource.ComponentFactory, summary *StructureSummary) (int32, error) {
	if instanceId != nil && *instanceId == "" {
		return 0, fmt.Errorf("instance id is empty")
	}

	subtreeRoot := SubtreeRoot{
		ComponentName: componentName,
		InstanceId:    instanceId,
	}

	if summary.hasScaleDefinition {
		return calculateSubtreeScaleByDefinition(ctx, componentName, subtreeRoot, factory, summary)
	}

	// if no component in the Karta had defined scale, return the count of leaf components in this subtree
	return calculateSubtreeScaleByLeaves(ctx, componentName, subtreeRoot, factory, summary)
}

// calculateSubtreeScaleByDefinition calculates the aggregated scale for a component subtree based on scale definitions
func calculateSubtreeScaleByDefinition(ctx context.Context, currentComponentName string, subtreeRoot SubtreeRoot, factory *resource.ComponentFactory, summary *StructureSummary) (int32, error) {
	// Get this component's scale
	component, err := factory.GetComponent(currentComponentName)
	if err != nil {
		return 0, err
	}

	scales, err := component.GetScale(ctx)
	if err != nil {
		var notFoundErr resource.DefinitionNotFoundError
		if !errors.As(err, &notFoundErr) {
			return 0, err
		}

		// it's allowed to not have scale definition
		scales = nil
	}

	var currentComponentScale int32

	// If the component has multiple instances and the instance id is specified for the subtree root, count the scale of the specific instance
	if subtreeRoot.ComponentName == currentComponentName && subtreeRoot.InstanceId != nil {
		if instanceScale, ok := scales[*subtreeRoot.InstanceId]; ok {
			currentComponentScale = getEffectiveMinReplicas(&instanceScale)
		} else {
			return 0, fmt.Errorf("instance id %s not found", *subtreeRoot.InstanceId)
		}
	} else {
		// Sum all scales for this component (array/map cases)
		for _, scale := range scales {
			currentComponentScale += getEffectiveMinReplicas(&scale)
		}
	}

	// Get children
	children := summary.childrenMap[currentComponentName]
	if len(children) == 0 {
		// Leaf component - return its total scale
		return currentComponentScale, nil
	}

	// Parent component - calculate children sum, then multiply by parent scale
	var childrenSum int32
	for _, child := range children {
		childScale, err := calculateSubtreeScaleByDefinition(ctx, child, subtreeRoot, factory, summary)
		if err != nil {
			return 0, err
		}
		childrenSum += childScale
	}

	// If child components have no scale definitions, assume the scale is defined in a higher level
	if childrenSum == 0 {
		return currentComponentScale, nil
	}

	// If current component has no scale definitions, carry over the children sum
	if currentComponentScale == 0 {
		return childrenSum, nil
	}

	// If both current component and children have scale definitions, multiply the current component scale by the children sum
	return currentComponentScale * childrenSum, nil
}

// calculateSubtreeScaleByLeaves is a fallback method for cases where the Karta does not contain any scale definition.
// It returns the number of leaf components (components with SpecDefinition) in the subtree rooted at the given component.
func calculateSubtreeScaleByLeaves(ctx context.Context, currentComponentName string, subtreeRoot SubtreeRoot, factory *resource.ComponentFactory, summary *StructureSummary) (int32, error) {
	// Get this component's scale
	component, err := factory.GetComponent(currentComponentName)
	if err != nil {
		return 0, err
	}

	// Calculate only if this component is a leaf
	if lo.Contains(summary.leafComponents, component.Name()) {
		// If the current component does not have instance id definition, we can return 1 without further instance considerations
		if !component.HasInstanceIdDefinition() {
			return 1, nil
		}

		instanceIds, err := component.GetInstanceIds(ctx)
		if err != nil {
			return 0, err
		}

		// If the current component is the subtree root and the instance id is specified, count the scale of the specific instance
		if currentComponentName == subtreeRoot.ComponentName && subtreeRoot.InstanceId != nil {
			if lo.Contains(instanceIds, *subtreeRoot.InstanceId) {
				return 1, nil
			} else {
				return 0, fmt.Errorf("instance id %s not found", *subtreeRoot.InstanceId)
			}
		} else {
			return int32(len(instanceIds)), nil
		}
	}

	// Recursively count leaves in all child subtrees
	var leafCount int32
	children := summary.childrenMap[currentComponentName]
	for _, child := range children {
		childLeafCount, err := calculateSubtreeScaleByLeaves(ctx, child, subtreeRoot, factory, summary)
		if err != nil {
			return 0, err
		}
		leafCount += childLeafCount
	}

	return leafCount, nil
}

// getEffectiveMinReplicas determines the minimum replicas for a component
func getEffectiveMinReplicas(scale *resource.Scale) int32 {
	if scale.MinReplicas != nil && *scale.MinReplicas > 0 {
		return *scale.MinReplicas
	}

	if scale.Replicas != nil && *scale.Replicas > 0 {
		return *scale.Replicas
	}

	return 0 // Assume the scale is defined in a higher level
}
