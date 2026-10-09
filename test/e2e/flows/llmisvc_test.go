// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

// llmStartupReasons mirrors the definition's list of Ready=False reasons the controller reports while it
// converges: the Deployment progressing or not yet available, the router waiting on the Gateway API
// objects, the autoscaler objects appearing, and a LeaderWorkerSet rolling.
var llmStartupReasons = []string{
	"Progressing", "MinimumReplicasUnavailable", "WaitingForGateway", "GatewaysNotReady",
	"HTTPRoutesNotReady", "InferencePoolNotReady", "HPAProgressing", "ScaledObjectProgressing", "AllGroupsReady",
}

// llmStopReason is the reason the controller writes once the serving.kserve.io/stop annotation is set.
const llmStopReason = "Stopped"

// readyFalseReason matches Ready=False whose reason is (or is not) one of the given reasons.
func readyFalseReason(within bool, reasons ...string) recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
		for _, c := range conds {
			m, ok := c.(map[string]any)
			if !ok || m["type"] != "Ready" || m["status"] != "False" {
				continue
			}
			for _, r := range reasons {
				if m["reason"] == r {
					return within
				}
			}
			return !within
		}
		return false
	}
}

// condWithReason matches a condition of the given type and status whose reason is (or is not) reason.
func condWithReason(condType, status, reason string, equal bool) recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
		for _, c := range conds {
			if m, ok := c.(map[string]any); ok && m["type"] == condType && m["status"] == status {
				return (m["reason"] == reason) == equal
			}
		}
		return false
	}
}

var _ = Describe("LLMInferenceService", Ordered, Label("kserve", "llmisvc"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/serving-kserve-io-llminferenceservice-v1alpha2.yaml", "serving-kserve-io-llminferenceservice-v1alpha2")
		// The manifests use a pvc:// model URI: the controller mounts the claim
		// read-only instead of spawning a storage-initializer that would try to
		// download a real model. The claim stays empty; the fake server never
		// reads it.
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "karta-e2e-llmisvc-model", Namespace: testNamespace},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("16Mi")},
				},
			},
		}
		Expect(k8sClient.Create(ctx, pvc)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { _ = k8sClient.Delete(ctx, pvc) })
		fx = recorder.Fixture{Operator: "kserve", Version: operatorVersion("kserve"), KartaName: "serving-kserve-io-llminferenceservice-v1alpha2", KartaFile: "docs/catalog/serving-kserve-io-llminferenceservice-v1alpha2.yaml"}
		// The predicates mirror the definition. PresetsCombined sits outside the Ready rollup: when it is
		// False the controller stops reconciling and Ready stays Unknown, which is a failure unless the
		// service is stopped.
		presetsFalse := CondStatus("PresetsCombined", "False")
		initializing := func(u *unstructured.Unstructured) bool {
			return (CondStatus("Ready", "Unknown")(u) && !presetsFalse(u)) || readyFalseReason(true, llmStartupReasons...)(u)
		}
		suspended := func(u *unstructured.Unstructured) bool {
			return condWithReason("Ready", "False", llmStopReason, true)(u) ||
				condWithReason("PresetsCombined", "False", llmStopReason, true)(u)
		}
		failed := func(u *unstructured.Unstructured) bool {
			return readyFalseReason(false, append([]string{llmStopReason}, llmStartupReasons...)...)(u) ||
				(CondStatus("Ready", "Unknown")(u) && condWithReason("PresetsCombined", "False", llmStopReason, false)(u))
		}
		rec = recorder.New(cfg).
			SetTimeout(6*time.Minute).
			AddState(kartav1alpha1.InitializingStatus, initializing).
			AddState(kartav1alpha1.RunningStatus, CondTrue("Ready")).
			AddState(kartav1alpha1.SuspendedStatus, suspended).
			AddState(kartav1alpha1.FailedStatus, failed)
	})

	It("running", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "running", "testdata/llmisvc/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("failed", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "failed", "testdata/llmisvc/failed.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.FailedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("stopped", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "stopped", "testdata/llmisvc/stopped.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).Do(KServeStop()),
			recorder.Reaches(kartav1alpha1.SuspendedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("missing-preset", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "missing-preset", "testdata/llmisvc/missing-preset.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.FailedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})
