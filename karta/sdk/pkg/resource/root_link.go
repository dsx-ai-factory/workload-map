// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package resource

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/jq/execution"
)

// LinksToRoot reports whether pod belongs to root through a component's RootLink:
// the pod matches the component's type selector, and its link label equals the
// value the link's ValuePath reads from root. Ownership is not consulted.
func LinksToRoot(ctx context.Context, root map[string]any, karta *v1alpha1.Karta, pod *corev1.Pod) (bool, error) {
	structure := karta.Spec.StructureDefinition
	components := append([]v1alpha1.ComponentDefinition{structure.RootComponent}, structure.ChildComponents...)
	rootEvaluator := execution.NewDefaultRunner(root)
	querier := NewPodQuerier(pod)

	for _, component := range components {
		if component.PodSelector == nil || component.PodSelector.RootLink == nil {
			continue
		}
		link := component.PodSelector.RootLink
		got, ok := pod.Labels[link.LabelKey]
		if !ok {
			continue
		}

		results, err := rootEvaluator.Evaluate(ctx, link.ValuePath)
		if err != nil {
			return false, fmt.Errorf("component %q: evaluate rootLink valuePath: %w", component.Name, err)
		}
		if len(results) != 1 {
			return false, fmt.Errorf("component %q: rootLink valuePath returned %d results, want 1", component.Name, len(results))
		}
		want, ok := results[0].(string)
		if !ok {
			return false, fmt.Errorf("component %q: rootLink valuePath returned %T, want string", component.Name, results[0])
		}
		if got != want {
			continue
		}

		matches, err := querier.MatchesComponentType(ctx, component.PodSelector.ComponentTypeSelector)
		if err != nil {
			return false, fmt.Errorf("component %q: match component type: %w", component.Name, err)
		}
		if matches {
			return true, nil
		}
	}
	return false, nil
}
