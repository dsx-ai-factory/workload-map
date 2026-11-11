// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package karta

import (
	"context"
	"fmt"
	"reflect"
	"slices"

	"k8s.io/apimachinery/pkg/runtime"
)

type mutateOptions struct {
	instances  []string
	skipAbsent bool
}

// MutateOption configures one MutatePodTemplates call.
type MutateOption func(*mutateOptions)

// OnInstances limits the call to the given instance ids. The default is every
// instance. An unknown id fails before anything is written.
func OnInstances(ids ...string) MutateOption {
	return func(o *mutateOptions) { o.instances = append(o.instances, ids...) }
}

// SkipAbsent skips targeted instances that are absent from the object instead
// of failing with ErrComponentAbsent. Use it when looping over every
// component of a definition whose components are optional.
func SkipAbsent() MutateOption {
	return func(o *mutateOptions) { o.skipAbsent = true }
}

func (w *workload) MutatePodTemplates(ctx context.Context, component string, fn PodTemplateMutateFunc, opts ...MutateOption) (bool, error) {
	def, ok := w.def.byName[component]
	if !ok {
		return false, fmt.Errorf("%w: %q", ErrComponentNotFound, component)
	}
	routes := w.def.routes[component]
	if len(routes) == 0 {
		return false, fmt.Errorf("%w: %q", ErrNoPodDefinition, component)
	}
	options := mutateOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	state, err := evaluateComponent(ctx, w.object, def, routes)
	if err != nil {
		return false, err
	}
	targets := map[int]bool{}
	for _, id := range options.instances {
		i := slices.Index(state.ids, id)
		if i < 0 {
			return false, fmt.Errorf("%w: component %q has no instance %q", ErrInstanceNotFound, component, id)
		}
		targets[i] = true
	}
	if len(options.instances) == 0 {
		for i := range state.ids {
			targets[i] = true
		}
	}

	// Snapshot every instance first: targeted ones are compared with what fn
	// produced, the others must read back unchanged.
	expected := make([]any, len(state.ids))
	for i := range state.ids {
		pod, err := state.project(routes, i)
		if err != nil {
			return false, err
		}
		if expected[i], err = toJSON(pod); err != nil {
			return false, err
		}
	}

	working := runtime.DeepCopyJSON(w.object)
	var root any = working
	changed := false
	for i, id := range state.ids {
		if !targets[i] {
			continue
		}
		if !state.present(w.object, routes, i) {
			if options.skipAbsent {
				continue
			}
			return false, fmt.Errorf("%w: component %q instance %q", ErrComponentAbsent, component, id)
		}
		pod, err := state.project(routes, i)
		if err != nil {
			return false, err
		}
		if err := fn(id, pod); err != nil {
			return false, fmt.Errorf("component %q instance %q: %w", component, id, err)
		}
		after, err := toJSON(pod)
		if err != nil {
			return false, err
		}
		var operations []operation
		diff(nil, expected[i], after, &operations)
		expected[i] = after

		for _, op := range operations {
			routed, err := routeOperation(routes, op)
			if fieldErr, ok := err.(*FieldError); ok {
				fieldErr.Component, fieldErr.Instance = component, id
				return false, fieldErr
			}
			for _, ro := range routed {
				field := dotted(ro.field)
				if err := state.pathErr[ro.route]; err != nil {
					return false, &FieldError{Component: component, Instance: id, Field: field, Err: fmt.Errorf("%w: %w", ErrPathNotAssignable, err)}
				}
				base := state.paths[ro.route][i]
				if base == nil {
					return false, &FieldError{Component: component, Instance: id, Field: field,
						Err: fmt.Errorf("%w: %s matched nothing", ErrPathNotAssignable, routes[ro.route].expr)}
				}
				target := append(slices.Clone(base), ro.field[len(routes[ro.route].field):]...)
				if ro.delete {
					err = deletePath(root, target)
				} else {
					root, err = setPath(root, target, runtime.DeepCopyJSONValue(ro.value), false)
				}
				if err != nil {
					return false, &FieldError{Component: component, Instance: id, Field: field, Err: err}
				}
				changed = true
			}
		}
	}
	if !changed {
		return false, nil
	}

	working, _ = root.(map[string]any)
	if err := verify(ctx, working, w, component, expected); err != nil {
		return false, err
	}
	w.object = working
	w.modified = true
	return true, nil
}

// verify re-reads every instance of the component from the mutated object.
// It catches definition paths that alias each other or a CR that stores a
// field in a lossy form.
func verify(ctx context.Context, object map[string]any, w *workload, component string, expected []any) error {
	routes := w.def.routes[component]
	state, err := evaluateComponent(ctx, object, w.def.byName[component], routes)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrVerificationFailed, err)
	}
	if len(state.ids) != len(expected) {
		return fmt.Errorf("%w: component %q now has %d instances, had %d", ErrVerificationFailed, component, len(state.ids), len(expected))
	}
	for i, id := range state.ids {
		pod, err := state.project(routes, i)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrVerificationFailed, err)
		}
		got, err := toJSON(pod)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrVerificationFailed, err)
		}
		if !reflect.DeepEqual(got, expected[i]) {
			var mismatch []operation
			diff(nil, expected[i], got, &mismatch)
			fields := make([]string, len(mismatch))
			for j, op := range mismatch {
				fields[j] = dotted(op.field)
			}
			return fmt.Errorf("%w: component %q instance %q fields %v", ErrVerificationFailed, component, id, fields)
		}
	}
	return nil
}
