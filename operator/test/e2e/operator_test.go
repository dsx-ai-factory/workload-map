//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	"strings"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	replicaSetGVK  = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"}
	daemonSetGVK   = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "DaemonSet"}
	statefulSetGVK = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"}
	cronJobGVK     = schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "CronJob"}
)

var _ = Describe("Karta operator on a live cluster", Serial, func() {
	It("drives a valid Karta to Ready", func() {
		k := createKarta(newKarta("e2e-ready", replicaSetGVK))

		expectCondition(k.Name, kartav1alpha1.ConditionValidated, metav1.ConditionTrue)
		expectCondition(k.Name, kartav1alpha1.ConditionCRDExists, metav1.ConditionTrue)
		expectConditionSettled(k.Name, kartav1alpha1.ConditionReady, metav1.ConditionTrue)
	})

	It("checks CRDExists=False and Ready=False when the referenced CRD is absent", func() {
		absent := schema.GroupVersionKind{Group: "absent.e2e.run.ai", Version: "v1", Kind: "Missing"}
		k := createKarta(newKarta("e2e-crd-absent", absent))

		expectCondition(k.Name, kartav1alpha1.ConditionCRDExists, metav1.ConditionFalse)
		expectConditionSettled(k.Name, kartav1alpha1.ConditionReady, metav1.ConditionFalse)
	})

	It("installs a CRD and checks CRDExists flips to True", func() {
		gvk := schema.GroupVersionKind{Group: "install.e2e.run.ai", Version: "v1", Kind: "Widget"}
		k := createKarta(newKarta("e2e-crd-install", gvk))

		// Installing a CRD does not bump generation, so the guard in conditionIs cannot
		// tell a stale False from a fresh one. Observing False first is what makes the
		// flip a transition rather than a coincidence.
		expectCondition(k.Name, kartav1alpha1.ConditionCRDExists, metav1.ConditionFalse)

		createCRD(gvk)

		expectCondition(k.Name, kartav1alpha1.ConditionCRDExists, metav1.ConditionTrue)
		expectConditionSettled(k.Name, kartav1alpha1.ConditionReady, metav1.ConditionTrue)
	})

	It("checks CRDExists flips back to False when the CRD is removed", func() {
		gvk := schema.GroupVersionKind{Group: "remove.e2e.run.ai", Version: "v1", Kind: "Widget"}
		crd := createCRD(gvk)
		k := createKarta(newKarta("e2e-crd-remove", gvk))
		expectCondition(k.Name, kartav1alpha1.ConditionCRDExists, metav1.ConditionTrue)

		Expect(k8sClient.Delete(testCtx, crd)).To(Succeed())

		expectCondition(k.Name, kartav1alpha1.ConditionCRDExists, metav1.ConditionFalse)
		expectConditionSettled(k.Name, kartav1alpha1.ConditionReady, metav1.ConditionFalse)
	})

	It("stamps the root GVK index labels", func() {
		k := createKarta(newKarta("e2e-labels", replicaSetGVK))
		expectCondition(k.Name, kartav1alpha1.ConditionReady, metav1.ConditionTrue)

		Eventually(func(g Gomega) {
			g.Expect(getKarta(g, k.Name).Labels).To(SatisfyAll(
				HaveKeyWithValue(kartav1alpha1.LabelRootGroup, replicaSetGVK.Group),
				HaveKeyWithValue(kartav1alpha1.LabelRootVersion, replicaSetGVK.Version),
				HaveKeyWithValue(kartav1alpha1.LabelRootKind, replicaSetGVK.Kind),
			))
		}, reconcileTimeout, pollInterval).Should(Succeed())
	})

	It("removes a Karta cleanly on delete", func() {
		k := createKarta(newKarta("e2e-delete", replicaSetGVK))
		expectCondition(k.Name, kartav1alpha1.ConditionReady, metav1.ConditionTrue)

		Expect(k8sClient.Delete(testCtx, k)).To(Succeed())

		expectGone(k.Name)
	})
})

