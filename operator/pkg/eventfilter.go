// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package pkg

import (
	"context"
	"fmt"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// MapCRDToKartaEvent maps a CRD event to reconcile requests for every Karta
// whose root component references the same group and kind as that CRD,
// regardless of version.
func (r *Reconciler) MapCRDToKartaEvent(ctx context.Context, obj client.Object) []reconcile.Request {
	logger := log.FromContext(ctx)

	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		logger.Error(fmt.Errorf("unexpected type %T", obj), "expected CustomResourceDefinition")
		return nil
	}

	kartas := &kartav1alpha1.KartaList{}
	if err := r.List(ctx, kartas, client.MatchingLabels{
		kartav1alpha1.LabelRootGroup: crd.Spec.Group,
		kartav1alpha1.LabelRootKind:  crd.Spec.Names.Kind,
	}); err != nil {
		logger.Error(err, "Failed to list Kartas for CRD event", "crd", crd.Name)
		return nil
	}

	requests := make([]reconcile.Request, 0, len(kartas.Items))
	for i := range kartas.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKey{Name: kartas.Items[i].Name},
		})
	}
	return requests
}
