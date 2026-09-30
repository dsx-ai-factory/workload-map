//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	reconcileTimeout = envDuration("KARTA_E2E_RECONCILE_TIMEOUT", 60*time.Second)
	settleWindow     = envDuration("KARTA_E2E_SETTLE_WINDOW", 10*time.Second)
	pollInterval     = envDuration("KARTA_E2E_POLL_INTERVAL", 500*time.Millisecond)
)

// Every fixture the suite creates carries this, so a purge can find its own
// leftovers without touching anything else on a reused cluster.
const (
	ownerLabelKey   = "e2e.karta.run.ai/owner"
	ownerLabelValue = "operator-suite"
)

var ownedBySuite = client.MatchingLabels{ownerLabelKey: ownerLabelValue}

// The arrangement the harness was asked to install. Selects which half of the
// invalid-Karta contract the specs assert.
const (
	webhookModeAuto        = "auto"
	webhookModeCertManager = "cert-manager"
	webhookModeDisabled    = "disabled"
)

var (
	webhookMode    = os.Getenv("KARTA_WEBHOOK_MODE")
	kartaNamespace = envOr("KARTA_NAMESPACE", "karta-system")
	kartaFullname  = envOr("KARTA_FULLNAME", "karta-operator")
)

var (
	k8sClient  client.Client
	testCtx    context.Context
	testCancel context.CancelFunc
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Karta Operator E2E Suite")
}

var _ = BeforeSuite(func() {
	testCtx, testCancel = context.WithCancel(context.Background())

	cfg, err := ctrl.GetConfig()
	Expect(err).NotTo(HaveOccurred(), "no kubeconfig; run make e2e-up first")

	k8sClient, err = client.New(cfg, client.Options{Scheme: buildScheme()})
	Expect(err).NotTo(HaveOccurred())

	Expect(k8sClient.List(testCtx, &kartav1alpha1.KartaList{})).To(Succeed(),
		"cannot list Kartas; run make e2e-up WORKLOADS=none")

	Expect(webhookMode).To(BeElementOf(webhookModeAuto, webhookModeCertManager, webhookModeDisabled),
		"KARTA_WEBHOOK_MODE must name the arrangement under test; run via make test-operator-e2e")
	verifyWebhookInstall()

	purgeFixtures()
})

// Asserts the cluster matches webhookMode, in BeforeSuite so a mismatch stops the run.
func verifyWebhookInstall() {
	GinkgoHelper()
	deploy := &appsv1.Deployment{}
	Expect(k8sClient.Get(testCtx, types.NamespacedName{Namespace: kartaNamespace, Name: kartaFullname}, deploy)).
		To(Succeed(), "no %s/%s Deployment; run make e2e-up WORKLOADS=none", kartaNamespace, kartaFullname)
	Expect(deploy.Spec.Template.Spec.Containers).NotTo(BeEmpty())
	args := deploy.Spec.Template.Spec.Containers[0].Args

	wantArg := map[string]string{
		webhookModeAuto:        "--webhook-cert-mode=auto",
		webhookModeCertManager: "--webhook-cert-mode=manual",
	}[webhookMode]
	if wantArg == "" {
		Expect(args).NotTo(ContainElement(HavePrefix("--webhook-cert-mode")),
			"mode %q, but the operator is serving a webhook", webhookMode)
	} else {
		Expect(args).To(ContainElement(wantArg),
			"mode %q, but the operator was started with %v", webhookMode, args)
	}

	validating, mutating := kartaWebhookRegistrations()
	if webhookMode == webhookModeDisabled {
		Expect(validating).To(BeEmpty(), "mode %q, but a validating webhook is registered", webhookMode)
		Expect(mutating).To(BeEmpty(), "mode %q, but a mutating webhook is registered", webhookMode)
		return
	}
	Expect(validating).NotTo(BeEmpty(), "mode %q, but no validating webhook is registered for kartas", webhookMode)
	Expect(mutating).NotTo(BeEmpty(), "mode %q, but no mutating webhook is registered for kartas", webhookMode)
}

// Names of the configurations registered against kartas, found by rule rather than
// by name because KARTA_FULLNAME moves the names.
func kartaWebhookRegistrations() (validating, mutating []string) {
	GinkgoHelper()
	vList := &admissionv1.ValidatingWebhookConfigurationList{}
	Expect(k8sClient.List(testCtx, vList)).To(Succeed())
	for _, cfg := range vList.Items {
		for _, webhook := range cfg.Webhooks {
			if admitsKartas(webhook.Rules) {
				validating = append(validating, cfg.Name)
				break
			}
		}
	}
	mList := &admissionv1.MutatingWebhookConfigurationList{}
	Expect(k8sClient.List(testCtx, mList)).To(Succeed())
	for _, cfg := range mList.Items {
		for _, webhook := range cfg.Webhooks {
			if admitsKartas(webhook.Rules) {
				mutating = append(mutating, cfg.Name)
				break
			}
		}
	}
	return validating, mutating
}

// Both halves matter: cert-manager registers "*/*" resources for its own groups.
func admitsKartas(rules []admissionv1.RuleWithOperations) bool {
	for _, rule := range rules {
		if slices.Contains(rule.APIGroups, kartav1alpha1.GroupVersion.Group) &&
			slices.Contains(rule.Resources, "kartas") {
			return true
		}
	}
	return false
}

