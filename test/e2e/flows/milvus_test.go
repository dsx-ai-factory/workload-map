// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

var _ = Describe("Milvus", Ordered, Label("milvus"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/milvus-io-milvus-v1beta1.yaml", "milvus-io-milvus-v1beta1")
		fx = recorder.Fixture{Operator: "milvus", Version: operatorVersion("milvus"), KartaName: "milvus-io-milvus-v1beta1", KartaFile: "docs/catalog/milvus-io-milvus-v1beta1.yaml"}
		rec = recorder.New(cfg).
			SetTimeout(8*time.Minute).
			AddState(kartav1alpha1.ProgressingStatus, MilvusProgressing()).
			AddState(kartav1alpha1.PendingStatus, MilvusPending()).
			AddState(kartav1alpha1.RunningStatus, PhaseEq("Healthy", "status", "status")).
			AddState(kartav1alpha1.DegradedStatus, PhaseEq("Unhealthy", "status", "status"))
	})

	It("running", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "running", "testdata/milvus/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.PendingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("pending", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "pending", "testdata/milvus/pending.yaml").Through(
			recorder.Reaches(kartav1alpha1.PendingStatus).With(AllOf(operatorCurrentObservation, PhaseEq("Pending", "status", "status")))).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})
