// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package instructions

import (
	"fmt"
	"sort"

	"github.com/samber/lo"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// StructureSummary provides a pre-computed summary of Karta structure
// for efficient navigation and lookup operations
type StructureSummary struct {
	karta                      *v1alpha1.Karta
	parentMap                  map[string]string                        // child component name -> parent component name
	childrenMap                map[string][]string                      // parent component name -> list of child component names
	componentDefinitionsByName map[string]*v1alpha1.ComponentDefinition // component name -> component definition
	leafComponents             []string                                 // list of component names that have pod definitions
	hasScaleDefinition         bool                                     // true if any of the karta components has a scale definition
	gangSchedulingSummary      *gangSchedulingSummary                   // summary of gang scheduling instructions
}

type effectiveComponentCandidate struct {
	effectiveComponent string                             // the actual effective component name
	podGroupName       string                             // the pod group name this belongs to
	member             *v1alpha1.PodGroupMemberDefinition // the specific member definition with selector
}

type gangSchedulingSummary struct {
	// Map from component name to all possible effective components for that component
	effectiveComponentCandidates map[string][]effectiveComponentCandidate
	podGroupsByName              map[string]*v1alpha1.PodGroupDefinition // pod group name -> pod group definition
}

// NewStructureSummary creates a new StructureSummary by analyzing the Karta structure
func NewStructureSummary(karta *v1alpha1.Karta) (*StructureSummary, error) {
	if karta == nil {
		return nil, fmt.Errorf("karta cannot be nil")
	}

	summary := &StructureSummary{
		karta:                      karta,
		parentMap:                  make(map[string]string),
		childrenMap:                make(map[string][]string),
		componentDefinitionsByName: make(map[string]*v1alpha1.ComponentDefinition),
		leafComponents:             make([]string, 0),
		hasScaleDefinition:         false,
	}

	if err := summary.build(); err != nil {
		return nil, fmt.Errorf("failed to build structure summary: %w", err)
	}

	return summary, nil
}

// GetKarta returns the underlying Karta
func (s *StructureSummary) GetKarta() *v1alpha1.Karta {
	return s.karta
}

// buildMaps constructs all the lookup maps and metadata from the Karta
func (s *StructureSummary) build() error {
	for _, component := range s.getAllComponents() {
		// Add to component definitions map
		s.componentDefinitionsByName[component.Name] = &component

		// Check if child has pod definition
		if hasPodDefinition(component) {
			s.leafComponents = append(s.leafComponents, component.Name)
		}

		// Check if component has scale definition
		if component.ScaleDefinition != nil {
			s.hasScaleDefinition = true
		}

		// Build parent-child relationships (only for child components with OwnerRef)
		if component.OwnerRef != nil {
			parentName := *component.OwnerRef

			// Add to parent map
			s.parentMap[component.Name] = parentName

			// Add to children map
			s.childrenMap[parentName] = append(s.childrenMap[parentName], component.Name)
		}
	}

	if s.karta.Spec.Instructions.GangScheduling != nil {
		s.gangSchedulingSummary = &gangSchedulingSummary{
			effectiveComponentCandidates: make(map[string][]effectiveComponentCandidate),
		}

		s.gangSchedulingSummary.podGroupsByName = lo.SliceToMap(
			s.karta.Spec.Instructions.GangScheduling.PodGroups,
			func(group v1alpha1.PodGroupDefinition) (string, *v1alpha1.PodGroupDefinition) {
				return group.Name, &group
			},
		)

		// Build effective component candidates for each component
		err := s.buildEffectiveComponentCandidates()
		if err != nil {
			return fmt.Errorf("failed to build effective component candidates: %w", err)
		}
	}

	return nil
}

// buildEffectiveComponentCandidates builds the map of all possible effective components for each component
func (s *StructureSummary) buildEffectiveComponentCandidates() error {
	// For each component, find all possible effective components
	for _, component := range s.getAllComponents() {
		candidates := s.findEffectiveComponentCandidates(component.Name)
		if len(candidates) > 0 {
			// Sort candidates by priority: direct mentions first, then others
			sortedCandidates := s.sortEffectiveComponentCandidatesByPriority(candidates, component.Name)
			s.gangSchedulingSummary.effectiveComponentCandidates[component.Name] = sortedCandidates
		}
	}

	return nil
}

// findEffectiveComponentCandidates finds all possible effective components for a given component
func (s *StructureSummary) findEffectiveComponentCandidates(componentName string) []effectiveComponentCandidate {
	var candidates []effectiveComponentCandidate

	// Check the component itself and all its parents
	current := componentName
	for current != "" {
		// Look for this component in all pod groups
		for _, group := range s.karta.Spec.Instructions.GangScheduling.PodGroups {
			for _, member := range group.Members {
				if member.ComponentName == current {
					candidates = append(candidates, effectiveComponentCandidate{
						effectiveComponent: current,
						podGroupName:       group.Name,
						member:             &member,
					})
				}
			}
		}

		// Move to parent
		current = s.parentMap[current]
	}

	return candidates
}

// sortEffectiveComponentCandidatesByPriority sorts candidates to prioritize direct component mentions
// over parent/ancestor components. This ensures that when resolving effective components at runtime,
// we try the most specific matches first (the component itself) before falling back to broader
// matches (parent components). This provides predictable "first hit wins" behavior where users
// can rely on more specific selectors taking precedence over general fallback rules.
// With all this, users are encouraged to define mutually exclusive pod groups for different components.
//
// Example: For component "worker":
//   - Direct mentions of "worker" in pod groups (with different selectors&filters) come first
//   - Mentions of parent components like "pytorch-job" come after
//
// Within the same priority level (all direct, or all parents), the original definition order
// is preserved via stable sort, giving users predictable behavior based on YAML ordering.
func (s *StructureSummary) sortEffectiveComponentCandidatesByPriority(candidates []effectiveComponentCandidate, originalComponent string) []effectiveComponentCandidate {
	sortedCandidates := make([]effectiveComponentCandidate, len(candidates))
	copy(sortedCandidates, candidates)

	sort.Slice(sortedCandidates, func(i, j int) bool {
		// Priority: Direct component mentions first, everything else after
		iIsDirect := sortedCandidates[i].effectiveComponent == originalComponent
		jIsDirect := sortedCandidates[j].effectiveComponent == originalComponent

		if iIsDirect != jIsDirect {
			return iIsDirect // Direct mentions first
		}

		// For same priority level, preserve original order (stable sort)
		return false
	})

	return sortedCandidates
}

func (s *StructureSummary) getAllComponents() []v1alpha1.ComponentDefinition {
	return append([]v1alpha1.ComponentDefinition{s.karta.Spec.StructureDefinition.RootComponent}, s.karta.Spec.StructureDefinition.ChildComponents...)
}

// hasPodDefinition returns true if the component has any pod-related definition
func hasPodDefinition(component v1alpha1.ComponentDefinition) bool {
	if component.SpecDefinition == nil {
		return false
	}

	return component.SpecDefinition.PodTemplateSpecPath != nil ||
		component.SpecDefinition.PodSpecPath != nil ||
		component.SpecDefinition.FragmentedPodSpecDefinition != nil
}
