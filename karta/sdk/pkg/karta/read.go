// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package karta

import (
	"context"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

// Status is the evaluated status of a component.
type Status = resource.Status

// Condition is one extracted Kubernetes-style condition.
type Condition = resource.Condition

// Scale is the replica information of one instance.
type Scale = resource.Scale

// PodFragments holds the raw values of a fragmented pod definition.
type PodFragments = resource.FragmentedPodSpec

// Snapshot is a point-in-time read of the whole workload. It is plain data,
// so tests can build one as a literal.
type Snapshot struct {
	// Status is the root component status, nil without a status definition.
	Status *Status
	// Components holds every component, the root first.
	Components []Component
}

// Get returns the named component.
func (s *Snapshot) Get(name string) (*Component, bool) {
	i := slices.IndexFunc(s.Components, func(c Component) bool { return c.Name == name })
	if i < 0 {
		return nil, false
	}
	return &s.Components[i], true
}

// Children returns the direct children of the named component.
func (s *Snapshot) Children(name string) []Component {
	var children []Component
	for _, c := range s.Components {
		if c.Parent == name {
			children = append(children, c)
		}
	}
	return children
}

// Component is the read state of one component.
type Component struct {
	ComponentInfo
	Status *Status
	// Instances are sorted by ID. A single-instance component has exactly
	// one instance with an empty ID.
	Instances []Instance
}

// Instance is the read state of one component instance.
type Instance struct {
	ID string
	// Present is false when the definition describes the instance but the
	// object does not hold it, for example an optional component.
	Present bool
	Scale   *Scale
	// Pod is the instance pod template, whichever way the definition
	// locates it. Nil without a pod definition or when not present.
	Pod *corev1.PodTemplateSpec
	// Fragments holds the raw fragment values for fragmented definitions,
	// including those Pod cannot place without ambiguity.
	Fragments *PodFragments
}

func (w *workload) Status(ctx context.Context) (*Status, error) {
	root, err := w.factory().GetRootComponent()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEvaluation, err)
	}
	status, err := root.GetStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEvaluation, err)
	}
	return status, nil
}

func (w *workload) Read(ctx context.Context) (*Snapshot, error) {
	factory := w.factory()
	snapshot := &Snapshot{}
	for _, info := range w.def.components {
		component, err := w.readComponent(ctx, factory, info)
		if err != nil {
			return nil, err
		}
		snapshot.Components = append(snapshot.Components, *component)
	}
	snapshot.Status = snapshot.Components[0].Status
	return snapshot, nil
}

func (w *workload) Component(ctx context.Context, name string) (*Component, error) {
	i := slices.IndexFunc(w.def.components, func(c ComponentInfo) bool { return c.Name == name })
	if i < 0 {
		return nil, fmt.Errorf("%w: %q", ErrComponentNotFound, name)
	}
	return w.readComponent(ctx, w.factory(), w.def.components[i])
}

func (w *workload) readComponent(ctx context.Context, factory *resource.ComponentFactory, info ComponentInfo) (*Component, error) {
	extractor, err := factory.GetComponent(info.Name)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrComponentNotFound, info.Name)
	}
	status, err := extractor.GetStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: status of %q: %w", ErrEvaluation, info.Name, err)
	}
	scales, err := extractor.GetScale(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: scale of %q: %w", ErrEvaluation, info.Name, err)
	}
	var fragments map[string]PodFragments
	if info.PodSource == PodSourceFragments {
		if fragments, err = extractor.GetFragmentedPodSpec(ctx); err != nil {
			return nil, fmt.Errorf("%w: fragments of %q: %w", ErrEvaluation, info.Name, err)
		}
	}
	routes := w.def.routes[info.Name]
	state, err := evaluateComponent(ctx, w.object, w.def.byName[info.Name], routes)
	if err != nil {
		return nil, err
	}

	component := &Component{ComponentInfo: info, Status: status}
	for i, id := range state.ids {
		instance := Instance{ID: id, Present: len(routes) == 0 || state.present(w.object, routes, i)}
		if scale, ok := scales[id]; ok {
			instance.Scale = &scale
		}
		if fragment, ok := fragments[id]; ok {
			instance.Fragments = &fragment
		}
		if len(routes) > 0 && instance.Present {
			if instance.Pod, err = state.project(routes, i); err != nil {
				return nil, err
			}
		}
		component.Instances = append(component.Instances, instance)
	}
	slices.SortFunc(component.Instances, func(a, b Instance) int { return strings.Compare(a.ID, b.ID) })
	return component, nil
}
