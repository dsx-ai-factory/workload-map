// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package karta

import (
	"errors"
	"fmt"
)

// Sentinel errors. Every error returned by this package wraps one of them, so
// callers branch with errors.Is and never see jq engine types.
var (
	ErrInvalidDefinition   = errors.New("invalid karta definition")
	ErrInvalidObject       = errors.New("invalid workload object")
	ErrComponentNotFound   = errors.New("component not found")
	ErrInstanceNotFound    = errors.New("instance not found")
	ErrComponentAbsent     = errors.New("component is absent from the object")
	ErrNoPodDefinition     = errors.New("component has no pod definition")
	ErrFieldNotRoutable    = errors.New("pod field has no write route in the definition")
	ErrPathNotAssignable   = errors.New("definition path is not assignable")
	ErrInstanceMismatch    = errors.New("definition path does not match the component instances")
	ErrVerificationFailed  = errors.New("mutation did not read back as written")
	ErrSuspendNotSupported = errors.New("definition does not support suspend")
	ErrEvaluation          = errors.New("definition evaluation failed")
)

// FieldError reports a pod field that cannot be written for one instance of a
// component. Field is the dotted path in the PodTemplateSpec view, for example
// "spec.tolerations".
type FieldError struct {
	Component string
	Instance  string
	Field     string
	Err       error
}

func (e *FieldError) Error() string {
	if e.Instance == "" {
		return fmt.Sprintf("component %q field %q: %v", e.Component, e.Field, e.Err)
	}
	return fmt.Sprintf("component %q instance %q field %q: %v", e.Component, e.Instance, e.Field, e.Err)
}

func (e *FieldError) Unwrap() error { return e.Err }
