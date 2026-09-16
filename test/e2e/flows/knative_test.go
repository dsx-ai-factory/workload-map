// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kartav1alpha1 "github.com/run-ai/karta/pkg/api/runai/v1alpha1"
	"github.com/run-ai/karta/test/e2e/recorder"
)

var _ = Describe("KnativeService", Ordered, Label("knative"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/serving-knative-dev-service-v1.yaml", "serving-knative-dev-service-v1")
		fx = recorder.Fixture{Operator: "knative", Version: operatorVersion("knative"), KartaName: "serving-knative-dev-service-v1", KartaFile: "docs/catalog/serving-knative-dev-service-v1.yaml"}
		rec = recorder.New(cfg).
			SetTimeout(5*time.Minute).
			AddState(kartav1alpha1.InitializingStatus, CondStatus("Ready", "Unknown")).
			AddState(kartav1alpha1.RunningStatus, CondTrue("Ready"))
	})

	It("running", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "running", "testdata/knative/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})
