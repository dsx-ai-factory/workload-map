// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package workload

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/metadata"
)

const (
	// maxOwnerDepth bounds the walk, which is what ends a cycle in malformed
	// data. Real chains are two or three hops.
	maxOwnerDepth = 6

	// ownerListChunk pages an owner list, following the kubectl convention.
	ownerListChunk = 500
)

// errChainTooLong reports a walk cut off before the chain ended, so whether an
// owner further up is covered was never checked.
var errChainTooLong = fmt.Errorf("owner chain longer than %d links", maxOwnerDepth)

// OwnerWalker climbs controller owner chains for one metadata list per owner
// kind and namespace. It is not safe for concurrent use.
type OwnerWalker struct {
	client metadata.Interface
	mapper meta.RESTMapper
	// lists keeps failures too, so a broken chain is not re-read once per object.
	lists map[listKey]ownerList
	// named holds the owners read one by one, for a kind the caller may get
	// but not list.
	named map[namedKey]namedOwner
}

type listKey struct {
	resource  schema.GroupVersionResource
	namespace string
}

type ownerList struct {
	byName map[string]*metav1.PartialObjectMetadata
	err    error
}

type namedKey struct {
	listKey
	name string
}

type namedOwner struct {
	owner *metav1.PartialObjectMetadata
	err   error
}

// NewOwnerWalker returns a walker reading owners through client.
func NewOwnerWalker(client metadata.Interface, mapper meta.RESTMapper) *OwnerWalker {
	return &OwnerWalker{
		client: client, mapper: mapper,
		lists: map[listKey]ownerList{}, named: map[namedKey]namedOwner{},
	}
}

// TopLevel reports whether no object in obj's controller chain is of a covered
// kind. An error means the chain could not be walked to its end.
func (w *OwnerWalker) TopLevel(
	ctx context.Context, obj *unstructured.Unstructured, covered func(schema.GroupKind) bool,
) (bool, error) {
	nested, err := w.climb(ctx, obj.GetOwnerReferences(), obj.GetNamespace(),
		func(refs []metav1.OwnerReference) bool {
			controller := controllerRef(refs)
			return controller != nil &&
				covered(schema.FromAPIVersionAndKind(controller.APIVersion, controller.Kind).GroupKind())
		})
	return !nested, err
}

// climb reports whether match accepts the owner references at any level of the
// controller chain from refs up. A missing owner ends the chain.
func (w *OwnerWalker) climb(
	ctx context.Context, refs []metav1.OwnerReference, namespace string,
	match func([]metav1.OwnerReference) bool,
) (bool, error) {
	for depth := 0; ; depth++ {
		if match(refs) {
			return true, nil
		}
		controller := controllerRef(refs)
		if controller == nil {
			return false, nil
		}
		if depth == maxOwnerDepth {
			return false, errChainTooLong
		}
		owner, err := w.get(ctx, *controller, namespace)
		if err != nil || owner == nil {
			return false, err
		}
		refs = owner.GetOwnerReferences()
	}
}

// controllerRef returns the single owner reference with Controller set, the
// only one an owner chain can be walked through unambiguously.
func controllerRef(refs []metav1.OwnerReference) *metav1.OwnerReference {
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return &refs[i]
		}
	}
	return nil
}

// get returns the owner ref points to, or nil when it is gone or was replaced
// by a same-name object with another UID.
func (w *OwnerWalker) get(
	ctx context.Context, ref metav1.OwnerReference, namespace string,
) (*metav1.PartialObjectMetadata, error) {
	mapping, err := w.restMapping(ref)
	if err != nil {
		return nil, err
	}

	// A cluster-scoped owner is not addressed by namespace, and a namespaced
	// request for one is a 404 that would end the chain early.
	if mapping.Scope.Name() != meta.RESTScopeNameNamespace {
		namespace = ""
	}
	client := w.client.Resource(mapping.Resource).Namespace(namespace)
	key := listKey{resource: mapping.Resource, namespace: namespace}
	owner, err := w.listed(ctx, client, key, ref.Name)

	// A role can grant get on an owner kind without list, so read the owner by
	// name rather than lose the chain.
	if apierrors.IsForbidden(err) {
		listErr := err
		owner, err = w.getNamed(ctx, client, namedKey{key, ref.Name})
		// Report the kind-wide denial, worded the same for every owner, so
		// callers can group it.
		if apierrors.IsForbidden(err) {
			err = listErr
		}
	}
	if owner == nil || owner.UID != ref.UID {
		return nil, err
	}
	return owner, nil
}

