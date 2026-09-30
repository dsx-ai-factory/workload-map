// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"errors"
	"slices"
	"testing"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/generator"
)

func TestOutputSetValid(t *testing.T) {
	o := NewOutputFlag(true)
	for _, v := range o.Allowed() {
		if err := o.Set(v); err != nil {
			t.Errorf("Set(%q) returned error: %v", v, err)
		}
		if o.String() != v {
			t.Errorf("after Set(%q), String() = %q", v, o.String())
		}
		if string(o.Get()) != v {
			t.Errorf("after Set(%q), Get() = %q", v, o.Get())
		}
	}
}

func TestOutputSetInvalid(t *testing.T) {
	o := NewOutputFlag(true)
	err := o.Set("bogus")
	if err == nil {
		t.Fatal("expected error for invalid output value, got nil")
	}
	if !errors.Is(err, ErrInvalidValue) {
		t.Errorf("error is not ErrInvalidValue: %v", err)
	}
	// An invalid Set must not mutate the value away from its default.
	if o.Get() != generator.OutputTable {
		t.Errorf("value changed after failed Set: %q", o.String())
	}
}

// A command rendering no extra columns leaves wide out, so the flag refuses it
// at parse time rather than the command refusing it later.
func TestOutputWithoutWide(t *testing.T) {
	o := NewOutputFlag(false)
	if slices.Contains(o.Allowed(), string(generator.OutputWide)) {
		t.Errorf("wide is still allowed: %v", o.Allowed())
	}
	if err := o.Set(string(generator.OutputWide)); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("Set(wide) = %v, want ErrInvalidValue", err)
	}
	if !slices.Contains(NewOutputFlag(true).Allowed(), string(generator.OutputWide)) {
		t.Error("wide is missing when the command supports it")
	}
}

func TestOutputType(t *testing.T) {
	if got := NewOutputFlag(true).Type(); got != "output" {
		t.Errorf("Type() = %q, want %q", got, "output")
	}
}
