// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package v1alpha1

type GangSchedulingInstruction struct {
	// PodGroups defines the alpha grouping format.
	// This is deprecated and will be removed in a future release.
	// Deprecated: Please use gangScheduling.podGroup  instead.
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=name
	PodGroups []PodGroupDefinition `json:"podGroups,omitempty"`

	// PodGroupComponentsMapping defines the grouping, subgroup, and topology behavior.
	// +kubebuilder:validation:Optional
	PodGroup *PodGroupComponentsMapping `json:"podGroup,omitempty"`
}

// PodGroupDefinition defines the alpha grouping format.
// This is deprecated and will be removed in a future release.
type PodGroupDefinition struct {
	// Name is the unique identifier for this pod group.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Members defines which components belong to this pod group.
	// +listType=map
	// +listMapKey=componentName
	Members []PodGroupMemberDefinition `json:"members"`
}

// PodGroupMemberDefinition defines how to select and filter components for grouping instructions.
// This is deprecated and will be removed in a future release.
type PodGroupMemberDefinition struct {
	// ComponentName references a component defined in the Karta structure.
	// +kubebuilder:validation:Required
	ComponentName string `json:"componentName"`

	// GroupByKeyPaths are JQ paths to values used for grouping (e.g., owner name, replica key)
	// If empty, grouping is done via owner reference traversal
	// Every path must return a single, non-empty value - otherwise grouping will fail
	// JQ paths are evaluated against individual pod objects, not the root resource spec
	// +kubebuilder:validation:Optional
	// +listType=set
	GroupByKeyPaths []string `json:"groupByKeyPaths,omitempty" jq:"validate"`

	// Filters are JQ filter expressions to select specific components (expressions are ANDed)
	// Example: '(.spec.containers[0].resources.limits["nvidia.com/gpu"] // 0) > 0'
	// JQ filters are evaluated against individual pod objects, not the root resource spec
	// +kubebuilder:validation:Optional
	// +listType=set
	Filters []string `json:"filters,omitempty" jq:"validate"`
}

// PodGroupComponentsMapping defines how to create a pod group from a set of components.
type PodGroupComponentsMapping struct {
	// Name is the unique identifier for this pod group.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// SubGroups defines which Karta components should become subGroups.
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=componentName
	SubGroups []SubGroupComponentMapping `json:"subGroups,omitempty"`

	// Topology defines the topology constraint for all workload pods.
	// +kubebuilder:validation:Optional
	Topology *TopologyConstraint `json:"topology,omitempty"`
}

type SubGroupComponentMapping struct {
	// ComponentName references a component defined in the Karta structure.
	// +kubebuilder:validation:Required
	ComponentName string `json:"componentName"`

	// Topology defines the topology constraint for this component's pods.
	// +kubebuilder:validation:Optional
	Topology *TopologyConstraint `json:"topology,omitempty"`
}

type TopologyConstraint struct {
	// TopologyName is the topology resource used by the constraint.
	// +kubebuilder:validation:Required
	TopologyName string `json:"topologyName"`

	// PreferredTopologyLevel is the preferred locality level.
	// +kubebuilder:validation:Required
	PreferredTopologyLevel string `json:"preferredTopologyLevel"`

	// RequiredTopologyLevel is the maximal level that all matching pods must
	// fit within.
	// +kubebuilder:validation:Required
	RequiredTopologyLevel string `json:"requiredTopologyLevel"`
}
