// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
)

// ObjectSelector restricts root workloads of one exact API version and kind.
// Omitting both applies the selector to every root workload.
type ObjectSelector struct {
	APIVersion string                `json:"apiVersion,omitempty" yaml:"apiVersion,omitempty"`
	Kind       string                `json:"kind,omitempty" yaml:"kind,omitempty"`
	Selector   *metav1.LabelSelector `json:"selector" yaml:"selector"`
}

func compileObjectSelectors(rules []ObjectSelector) (map[schema.GroupVersionKind]labels.Selector, error) {
	result := make(map[schema.GroupVersionKind]labels.Selector, len(rules))
	for _, rule := range rules {
		var gvk schema.GroupVersionKind
		if rule.APIVersion != "" || rule.Kind != "" {
			gv, err := schema.ParseGroupVersion(rule.APIVersion)
			if err != nil || len(validation.IsDNS1035Label(gv.Version)) != 0 || len(validation.IsCIdentifier(rule.Kind)) != 0 || (gv.Group != "" && len(validation.IsDNS1123Subdomain(gv.Group)) != 0) {
				return nil, fmt.Errorf("objectSelectors requires both a valid apiVersion and kind, or neither: %q %q", rule.APIVersion, rule.Kind)
			}
			gvk = gv.WithKind(rule.Kind)
		}
		if _, exists := result[gvk]; exists {
			if gvk.Empty() {
				return nil, fmt.Errorf("duplicate global objectSelectors entry")
			}
			return nil, fmt.Errorf("duplicate objectSelectors kind: %s", gvk)
		}
		selected, err := selector(rule.Selector)
		if err != nil {
			return nil, fmt.Errorf("objectSelectors %s: %w", gvk, err)
		}
		result[gvk] = selected
	}
	return result, nil
}

// ValidateObjectSelectors rejects malformed selectors and duplicate scopes,
// including more than one global entry.
func ValidateObjectSelectors(rules []ObjectSelector) error {
	_, err := compileObjectSelectors(rules)
	return err
}

func (r *controllerState) isObjectSelected(object *unstructured.Unstructured) bool {
	objectLabels := labelsFor(object)
	if global, configured := r.objectSelectors[schema.GroupVersionKind{}]; configured && !global.Matches(objectLabels) {
		return false
	}
	selected, configured := r.objectSelectors[object.GroupVersionKind()]
	return !configured || selected.Matches(objectLabels)
}
