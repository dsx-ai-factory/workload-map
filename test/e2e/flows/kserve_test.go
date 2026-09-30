// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

var _ = Describe("KServe InferenceService", Ordered, Label("kserve"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/serving-kserve-io-inferenceservice-v1beta1.yaml", "serving-kserve-io-inferenceservice-v1beta1")
		fx = recorder.Fixture{Operator: "kserve", Version: operatorVersion("kserve"), KartaName: "serving-kserve-io-inferenceservice-v1beta1", KartaFile: "docs/catalog/serving-kserve-io-inferenceservice-v1beta1.yaml"}
		// The definition maps no Initializing for an InferenceService (the deploy window only reports
		// Unknown conditions), so the journeys start at the first mapped state and the deploy frames
		// stay recorded as Undefined.
		rec = recorder.New(cfg).
			SetTimeout(6*time.Minute).
			AddState(kartav1alpha1.RunningStatus, CondTrue("Ready")).
			AddState(kartav1alpha1.FailedStatus, CondsFalse("PredictorReady", "PredictorConfigurationReady", "RoutesReady"))
	})

	It("running", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "running", "testdata/kserve/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.RunningStatus)).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	// Custom predictor with a nonexistent-registry image: PredictorReady/PredictorConfigurationReady/
	// RoutesReady all False -> Failed.
	It("failed", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "failed", "testdata/kserve/failed.yaml").Through(
			recorder.Reaches(kartav1alpha1.FailedStatus)).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})
