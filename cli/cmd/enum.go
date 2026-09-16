// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/generator"
	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// ErrInvalidValue is wrapped by Enum.Set when a value is not one of the allowed
// set, so callers can match it with errors.Is.
var ErrInvalidValue = errors.New("invalid value")

// Enum backs a --flag whose value must be one of a fixed set. It implements
// pflag.Value and works with any ~string type.
type Enum[T ~string] struct {
	val      T
	allowed  []T
	typeName string
}

// NewEnum returns an Enum seeded with def and constrained to allowed. typeName
// is shown for the flag's value in help/usage.
func NewEnum[T ~string](typeName string, def T, allowed ...T) *Enum[T] {
	return &Enum[T]{val: def, allowed: allowed, typeName: typeName}
}

// Get returns the current typed value.
func (e *Enum[T]) Get() T { return e.val }

func (e *Enum[T]) String() string { return string(e.val) }

func (e *Enum[T]) Type() string { return e.typeName }

func (e *Enum[T]) Set(v string) error {
	val, err := oneOf(e.allowed, v)
	if err != nil {
		return err
	}
	e.val = val
	return nil
}

// Allowed returns the permitted values as strings, for usage text and shell
// completion.
func (e *Enum[T]) Allowed() []string { return allowedStrings(e.allowed) }

// EnumSlice backs a repeatable --flag constrained to a fixed set. Values
// accumulate across occurrences, and one occurrence may carry a comma list.
type EnumSlice[T ~string] struct {
	vals     []T
	allowed  []T
	typeName string
}

// NewEnumSlice returns an empty EnumSlice constrained to allowed. typeName is
// shown for the flag's value in help/usage.
func NewEnumSlice[T ~string](typeName string, allowed ...T) *EnumSlice[T] {
	return &EnumSlice[T]{allowed: allowed, typeName: typeName}
}

// Get returns the values collected so far.
func (e *EnumSlice[T]) Get() []T { return e.vals }

// String is empty for no values, which is how pflag knows not to print a default.
func (e *EnumSlice[T]) String() string {
	if len(e.vals) == 0 {
		return ""
	}
	return strings.Join(allowedStrings(e.vals), ",")
}

func (e *EnumSlice[T]) Type() string { return e.typeName }

func (e *EnumSlice[T]) Set(v string) error {
	parts := strings.Split(v, ",")
	vals := make([]T, 0, len(parts))
	for _, part := range parts {
		val, err := oneOf(e.allowed, part)
		if err != nil {
			return err
		}
		vals = append(vals, val)
	}
	e.vals = append(e.vals, vals...)
	return nil
}

// Allowed returns the permitted values as strings, for usage text and shell
// completion.
func (e *EnumSlice[T]) Allowed() []string { return allowedStrings(e.allowed) }

// oneOf resolves v against allowed, giving both flag types the same rejection.
func oneOf[T ~string](allowed []T, v string) (T, error) {
	for _, a := range allowed {
		if string(a) == v {
			return a, nil
		}
	}
	var zero T
	return zero, fmt.Errorf("%w: must be one of %s", ErrInvalidValue, strings.Join(allowedStrings(allowed), ", "))
}

func allowedStrings[T ~string](allowed []T) []string {
	out := make([]string, len(allowed))
	for i, a := range allowed {
		out[i] = string(a)
	}
	return out
}

// NewOutputFlag returns an Enum backing the -o/--output flag, defaulting to
// table. A command rendering no extra columns leaves wide out, so the flag
// rejects it at parse time rather than the command rejecting it later.
func NewOutputFlag(supportsWide bool) *Enum[generator.Output] {
	allowed := []generator.Output{generator.OutputTable}
	if supportsWide {
		allowed = append(allowed, generator.OutputWide)
	}
	allowed = append(allowed, generator.OutputJSON, generator.OutputYAML)
	return NewEnum("output", generator.OutputTable, allowed...)
}

// NewPhaseFlag returns an EnumSlice backing the --phase flag. Entries() omits
// Undefined, which a workload without a status mapping still resolves to.
func NewPhaseFlag() *EnumSlice[string] {
	allowed := []string{string(v1alpha1.UndefinedStatus)}
	for _, entry := range (v1alpha1.StatusMappings{}).Entries() {
		allowed = append(allowed, string(entry.Status))
	}
	return NewEnumSlice("phase", allowed...)
}
