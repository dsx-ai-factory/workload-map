// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package karta

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/dsx-ai-factory/workload-map/karta/sdk/internal/jq/execution"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

// route maps a subtree of the PodTemplateSpec view onto a definition path.
// field is the subtree in the view (strings are keys, ints are list
// indexes); an empty field is the whole template.
type route struct {
	field []any
	expr  string
	// whole is true when expr points at a pod sub-object (template, spec,
	// metadata) rather than a single fragment field.
	whole bool
}

// podRoutes is the route table. Fragments that cannot be placed in the view
// without ambiguity are left out and stay read-only: a separate model
// container next to containersPath, and CR-level resources next to a
// container path.
func podRoutes(spec *v1alpha1.SpecDefinition) []route {
	switch {
	case spec == nil:
		return nil
	case spec.PodTemplateSpecPath != nil:
		return []route{{field: nil, expr: *spec.PodTemplateSpecPath, whole: true}}
	case spec.PodSpecPath != nil:
		routes := []route{{field: []any{"spec"}, expr: *spec.PodSpecPath, whole: true}}
		if spec.MetadataPath != nil {
			routes = append(routes, route{field: []any{"metadata"}, expr: *spec.MetadataPath, whole: true})
		}
		return routes
	case spec.FragmentedPodSpecDefinition == nil:
		return nil
	}
	f := spec.FragmentedPodSpecDefinition
	var routes []route
	add := func(expr *string, field ...any) {
		if expr != nil {
			routes = append(routes, route{field: field, expr: *expr})
		}
	}
	add(f.LabelsPath, "metadata", "labels")
	add(f.AnnotationsPath, "metadata", "annotations")
	add(f.SchedulerNamePath, "spec", "schedulerName")
	add(f.PriorityClassNamePath, "spec", "priorityClassName")
	add(f.ResourceClaimsPath, "spec", "resourceClaims")
	add(f.PodAffinityPath, "spec", "affinity", "podAffinity")
	add(f.NodeAffinityPath, "spec", "affinity", "nodeAffinity")
	switch {
	case f.ContainersPath != nil:
		add(f.ContainersPath, "spec", "containers")
	case f.ContainerPath != nil:
		add(f.ContainerPath, "spec", "containers", 0)
	}
	if f.ContainersPath == nil {
		add(f.ImagePath, "spec", "containers", 0, "image")
	}
	if f.ContainersPath == nil && f.ContainerPath == nil {
		add(f.ResourcesPath, "spec", "containers", 0, "resources")
	}
	// Shorter fields first, so projection lets a narrower route override a
	// wider one (imagePath over containerPath).
	slices.SortStableFunc(routes, func(a, b route) int { return len(a.field) - len(b.field) })
	return routes
}

// componentState is one evaluation of a component's routes over an object.
type componentState struct {
	ids []string
	// values[r][i] is route r's value for instance i; nil when absent.
	values [][]any
	// paths[r][i] is route r's concrete path for instance i; nil when the
	// route matched nothing for that instance.
	paths [][][]any
	// pathErr[r] is set when route r is not a path expression.
	pathErr []error
}

func evaluateComponent(ctx context.Context, object map[string]any, def v1alpha1.ComponentDefinition, routes []route) (*componentState, error) {
	runner := execution.NewDefaultRunner(object)
	state := &componentState{ids: []string{""}}
	if def.InstanceIdPath != nil {
		raw, err := runner.Evaluate(ctx, *def.InstanceIdPath)
		if err != nil {
			return nil, fmt.Errorf("%w: instance ids of %q: %w", ErrEvaluation, def.Name, err)
		}
		state.ids = make([]string, len(raw))
		for i, id := range raw {
			s, ok := id.(string)
			if !ok || s == "" {
				return nil, fmt.Errorf("%w: component %q has a non-string or empty instance id %v", ErrEvaluation, def.Name, id)
			}
			state.ids[i] = s
		}
	}
	for _, r := range routes {
		raw, err := runner.Evaluate(ctx, r.expr)
		if err != nil {
			return nil, fmt.Errorf("%w: %s of %q: %w", ErrEvaluation, r.expr, def.Name, err)
		}
		values, err := alignToInstances(def, raw, len(state.ids), r.expr)
		if err != nil {
			return nil, err
		}
		state.values = append(state.values, values)

		var paths [][]any
		rawPaths, err := runner.Evaluate(ctx, "[path("+r.expr+")]")
		if err == nil {
			var list []any
			if len(rawPaths) == 1 {
				list, _ = rawPaths[0].([]any)
			}
			paths = make([][]any, len(list))
			for i, p := range list {
				paths[i] = normalizePath(p.([]any))
			}
			paths, err = alignToInstances(def, paths, len(state.ids), r.expr)
		}
		state.paths = append(state.paths, paths)
		state.pathErr = append(state.pathErr, err)
	}
	return state, nil
}

