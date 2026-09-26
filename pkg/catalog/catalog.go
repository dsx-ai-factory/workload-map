// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package catalog exposes the immutable catalog of built-in Karta definitions,
// keyed by the root component's workload GVK. The catalog is built once from the
// typed Go definitions in pkg/catalog/kartas and never mutated, so callers can
// resolve a built-in Karta for a workload GVK with no cluster access.
package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	yamlv3 "go.yaml.in/yaml/v3"
	"k8s.io/apimachinery/pkg/runtime/schema"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/catalog/kartas"
)

// yamlHeader is prepended to every generated catalog file.
const yamlHeader = "# SPDX-License-Identifier: Apache-2.0\n# Copyright (c) 2026 NVIDIA Corporation\n"

// ErrNotFound is returned by Get when no built-in Karta is registered for a GVK.
var ErrNotFound = errors.New("catalog: no Karta registered for GVK")

// ErrInvalidGVK wraps failures to derive a root-component GVK from a Karta.
var ErrInvalidGVK = errors.New("catalog: root component GVK incomplete")

// definitions is the single wiring point for the catalog supported workloads.
var definitions = []func() *v1alpha1.Karta{
	kartas.BatchJob,
	kartas.CronJob,
	kartas.Deployment,
	kartas.StatefulSet,
	kartas.Pod,
	kartas.Jobset,
	kartas.Pytorch,
	kartas.Mpijob,
	kartas.Raycluster,
	kartas.Rayjob,
	kartas.RayService,
	kartas.LWS,
	kartas.KServe,
	kartas.LLMInferenceService,
	kartas.LLMInferenceServiceV1alpha2,
	kartas.KnativeServing,
	kartas.NIMService,
	kartas.NIMCache,
	kartas.Milvus,
	kartas.Dynamo,
	kartas.DynamoV1beta1,
	kartas.GrovePodCliqueSet,
}

// Catalog is an immutable set of built-in Kartas indexed by their root component GVK.
type Catalog struct {
	byGVK map[schema.GroupVersionKind]*v1alpha1.Karta
}

// New builds the catalog from the definitions list. It panics in case of catalog
// violation, which can only happen at package init since definitions is fixed at
// compile time.
func New() *Catalog {
	c, err := newCatalog(definitions)
	if err != nil {
		panic(fmt.Sprintf("catalog: %v", err))
	}
	return c
}

// newCatalog builds a catalog from an explicit definitions list. It reports a
// violation as an error so tests can assert on it without recover; New turns that
// error into the package's single panic.
func newCatalog(defs []func() *v1alpha1.Karta) (*Catalog, error) {
	c := &Catalog{
		byGVK: make(map[schema.GroupVersionKind]*v1alpha1.Karta, len(defs)),
	}
	for _, def := range defs {
		karta := def()
		root := karta.Spec.StructureDefinition.RootComponent
		// Group may be empty (core workloads such as Pod), but Version and Kind are required.
		if root.Kind == nil || root.Kind.Version == "" || root.Kind.Kind == "" {
			return nil, fmt.Errorf("%w: Karta %q", ErrInvalidGVK, karta.Name)
		}
		gvk := RootKey(karta)
		if existing, ok := c.byGVK[gvk]; ok {
			return nil, fmt.Errorf("duplicate GVK %s defined by %q and %q", gvk, existing.Name, karta.Name)
		}
		c.byGVK[gvk] = karta
	}
	return c, nil
}

// RootKey returns the workload GVK of a Karta's root component.
func RootKey(k *v1alpha1.Karta) schema.GroupVersionKind {
	kind := k.Spec.StructureDefinition.RootComponent.Kind
	if kind == nil {
		return schema.GroupVersionKind{}
	}
	return schema.GroupVersionKind{Group: kind.Group, Version: kind.Version, Kind: kind.Kind}
}

