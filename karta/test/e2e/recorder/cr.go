// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package recorder

import (
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// volatileFields are bumped by the apiserver on every write without a real change.
var volatileFields = [][]string{
	{"metadata", "resourceVersion"},
	{"metadata", "managedFields"},
}

// stripVolatileFields drops the volatile fields so keep dedups on real changes.
func stripVolatileFields(cr *unstructured.Unstructured) map[string]any {
	stripped := cr.DeepCopy().Object
	for _, field := range volatileFields {
		unstructured.RemoveNestedField(stripped, field...)
	}
	return stripped
}

// hasObservedCurrentGeneration reports whether the workload's controller has observed its current spec
// (status.observedGeneration >= metadata.generation); workloads without observedGeneration count as observed.
func hasObservedCurrentGeneration(cr *unstructured.Unstructured) bool {
	observed, hasObserved, _ := unstructured.NestedInt64(cr.Object, "status", "observedGeneration")
	if !hasObserved {
		return true
	}
	return observed >= cr.GetGeneration()
}

// blankWithGVK returns a fresh object carrying only src's GVK.
func blankWithGVK(src *unstructured.Unstructured) *unstructured.Unstructured {
	blank := &unstructured.Unstructured{}
	blank.SetGroupVersionKind(src.GroupVersionKind())
	return blank
}

// dumpStatus renders a CR's status block for the failure messages, best effort; safe on a nil object.
func dumpStatus(cr *unstructured.Unstructured) string {
	if cr == nil {
		return "(no object observed)"
	}
	status, _, _ := unstructured.NestedMap(cr.Object, "status")
	b, err := json.MarshalIndent(status, "  ", "  ")
	if err != nil {
		return fmt.Sprintf("(status marshal error: %v)", err)
	}
	// MarshalIndent prefixes every line except the first; indent it too so the block lines up.
	return "  " + string(b)
}
