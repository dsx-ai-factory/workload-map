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
type ObjectSelector struct {
	APIVersion string                `json:"apiVersion" yaml:"apiVersion"`
	Kind       string                `json:"kind" yaml:"kind"`
	Selector   *metav1.LabelSelector `json:"selector" yaml:"selector"`
}

func compileObjectSelectors(rules []ObjectSelector) (map[schema.GroupVersionKind]labels.Selector, error) {
	result := make(map[schema.GroupVersionKind]labels.Selector, len(rules))
	for _, rule := range rules {
		gv, err := schema.ParseGroupVersion(rule.APIVersion)
		if err != nil || len(validation.IsDNS1035Label(gv.Version)) != 0 || len(validation.IsCIdentifier(rule.Kind)) != 0 || (gv.Group != "" && len(validation.IsDNS1123Subdomain(gv.Group)) != 0) {
			return nil, fmt.Errorf("objectSelectors requires apiVersion and kind: %q %q", rule.APIVersion, rule.Kind)
		}
		gvk := gv.WithKind(rule.Kind)
		if _, exists := result[gvk]; exists {
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

func ValidateObjectSelectors(rules []ObjectSelector) error {
	_, err := compileObjectSelectors(rules)
	return err
}

func (r *controllerState) matchesObjectSelectors(object *unstructured.Unstructured) bool {
	if !r.objectSelector.Matches(labelsFor(object)) {
		return false
	}
	selected, configured := r.objectSelectors[object.GroupVersionKind()]
	return !configured || selected.Matches(labelsFor(object))
}
