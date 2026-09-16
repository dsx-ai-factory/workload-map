// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package pkg

import (
	"context"
	stderrors "errors"
	"fmt"
	"slices"
	"strings"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	ControllerName = "karta-controller"
	// crdGroupKindIndexKey indexes CustomResourceDefinitions by group and kind so
	// the reconciler can locate the CRD for a Karta root GVK directly.
	crdGroupKindIndexKey = "spec.group+spec.names.kind"
	// kartaGVKIndexKey indexes Kartas by their full root GVK (group/version/kind),
	// read from the spec (not the metadata labels), so a lookup by workload type
	// does not depend on the GVK index labels being stamped yet. The version is
	// part of the key, so two Kartas for the same group/kind but different
	// versions are distinct and both allowed.
	kartaGVKIndexKey = "spec.rootComponent.gvk"
)

// Reconciler reconciles Karta CRs.
type Reconciler struct {
	client.Client
	recorder   events.EventRecorder
	nativeGVKs map[schema.GroupVersionKind]bool
}

// NewReconciler constructs a new Reconciler.
func NewReconciler(c client.Client, recorder events.EventRecorder) *Reconciler {
	return &Reconciler{Client: c, recorder: recorder}
}

// SetupWithManager registers the reconciler with the given manager.
//
// Two watches are registered:
//  1. Karta — all create/update/delete events trigger Reconcile.
//  2. CustomResourceDefinition — events are mapped to the Kartas that
//     reference the same group+kind.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	dc, err := discovery.NewDiscoveryClientForConfig(mgr.GetConfig())
	if err != nil {
		return fmt.Errorf("create discovery client: %w", err)
	}

	natives, err := discoverNativeGVKs(context.Background(), dc, mgr.GetAPIReader(), mgr.GetLogger())
	if err != nil {
		return fmt.Errorf("discover natives GVKs: %w", err)
	}
	r.nativeGVKs = natives

	if err := mgr.GetFieldIndexer().IndexField(context.Background(),
		&apiextensionsv1.CustomResourceDefinition{}, crdGroupKindIndexKey,
		func(obj client.Object) []string {
			crd := obj.(*apiextensionsv1.CustomResourceDefinition)
			return []string{schema.GroupKind{Group: crd.Spec.Group, Kind: crd.Spec.Names.Kind}.String()}
		}); err != nil {
		return fmt.Errorf("index CRDs by %s: %w", crdGroupKindIndexKey, err)
	}

	if err := mgr.GetFieldIndexer().IndexField(context.Background(),
		&kartav1alpha1.Karta{}, kartaGVKIndexKey, indexKartaByRootGVK); err != nil {
		return fmt.Errorf("index Kartas by %s: %w", kartaGVKIndexKey, err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(&kartav1alpha1.Karta{}).
		Watches(
			&apiextensionsv1.CustomResourceDefinition{},
			handler.EnqueueRequestsFromMapFunc(r.MapCRDToKartaEvent),
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		Complete(r)
}

func indexKartaByRootGVK(obj client.Object) []string {
	gvk := rootGVK(obj.(*kartav1alpha1.Karta))
	if gvk == nil {
		return nil
	}
	return []string{gvk.String()}
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("karta", req.Name)

	karta := &kartav1alpha1.Karta{}
	if err := r.Get(ctx, req.NamespacedName, karta); err != nil {
		if errors.IsNotFound(err) {
			logger.V(1).Info("Karta not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get karta %q: %w", req.Name, err)
	}

	if !karta.DeletionTimestamp.IsZero() {
		logger.V(1).Info("Karta is being deleted, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	logger.Info("Reconciling Karta")

	base := karta.DeepCopy()
	err := r.reconcile(ctx, logger, karta)
	if patchErr := r.Status().Patch(ctx, karta, client.MergeFrom(base)); patchErr != nil {
		patchErr = fmt.Errorf("failed to update status for karta %q: %w", karta.Name, patchErr)
		err = stderrors.Join(err, patchErr)
	}
	return ctrl.Result{}, err
}

// reconcile runs the reconciliation logic for one Karta.
func (r *Reconciler) reconcile(ctx context.Context, logger logr.Logger, karta *kartav1alpha1.Karta) error {
	setDefaultConditions(&karta.Status, karta.Generation)
	r.validateKarta(logger, karta)
	if err := r.checkCRDExists(ctx, logger, karta); err != nil {
		return err
	}
	if err := r.ensureLabels(ctx, logger, karta); err != nil {
		return err
	}

	ready := setReady(&karta.Status, karta.Generation)
	logger.V(1).Info("Derived Ready condition", "ready", ready)
	return nil
}

// validateKarta runs the Karta spec validator and writes the Validated condition.
func (r *Reconciler) validateKarta(logger logr.Logger, karta *kartav1alpha1.Karta) {
	if err := kartav1alpha1.NewKartaValidator(karta).Validate(); err != nil {
		logger.Info("Karta spec validation failed", "error", err.Error())
		r.recorder.Eventf(karta, nil, corev1.EventTypeWarning, ReasonValidationFailed, "Validating", "%s", err.Error())
		setValidated(&karta.Status, karta.Generation, metav1.ConditionFalse, err.Error())
		return
	}
	logger.V(1).Info("Karta spec validated")
	setValidated(&karta.Status, karta.Generation, metav1.ConditionTrue, "")
}

// checkCRDExists looks up the referenced CRD and writes the CRDExists condition.
func (r *Reconciler) checkCRDExists(ctx context.Context, logger logr.Logger, karta *kartav1alpha1.Karta) error {
	gvk := rootGVK(karta)
	if gvk == nil {
		logger.V(1).Info("karta has no root component kind")
		setCRDExists(&karta.Status, karta.Generation, metav1.ConditionFalse, "")
		return nil
	}

	exists, err := r.checkGVKServed(ctx, *gvk)
	if err != nil {
		logger.Error(err, "Failed to check CRD existence", "gvk", gvk.String())
		return fmt.Errorf("check CRD existence for karta %q: %w", karta.Name, err)
	}

	if exists {
		logger.V(1).Info("CRD found", "gvk", gvk.String())
		setCRDExists(&karta.Status, karta.Generation, metav1.ConditionTrue, "")
	} else {
		msg := fmt.Sprintf("CRD for %s/%s not found or does not serve version %s",
			gvk.Group, gvk.Kind, gvk.Version)
		logger.V(1).Info("CRD not found", "gvk", gvk.String())
		r.recorder.Eventf(karta, nil, corev1.EventTypeWarning, ReasonCRDNotFound, "CheckingCRD", "%s", msg)
		setCRDExists(&karta.Status, karta.Generation, metav1.ConditionFalse, msg)
	}
	return nil
}

// ensureLabels stamps the three GVK index labels (run.ai/karta-group,
// run.ai/karta-version, run.ai/karta-kind) onto the Karta metadata so that
// consumers can locate a Karta by GVK via a label-selector List.
func (r *Reconciler) ensureLabels(ctx context.Context, logger logr.Logger, karta *kartav1alpha1.Karta) error {
	desired := desiredRootLabels(karta)
	if desired == nil {
		return nil
	}

	if labelsMatch(karta.Labels, desired) {
		return nil
	}

	base := karta.DeepCopy()
	if karta.Labels == nil {
		karta.Labels = map[string]string{}
	}
	for k, v := range desired {
		karta.Labels[k] = v
	}

	if err := r.Patch(ctx, karta, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("patch labels for karta %q: %w", karta.Name, err)
	}

	logger.V(1).Info("Stamped GVK index labels",
		"group", desired[kartav1alpha1.LabelRootGroup],
		"version", desired[kartav1alpha1.LabelRootVersion],
		"kind", desired[kartav1alpha1.LabelRootKind])
	return nil
}

func desiredRootLabels(karta *kartav1alpha1.Karta) map[string]string {
	gvk := rootGVK(karta)
	if gvk == nil {
		return nil
	}
	return map[string]string{
		kartav1alpha1.LabelRootGroup:   gvk.Group,
		kartav1alpha1.LabelRootVersion: gvk.Version,
		kartav1alpha1.LabelRootKind:    gvk.Kind,
	}
}

// labelsMatch returns true when current already contains all desired key/value pairs.
func labelsMatch(current, desired map[string]string) bool {
	for k, v := range desired {
		if current[k] != v {
			return false
		}
	}
	return true
}

// rootGVK extracts the GroupVersionKind of the Karta root component, or nil
// when the Karta has no root component kind defined.
func rootGVK(karta *kartav1alpha1.Karta) *schema.GroupVersionKind {
	kind := karta.Spec.StructureDefinition.RootComponent.Kind
	if kind == nil {
		return nil
	}
	return &schema.GroupVersionKind{
		Group:   kind.Group,
		Version: kind.Version,
		Kind:    kind.Kind,
	}
}

// discoverNativeGVKs returns the served GVKs that are built-in
func discoverNativeGVKs(ctx context.Context, dc discovery.DiscoveryInterface, reader client.Reader, logger logr.Logger) (map[schema.GroupVersionKind]bool, error) {
	crds := &apiextensionsv1.CustomResourceDefinitionList{}
	if err := reader.List(ctx, crds); err != nil {
		return nil, fmt.Errorf("list CRDs: %w", err)
	}
	crdGKs := make(map[schema.GroupKind]bool, len(crds.Items))
	for i := range crds.Items {
		crdSpec := crds.Items[i].Spec
		crdGKs[schema.GroupKind{Group: crdSpec.Group, Kind: crdSpec.Names.Kind}] = true
	}

	_, resourceLists, err := dc.ServerGroupsAndResources()
	if err != nil {
		if !discovery.IsGroupDiscoveryFailedError(err) {
			return nil, fmt.Errorf("list server resources: %w", err)
		}
		logger.Info("Ignoring failed groups during discovery", "error", err.Error())
	}

	natives := make(map[schema.GroupVersionKind]bool)
	for _, list := range resourceLists {
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			logger.Info("Skipping malformed GroupVersion from discovery", "groupVersion", list.GroupVersion, "error", err.Error())
			continue
		}
		for _, res := range list.APIResources {
			if strings.Contains(res.Name, "/") {
				continue // subresource, e.g. deployments/scale
			}
			if crdGKs[schema.GroupKind{Group: gv.Group, Kind: res.Kind}] {
				continue
			}
			if !slices.Contains(res.Verbs, "list") || !slices.Contains(res.Verbs, "watch") {
				continue // not a plausible Karta root (virtual/imperative)
			}
			natives[schema.GroupVersionKind{Group: gv.Group, Version: gv.Version, Kind: res.Kind}] = true
		}
	}
	return natives, nil
}

// checkGVKServed reports whether the given group, version and kind is served
// by the cluster, either as a built-in (native) type or by a CRD.
func (r *Reconciler) checkGVKServed(ctx context.Context, gvk schema.GroupVersionKind) (bool, error) {
	if r.nativeGVKs[gvk] {
		return true, nil
	}

	crds := &apiextensionsv1.CustomResourceDefinitionList{}
	if err := r.List(ctx, crds, client.MatchingFields{
		crdGroupKindIndexKey: schema.GroupKind{Group: gvk.Group, Kind: gvk.Kind}.String(),
	}); err != nil {
		return false, fmt.Errorf("list CRDs: %w", err)
	}

	for i := range crds.Items {
		if crdServesVersion(&crds.Items[i], gvk.Version) {
			return true, nil
		}
	}
	return false, nil
}

func crdServesVersion(crd *apiextensionsv1.CustomResourceDefinition, version string) bool {
	for _, v := range crd.Spec.Versions {
		if v.Name == version && v.Served {
			return true
		}
	}
	return false
}