var _ = Describe("an invalid Karta, webhook installed", Serial, Label("webhook"), func() {
	BeforeEach(func() {
		if !webhookEnabled() {
			Skip("no validating webhook on this cluster (KARTA_WEBHOOK_MODE=disabled)")
		}
	})

	It("is refused at admission and does not persist", func() {
		k := newInvalidKarta("e2e-invalid-admission", daemonSetGVK)

		Expect(k8sClient.Create(testCtx, k)).To(MatchError(ContainSubstring("status definition")))

		err := k8sClient.Get(testCtx, types.NamespacedName{Name: k.Name}, &kartav1alpha1.Karta{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "a refused create must leave nothing behind")
	})

	// Uniqueness is enforced only at admission, so it is only observable here.
	// Dry run so only the mutating webhook can stamp the labels; Reconciler.ensureLabels
	// sets the same keys and would otherwise mask a dead mutator.
	It("stamps the root GVK index labels at admission", func() {
		probe := newKarta("e2e-labels-admission", cronJobGVK)

		Expect(k8sClient.Create(testCtx, probe, client.DryRunAll)).To(Succeed())

		Expect(probe.Labels).To(SatisfyAll(
			HaveKeyWithValue(kartav1alpha1.LabelRootGroup, cronJobGVK.Group),
			HaveKeyWithValue(kartav1alpha1.LabelRootVersion, cronJobGVK.Version),
			HaveKeyWithValue(kartav1alpha1.LabelRootKind, cronJobGVK.Kind),
		))
	})

	It("refuses a second Karta claiming the same root GVK", func() {
		first := createKarta(newKarta("e2e-unique-first", statefulSetGVK))
		expectCondition(first.Name, kartav1alpha1.ConditionReady, metav1.ConditionTrue)

		second := newKarta("e2e-unique-second", statefulSetGVK)
		Expect(k8sClient.Create(testCtx, second)).To(
			MatchError(ContainSubstring("only one Karta per group/version/kind")))
	})
})

var _ = Describe("an invalid Karta, webhook disabled", Serial, Label("no-webhook"), func() {
	BeforeEach(func() {
		if webhookEnabled() {
			Skip("validating webhook is installed, so an invalid Karta is refused before any condition is written")
		}
	})

	It("is admitted and reported as Validated=False then Ready=False", func() {
		k := createKarta(newInvalidKarta("e2e-invalid-reported", daemonSetGVK))

		expectCondition(k.Name, kartav1alpha1.ConditionValidated, metav1.ConditionFalse)
		expectConditionSettled(k.Name, kartav1alpha1.ConditionReady, metav1.ConditionFalse)
	})

	It("returns to Ready once the spec is fixed", func() {
		k := createKarta(newInvalidKarta("e2e-invalid-fixed", daemonSetGVK))
		expectCondition(k.Name, kartav1alpha1.ConditionValidated, metav1.ConditionFalse)

		// Retried because the operator is patching status underneath this.
		Eventually(func(g Gomega) {
			cur := getKarta(g, k.Name)
			cur.Spec.StructureDefinition.RootComponent.StatusDefinition = &kartav1alpha1.StatusDefinition{
				StatusMappings: kartav1alpha1.StatusMappings{},
			}
			g.Expect(k8sClient.Update(testCtx, cur)).To(Succeed())
		}, reconcileTimeout, pollInterval).Should(Succeed())

		// A spec change bumps generation, so the guard in conditionIs is load bearing
		// here: it is what stops this passing on the pre-update False.
		expectCondition(k.Name, kartav1alpha1.ConditionValidated, metav1.ConditionTrue)
		expectConditionSettled(k.Name, kartav1alpha1.ConditionReady, metav1.ConditionTrue)
	})

	It("carries the validator error in the condition message", func() {
		k := createKarta(newInvalidKarta("e2e-invalid-message", daemonSetGVK))

		expectCondition(k.Name, kartav1alpha1.ConditionValidated, metav1.ConditionFalse)
		Eventually(func(g Gomega) {
			for _, c := range getKarta(g, k.Name).Status.Conditions {
				if c.Type == string(kartav1alpha1.ConditionValidated) {
					g.Expect(c.Message).To(ContainSubstring("status definition"))
					return
				}
			}
			g.Expect(false).To(BeTrue(), "Validated is not set")
		}, reconcileTimeout, pollInterval).Should(Succeed())
	})
})

func createCRD(gvk schema.GroupVersionKind) *apiextensionsv1.CustomResourceDefinition {
	GinkgoHelper()
	plural := strings.ToLower(gvk.Kind) + "s"
	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name:   plural + "." + gvk.Group,
			Labels: map[string]string{ownerLabelKey: ownerLabelValue},
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: gvk.Group,
			Scope: apiextensionsv1.NamespaceScoped,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural: plural,
				Kind:   gvk.Kind,
			},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name:    gvk.Version,
				Served:  true,
				Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{Type: "object"},
				},
			}},
		},
	}
	Expect(k8sClient.Create(testCtx, crd)).To(Succeed())
	DeferCleanup(func() {
		Expect(client.IgnoreNotFound(k8sClient.Delete(testCtx, crd))).To(Succeed())
		// Wait it out: a surviving CRD would flip the next spec's CRDExists.
		Eventually(func() bool {
			err := k8sClient.Get(testCtx, types.NamespacedName{Name: crd.Name}, &apiextensionsv1.CustomResourceDefinition{})
			return apierrors.IsNotFound(err)
		}, reconcileTimeout, pollInterval).Should(BeTrue(), "CRD %s outlived its spec", crd.Name)
	})
	return crd
}
