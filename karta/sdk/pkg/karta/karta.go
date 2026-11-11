// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package karta is the consumer entry point of the Karta SDK. Load a workload
// object against its Karta definition, read its components, instances, pods
// and status, mutate pod templates with plain Go code, suspend or resume it,
// and get the mutated object back to apply.
//
//	w, err := karta.Load(definition, obj)
//	changed, err := w.MutatePodTemplates(ctx, "worker", func(_ string, pod *corev1.PodTemplateSpec) error {
//		pod.Spec.SchedulerName = "kai-scheduler"
//		return nil
//	})
//	if changed {
//		err = c.Update(ctx, w.Object())
//	}
package karta

//go:generate go run go.uber.org/mock/mockgen -source=karta.go -destination=kartamock/karta_mock.go -package=kartamock

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

// Workload is a Karta-described workload object held in memory. It is not
// safe for concurrent use.
type Workload interface {
	Reader
	Mutator
	Definition() Definition
}

// Reader inspects the workload.
type Reader interface {
	// Status returns the root status, or nil when the definition has none.
	Status(ctx context.Context) (*Status, error)
	// Read returns every component, the root included, with its instances.
	Read(ctx context.Context) (*Snapshot, error)
	// Component reads one component.
	Component(ctx context.Context, name string) (*Component, error)
}

// Mutator changes the workload. Every call is all or nothing: on error the
// object is unchanged.
type Mutator interface {
	// MutatePodTemplates runs fn on the pod template of every targeted
	// instance of a component and writes back what fn changed. It reports
	// whether the object changed. Running pods are never touched; the
	// workload's controller creates pods from the written template.
	//
	// The template fn leaves is the template written. The caller owns
	// these semantics:
	//   - A field fn removes or clears is deleted from the object.
	//     Replacing pod.Spec wholesale deletes every field not set again.
	//   - An empty string, nil or empty map means unset. A label or
	//     annotation with an empty value cannot be written.
	//   - Lists of named objects (containers, volumes, env) are changed
	//     entry by entry when the names match in order. Any other changed
	//     list is replaced whole, and fields the CRD adds inside its
	//     entries, outside corev1, are lost.
	//   - A changed field the definition gives no path fails the whole call
	//     with ErrFieldNotRoutable. ComponentInfo.Writable lists the
	//     writable fields up front.
	MutatePodTemplates(ctx context.Context, component string, fn PodTemplateMutateFunc, opts ...MutateOption) (bool, error)
	// CanSuspend reports whether the definition declares suspend actions.
	CanSuspend() bool
	// Suspend applies the suspend actions of every suspendable component.
	Suspend(ctx context.Context) error
	// Resume applies the resume actions of every suspendable component.
	Resume(ctx context.Context) error
	// Object returns a deep copy of the current object.
	Object() *unstructured.Unstructured
	// Modified reports whether any call changed the object since Load.
	Modified() bool
}

// Definition is a validated, indexed Karta definition. It is immutable and
// safe to share across goroutines and objects.
type Definition interface {
	// Karta returns a deep copy of the source definition.
	Karta() *v1alpha1.Karta
	// Components lists every component, the root first.
	Components() []ComponentInfo
	// Load binds a workload object to the definition. The object is deep-copied.
	Load(obj runtime.Object) (Workload, error)
}

// ComponentInfo is the static description of a component.
type ComponentInfo struct {
	Name string
	// Parent is empty for the root component.
	Parent string
	// Kind is nil for logical grouping components.
	Kind          *schema.GroupVersionKind
	PodSource     PodSource
	MultiInstance bool
	Suspendable   bool
	// Writable lists the PodTemplateSpec fields MutatePodTemplates can write, as
	// dotted paths. "metadata" or "spec" means every field below it.
	Writable []string
	Selector *v1alpha1.PodSelector
}

// PodSource names how the definition locates a component's pods.
type PodSource string

const (
	PodSourceNone      PodSource = ""
	PodSourceTemplate  PodSource = "PodTemplateSpec"
	PodSourceSpec      PodSource = "PodSpec"
	PodSourceFragments PodSource = "Fragments"
)

// PodTemplateMutateFunc edits the pod template of one instance in place.
// instanceID is empty for single-instance components. Returning an error
// aborts the call. Edit only the fields to change: whatever fn removes is
// deleted from the object (see Mutator.MutatePodTemplates).
type PodTemplateMutateFunc func(instanceID string, pod *corev1.PodTemplateSpec) error

