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

	// Listing every kind first settles the covered set, so a kind left out
	// never hides what it owns.
	mappings, warnings, err := discoverKinds(look, versions, kinds)
	if err != nil {
		return nil, warnings, err
	}
	var listed []*listedKind
	for _, gk := range kinds {
		mapping, ok := mappings[gk]
		if !ok {
			continue
		}
		kind, warning, err := listKind(ctx, look, opts, mapping)
		switch {
		case err != nil:
			return nil, warnings, err
		case kind == nil:
			if warning != "" {
				warnings = append(warnings, warning)
			}
			delete(versions, gk)
		default:
			listed = append(listed, kind)
		}
	}

	walker := workload.NewOwnerWalker(look.metadata, look.mapper)
	covered := func(gk schema.GroupKind) bool {
		_, ok := versions[gk]
		return ok
	}

	// Grouping by cause keeps one unreadable owner kind to one warning.
	var (
		views      []workload.View
		causes     []string
		unanswered = map[string][]string{}
	)
	for _, kind := range listed {
		for i := range kind.objects {
			object := &kind.objects[i]
			// Walking first drops nested objects before the costlier resolve.
			top, walkErr := walker.TopLevel(ctx, object, covered)
			if !top {
				continue
			}
			view, err := workload.Resolve(ctx, object, kind.def)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("could not resolve %s/%s: %v", kind.gk.Kind, object.GetName(), err))
				continue
			}
			if !matchesPhase(view, opts.phases) {
				continue
			}
			if walkErr != nil {
				// Hiding the object would drop a real workload over a permission
				// error, with nothing to tell the caller.
				cause := walkErr.Error()
				if _, seen := unanswered[cause]; !seen {
					causes = append(causes, cause)
				}
				unanswered[cause] = append(unanswered[cause], kind.gk.Kind+"/"+object.GetName())
			}
			views = append(views, *view)
		}
	}
	for _, cause := range causes {
		objects := unanswered[cause]
		if len(objects) == 1 {
			warnings = append(warnings, fmt.Sprintf("listing %s, though it may belong to another workload: %s",
				objects[0], cause))
			continue
		}
		warnings = append(warnings, fmt.Sprintf("listing %s and %d more, though they may belong to another workload: %s",
			objects[0], len(objects)-1, cause))
	}
	return views, warnings, nil
}

// discoverKinds maps each covered Kind to its served version. A Kind it cannot
// map, or finds only at an uncovered version, leaves versions, the latter with a warning.
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
			// Absent types are the norm, so skipping one is silent.
			delete(versions, gk)
			continue
		case err != nil:
			return nil, warnings, fmt.Errorf("discover %s: %w", gk.Kind, err)
		}
		// A Karta maps one version's fields, which another version may lack.
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

// listedKind is one covered Kind's objects, with the definition that reads them.
type listedKind struct {
	gk      schema.GroupKind
	def     definitions.Definition
	objects []unstructured.Unstructured
}

// listKind lists one covered Kind. A nil result leaves it out, with a warning
// unless the server just stopped serving it.
func listKind(
	ctx context.Context, look lookup, opts *getOptions, mapping *meta.RESTMapping,
) (*listedKind, string, error) {
	gk := mapping.GroupVersionKind.GroupKind()
	def, err := look.resolver.Resolve(mapping.GroupVersionKind)
	if err != nil {
		return nil, fmt.Sprintf("not listing %s: %v", gk.Kind, err), nil
	}

	namespace := look.namespace
	if mapping.Scope.Name() == meta.RESTScopeNameRoot {
		namespace = ""
	}
	objects, err := list(ctx, look.dyn, mapping, namespace, opts)
	switch {
	case err == nil:
		return &listedKind{gk: gk, def: def, objects: objects}, "", nil
	case apierrors.IsForbidden(err):
		return nil, fmt.Sprintf("not allowed to list %s, so the result leaves it out", gk.Kind), nil
	case apierrors.IsNotFound(err):
		// A cached discovery can outlive an uninstalled CRD.
		return nil, "", nil
	default:
		return nil, "", fmt.Errorf("list %s: %w", gk.Kind, err)
	}
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
