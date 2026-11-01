// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/metadata"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/discovery"
	inventory "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

// Reader exposes the accepted shared collection caches to ownership discovery.
// Cache reads share the controller state lock; mapper reset is independent.
type Reader struct {
	*objectCache
	clients         Clients
	options         ReaderOptions
	ensure          func(ctx context.Context, gvk schema.GroupVersionKind, namespace string, metadataOnly bool) (watchKey, error)
	mapperMu        sync.Mutex
	lastMapperReset time.Time
}
type Clients struct {
	Dynamic  dynamic.Interface
	Metadata metadata.Interface
	Mapper   meta.RESTMapper
}
type ReaderOptions struct{ RequestTimeout time.Duration }
type objectKey struct {
	GVK             schema.GroupVersionKind
	Namespace, Name string
}
type cachedObject struct {
	object *unstructured.Unstructured
	source watchKey
}
type cacheSource struct {
	gvk                   schema.GroupVersionKind
	hasFullObject, synced bool
	err                   error
}

func NewReader(clients Clients, options ReaderOptions) (*Reader, error) {
	if clients.Dynamic == nil || clients.Metadata == nil || clients.Mapper == nil {
		return nil, errors.New("dynamic client, metadata client and REST mapper are required")
	}
	if options.RequestTimeout == 0 {
		options.RequestTimeout = 30 * time.Second
	}
	if options.RequestTimeout < 0 {
		return nil, errors.New("request timeout must be positive")
	}
	return &Reader{clients: clients, options: options, objectCache: &objectCache{objects: map[objectKey]cachedObject{}, sources: map[watchKey]cacheSource{}}}, nil
}

func (r *Reader) Cached(ref discovery.ObjectRef) (*unstructured.Unstructured, bool) {
	object, _, err := r.read(ref, false)
	return object, err == nil
}

func (r *Reader) GetMetadata(ctx context.Context, ref discovery.ObjectRef) (*metav1.PartialObjectMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.ensure == nil {
		return nil, discovery.ErrPending
	}
	if _, err := r.ensure(ctx, ref.GVK, ref.Namespace, true); err != nil {
		return nil, err
	}
	object, _, err := r.read(ref, false)
	if err != nil {
		return nil, err
	}
	result := &metav1.PartialObjectMetadata{}
	if err := kruntime.DefaultUnstructuredConverter.FromUnstructured(object.Object, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Reader) read(ref discovery.ObjectRef, requireFullObject bool) (*unstructured.Unstructured, watchKey, error) {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.lookupObject(ref, requireFullObject)
}

func (r *Reader) restMapping(ctx context.Context, gvk schema.GroupVersionKind) (*meta.RESTMapping, error) {
	requestCtx, cancel := context.WithTimeout(ctx, r.options.RequestTimeout)
	defer cancel()
	if err := requestCtx.Err(); err != nil {
		return nil, err
	}
	mapping, err := meta.ToRESTMapperWithContext(r.clients.Mapper).RESTMappingWithContext(requestCtx, gvk.GroupKind(), gvk.Version)
	if meta.IsNoMatchError(err) {
		r.resetMapperIfNeeded(requestCtx)
	}
	if err != nil {
		return nil, fmt.Errorf("map %s: %w", gvk, err)
	}
	return mapping, nil
}

func (r *Reader) resetMapperIfNeeded(ctx context.Context) {
	r.mapperMu.Lock()
	defer r.mapperMu.Unlock()
	if time.Since(r.lastMapperReset) < 45*time.Second {
		return
	}
	if mapper, ok := r.clients.Mapper.(meta.ResettableRESTMapper); ok {
		meta.ToResettableRESTMapperWithContext(mapper).ResetWithContext(ctx)
		r.lastMapperReset = time.Now()
	}
}

func keyOf(ref discovery.ObjectRef) objectKey {
	return objectKey{GVK: ref.GVK, Namespace: ref.Namespace, Name: ref.Name}
}
func refOf(object *unstructured.Unstructured) discovery.ObjectRef {
	return discovery.ObjectRef{GVK: object.GroupVersionKind(), Namespace: object.GetNamespace(), Name: object.GetName(), UID: object.GetUID(), ResourceVersion: object.GetResourceVersion()}
}
func storedRef(ref discovery.ObjectRef) store.ObjectRef {
	return store.ObjectRef{GVK: ref.GVK, Namespace: ref.Namespace, Name: ref.Name, UID: ref.UID, ResourceVersion: ref.ResourceVersion}
}
func failureReason(err error) string {
	switch {
	case err == nil:
		return ""
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err), errors.Is(err, discovery.ErrPermissionDenied), errors.Is(err, inventory.ErrPermissionDenied):
		return store.ReasonPermissionDenied
	case errors.Is(err, discovery.ErrUnresolvedOwner), apierrors.IsNotFound(err):
		return store.ReasonUnresolvedOwner
	case errors.Is(err, discovery.ErrInvalidDefinition), errors.Is(err, inventory.ErrInvalidDefinition):
		return store.ReasonInvalidDefinition
	case errors.Is(err, discovery.ErrEvaluation):
		return store.ReasonEvaluationFailed
	default:
		return store.ReasonAPIUnavailable
	}
}
