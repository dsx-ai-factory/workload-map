// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package generator

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"sigs.k8s.io/yaml"
)

var ErrUnsupportedOutput = errors.New("unsupported output format")

// list is the envelope the machine formats carry, so a consumer reads one shape
// whatever the result size, and reads the total without walking the items.
type list[T any] struct {
	Items []T `json:"items"`
	Count int `json:"count"`
}

func newList[T any](items []T) list[T] {
	if items == nil {
		// A nil slice marshals as null, where an empty result is an empty list.
		items = []T{}
	}
	return list[T]{Items: items, Count: len(items)}
}

// Render writes items in the machine formats and hands the human ones to table.
func Render[T any](out io.Writer, format Output, items []T, byName bool, table func(io.Writer) error) error {
	if byName && len(items) == 1 {
		return render(out, format, items[0], table)
	}
	return render(out, format, newList(items), table)
}

func render(out io.Writer, format Output, payload any, table func(io.Writer) error) error {
	switch format {
	case OutputTable, OutputWide:
		return table(out)

	case OutputJSON:
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(payload); err != nil {
			return fmt.Errorf("encode as json: %w", err)
		}
		return nil

	case OutputYAML:
		data, err := yaml.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode as yaml: %w", err)
		}
		if _, err := out.Write(data); err != nil {
			return fmt.Errorf("write yaml: %w", err)
		}
		return nil

	default:
		return fmt.Errorf("%w %q", ErrUnsupportedOutput, format)
	}
}
