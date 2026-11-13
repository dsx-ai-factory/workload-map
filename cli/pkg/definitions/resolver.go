// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package definitions

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog"
)

type Origin string

const (
	OriginCatalog Origin = "catalog"
	OriginCluster Origin = "cluster"
)

var (
	ErrNotFound     = errors.New("definitions: no Karta definition for GVK")
	ErrNameNotFound = errors.New("definitions: no Karta definition named")
	ErrAmbiguous    = errors.New("definitions: more than one Karta definition for GVK")
)

// Status says whether the CLI selects a definition for the workload type it
// covers. It describes the lookup, not whether any workload is running.
type Status string

const (
	// StatusActive is the one definition selected for its workload type.
	StatusActive Status = "active"
	// StatusInactive is never selected: a cluster definition overrides it, or it
	// names no workload type.
	StatusInactive Status = "inactive"
	// StatusAmbiguous claims its workload type together with another definition
	// from the same source, so none of them is selected.
	StatusAmbiguous Status = "ambiguous"
)

// Definition is a Karta together with the source it was read from.
type Definition struct {
	Karta  *v1alpha1.Karta
	Origin Origin
}

// Listed is a definition together with its selection status.
type Listed struct {
	Definition
	Status Status
}

// Collision records a root GVK claimed by more than one definition from the same
// source.
type Collision struct {
	GVK   schema.GroupVersionKind
	Names []string // metadata.names claiming this GVK, name-sorted
}

// Resolver is an immutable lookup over the merged catalog and cluster definitions.
type Resolver struct {
	definitions []Definition
	// overridden holds the catalog definitions a cluster definition replaced,
	// sorted by GVK then name. Lookups never select them.
	overridden []Definition
}

// New merges catalog and cluster definitions into a Resolver. The catalog is
// indexed first so a cluster definition overrides a catalog one claiming the
// same root GVK.
func New(catalogKartas, cluster []*v1alpha1.Karta) *Resolver {
	r := &Resolver{}
	listing := make(map[schema.GroupVersionKind][]Definition, len(catalogKartas)+len(cluster))
	unmapped, _ := index(listing, catalogKartas, OriginCatalog)
	clusterUnmapped, overridden := index(listing, cluster, OriginCluster)
	unmapped = append(unmapped, clusterUnmapped...)
	slices.SortFunc(overridden, func(a, b Definition) int {
		ga, _ := mappable(a)
		gb, _ := mappable(b)
		return cmp.Or(strings.Compare(ga.String(), gb.String()), strings.Compare(a.Karta.Name, b.Karta.Name))
	})
	r.overridden = overridden

	gvks := slices.SortedFunc(maps.Keys(listing), func(a, b schema.GroupVersionKind) int {
		return strings.Compare(a.String(), b.String())
	})
	r.definitions = make([]Definition, 0, len(listing)+len(unmapped))
	for _, gvk := range gvks {
		r.definitions = append(r.definitions, listing[gvk]...)
	}
	slices.SortFunc(unmapped, func(a, b Definition) int {
		return strings.Compare(a.Karta.Name, b.Karta.Name)
	})
	r.definitions = append(r.definitions, unmapped...)
	return r
}

// mappable reports the root GVK a definition can be looked up by.
func mappable(d Definition) (schema.GroupVersionKind, bool) {
	gvk := catalog.RootKey(d.Karta)
	return gvk, gvk.Version != "" && gvk.Kind != ""
}

// index adds one source to the resolver and returns the definitions that name no
// GVK and the earlier definitions this source replaced. Sorting by name keeps the
// outcome deterministic.
func index(listing map[schema.GroupVersionKind][]Definition, kartas []*v1alpha1.Karta, origin Origin) (unmapped, replaced []Definition) {
	sorted := slices.Clone(kartas)
	slices.SortFunc(sorted, func(a, b *v1alpha1.Karta) int {
		return strings.Compare(a.Name, b.Name)
	})

	claimed := make(map[schema.GroupVersionKind][]Definition, len(sorted))
	for _, karta := range sorted {
		def := Definition{Karta: karta, Origin: origin}
		gvk, ok := mappable(def)
		if !ok {
			unmapped = append(unmapped, def)
			continue
		}
		claimed[gvk] = append(claimed[gvk], def)
	}

	for gvk, defs := range claimed {
		replaced = append(replaced, listing[gvk]...)
		// Assignment, not append: this source replaces an earlier one for the GVK.
		listing[gvk] = defs
	}
	return unmapped, replaced
}

