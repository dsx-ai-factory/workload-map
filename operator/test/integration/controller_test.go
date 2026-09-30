// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package integration

import (
	"strings"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/dsx-ai-factory/workload-map/operator/pkg"
	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const testCRDLabel = "karta-integration-test"

var _ = Describe("Reconciler (envtest)", func() {
	AfterEach(func() {
		_ = k8sClient.DeleteAllOf(testCtx, &kartav1alpha1.Karta{})
		_ = k8sClient.DeleteAllOf(testCtx,
			&apiextensionsv1.CustomResourceDefinition{},
			client.MatchingLabels{testCRDLabel: "true"},
		)
	})

	It("sets Validated=False with the real validator error for an invalid Karta", func() {
		gvk := schema.GroupVersionKind{Group: "test.run.ai", Version: "v1", Kind: "Foo"}
		k := newKarta("envtest-invalid", &gvk) // no StatusDefinition → invalid
		Expect(k8sClient.Create(testCtx, k)).To(Succeed())

		Eventually(func(g Gomega) {
			c := apimeta.FindStatusCondition(getKarta(k.Name).Status.Conditions, string(kartav1alpha1.ConditionValidated))
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(c.Reason).To(Equal(pkg.ReasonValidationFailed))
			g.Expect(c.Message).NotTo(BeEmpty())
			g.Expect(c.Message).NotTo(Equal("Karta validation failed"),
				"expected the real validator error, not the generic fallback")
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
	})

	It("sets CRDExists=False and Ready=False when the referenced CRD is missing", func() {
		gvk := schema.GroupVersionKind{Group: "absent.run.ai", Version: "v1", Kind: "Missing"}
		k := newValidKarta("envtest-no-crd", &gvk)
		Expect(k8sClient.Create(testCtx, k)).To(Succeed())

		Eventually(func(g Gomega) {
			conds := getKarta(k.Name).Status.Conditions
			crd := apimeta.FindStatusCondition(conds, string(kartav1alpha1.ConditionCRDExists))
			g.Expect(crd).NotTo(BeNil())
			g.Expect(crd.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(crd.Message).To(ContainSubstring("absent.run.ai"))
			ready := apimeta.FindStatusCondition(conds, string(kartav1alpha1.ConditionReady))
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
	})

	It("sets CRDExists=True for a built-in GVK with no backing CRD", func() {
		gvk := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
		k := newValidKarta("envtest-builtin-gvk", &gvk)
		Expect(k8sClient.Create(testCtx, k)).To(Succeed())

		Eventually(func(g Gomega) {
			conds := getKarta(k.Name).Status.Conditions
			crd := apimeta.FindStatusCondition(conds, string(kartav1alpha1.ConditionCRDExists))
			g.Expect(crd).NotTo(BeNil())
			g.Expect(crd.Status).To(Equal(metav1.ConditionTrue))
			ready := apimeta.FindStatusCondition(conds, string(kartav1alpha1.ConditionReady))
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
	})

	It("sets Ready=True and stamps index labels when the CRD exists", func() {
		crd := buildCRD("test.run.ai", "Widget", "v1")
		Expect(k8sClient.Create(testCtx, crd)).To(Succeed())

		gvk := schema.GroupVersionKind{Group: "test.run.ai", Version: "v1", Kind: "Widget"}
		k := newValidKarta("envtest-ready", &gvk)
		Expect(k8sClient.Create(testCtx, k)).To(Succeed())

		Eventually(func(g Gomega) {
			got := getKarta(k.Name)
			validated := apimeta.FindStatusCondition(got.Status.Conditions, string(kartav1alpha1.ConditionValidated))
			g.Expect(validated).NotTo(BeNil())
			g.Expect(validated.Status).To(Equal(metav1.ConditionTrue))
			crdExists := apimeta.FindStatusCondition(got.Status.Conditions, string(kartav1alpha1.ConditionCRDExists))
			g.Expect(crdExists).NotTo(BeNil())
			g.Expect(crdExists.Status).To(Equal(metav1.ConditionTrue))
			ready := apimeta.FindStatusCondition(got.Status.Conditions, string(kartav1alpha1.ConditionReady))
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(got.Labels[kartav1alpha1.LabelRootGroup]).To(Equal(gvk.Group))
			g.Expect(got.Labels[kartav1alpha1.LabelRootVersion]).To(Equal(gvk.Version))
			g.Expect(got.Labels[kartav1alpha1.LabelRootKind]).To(Equal(gvk.Kind))
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
	})

	It("preserves a foreign condition written by another controller", func() {
		gvk := schema.GroupVersionKind{Group: "absent.run.ai", Version: "v1", Kind: "Missing"}
		k := newValidKarta("envtest-foreign", &gvk)
		Expect(k8sClient.Create(testCtx, k)).To(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(apimeta.FindStatusCondition(getKarta(k.Name).Status.Conditions, string(kartav1alpha1.ConditionReady))).NotTo(BeNil())
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())

		Eventually(func() error {
			got := getKarta(k.Name)
			got.Status.Conditions = append(got.Status.Conditions, metav1.Condition{
				Type: "RBACReady", Status: metav1.ConditionTrue, Reason: "EWI",
				LastTransitionTime: metav1.Now(),
			})
			return k8sClient.Status().Update(testCtx, got)
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())

		Eventually(func() error {
			got := getKarta(k.Name)
			got.Spec.StructureDefinition.RootComponent.Name = "renamed-root"
			return k8sClient.Update(testCtx, got)
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())

		Consistently(func(g Gomega) {
			rbac := apimeta.FindStatusCondition(getKarta(k.Name).Status.Conditions, "RBACReady")
			g.Expect(rbac).NotTo(BeNil())
			g.Expect(rbac.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(rbac.Reason).To(Equal("EWI"))
		}, 2*time.Second, eventuallyInterval).Should(Succeed())
	})

	It("flips CRDExists and Ready to True when the referenced CRD is installed", func() {
		gvk := schema.GroupVersionKind{Group: "install.run.ai", Version: "v1", Kind: "Gadget"}
		k := newValidKarta("envtest-crd-install", &gvk)
		Expect(k8sClient.Create(testCtx, k)).To(Succeed())

		Eventually(func(g Gomega) {
			got := getKarta(k.Name)
			crd := apimeta.FindStatusCondition(got.Status.Conditions, string(kartav1alpha1.ConditionCRDExists))
			g.Expect(crd).NotTo(BeNil())
			g.Expect(crd.Status).To(Equal(metav1.ConditionFalse))
			ready := apimeta.FindStatusCondition(got.Status.Conditions, string(kartav1alpha1.ConditionReady))
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(got.Labels[kartav1alpha1.LabelRootGroup]).To(Equal(gvk.Group))
			g.Expect(got.Labels[kartav1alpha1.LabelRootKind]).To(Equal(gvk.Kind))
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())

		crd := buildCRD("install.run.ai", "Gadget", "v1")
		Expect(k8sClient.Create(testCtx, crd)).To(Succeed())

		Eventually(func(g Gomega) {
			conds := getKarta(k.Name).Status.Conditions
			crdExists := apimeta.FindStatusCondition(conds, string(kartav1alpha1.ConditionCRDExists))
			g.Expect(crdExists).NotTo(BeNil())
			g.Expect(crdExists.Status).To(Equal(metav1.ConditionTrue))
			ready := apimeta.FindStatusCondition(conds, string(kartav1alpha1.ConditionReady))
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
	})

	It("flips CRDExists and Ready to False when the referenced CRD is removed", func() {
		crd := buildCRD("remove.run.ai", "Sprocket", "v1")
		Expect(k8sClient.Create(testCtx, crd)).To(Succeed())

		gvk := schema.GroupVersionKind{Group: "remove.run.ai", Version: "v1", Kind: "Sprocket"}
		k := newValidKarta("envtest-crd-remove", &gvk)
		Expect(k8sClient.Create(testCtx, k)).To(Succeed())

		Eventually(func(g Gomega) {
			ready := apimeta.FindStatusCondition(getKarta(k.Name).Status.Conditions, string(kartav1alpha1.ConditionReady))
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())

		Expect(k8sClient.Delete(testCtx, crd)).To(Succeed())

		Eventually(func(g Gomega) {
			conds := getKarta(k.Name).Status.Conditions
			crdExists := apimeta.FindStatusCondition(conds, string(kartav1alpha1.ConditionCRDExists))
			g.Expect(crdExists).NotTo(BeNil())
			g.Expect(crdExists.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(crdExists.Message).To(ContainSubstring("remove.run.ai"))
			ready := apimeta.FindStatusCondition(conds, string(kartav1alpha1.ConditionReady))
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
	})

	It("flips Validated and Ready to False when the spec becomes invalid", func() {
		crd := buildCRD("flip.run.ai", "Knob", "v1")
		Expect(k8sClient.Create(testCtx, crd)).To(Succeed())

		gvk := schema.GroupVersionKind{Group: "flip.run.ai", Version: "v1", Kind: "Knob"}
		k := newValidKarta("envtest-valid-to-invalid", &gvk)
		Expect(k8sClient.Create(testCtx, k)).To(Succeed())

		Eventually(func(g Gomega) {
			conds := getKarta(k.Name).Status.Conditions
			validated := apimeta.FindStatusCondition(conds, string(kartav1alpha1.ConditionValidated))
			g.Expect(validated).NotTo(BeNil())
			g.Expect(validated.Status).To(Equal(metav1.ConditionTrue))
			ready := apimeta.FindStatusCondition(conds, string(kartav1alpha1.ConditionReady))
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())

		updateKarta(k, func(got *kartav1alpha1.Karta) {
			got.Spec.StructureDefinition.RootComponent.StatusDefinition = nil
		})

		Eventually(func(g Gomega) {
			conds := getKarta(k.Name).Status.Conditions
			validated := apimeta.FindStatusCondition(conds, string(kartav1alpha1.ConditionValidated))
			g.Expect(validated).NotTo(BeNil())
			g.Expect(validated.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(validated.Reason).To(Equal(pkg.ReasonValidationFailed))
			ready := apimeta.FindStatusCondition(conds, string(kartav1alpha1.ConditionReady))
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
	})

	It("sets CRDExists=False when the CRD does not serve the referenced version", func() {
		crd := buildCRD("ver.run.ai", "Lever", "v1")
		Expect(k8sClient.Create(testCtx, crd)).To(Succeed())

		gvk := schema.GroupVersionKind{Group: "ver.run.ai", Version: "v2", Kind: "Lever"}
		k := newValidKarta("envtest-wrong-version", &gvk)
		Expect(k8sClient.Create(testCtx, k)).To(Succeed())

		Eventually(func(g Gomega) {
			conds := getKarta(k.Name).Status.Conditions
			validated := apimeta.FindStatusCondition(conds, string(kartav1alpha1.ConditionValidated))
			g.Expect(validated).NotTo(BeNil())
			g.Expect(validated.Status).To(Equal(metav1.ConditionTrue))
			crdExists := apimeta.FindStatusCondition(conds, string(kartav1alpha1.ConditionCRDExists))
			g.Expect(crdExists).NotTo(BeNil())
			g.Expect(crdExists.Status).To(Equal(metav1.ConditionFalse))
			ready := apimeta.FindStatusCondition(conds, string(kartav1alpha1.ConditionReady))
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
	})

	It("does not reconcile a Karta when an unrelated CRD changes", func() {
		gvkA := schema.GroupVersionKind{Group: "aaa.run.ai", Version: "v1", Kind: "Alpha"}
		kA := newValidKarta("envtest-selectivity-a", &gvkA)
		Expect(k8sClient.Create(testCtx, kA)).To(Succeed())

		gvkB := schema.GroupVersionKind{Group: "bbb.run.ai", Version: "v1", Kind: "Beta"}
		kB := newValidKarta("envtest-selectivity-b", &gvkB)
		Expect(k8sClient.Create(testCtx, kB)).To(Succeed())

		var baselineA metav1.Time
		Eventually(func(g Gomega) {
			readyA := apimeta.FindStatusCondition(getKarta(kA.Name).Status.Conditions, string(kartav1alpha1.ConditionReady))
			g.Expect(readyA).NotTo(BeNil())
			g.Expect(readyA.Status).To(Equal(metav1.ConditionFalse))
			readyB := apimeta.FindStatusCondition(getKarta(kB.Name).Status.Conditions, string(kartav1alpha1.ConditionReady))
			g.Expect(readyB).NotTo(BeNil())
			g.Expect(readyB.Status).To(Equal(metav1.ConditionFalse))
			baselineA = readyA.LastTransitionTime
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())

		crd := buildCRD("bbb.run.ai", "Beta", "v1")
		Expect(k8sClient.Create(testCtx, crd)).To(Succeed())

		Eventually(func(g Gomega) {
			ready := apimeta.FindStatusCondition(getKarta(kB.Name).Status.Conditions, string(kartav1alpha1.ConditionReady))
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())

		Consistently(func(g Gomega) {
			ready := apimeta.FindStatusCondition(getKarta(kA.Name).Status.Conditions, string(kartav1alpha1.ConditionReady))
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(ready.LastTransitionTime).To(Equal(baselineA))
		}, 2*time.Second, eventuallyInterval).Should(Succeed())
	})
})

func getKarta(name string) *kartav1alpha1.Karta {
	out := &kartav1alpha1.Karta{}
	Expect(k8sClient.Get(testCtx, client.ObjectKey{Name: name}, out)).To(Succeed())
	return out
}

func newKarta(name string, gvk *schema.GroupVersionKind) *kartav1alpha1.Karta {
	k := &kartav1alpha1.Karta{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	if gvk != nil {
		k.Spec.StructureDefinition.RootComponent.Name = name + "-root"
		k.Spec.StructureDefinition.RootComponent.Kind = &kartav1alpha1.GroupVersionKind{
			Group:   gvk.Group,
			Version: gvk.Version,
			Kind:    gvk.Kind,
		}
	}
	return k
}

func newValidKarta(name string, gvk *schema.GroupVersionKind) *kartav1alpha1.Karta {
	k := newKarta(name, gvk)
	if gvk != nil {
		k.Spec.StructureDefinition.RootComponent.StatusDefinition = &kartav1alpha1.StatusDefinition{
			StatusMappings: kartav1alpha1.StatusMappings{},
		}
	}
	return k
}

// updateKarta re-fetches the Karta and applies mutate, retrying on conflict
// until the update succeeds.
func updateKarta(k *kartav1alpha1.Karta, mutate func(*kartav1alpha1.Karta)) {
	Eventually(func() error {
		got := &kartav1alpha1.Karta{}
		if err := k8sClient.Get(testCtx, client.ObjectKeyFromObject(k), got); err != nil {
			return err
		}
		mutate(got)
		return k8sClient.Update(testCtx, got)
	}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
}

func buildCRD(group, kind, version string) *apiextensionsv1.CustomResourceDefinition {
	plural := strings.ToLower(kind) + "s"
	preserve := true
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name:   plural + "." + group,
			Labels: map[string]string{testCRDLabel: "true"},
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: group,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:   kind,
				Plural: plural,
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name:    version,
				Served:  true,
				Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
						Type:                   "object",
						XPreserveUnknownFields: &preserve,
					},
				},
			}},
		},
	}
}
