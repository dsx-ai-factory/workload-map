// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package discovery

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

type discovery struct {
	reader  ObjectReader
	ignored map[schema.GroupVersionKind]bool
}

// Config controls which owner types end traversal before the owner is read.
type Config struct {
	IgnoredTopOwners []schema.GroupVersionKind
}

// ErrReaderRequired indicates that no object reader was provided.
var ErrReaderRequired = errors.New("reader required")

// New constructs an owner resolver. The reader must be safe for concurrent use.
func New(reader ObjectReader, config Config) (Discoverer, error) {
	if reader == nil {
		return nil, ErrReaderRequired
	}
	d := &discovery{reader: reader, ignored: make(map[schema.GroupVersionKind]bool)}
	for _, gvk := range config.IgnoredTopOwners {
		d.ignored[gvk] = true
	}
	return d, nil
}

func (d *discovery) Get(ctx context.Context, ref ObjectRef) (Result, error) {
	workload := &GenericWorkload{Resolution: Resolved, Path: make([]ObjectRef, 0, 3)}
	seen := make(map[types.UID]struct{})
	for {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if _, exists := seen[ref.UID]; exists {
			return Result{}, fmt.Errorf("%w: ownership cycle at %s", ErrUnresolvedOwner, ref.Name)
		}
		if len(seen) >= 1024 {
			return Result{}, fmt.Errorf("%w: owner chain exceeds limit", ErrUnresolvedOwner)
		}
		var object metav1.Object
		if cached, ok := d.reader.Cached(ref); ok && cached != nil {
			object = cached
		} else {
			metadata, err := d.reader.GetMetadata(ctx, ref)
			if err != nil {
				return Result{}, readError(err)
			}
			if metadata == nil {
				return Result{}, fmt.Errorf("%w: empty metadata", ErrUnavailable)
			}
			object = metadata
		}
		uid, namespace, version := object.GetUID(), object.GetNamespace(), object.GetResourceVersion()
		// The reader resolves API scope; a cluster-scoped owner has no namespace.
		if len(workload.Path) > 0 && namespace == "" {
			ref.Namespace = ""
		}
		if ref.Name != object.GetName() || ref.Namespace != namespace || uid == "" || (ref.UID != "" && ref.UID != uid) {
			return Result{}, fmt.Errorf("%w: object identity changed", ErrStale)
		}
		if ref.ResourceVersion != "" && ref.ResourceVersion != version {
			return Result{}, fmt.Errorf("%w: resource version changed", ErrStale)
		}
		ref.UID = uid
		ref.ResourceVersion = version
		seen[uid] = struct{}{}
		workload.Path = append(workload.Path, ref)
		owner, valid := ControllerOwner(object)
		if !valid {
			return Result{}, fmt.Errorf("%w: invalid controller reference", ErrUnresolvedOwner)
		}
		if owner.UID == "" {
			break
		}
		if d.ignored[owner.GVK] {
			workload.Resolution = IgnoredOwner
			break
		}
		owner.Namespace = ref.Namespace
		ref = owner
	}
	workload.Root = ref
	return Result{Workload: workload}, nil
}

// ControllerOwner returns the controller reference, or an empty reference if
// there is none. Malformed or multiple controller references return false.
func ControllerOwner(object metav1.Object) (ObjectRef, bool) {
	var ref ObjectRef
	for _, owner := range object.GetOwnerReferences() {
		if owner.Controller == nil || !*owner.Controller {
			continue
		}
		gv, err := schema.ParseGroupVersion(owner.APIVersion)
		if ref.UID != "" || err != nil || gv.Version == "" || owner.Name == "" || owner.Kind == "" || owner.UID == "" {
			return ObjectRef{}, false
		}
		ref = ObjectRef{GVK: gv.WithKind(owner.Kind), Name: owner.Name, UID: owner.UID}
	}
	return ref, true
}

func readError(err error) error {
	switch {
	case errors.Is(err, ErrPending):
		return fmt.Errorf("%w: %w", ErrPending, err)
	case errors.Is(err, ErrStale):
		return err
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err), errors.Is(err, ErrPermissionDenied):
		return fmt.Errorf("%w: %w", ErrPermissionDenied, err)
	case apierrors.IsNotFound(err), errors.Is(err, ErrUnresolvedOwner):
		return fmt.Errorf("%w: %w", ErrUnresolvedOwner, err)
	default:
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
}