// Resolve returns the one definition covering gvk.
func (r *Resolver) Resolve(gvk schema.GroupVersionKind) (Definition, error) {
	var defs []Definition
	for _, def := range r.definitions {
		if root, ok := mappable(def); ok && root == gvk {
			defs = append(defs, def)
		}
	}
	switch len(defs) {
	case 0:
		return Definition{}, fmt.Errorf("%w: %s", ErrNotFound, FormatGVK(gvk))
	case 1:
		return defs[0], nil
	default:
		names := make([]string, 0, len(defs))
		for _, def := range defs {
			names = append(names, fmt.Sprintf("%q", def.Karta.Name))
		}
		return Definition{}, fmt.Errorf("%w: %s: %s", ErrAmbiguous, FormatGVK(gvk), strings.Join(names, ", "))
	}
}

// List returns every visible definition sorted by GVK then name, one entry per
// claimant, with definitions that name no GVK last.
func (r *Resolver) List() []Definition {
	return r.definitions
}

// ListAll returns every definition with its status: the ones List returns, in
// the same order, followed by the catalog definitions a cluster definition
// overrides. List stays the set lookups choose from; ListAll also shows what the
// cluster hides.
func (r *Resolver) ListAll() []Listed {
	claimants := make(map[schema.GroupVersionKind]int, len(r.definitions))
	for _, def := range r.definitions {
		if gvk, ok := mappable(def); ok {
			claimants[gvk]++
		}
	}

	out := make([]Listed, 0, len(r.definitions)+len(r.overridden))
	for _, def := range r.definitions {
		gvk, ok := mappable(def)
		switch {
		case !ok:
			out = append(out, Listed{Definition: def, Status: StatusInactive})
		case claimants[gvk] > 1:
			out = append(out, Listed{Definition: def, Status: StatusAmbiguous})
		default:
			out = append(out, Listed{Definition: def, Status: StatusActive})
		}
	}
	for _, def := range r.overridden {
		out = append(out, Listed{Definition: def, Status: StatusInactive})
	}
	return out
}

// ByRootKind matches the Kind segment of a GVK.
func (r *Resolver) ByRootKind(kind string) []Definition {
	query := strings.ToLower(kind)

	out := make([]Definition, 0)
	for _, def := range r.definitions {
		gvk, ok := mappable(def)
		if !ok {
			continue
		}
		if strings.ToLower(gvk.Kind) == query {
			out = append(out, def)
		}
	}
	return out
}

// ByName returns the definition called name, matched exactly: names are
// identifiers copied from list output. A cluster definition wins over a catalog
// one with the same name, and a catalog definition the cluster overrides is
// found only when nothing else carries the name.
func (r *Resolver) ByName(name string) (Definition, error) {
	var catalogMatch *Definition
	for i := range r.definitions {
		def := &r.definitions[i]
		if def.Karta.Name != name {
			continue
		}
		if def.Origin == OriginCluster {
			return *def, nil
		}
		if catalogMatch == nil {
			catalogMatch = def
		}
	}
	if catalogMatch != nil {
		return *catalogMatch, nil
	}
	for _, def := range r.overridden {
		if def.Karta.Name == name {
			return def, nil
		}
	}
	return Definition{}, fmt.Errorf("%w: %q", ErrNameNotFound, name)
}

// Collisions returns root GVKs claimed by more than one definition from the same
// source.
func (r *Resolver) Collisions() []Collision {
	var out []Collision
	var run schema.GroupVersionKind
	var names []string
	flush := func() {
		if len(names) > 1 {
			out = append(out, Collision{GVK: run, Names: names})
		}
		names = nil
	}
	for _, def := range r.definitions {
		gvk, ok := mappable(def)
		if !ok || gvk != run {
			flush()
			run = gvk
		}
		if ok {
			names = append(names, def.Karta.Name)
		}
	}
	flush()
	return out
}

// FormatGVK renders gvk as group/version/Kind, the apiVersion a manifest carries
// followed by its kind. apimachinery's "group/version, Kind=Kind" reads as a Go
// struct dump rather than as one name for the type.
func FormatGVK(gvk schema.GroupVersionKind) string {
	return gvk.GroupVersion().String() + "/" + gvk.Kind
}