// alignToInstances pairs route results with instance ids: one result per
// instance, or zero or one result for a single-instance component.
func alignToInstances[T any](def v1alpha1.ComponentDefinition, results []T, instances int, expr string) ([]T, error) {
	switch {
	case len(results) == instances:
		return results, nil
	case def.InstanceIdPath == nil && len(results) == 0:
		return make([]T, 1), nil
	default:
		return nil, fmt.Errorf("%w: %q path %s matched %d values for %d instances", ErrInstanceMismatch, def.Name, expr, len(results), instances)
	}
}

func normalizePath(path []any) []any {
	out := make([]any, len(path))
	for i, step := range path {
		if f, ok := step.(float64); ok {
			out[i] = int(f)
			continue
		}
		out[i] = step
	}
	return out
}

// present reports whether instance i exists in the object: a whole route has
// a value, or a fragment route's parent object exists.
func (s *componentState) present(object map[string]any, routes []route, i int) bool {
	for r, rt := range routes {
		if s.values[r][i] != nil {
			return true
		}
		if !rt.whole && s.pathErr[r] == nil && len(s.paths[r][i]) > 0 {
			if parent := getPath(object, s.paths[r][i][:len(s.paths[r][i])-1]); parent != nil {
				return true
			}
		}
	}
	return false
}

// project builds the PodTemplateSpec view of instance i.
func (s *componentState) project(routes []route, i int) (*corev1.PodTemplateSpec, error) {
	var view any = map[string]any{}
	for r, rt := range routes {
		if s.values[r][i] == nil {
			continue
		}
		var err error
		if view, err = setPath(view, rt.field, s.values[r][i], true); err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		return nil, err
	}
	pod := &corev1.PodTemplateSpec{}
	if err := json.Unmarshal(encoded, pod); err != nil {
		return nil, fmt.Errorf("%w: pod template view: %w", ErrEvaluation, err)
	}
	return pod, nil
}

// operation is one change between two pod views.
type operation struct {
	field  []any
	value  any
	old    any
	delete bool
}

// toJSON converts a pod to its JSON-primitive form with nulls, empty strings
// and empty objects pruned, so a missing and a zero value compare equal, as
// they do for the typed Kubernetes API.
func toJSON(pod *corev1.PodTemplateSpec) (any, error) {
	encoded, err := json.Marshal(pod)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, err
	}
	return prune(out), nil
}

func prune(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			if item = prune(item); item != nil {
				out[key] = item
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = prune(item)
		}
		return out
	case string:
		if v == "" {
			return nil
		}
		return v
	default:
		return v
	}
}

// diff lists the changes from before to after. Objects are compared key by
// key. Lists of named objects with the same names in the same order
// (containers, env, volumes) are compared entry by entry, so unknown fields
// a CR keeps on its own entries survive; any other changed list is replaced.
func diff(field []any, before, after any, out *[]operation) {
	switch b := before.(type) {
	case map[string]any:
		a, ok := after.(map[string]any)
		if !ok {
			break
		}
		keys := slices.Sorted(maps.Keys(b))
		for key := range a {
			if _, ok := b[key]; !ok {
				keys = append(keys, key)
			}
		}
		for _, key := range keys {
			child := append(slices.Clone(field), key)
			oldValue, hadOld := b[key]
			newValue, hasNew := a[key]
			switch {
			case !hasNew:
				*out = append(*out, operation{field: child, old: oldValue, delete: true})
			case !hadOld:
				*out = append(*out, operation{field: child, value: newValue})
			default:
				diff(child, oldValue, newValue, out)
			}
		}
		return
	case []any:
		a, ok := after.([]any)
		if ok && sameNames(b, a) {
			for i := range b {
				diff(append(slices.Clone(field), i), b[i], a[i], out)
			}
			return
		}
	}
	if !reflect.DeepEqual(before, after) {
		*out = append(*out, operation{field: field, value: after, old: before, delete: after == nil})
	}
}

func sameNames(before, after []any) bool {
	if len(before) != len(after) || len(before) == 0 {
		return false
	}
	for i := range before {
		b, okB := before[i].(map[string]any)
		a, okA := after[i].(map[string]any)
		if !okB || !okA {
			return false
		}
		name, ok := b["name"].(string)
		if !ok || name == "" || a["name"] != name {
			return false
		}
	}
	return true
}

