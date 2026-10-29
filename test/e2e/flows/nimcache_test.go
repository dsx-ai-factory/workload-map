// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

var _ = Describe("NIMCache", Ordered, Label("nim", "nimcache"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/apps-nvidia-com-nimcache-v1alpha1.yaml", "apps-nvidia-com-nimcache-v1alpha1")
		ensureSecret(ctx, "nimcache-auth", map[string]string{"NGC_API_KEY": "dummy-not-a-real-token"})
		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "nimcache-pull", Namespace: testNamespace},
			Type:       corev1.SecretTypeDockerConfigJson,
			StringData: map[string]string{corev1.DockerConfigJsonKey: `{"auths":{}}`},
		}
		Expect(k8sClient.Create(ctx, pullSecret)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { _ = k8sClient.Delete(ctx, pullSecret) })
		fx = recorder.Fixture{Operator: "nim", Version: operatorVersion("nim"), KartaName: "apps-nvidia-com-nimcache-v1alpha1", KartaFile: "docs/catalog/apps-nvidia-com-nimcache-v1alpha1.yaml"}
		rec = recorder.New(cfg).
			SetTimeout(5*time.Minute).
			AddState(kartav1alpha1.ProgressingStatus, nimCacheProgressing).
			AddState(kartav1alpha1.PendingStatus, nimCachePending).
			// The native InProgress state means Job.status.active > 0, which can include Pending Pods.
			AddState(kartav1alpha1.RunningStatus, PhaseEq("InProgress", "status", "state")).
			AddState(kartav1alpha1.CompletedStatus, AllOf(PhaseEq("Ready", "status", "state"), CondTrue("NIM_CACHE_JOB_COMPLETED"))).
			AddState(kartav1alpha1.FailedStatus, PhaseEq("Failed", "status", "state"))
	})

	It("completed", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "completed", "testdata/nimcache/completed.yaml").Through(
			recorder.Reaches(kartav1alpha1.PendingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).Optional(),
			recorder.Reaches(kartav1alpha1.PendingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.CompletedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("failed", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "failed", "testdata/nimcache/failed.yaml").Through(
			recorder.Reaches(kartav1alpha1.PendingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).Optional(),
			recorder.Reaches(kartav1alpha1.PendingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.FailedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})
