// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package v1alpha1

// ConditionType is a typed constant for Karta status condition types.
type ConditionType string

const (
	// ConditionValidated indicates that the Karta spec was validated by the
	// operator. When True, the spec is structurally valid (component
	// hierarchy, ownership graph, instructions) and all JQ expressions parse
	// successfully.
	ConditionValidated ConditionType = "Validated"

	// ConditionCRDExists indicates that the CustomResourceDefinition for the
	// root component GroupVersionKind exists in the cluster and serves the
	// referenced version.
	ConditionCRDExists ConditionType = "CRDExists"

	// ConditionReady is the aggregate condition signaling that a Karta is
	// ready to be used by consumers.
	ConditionReady ConditionType = "Ready"
)
