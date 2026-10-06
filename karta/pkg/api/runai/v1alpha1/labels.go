// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package v1alpha1

// Label keys stamped by the operator on every Karta whose root
// component has a GVK defined.
const (
	// LabelRootGroup is the API group of the root component
	// (e.g. "ray.io", "kubeflow.org").
	LabelRootGroup = "karta.run.ai/group"

	// LabelRootVersion is the API version of the root component
	// (e.g. "v1", "v1alpha1").
	LabelRootVersion = "karta.run.ai/version"

	// LabelRootKind is the kind of the root component
	// (e.g. "RayCluster", "PyTorchJob").
	LabelRootKind = "karta.run.ai/kind"
)
