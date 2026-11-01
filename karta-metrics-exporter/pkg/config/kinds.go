// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package config

import (
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Kinds keeps the public group/version/Kind string syntax and parsed internal values.
type Kinds []schema.GroupVersionKind

func (k *Kinds) UnmarshalJSON(data []byte) error {
	var values []string
	if err := json.Unmarshal(data, &values); err != nil {
		return fmt.Errorf("kinds must be group/version/Kind or version/Kind strings: %w", err)
	}
	parsed := make(Kinds, 0, len(values))
	seen := map[schema.GroupVersionKind]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		parts := strings.Split(value, "/")
		if len(parts) < 2 || len(parts) > 3 {
			return fmt.Errorf("invalid kind %q: expected group/version/Kind or version/Kind", value)
		}
		for _, part := range parts {
			if part == "" || strings.ContainsAny(part, " \t\r\n*") {
				return fmt.Errorf("invalid kind %q: every segment must be nonempty and exact", value)
			}
		}
		gv, err := schema.ParseGroupVersion(strings.Join(parts[:len(parts)-1], "/"))
		if err != nil {
			return fmt.Errorf("invalid kind %q: %w", value, err)
		}
		gvk := gv.WithKind(parts[len(parts)-1])
		if !seen[gvk] {
			parsed = append(parsed, gvk)
			seen[gvk] = true
		}
	}
	*k = parsed
	return nil
}

func (k Kinds) MarshalJSON() ([]byte, error) {
	values := make([]string, 0, len(k))
	for _, gvk := range k {
		values = append(values, gvk.GroupVersion().String()+"/"+gvk.Kind)
	}
	return json.Marshal(values)
}