// routeOperation finds the narrowest route covering op. An operation above
// every route (setting spec.affinity when only its children are routed) is
// split into its children. So is an object written at the root of a whole
// route: the CR object there holds more than the pod view, so it is merged
// key by key, never replaced.
func routeOperation(routes []route, op operation) ([]routedOperation, error) {
	best := -1
	for r, rt := range routes {
		if hasPrefix(op.field, rt.field) && (best < 0 || len(rt.field) > len(routes[best].field)) {
			best = r
		}
	}
	source := op.value
	if op.delete {
		source = op.old
	}
	_, isObject := source.(map[string]any)
	atWholeRoot := best >= 0 && routes[best].whole && len(routes[best].field) == len(op.field) && isObject
	if best >= 0 && !atWholeRoot {
		return []routedOperation{{route: best, operation: op}}, nil
	}
	below := atWholeRoot || slices.ContainsFunc(routes, func(rt route) bool { return hasPrefix(rt.field, op.field) })
	var children []operation
	switch v := source.(type) {
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(v)) {
			children = append(children, operation{field: append(slices.Clone(op.field), key), value: v[key], old: v[key], delete: op.delete})
		}
	case []any:
		if !op.delete {
			for i, item := range v {
				children = append(children, operation{field: append(slices.Clone(op.field), i), value: item})
			}
		}
	}
	if !below || len(children) == 0 {
		return nil, &FieldError{Field: dotted(op.field), Err: ErrFieldNotRoutable}
	}
	var routed []routedOperation
	for _, child := range children {
		next, err := routeOperation(routes, child)
		if err != nil {
			return nil, err
		}
		routed = append(routed, next...)
	}
	return routed, nil
}

type routedOperation struct {
	route int
	operation
}

func hasPrefix(path, prefix []any) bool {
	if len(path) < len(prefix) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

func dotted(field []any) string {
	parts := make([]string, len(field))
	for i, step := range field {
		switch s := step.(type) {
		case int:
			parts[i] = strconv.Itoa(s)
		default:
			parts[i] = fmt.Sprint(s)
		}
	}
	return strings.Join(parts, ".")
}

func getPath(value any, path []any) any {
	for _, step := range path {
		switch s := step.(type) {
		case string:
			m, ok := value.(map[string]any)
			if !ok {
				return nil
			}
			value = m[s]
		case int:
			list, ok := value.([]any)
			if !ok || s < 0 || s >= len(list) {
				return nil
			}
			value = list[s]
		}
	}
	return value
}

// setPath sets value at path and returns the new root. Missing objects are
// created. A list index must exist unless grow is set, which appends.
func setPath(root any, path []any, value any, grow bool) (any, error) {
	if len(path) == 0 {
		return value, nil
	}
	switch s := path[0].(type) {
	case string:
		m, ok := root.(map[string]any)
		if root == nil {
			m, ok = map[string]any{}, true
		}
		if !ok {
			return nil, fmt.Errorf("%w: %q is under a %T, not an object", ErrPathNotAssignable, s, root)
		}
		child, err := setPath(m[s], path[1:], value, grow)
		if err != nil {
			return nil, err
		}
		m[s] = child
		return m, nil
	case int:
		list, _ := root.([]any)
		if root != nil && list == nil {
			return nil, fmt.Errorf("%w: index %d is under a %T, not a list", ErrPathNotAssignable, s, root)
		}
		switch {
		case s < len(list):
		case grow && s == len(list):
			list = append(list, nil)
		default:
			return nil, fmt.Errorf("%w: index %d is out of range", ErrPathNotAssignable, s)
		}
		child, err := setPath(list[s], path[1:], value, grow)
		if err != nil {
			return nil, err
		}
		list[s] = child
		return list, nil
	}
	return nil, fmt.Errorf("%w: unsupported path step %v", ErrPathNotAssignable, path[0])
}

// deletePath removes the object key at path. A missing parent is a no-op.
func deletePath(root any, path []any) error {
	parent := getPath(root, path[:len(path)-1])
	if parent == nil {
		return nil
	}
	m, ok := parent.(map[string]any)
	key, isKey := path[len(path)-1].(string)
	if !ok || !isKey {
		return fmt.Errorf("%w: cannot delete %v", ErrPathNotAssignable, path)
	}
	delete(m, key)
	return nil
}
