// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package definitions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/catalog"
)

var GVR = schema.GroupVersionResource{Group: "run.ai", Version: "v1alpha1", Resource: "kartas"}

type Warning struct {
	Reason  string
	Message string
}

const (
	ReasonCollision   = "collision"
	ReasonNoCRD       = "no_crd"
	ReasonForbidden   = "forbidden"
	ReasonUnreachable = "unreachable"
	ReasonInvalid     = "invalid"
)

// loader accumulates the warnings one cluster read produces.
type loader struct {
	warnings []Warning
}

func (l *loader) warn(reason, format string, args ...any) {
	l.warnings = append(l.warnings, Warning{Reason: reason, Message: fmt.Sprintf(format, args...)})
}

// Load builds the resolver every CLI command looks definitions up in. It seeds
// the built-in catalog and then best-effort merges the cluster's Karta
// definitions on top.
func Load(ctx context.Context, rcg genericclioptions.RESTClientGetter) (*Resolver, []Warning) {
	l := &loader{}
	cluster, err := l.fetchCluster(ctx, rcg)
	if err != nil {
		if w, ok := classify(err); ok {
			l.warnings = append(l.warnings, w)
		}
		cluster = nil
	}

	resolver := New(catalog.List(), cluster)
	for _, c := range resolver.Collisions() {
		names := make([]string, 0, len(c.Names))
		for _, name := range c.Names {
			names = append(names, fmt.Sprintf("%q", name))
		}
		l.warn(ReasonCollision, "%d cluster Karta definitions claim %s: %s",
			len(c.Names), c.GVK, strings.Join(names, ", "))
	}
	return resolver, l.warnings
}

// classify turns a cluster read failure into either silence or a warning.
func classify(err error) (Warning, bool) {
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if clientcmd.IsEmptyConfig(cause) {
			return Warning{}, false
		}
	}

	switch {
	case apierrors.IsNotFound(err):
		return Warning{
			Reason:  ReasonNoCRD,
			Message: "the kartas.run.ai CRD is not installed; showing built-in definitions only",
		}, true
	case apierrors.IsForbidden(err):
		return Warning{
			Reason: ReasonForbidden,
			Message: "not allowed to list kartas.run.ai; showing built-in definitions only. " +
				"Ask a cluster administrator for \"list\" permission on kartas.run.ai to include cluster definitions.",
		}, true
	default:
		return Warning{
			Reason: ReasonUnreachable,
			Message: strings.Join(strings.Fields(
				fmt.Sprintf("could not read Karta definitions from the cluster: %v; showing built-in definitions only", err)), " "),
		}, true
	}
}

// fetchCluster reads the Karta definitions installed in the cluster that rcg
// points at. An empty cluster yields an empty result and no error.
func (l *loader) fetchCluster(ctx context.Context, rcg genericclioptions.RESTClientGetter) ([]*v1alpha1.Karta, error) {
	cfg, err := rcg.ToRESTConfig()
	if err != nil {
		return nil, fmt.Errorf("kubernetes config: %w", err)
	}

	// Per-config: the global handler would leak to every other client, and its
	// output would land in the middle of machine-readable stderr.
	cfg.WarningHandler = rest.NoWarnings{}

	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes dynamic client: %w", err)
	}
	return l.listWithClient(ctx, dyn)
}

// listWithClient reads every Karta in the cluster through a dynamic client.
// Kartas are cluster-scoped, so the request carries no namespace and no selector.
func (l *loader) listWithClient(ctx context.Context, dyn dynamic.Interface) ([]*v1alpha1.Karta, error) {
	list, err := dyn.Resource(GVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list Karta definitions: %w", err)
	}

	kartas := make([]*v1alpha1.Karta, 0, len(list.Items))
	for i := range list.Items {
		var karta v1alpha1.Karta
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(list.Items[i].Object, &karta); err != nil {
			// One definition this build cannot read must not cost the user the
			// ones it can. Reached through CRD schema skew, not bad input.
			l.warn(ReasonInvalid, "skipping Karta definition %q: %v", list.Items[i].GetName(), err)
			continue
		}
		kartas = append(kartas, &karta)
	}
	return kartas, nil
}
