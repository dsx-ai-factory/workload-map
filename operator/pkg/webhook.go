// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package pkg

import (
	"context"
	"fmt"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/mutate-run-ai-v1alpha1-karta,mutating=true,failurePolicy=fail,sideEffects=None,groups=run.ai,resources=kartas,verbs=create,versions=v1alpha1,name=mkarta.run.ai,admissionReviewVersions=v1

type KartaLabeler struct{}

var _ admission.Defaulter[*kartav1alpha1.Karta] = (*KartaLabeler)(nil)

func SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &kartav1alpha1.Karta{}).
		WithDefaulter(&KartaLabeler{}).
		WithValidator(&KartaValidator{client: mgr.GetClient()}).
		Complete()
}

func (w *KartaLabeler) Default(_ context.Context, karta *kartav1alpha1.Karta) error {
	desired := desiredRootLabels(karta)
	if desired == nil {
		return nil
	}
	if karta.Labels == nil {
		karta.Labels = make(map[string]string, len(desired))
	}
	for k, v := range desired {
		karta.Labels[k] = v
	}
	return nil
}

// +kubebuilder:webhook:path=/validate-run-ai-v1alpha1-karta,mutating=false,failurePolicy=fail,sideEffects=None,groups=run.ai,resources=kartas,verbs=create;update,versions=v1alpha1,name=vkarta.run.ai,admissionReviewVersions=v1

type KartaValidator struct {
	client client.Client
}

var _ admission.Validator[*kartav1alpha1.Karta] = (*KartaValidator)(nil)

func (v *KartaValidator) ValidateCreate(ctx context.Context, karta *kartav1alpha1.Karta) (admission.Warnings, error) {
	if err := kartav1alpha1.NewKartaValidator(karta).Validate(); err != nil {
		return nil, err
	}
	return nil, v.checkUniqueRootGVK(ctx, karta)
}

// checkUniqueRootGVK rejects a Karta whose full root GVK (group/version/kind)
// already has one; two Kartas for the same group/kind but different versions are
// allowed. This is best-effort: two
// Kartas created at the same time can both pass, because admission is not
// serialized and neither List sees the other in-flight object. failurePolicy
// does not change that. Guaranteed enforcement (reconcile-to-one in the
// reconciler) is a follow-up; see #198.
func (v *KartaValidator) checkUniqueRootGVK(ctx context.Context, karta *kartav1alpha1.Karta) error {
	gvk := rootGVK(karta)
	if gvk == nil {
		return nil
	}
	list := &kartav1alpha1.KartaList{}
	if err := v.client.List(ctx, list, client.MatchingFields{
		kartaGVKIndexKey: gvk.String(),
	}); err != nil {
		return fmt.Errorf("check karta uniqueness: %w", err)
	}
	for i := range list.Items {
		if list.Items[i].Name != karta.Name {
			return fmt.Errorf("a Karta for group %q version %q kind %q already exists (%q); only one Karta per group/version/kind is allowed",
				gvk.Group, gvk.Version, gvk.Kind, list.Items[i].Name)
		}
	}
	return nil
}

func (v *KartaValidator) ValidateUpdate(ctx context.Context, oldObj, karta *kartav1alpha1.Karta) (admission.Warnings, error) {
	if err := kartav1alpha1.NewKartaValidator(karta).Validate(); err != nil {
		return nil, err
	}
	oldGVK, newGVK := rootGVK(oldObj), rootGVK(karta)
	if oldGVK == nil || newGVK == nil || *oldGVK != *newGVK {
		return nil, fmt.Errorf("the root component GVK is immutable and cannot be changed after creation")
	}
	if err := checkRootLabels(karta); err != nil {
		return nil, err
	}
	return nil, v.checkUniqueRootGVK(ctx, karta)
}

func checkRootLabels(karta *kartav1alpha1.Karta) error {
	for k, want := range desiredRootLabels(karta) {
		if got, ok := karta.Labels[k]; !ok || got != want {
			return fmt.Errorf("label %q is managed by the operator and must equal %q; it cannot be edited or removed", k, want)
		}
	}
	return nil
}

func (v *KartaValidator) ValidateDelete(_ context.Context, _ *kartav1alpha1.Karta) (admission.Warnings, error) {
	return nil, nil
}
