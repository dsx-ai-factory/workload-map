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

var _ = Describe("DynamoGraphDeployment", Ordered, Label("dynamo"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/nvidia-com-dynamographdeployment-v1alpha1.yaml", "nvidia-com-dynamographdeployment-v1alpha1")
		// The decode worker pulls env from hf-token-secret; the operator reads it from the workload's own
		// namespace, so seed it here (up.sh only creates it in default).
		ensureSecret(ctx, "hf-token-secret", map[string]string{"HF_TOKEN": "dummy"})
		fx = recorder.Fixture{Operator: "dynamo", Version: operatorVersion("dynamo"), KartaName: "nvidia-com-dynamographdeployment-v1alpha1", KartaFile: "docs/catalog/nvidia-com-dynamographdeployment-v1alpha1.yaml"}
		rec = recorder.New(cfg).
			SetTimeout(8*time.Minute).
			AddState(kartav1alpha1.InitializingStatus, PhaseAny([]string{"initializing", "pending"}, "status", "state")).
			AddState(kartav1alpha1.RunningStatus, PhaseEq("successful", "status", "state"))
	})

	It("running", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "running", "testdata/dynamo/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("initializing", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "initializing", "testdata/dynamo/initializing.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus)).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})
