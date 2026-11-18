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
		separator := strings.LastIndexByte(value, '/')
		if separator < 0 {
			return fmt.Errorf("invalid kind %q: expected group/version/Kind or version/Kind", value)
		}
		gv, err := schema.ParseGroupVersion(value[:separator])
		if err != nil {
			return fmt.Errorf("invalid kind %q: expected group/version/Kind or version/Kind", value)
		}
		kind := value[separator+1:]
		if gv.Version == "" || kind == "" || value[0] == '/' || strings.ContainsAny(value, " \t\r\n*") {
			return fmt.Errorf("invalid kind %q: every segment must be nonempty and exact", value)
		}
		gvk := gv.WithKind(kind)
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
