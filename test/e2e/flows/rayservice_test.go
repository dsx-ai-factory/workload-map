// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

var _ = Describe("RayService", Ordered, Label("kuberay", "rayservice"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/ray-io-rayservice-v1.yaml", "ray-io-rayservice-v1")
		app, err := os.ReadFile("testdata/rayservice/serve_app.py")
		Expect(err).NotTo(HaveOccurred())
		config := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "karta-e2e-serve-app", Namespace: testNamespace},
			Data:       map[string]string{"serve_app.py": string(app)},
		}
		Expect(k8sClient.Create(ctx, config)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { _ = k8sClient.Delete(ctx, config) })
		fx = recorder.Fixture{Operator: "kuberay", Version: operatorVersion("kuberay"), KartaName: "ray-io-rayservice-v1", KartaFile: "docs/catalog/ray-io-rayservice-v1.yaml"}
		rec = recorder.New(cfg).
			SetTimeout(12*time.Minute).
			AddState(kartav1alpha1.ProgressingStatus, rayServiceProgressing).
			AddState(kartav1alpha1.PendingStatus, rayServicePending).
			AddState(kartav1alpha1.RunningStatus, CondTrue("Ready")).
			AddState(kartav1alpha1.DegradedStatus, operatorConditionReason("Ready", "False", "ZeroServeEndpoints")).
			AddState(kartav1alpha1.FailedStatus, rayServiceFailed)
	})

	It("running", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "running", "testdata/rayservice/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.PendingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("upgraded", func(ctx SpecContext) {
		var originalCluster string
		out, err := recorder.NewFlow(rec, "upgraded", "testdata/rayservice/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.PendingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(func(u *unstructured.Unstructured) bool {
				originalCluster, _, _ = unstructured.NestedString(u.Object, "status", "activeServiceStatus", "rayClusterName")
				return originalCluster != "" && !rayServiceProgressing(u)
			}).Do(&recorder.Action{Type: "Upgrade", Patch: []byte(`{"spec":{"rayClusterConfig":{"headGroupSpec":{"template":{"metadata":{"annotations":{"karta-e2e-upgrade":"replacement"}}}}}}}`)}),
			// This gate requires a real frame with both Running and Progressing before completion.
			recorder.Reaches(kartav1alpha1.RunningStatus).With(AllOf(rayServiceProgressing, CondTrue("UpgradeInProgress"))),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(func(u *unstructured.Unstructured) bool {
				active, _, _ := unstructured.NestedString(u.Object, "status", "activeServiceStatus", "rayClusterName")
				pending, _, _ := unstructured.NestedString(u.Object, "status", "pendingServiceStatus", "rayClusterName")
				return active != "" && active != originalCluster && pending == "" && !rayServiceProgressing(u)
			}),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})

func rayServiceFailed(u *unstructured.Unstructured) bool {
	return operatorConditionReason("Ready", "False", "InitializingTimeout", "ValidationFailed")(u)
}

func rayServiceCanTransition(u *unstructured.Unstructured) bool {
	return notDeleting(u) && !BoolTrue("spec", "suspend")(u) && operatorCurrentObservation(u) &&
		!CondTrue("Suspending", "Suspended")(u) && !rayServiceFailed(u)
}

func rayServicePending(u *unstructured.Unstructured) bool {
	if rayServiceCanTransition(u) && operatorConditionReason("Ready", "False", "Initializing")(u) {
		return true
	}
	status, _, err := unstructured.NestedMap(u.Object, "status")
	cluster, hasCluster, _ := unstructured.NestedMap(u.Object, "spec", "rayClusterConfig")
	serve, _, _ := unstructured.NestedString(u.Object, "spec", "serveConfigV2")
	return notDeleting(u) && u.GetGeneration() > 0 && !BoolTrue("spec", "suspend")(u) &&
		hasCluster && cluster != nil && serve != "" && err == nil && len(status) == 0
}

func rayServiceProgressing(u *unstructured.Unstructured) bool {
	if !rayServiceCanTransition(u) {
		return false
	}
	pending, _, _ := unstructured.NestedString(u.Object, "status", "pendingServiceStatus", "rayClusterName")
	active, _, _ := unstructured.NestedString(u.Object, "status", "activeServiceStatus", "rayClusterName")
	return CondTrue("UpgradeInProgress", "RollbackInProgress")(u) || ((pending != "" || active != "") && rayServicePending(u))
}
