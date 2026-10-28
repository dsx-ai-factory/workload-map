// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package discovery

import (
	"context"
	"errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	inventory "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/kartadefinitioninventory"
)

// Discoverer resolves an object to its top owner without starting watches.
type Discoverer interface {
	Get(context.Context, ObjectRef) (Result, error)
}

// Result includes the resolved workload and root watches the caller may need.
type Result struct {
	Workload        *GenericWorkload
	MissingCoverage []WatchRequest
}

// GenericWorkload describes ownership independently of a Karta definition.
type GenericWorkload struct {
	Root       ObjectRef
	Path       []ObjectRef // Input object first, root last.
	Resolution OwnerResolution
}

type ObjectRef struct {
	GVK             schema.GroupVersionKind
	Namespace       string
	Name            string
	UID             types.UID
	ResourceVersion string
}

type WatchRequest struct {
	GVK       schema.GroupVersionKind
	Namespace string
}

type OwnerResolution string

const (
	Resolved     OwnerResolution = "resolved"
	IgnoredOwner OwnerResolution = "ignored_owner"
	UnknownOwner OwnerResolution = "unknown"
)

// ObjectReader reads cached objects and fetches metadata on a cache miss.
type ObjectReader interface {
	Cached(ObjectRef) (*unstructured.Unstructured, bool)
	GetMetadata(context.Context, ObjectRef) (*metav1.PartialObjectMetadata, error)
}

type Dependencies struct {
	Reader    ObjectReader
	Inventory inventory.Reader
}

var (
	ErrPending           = errors.New("discovery inputs not synchronized")
	ErrPermissionDenied  = errors.New("discovery read denied")
	ErrUnavailable       = errors.New("discovery source unavailable")
	ErrUnresolvedOwner   = errors.New("owner chain unresolved")
	ErrInvalidDefinition = errors.New("invalid Karta definition")
	ErrEvaluation        = errors.New("karta evaluation failed")
	ErrStale             = errors.New("discovery inputs changed")
)
