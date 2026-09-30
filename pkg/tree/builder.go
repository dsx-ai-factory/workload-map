// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package tree

import (
	"context"
	"fmt"
	"sort"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/resource"
)

// Build constructs a WorkloadTree describing the desired hierarchy of a workload:
// its components, their instances, and normalized status. The factory carries the
// karta definition and the workload object it was built from.
func Build(
	ctx context.Context,
	factory *resource.ComponentFactory,
) (*WorkloadTree, error) {
	if err := v1alpha1.NewKartaValidator(factory.GetKarta()).Validate(); err != nil {
		return nil, fmt.Errorf("invalid karta: %w", err)
	}

	rootComponent, err := factory.GetRootComponent()
	if err != nil {
		return nil, fmt.Errorf("failed to get root component: %w", err)
	}

	// Build the root as a node; the root is not exposed, only its hoisted children are used.
	rootNodes, err := buildComponentNodes(ctx, []*resource.Component{rootComponent}, factory)
	if err != nil {
		return nil, err
	}
	rootNode := rootNodes[0]

	tree := &WorkloadTree{}
	if len(rootNode.Instances) > 0 {
		tree.Children = rootNode.Instances[0].Children
	}

	status, err := rootComponent.GetStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get root status: %w", err)
	}
	if status != nil {
		tree.Status = &WorkloadStatus{Phases: matchedStatusStrings(status)}
	}

	return tree, nil
}

func buildComponentNodes(
	ctx context.Context,
	components []*resource.Component,
	factory *resource.ComponentFactory,
) ([]ComponentNode, error) {
	nodes := make([]ComponentNode, 0, len(components))

	for _, component := range components {
		instanceKeys, err := component.GetInstanceIds(ctx)
		if err != nil {
			return nil, fmt.Errorf("get instance ids for component %s: %w", component.Name(), err)
		}

		children, err := factory.GetChildComponentsOf(component.Name())
		if err != nil {
			return nil, err
		}

		extractedInstances, err := component.GetExtractedInstances(ctx)
		if err != nil {
			return nil, fmt.Errorf("get extracted instances for component %s: %w", component.Name(), err)
		}

		instances, err := buildInstanceNodes(ctx, instanceKeys, extractedInstances, children, factory)
		if err != nil {
			return nil, fmt.Errorf("build instance nodes for component %s: %w", component.Name(), err)
		}

		nodes = append(nodes, ComponentNode{
			Name:             component.Name(),
			Kind:             component.Kind(),
			HasPodDefinition: component.HasPodDefinition(),
			Instances:        instances,
		})
	}

	return nodes, nil
}

// buildInstanceNodes builds one InstanceNode per spec-defined instance key, in
// sorted order. The replica dimension (ReplicaKey) is not populated by the
// builder.
func buildInstanceNodes(
	ctx context.Context,
	instanceKeys []string,
	extractedInstances map[string]resource.ExtractedInstance,
	children []*resource.Component,
	factory *resource.ComponentFactory,
) ([]InstanceNode, error) {
	sort.Strings(instanceKeys)

	// Child components are extracted from the spec, not scoped to a parent instance,
	// so every instance's subtree is identical: build it once, then give each instance
	// its own copy.
	baseChildren, err := buildComponentNodes(ctx, children, factory)
	if err != nil {
		return nil, err
	}

	instances := make([]InstanceNode, 0, len(instanceKeys))

	for _, key := range instanceKeys {
		var instanceKey *string
		if key != "" {
			instanceKey = &key
		}

		var scale *resource.Scale
		var extracted *resource.ExtractedInstance
		if ei, ok := extractedInstances[key]; ok {
			extracted = &ei
			scale = ei.Scale
		}

		// Clone on every iteration so each instance owns its subtree;
		childNodes := cloneComponentNodes(baseChildren)
		instances = append(instances, InstanceNode{
			InstanceKey:       instanceKey,
			Scale:             scale,
			ExtractedInstance: extracted,
			Children:          childNodes,
		})
	}

	return instances, nil
}

func cloneComponentNodes(nodes []ComponentNode) []ComponentNode {
	if nodes == nil {
		return nil
	}
	out := make([]ComponentNode, len(nodes))
	for i := range nodes {
		out[i] = nodes[i]
		out[i].Instances = cloneInstanceNodes(nodes[i].Instances)
	}
	return out
}

func cloneInstanceNodes(instances []InstanceNode) []InstanceNode {
	if instances == nil {
		return nil
	}
	out := make([]InstanceNode, len(instances))
	for i := range instances {
		out[i] = instances[i]
		out[i].Children = cloneComponentNodes(instances[i].Children)
	}
	return out
}

func matchedStatusStrings(status *resource.Status) []string {
	if status == nil {
		return nil
	}
	// MatchedStatuses is []v1alpha1.ResourceStatus (a named string type), so it is
	// converted to plain strings for the tree's status-agnostic Phases field.
	phases := make([]string, len(status.MatchedStatuses))
	for i, s := range status.MatchedStatuses {
		phases[i] = string(s)
	}
	return phases
}