type definition struct {
	karta      *v1alpha1.Karta
	components []ComponentInfo
	byName     map[string]v1alpha1.ComponentDefinition
	routes     map[string][]route
}

type workload struct {
	def      *definition
	object   map[string]any
	modified bool
}

// Compile validates and indexes a definition. Reuse the result for every
// object of that kind.
func Compile(def *v1alpha1.Karta) (Definition, error) {
	if def == nil {
		return nil, fmt.Errorf("%w: nil definition", ErrInvalidDefinition)
	}
	if err := v1alpha1.NewKartaValidator(def).Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidDefinition, err)
	}
	d := &definition{
		karta:  def.DeepCopy(),
		byName: map[string]v1alpha1.ComponentDefinition{},
		routes: map[string][]route{},
	}
	structure := d.karta.Spec.StructureDefinition
	for _, component := range append([]v1alpha1.ComponentDefinition{structure.RootComponent}, structure.ChildComponents...) {
		routes := podRoutes(component.SpecDefinition)
		info := ComponentInfo{
			Name:          component.Name,
			PodSource:     podSource(component.SpecDefinition),
			MultiInstance: component.InstanceIdPath != nil,
			Suspendable:   component.SuspendDefinition != nil,
			Selector:      component.PodSelector,
		}
		if component.OwnerRef != nil {
			info.Parent = *component.OwnerRef
		}
		if component.Kind != nil {
			info.Kind = &schema.GroupVersionKind{Group: component.Kind.Group, Version: component.Kind.Version, Kind: component.Kind.Kind}
		}
		for _, r := range routes {
			info.Writable = append(info.Writable, dotted(r.field))
		}
		if len(routes) > 0 && len(routes[0].field) == 0 {
			info.Writable = []string{"metadata", "spec"}
		}
		slices.Sort(info.Writable)
		d.components = append(d.components, info)
		d.byName[component.Name] = component
		d.routes[component.Name] = routes
	}
	return d, nil
}

// Load compiles def and binds obj to it. Use Compile once and
// Definition.Load per object when loading many objects of one kind.
func Load(def *v1alpha1.Karta, obj runtime.Object) (Workload, error) {
	d, err := Compile(def)
	if err != nil {
		return nil, err
	}
	return d.Load(obj)
}

func (d *definition) Karta() *v1alpha1.Karta { return d.karta.DeepCopy() }

func (d *definition) Components() []ComponentInfo { return slices.Clone(d.components) }

func (d *definition) Load(obj runtime.Object) (Workload, error) {
	if obj == nil {
		return nil, fmt.Errorf("%w: nil object", ErrInvalidObject)
	}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidObject, err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidObject, err)
	}
	u := &unstructured.Unstructured{Object: object}
	if u.GetKind() == "" || u.GetAPIVersion() == "" {
		return nil, fmt.Errorf("%w: missing apiVersion or kind", ErrInvalidObject)
	}
	if u.GetName() == "" && u.GetGenerateName() == "" {
		return nil, fmt.Errorf("%w: missing metadata.name or metadata.generateName", ErrInvalidObject)
	}
	if kind := d.karta.Spec.StructureDefinition.RootComponent.Kind; kind != nil {
		gvk := u.GroupVersionKind()
		if gvk.Group != kind.Group || gvk.Version != kind.Version || gvk.Kind != kind.Kind {
			return nil, fmt.Errorf("%w: object is %s, definition expects %s/%s %s", ErrInvalidObject, gvk, kind.Group, kind.Version, kind.Kind)
		}
	}
	return &workload{def: d, object: object}, nil
}

func (w *workload) Definition() Definition { return w.def }

func (w *workload) Object() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: runtime.DeepCopyJSON(w.object)}
}

func (w *workload) Modified() bool { return w.modified }

// factory builds the extraction engine over the current object. The object
// is read only through it.
func (w *workload) factory() *resource.ComponentFactory {
	return resource.NewComponentFactoryFromObject(w.def.karta, &unstructured.Unstructured{Object: w.object})
}

func podSource(spec *v1alpha1.SpecDefinition) PodSource {
	switch {
	case spec == nil:
		return PodSourceNone
	case spec.PodTemplateSpecPath != nil:
		return PodSourceTemplate
	case spec.PodSpecPath != nil:
		return PodSourceSpec
	case spec.FragmentedPodSpecDefinition != nil:
		return PodSourceFragments
	default:
		return PodSourceNone
	}
}
