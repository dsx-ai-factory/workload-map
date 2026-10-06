// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package workload

import (
	"context"
	"fmt"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// maxOwnerDepth bounds the walk, which is what ends a cycle in malformed data.
// Real chains are two or three hops.
const maxOwnerDepth = 6

// OwnerWalker climbs controller owner-reference chains, fetching each owner at
// most once however many objects share it. It is safe for concurrent use.
type OwnerWalker struct {
	dyn    dynamic.Interface
	mapper meta.RESTMapper

	mu    sync.Mutex
	cache map[ownerKey]*ownerEntry
}

type ownerKey struct {
	gvk       schema.GroupVersionKind
	namespace string
	name      string
}

// ownerEntry is one owner fetch. ready closes once obj and err are set, so a
// walk reaching an owner already in flight waits for that fetch rather than
// issuing its own.
type ownerEntry struct {
	ready chan struct{}
	obj   *unstructured.Unstructured
	err   error
}

func NewOwnerWalker(dyn dynamic.Interface, mapper meta.RESTMapper) *OwnerWalker {
	return &OwnerWalker{dyn: dyn, mapper: mapper, cache: map[ownerKey]*ownerEntry{}}
}

// TopLevel reports whether no object in obj's controller chain is of a covered
// kind. A covered controller is recognized from the owner reference alone, so
// only an uncovered one costs a fetch. An owner that no longer exists ends the
// chain; one that cannot be read leaves the question open and is returned as
// the error.
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

// climb walks the controller chain up from refs until match accepts the owner
// references of one level. A chain that ends, runs past maxOwnerDepth or
// reaches an owner that no longer exists is no match.
func (w *OwnerWalker) climb(
	ctx context.Context, refs []metav1.OwnerReference, namespace string,
	match func([]metav1.OwnerReference) bool,
) (bool, error) {
	for range maxOwnerDepth {
		if match(refs) {
			return true, nil
		}
		controller := controllerRef(refs)
		if controller == nil {
			return false, nil
		}
		owner, err := w.get(ctx, *controller, namespace)
		switch {
		case apierrors.IsNotFound(err):
			return false, nil
		case err != nil:
			return false, err
		}
		refs = owner.GetOwnerReferences()
	}
	return false, nil
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

func (w *OwnerWalker) get(
	ctx context.Context, ref metav1.OwnerReference, namespace string,
) (*unstructured.Unstructured, error) {
	gv, err := schema.ParseGroupVersion(ref.APIVersion)
	if err != nil {
		return nil, fmt.Errorf("parse owner apiVersion %q: %w", ref.APIVersion, err)
	}
	gvk := gv.WithKind(ref.Kind)
	mapping, err := w.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, fmt.Errorf("discover %s: %w", gvk.Kind, err)
	}

	// A cluster-scoped owner is not addressed by namespace, and a namespaced
	// request for one is a 404 that would end the chain early.
	client := dynamic.ResourceInterface(w.dyn.Resource(mapping.Resource))
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		client = w.dyn.Resource(mapping.Resource).Namespace(namespace)
	} else {
		namespace = ""
	}

	key := ownerKey{gvk: gvk, namespace: namespace, name: ref.Name}
	w.mu.Lock()
	entry, cached := w.cache[key]
	if !cached {
		entry = &ownerEntry{ready: make(chan struct{})}
		w.cache[key] = entry
	}
	w.mu.Unlock()

	if cached {
		<-entry.ready
		return entry.obj, entry.err
	}
	// The miss is cached too, so a broken chain is not re-fetched once per object.
	entry.obj, entry.err = client.Get(ctx, ref.Name, metav1.GetOptions{})
	close(entry.ready)
	return entry.obj, entry.err
}