// DeferCleanup does not run when a suite is interrupted or times out, and up.sh
// reuses clusters, so fixtures can outlive the run that made them. Their names are
// fixed and Kartas are cluster scoped, so the next run would fail on AlreadyExists
// or on the one-Karta-per-root-GVK rule rather than on anything real.
func purgeFixtures() {
	GinkgoHelper()
	Expect(k8sClient.DeleteAllOf(testCtx, &kartav1alpha1.Karta{}, ownedBySuite)).To(Succeed())
	Expect(k8sClient.DeleteAllOf(testCtx, &apiextensionsv1.CustomResourceDefinition{}, ownedBySuite)).To(Succeed())

	Eventually(func(g Gomega) {
		kartas := &kartav1alpha1.KartaList{}
		g.Expect(k8sClient.List(testCtx, kartas, ownedBySuite)).To(Succeed())
		g.Expect(kartas.Items).To(BeEmpty(), "a stale Karta outlived the purge")

		crds := &apiextensionsv1.CustomResourceDefinitionList{}
		g.Expect(k8sClient.List(testCtx, crds, ownedBySuite)).To(Succeed())
		g.Expect(crds.Items).To(BeEmpty(), "a stale CRD outlived the purge")
	}, reconcileTimeout, pollInterval).Should(Succeed())
}

var _ = AfterSuite(func() {
	if testCancel != nil {
		testCancel()
	}
})

func buildScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	Expect(kartav1alpha1.AddToScheme(s)).To(Succeed())
	Expect(apiextensionsv1.AddToScheme(s)).To(Succeed())
	Expect(admissionv1.AddToScheme(s)).To(Succeed())
	Expect(appsv1.AddToScheme(s)).To(Succeed())
	return s
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		panic(fmt.Sprintf("%s=%q is not a duration: %v", key, raw, err))
	}
	return d
}

func newKarta(name string, gvk schema.GroupVersionKind) *kartav1alpha1.Karta {
	return &kartav1alpha1.Karta{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{ownerLabelKey: ownerLabelValue},
		},
		Spec: kartav1alpha1.KartaSpec{
			StructureDefinition: kartav1alpha1.StructureDefinition{
				RootComponent: kartav1alpha1.ComponentDefinition{
					Name: name + "-root",
					Kind: &kartav1alpha1.GroupVersionKind{
						Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind,
					},
					StatusDefinition: &kartav1alpha1.StatusDefinition{
						StatusMappings: kartav1alpha1.StatusMappings{},
					},
				},
			},
		},
	}
}

func newInvalidKarta(name string, gvk schema.GroupVersionKind) *kartav1alpha1.Karta {
	k := newKarta(name, gvk)
	k.Spec.StructureDefinition.RootComponent.StatusDefinition = nil
	return k
}

func webhookEnabled() bool { return webhookMode != webhookModeDisabled }

func createKarta(k *kartav1alpha1.Karta) *kartav1alpha1.Karta {
	GinkgoHelper()
	Expect(k8sClient.Create(testCtx, k)).To(Succeed())
	DeferCleanup(func() {
		Expect(client.IgnoreNotFound(k8sClient.Delete(testCtx, k))).To(Succeed())
	})
	return k
}

func getKarta(g Gomega, name string) *kartav1alpha1.Karta {
	k := &kartav1alpha1.Karta{}
	g.Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: name}, k)).To(Succeed())
	return k
}

// The generation guard stops an assertion passing on the status a previous reconcile
// left behind. It is inert where the spec itself does not bump generation.
func conditionIs(k *kartav1alpha1.Karta, ct kartav1alpha1.ConditionType, status metav1.ConditionStatus) (bool, string) {
	for _, c := range k.Status.Conditions {
		if c.Type != string(ct) {
			continue
		}
		switch {
		case c.ObservedGeneration != k.Generation:
			return false, fmt.Sprintf("%s is stale (observed %d, want %d)", ct, c.ObservedGeneration, k.Generation)
		case c.Status != status:
			return false, fmt.Sprintf("%s is %s (reason %q), want %s", ct, c.Status, c.Reason, status)
		}
		return true, ""
	}
	return false, fmt.Sprintf("%s is not set", ct)
}

func expectCondition(name string, ct kartav1alpha1.ConditionType, status metav1.ConditionStatus) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		ok, why := conditionIs(getKarta(g, name), ct, status)
		g.Expect(ok).To(BeTrue(), why)
	}, reconcileTimeout, pollInterval).Should(Succeed())
}

// Eventually alone accepts a value that flickers past.
func expectConditionSettled(name string, ct kartav1alpha1.ConditionType, status metav1.ConditionStatus) {
	GinkgoHelper()
	expectCondition(name, ct, status)
	Consistently(func(g Gomega) {
		ok, why := conditionIs(getKarta(g, name), ct, status)
		g.Expect(ok).To(BeTrue(), why)
	}, settleWindow, pollInterval).Should(Succeed())
}

func expectGone(name string) {
	GinkgoHelper()
	Eventually(func() bool {
		err := k8sClient.Get(testCtx, types.NamespacedName{Name: name}, &kartav1alpha1.Karta{})
		return apierrors.IsNotFound(err)
	}, reconcileTimeout, pollInterval).Should(BeTrue(), "Karta %s was not removed", name)
}
