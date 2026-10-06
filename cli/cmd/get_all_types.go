// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"context"
	"fmt"
	"slices"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/definitions"
	"github.com/dsx-ai-factory/workload-map/cli/pkg/workload"
	"github.com/dsx-ai-factory/workload-map/pkg/catalog"
)

// ownerWalkConcurrency bounds the owner reads in flight. A workload wrapped in
// an uncovered CR costs one read per row, so a serial walk scales badly.
const ownerWalkConcurrency = 8

// collectAllTypes lists every covered type discovery maps and keeps the
// workloads whose controller-owner chain reaches no other covered object.
// Being unable to list one type, or to walk one object's owners, degrades the
// result with a warning rather than failing it.
func collectAllTypes(ctx context.Context, look lookup, opts *getOptions) ([]workload.View, []string, error) {
	versions, kinds := coveredVersions(look.resolver)

	var (
		candidates []candidate
		warnings   []string
	)
	for _, gk := range kinds {
		listed, listWarnings, err := listKind(ctx, look, opts, gk, versions[gk])
		warnings = append(warnings, listWarnings...)
		if err != nil {
			return nil, warnings, err
		}
		candidates = append(candidates, listed...)
	}

	covered := func(gk schema.GroupKind) bool {
		_, ok := versions[gk]
		return ok
	}
	views, walkWarnings := keepTopLevel(ctx, workload.NewOwnerWalker(look.dyn, look.mapper), candidates, covered)
	return views, append(warnings, walkWarnings...), nil
}

// coveredVersions groups the root GVKs of every definition by GroupKind, in
// resolver order, with each Kind's versions most stable first. A Kind covered
// at several versions is listed once: the server returns the same objects at
// each.
func coveredVersions(resolver *definitions.Resolver) (map[schema.GroupKind][]string, []schema.GroupKind) {
	versions := map[schema.GroupKind][]string{}
	var kinds []schema.GroupKind
	for _, def := range resolver.List() {
		gvk := catalog.RootKey(def.Karta)
		if gvk.Version == "" || gvk.Kind == "" {
			continue
		}
		gk := gvk.GroupKind()
		if _, seen := versions[gk]; !seen {
			kinds = append(kinds, gk)
		}
		if !slices.Contains(versions[gk], gvk.Version) {
			versions[gk] = append(versions[gk], gvk.Version)
		}
	}
	for _, gk := range kinds {
		slices.SortFunc(versions[gk], func(a, b string) int {
			return version.CompareKubeAwareVersionStrings(b, a)
		})
	}
	return versions, kinds
}

// listKind lists one covered Kind at the most stable version the server serves
// and resolves each object, keeping those that pass --phase. Resolving before
// the owner walk lets --phase narrow the objects before any owner read.
func listKind(
	ctx context.Context, look lookup, opts *getOptions, gk schema.GroupKind, versions []string,
) ([]candidate, []string, error) {
	mapping, err := look.mapper.RESTMapping(gk, versions...)
	switch {
	case meta.IsNoMatchError(err):
		// No type was named, and most covered types are absent from any one
		// cluster, so an absent one is not worth a warning.
		return nil, nil, nil
	case err != nil:
		return nil, nil, fmt.Errorf("discover %s: %w", gk.Kind, err)
	}

	def, err := look.resolver.Resolve(mapping.GroupVersionKind)
	if err != nil {
		return nil, []string{fmt.Sprintf("not listing %s: %v", gk.Kind, err)}, nil
	}

	namespace := look.namespace
	if mapping.Scope.Name() == meta.RESTScopeNameRoot {
		namespace = ""
	}
	objects, err := list(ctx, look.dyn, mapping, namespace, opts)
	switch {
	case err == nil:
	case apierrors.IsForbidden(err):
		return nil, []string{fmt.Sprintf("not allowed to list %s, so the result leaves it out", gk.Kind)}, nil
	default:
		return nil, nil, fmt.Errorf("list %s: %w", gk.Kind, err)
	}

	var (
		candidates []candidate
		warnings   []string
	)
	for i := range objects {
		view, err := workload.Resolve(ctx, &objects[i], def)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("could not resolve %s/%s: %v",
				gk.Kind, objects[i].GetName(), err))
			continue
		}
		if matchesPhase(view, opts.phases) {
			candidates = append(candidates, candidate{object: &objects[i], view: *view})
		}
	}
	return candidates, warnings, nil
}

// candidate is a listed object that passed the filters, kept with its view
// until the owner walk decides whether it is a row.
type candidate struct {
	object *unstructured.Unstructured
	view   workload.View
}

// keepTopLevel walks each candidate's owners, at most ownerWalkConcurrency at a
// time, and returns the views of the top-level ones in candidate order.
func keepTopLevel(
	ctx context.Context, walker *workload.OwnerWalker, candidates []candidate, covered func(schema.GroupKind) bool,
) ([]workload.View, []string) {
	topLevel := make([]bool, len(candidates))
	walkErrs := make([]error, len(candidates))
	inFlight := make(chan struct{}, ownerWalkConcurrency)
	var wg sync.WaitGroup
	for i, c := range candidates {
		inFlight <- struct{}{}
		wg.Go(func() {
			defer func() { <-inFlight }()
			topLevel[i], walkErrs[i] = walker.TopLevel(ctx, c.object, covered)
		})
	}
	wg.Wait()

	var (
		views    []workload.View
		warnings []string
	)
	for i, c := range candidates {
		if walkErrs[i] != nil {
			// Hiding the object would drop a real workload over a permission
			// error, with nothing to tell the caller.
			warnings = append(warnings, fmt.Sprintf("listing %s/%s, though it may belong to another workload: %v",
				c.view.Kind, c.view.Name, walkErrs[i]))
		}
		if topLevel[i] {
			views = append(views, c.view)
		}
	}
	return views, warnings
}

// typeColumn names a row by its Kind, qualified as Kind.group when more than
// one covered group shares that Kind, so each name stays the same whatever the
// result holds.
func typeColumn(resolver *definitions.Resolver) func(workload.View) string {
	groups := map[string]map[string]bool{}
	for _, def := range resolver.List() {
		gvk := catalog.RootKey(def.Karta)
		if groups[gvk.Kind] == nil {
			groups[gvk.Kind] = map[string]bool{}
		}
		groups[gvk.Kind][gvk.Group] = true
	}
	return func(view workload.View) string {
		group := schema.FromAPIVersionAndKind(view.APIVersion, view.Kind).Group
		if len(groups[view.Kind]) < 2 || group == "" {
			return view.Kind
		}
		return view.Kind + "." + group
	}
}