// Get returns the built-in Karta whose root component matches gvk. The returned
// Karta is a deep copy, so callers may mutate it freely without affecting the
// immutable catalog.
func (c *Catalog) Get(gvk schema.GroupVersionKind) (*v1alpha1.Karta, error) {
	if k, ok := c.byGVK[gvk]; ok {
		return k.DeepCopy(), nil
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, gvk)
}

// List returns all built-in Kartas sorted by GVK. Each entry is a deep copy, so
// callers may mutate the slice or the Kartas without affecting the immutable
// catalog.
func (c *Catalog) List() []*v1alpha1.Karta {
	keys := slices.SortedFunc(maps.Keys(c.byGVK), func(a, b schema.GroupVersionKind) int {
		return strings.Compare(a.String(), b.String())
	})
	out := make([]*v1alpha1.Karta, 0, len(keys))
	for _, gvk := range keys {
		out = append(out, c.byGVK[gvk].DeepCopy())
	}
	return out
}

// defaultCatalog is the package-global instance callers use directly.
var defaultCatalog = New()

// Get resolves a built-in Karta for gvk from the default catalog.
func Get(gvk schema.GroupVersionKind) (*v1alpha1.Karta, error) { return defaultCatalog.Get(gvk) }

// List returns all built-in Kartas from the default catalog, sorted by GVK.
func List() []*v1alpha1.Karta { return defaultCatalog.List() }

// Slug returns the GVK-derived filename stem for a Karta, of the form
// {group-slug}-{kind-lowercase}-{version}. The core (empty) group maps to
// "core" so Pod becomes "core-pod-v1".
func Slug(k *v1alpha1.Karta) (string, error) {
	gvk := RootKey(k)
	if gvk.Version == "" || gvk.Kind == "" {
		return "", fmt.Errorf("%w: Karta %q", ErrInvalidGVK, k.Name)
	}
	group := gvk.Group
	if group == "" {
		group = "core"
	}
	return strings.ReplaceAll(group, ".", "-") + "-" + strings.ToLower(gvk.Kind) + "-" + gvk.Version, nil
}

// MarshalYAML renders a Karta into YAML with required header, keeping fields in
// Go struct declaration order. The catalog files are pure definitions, so the
// empty generated status field is dropped.
func MarshalYAML(k *v1alpha1.Karta) ([]byte, error) {
	// Rendering through JSON is what keeps the field order. The API types carry
	// only json tags, so a YAML encoder cannot marshal them directly (it would
	// lowercase field names), and sigs.k8s.io/yaml converts through a Go map,
	// which sorts keys alphabetically. encoding/json honors the tags and walks
	// fields in declaration order; the JSON is then re-encoded as YAML through a
	// yaml.Node, which keeps that order because a Node stores mapping keys in a
	// slice rather than a map.
	jsonBytes, err := json.Marshal(k)
	if err != nil {
		return nil, fmt.Errorf("marshal Karta %q: %w", k.Name, err)
	}
	var node yamlv3.Node
	if err := yamlv3.Unmarshal(jsonBytes, &node); err != nil {
		return nil, fmt.Errorf("re-parse Karta %q: %w", k.Name, err)
	}
	clearStyle(&node)
	dropMapKey(node.Content[0], "status")
	var buf bytes.Buffer
	buf.WriteString(yamlHeader)
	enc := yamlv3.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&node); err != nil {
		return nil, fmt.Errorf("encode Karta %q: %w", k.Name, err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode Karta %q: %w", k.Name, err)
	}
	return buf.Bytes(), nil
}

// dropMapKey removes key and its value from a mapping node.
func dropMapKey(mapping *yamlv3.Node, key string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

// clearStyle strips per-node style recursively so JSON flow syntax ({...}, "...")
// re-renders as block-style YAML.
func clearStyle(n *yamlv3.Node) {
	// The JSON parse stamps each node with flow style (the {...} JSON form).
	// Converting to block style, the zero value of Style, makes the encoder
	// render indented YAML instead.
	n.Style = 0
	for _, c := range n.Content {
		clearStyle(c)
	}
}
