// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/definitions"
	"github.com/dsx-ai-factory/workload-map/cli/pkg/workload"
	"github.com/dsx-ai-factory/workload-map/pkg/catalog"
)

// collectAllTypes lists the top-level workloads of every covered type. A type
// or an owner the caller cannot read degrades the result with a warning.
func collectAllTypes(ctx context.Context, look lookup, opts *getOptions) ([]workload.View, []string, error) {
	versions, kinds := coveredVersions(look.resolver)

	// Every mapping is resolved before any owner walk, because a kind that
	// cannot be mapped at a covered version leaves the covered set.
	mappings, warnings, err := discoverKinds(look, versions, kinds)
	if err != nil {
		return nil, warnings, err
	}

	walker := workload.NewOwnerWalker(look.metadata, look.mapper)
	topLevel := func(obj *unstructured.Unstructured) (bool, error) {
		return walker.TopLevel(ctx, obj, func(gk schema.GroupKind) bool {
			_, ok := versions[gk]
			return ok
		})
	}

	var views []workload.View
	for _, gk := range kinds {
		mapping, ok := mappings[gk]
		if !ok {
			continue
		}
		listed, listWarnings, err := listKind(ctx, look, opts, mapping, topLevel)
		warnings = append(warnings, listWarnings...)
		if err != nil {
			return nil, warnings, err
		}
		views = append(views, listed...)
	}
	return views, warnings, nil
}

// discoverKinds maps each covered Kind to the version the cluster serves. A Kind
// it cannot map is removed from versions, so the owner walk treats it as
// uncovered: an object it owns is never hidden behind an owner that is not
// listed. A Kind served only at versions no Karta covers also warns.
func discoverKinds(
	look lookup, versions map[schema.GroupKind][]string, kinds []schema.GroupKind,
) (map[schema.GroupKind]*meta.RESTMapping, []string, error) {
	var warnings []string
	mappings := map[schema.GroupKind]*meta.RESTMapping{}
	for _, gk := range kinds {
		mapping, err := look.mapper.RESTMapping(gk, versions[gk]...)
		switch {
		case err == nil:
			mappings[gk] = mapping
			continue
		case !meta.IsNoMatchError(err):
			return nil, warnings, fmt.Errorf("discover %s: %w", gk.Kind, err)
		}
		served, err := look.mapper.RESTMapping(gk)
		switch {
		case meta.IsNoMatchError(err):
			// Most covered types are absent from any one cluster, so absence is
			// not news. No object names an absent kind as its owner, unless
			// discovery missed a group, and then the walk warns on that object.
			delete(versions, gk)
			continue
		case err != nil:
			return nil, warnings, fmt.Errorf("discover %s: %w", gk.Kind, err)
		}
		// A Karta maps the fields of one version, which another may not have.
		// Treating the kind as uncovered lets the objects it owns surface
		// instead of hiding behind an owner that is never listed.
		warnings = append(warnings, fmt.Sprintf("not listing %s: %s, but the cluster serves %s",
			gk.Kind, coveringKartas(look.resolver, gk), definitions.FormatGVK(served.GroupVersionKind)))
		delete(versions, gk)
	}
	return mappings, warnings, nil
}

// coveringKartas names the Kartas covering gk and the versions they map, as in
// `Karta "stack" covers example.com/v99/WorkloadStack`.
func coveringKartas(resolver *definitions.Resolver, gk schema.GroupKind) string {
	var covers []string
	for _, def := range resolver.List() {
		gvk := catalog.RootKey(def.Karta)
		if gvk.GroupKind() == gk {
			covers = append(covers, fmt.Sprintf("Karta %q covers %s", def.Karta.Name, definitions.FormatGVK(gvk)))
		}
	}
	return strings.Join(covers, " and ")
}

// coveredVersions returns each covered Kind's versions, most stable first, so a
// Kind covered at several versions is listed once.
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

// listKind lists one covered Kind and returns the views of its top-level
// objects that pass --phase.
func listKind(
	ctx context.Context, look lookup, opts *getOptions, mapping *meta.RESTMapping,
	topLevel func(*unstructured.Unstructured) (bool, error),
) ([]workload.View, []string, error) {
	gk := mapping.GroupVersionKind.GroupKind()
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
		views    []workload.View
		warnings []string
	)
	for i := range objects {
		object := &objects[i]
		view, err := workload.Resolve(ctx, object, def)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("could not resolve %s/%s: %v", gk.Kind, object.GetName(), err))
			continue
		}
		// Filtering first spares the owner reads of objects --phase drops.
		if !matchesPhase(view, opts.phases) {
			continue
		}
		top, err := topLevel(object)
		if err != nil {
			// Hiding the object would drop a real workload over a permission
			// error, with nothing to tell the caller.
			warnings = append(warnings, fmt.Sprintf("listing %s/%s, though it may belong to another workload: %v",
				gk.Kind, object.GetName(), err))
		}
		if top {
			views = append(views, *view)
		}
	}
	return views, warnings, nil
}

// typeColumn names a row by Kind, as Kind.group where covered groups share it.
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