// restMapping maps the kind ref names to the resource to read it from.
func (w *OwnerWalker) restMapping(ref metav1.OwnerReference) (*meta.RESTMapping, error) {
	gv, err := schema.ParseGroupVersion(ref.APIVersion)
	if err != nil {
		return nil, fmt.Errorf("parse owner apiVersion %q: %w", ref.APIVersion, err)
	}
	gvk := gv.WithKind(ref.Kind)
	mapping, err := w.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if meta.IsNoMatchError(err) {
		// The reference may name a version the CRD no longer serves, and a
		// metadata read is the same at any served version.
		mapping, err = w.mapper.RESTMapping(gvk.GroupKind())
	}
	if err != nil {
		return nil, fmt.Errorf("discover %s: %w", gvk.Kind, err)
	}
	return mapping, nil
}

// listed returns the owner called name from the list of key, reading the list
// once per key.
func (w *OwnerWalker) listed(
	ctx context.Context, client metadata.ResourceInterface, key listKey, name string,
) (*metav1.PartialObjectMetadata, error) {
	owners, cached := w.lists[key]
	if !cached {
		owners.byName, owners.err = listOwners(ctx, client)
		w.lists[key] = owners
	}
	return owners.byName[name], owners.err
}

// getNamed reads one owner by name, or nil when it no longer exists. The result
// is cached, failures too, so sibling objects share the read.
func (w *OwnerWalker) getNamed(
	ctx context.Context, client metadata.ResourceInterface, key namedKey,
) (*metav1.PartialObjectMetadata, error) {
	named, cached := w.named[key]
	if !cached {
		named.owner, named.err = client.Get(ctx, key.name, metav1.GetOptions{})
		if apierrors.IsNotFound(named.err) {
			named = namedOwner{}
		}
		w.named[key] = named
	}
	return named.owner, named.err
}

// listOwners reads every object of one kind in one namespace, indexed by name.
func listOwners(ctx context.Context, client metadata.ResourceInterface) (map[string]*metav1.PartialObjectMetadata, error) {
	owners := map[string]*metav1.PartialObjectMetadata{}
	options := metav1.ListOptions{Limit: ownerListChunk}
	for {
		page, err := client.List(ctx, options)
		if err != nil {
			return nil, err
		}
		for i := range page.Items {
			owners[page.Items[i].Name] = &page.Items[i]
		}
		options.Continue = page.Continue
		if options.Continue == "" {
			return owners, nil
		}
	}
}

// WalkFailures groups the objects whose owner chain could not be walked by the
// error that stopped each walk, in the order the causes were first met.
type WalkFailures struct {
	causes  []string
	objects map[string][]string
}

// Add records object under the cause err names.
func (f *WalkFailures) Add(err error, object string) {
	cause := err.Error()
	if f.objects == nil {
		f.objects = map[string][]string{}
	}
	if _, seen := f.objects[cause]; !seen {
		f.causes = append(f.causes, cause)
	}
	f.objects[cause] = append(f.objects[cause], object)
}

// Warnings renders one warning per cause.
func (f *WalkFailures) Warnings(warning func(cause string, objects []string) string) []string {
	warnings := make([]string, 0, len(f.causes))
	for _, cause := range f.causes {
		warnings = append(warnings, warning(cause, f.objects[cause]))
	}
	return warnings
}
