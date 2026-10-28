// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartadefinitioninventory

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

// Reader looks up the selected Karta definition for a workload type.
type Reader interface {
	Lookup(schema.GroupVersionKind) (Selection, error)
}

// Selection pairs a definition with the source it came from.
type Selection struct {
	Definition *v1alpha1.Karta
	GVK        schema.GroupVersionKind
	Origin     Origin
}

// Origin identifies the definition. Catalog entries have no UID or resource version.
type Origin struct {
	Source          Source
	Name            string
	UID             types.UID
	ResourceVersion string
}

type Source string

const (
	SourceCatalog Source = "catalog"
	SourceCluster Source = "cluster"
)

// Config selects definitions before they enter the lookup caches.
type Config struct {
	Catalog CatalogConfig
	Cluster ClusterConfig
}

type CatalogConfig struct {
	Enabled bool
	Include []string
	Exclude []string
}

type ClusterConfig struct {
	Enabled       bool
	Include       []string
	Exclude       []string
	LabelSelector *metav1.LabelSelector
}
