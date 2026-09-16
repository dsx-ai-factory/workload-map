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

var _ = Describe("NIMService", Ordered, Label("nim"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/apps-nvidia-com-nimservice-v1alpha1.yaml", "apps-nvidia-com-nimservice-v1alpha1")
		// The NIMService references authSecret ngc-secret; the operator reads it from the workload's own
		// namespace, so seed it here (up.sh only creates it in default).
		ensureSecret(ctx, "ngc-secret", map[string]string{"NGC_API_KEY": "dummy-not-a-real-token"})
		fx = recorder.Fixture{Operator: "nim", Version: operatorVersion("nim"), KartaName: "apps-nvidia-com-nimservice-v1alpha1", KartaFile: "docs/catalog/apps-nvidia-com-nimservice-v1alpha1.yaml"}
		rec = recorder.New(cfg).
			SetTimeout(5*time.Minute).
			AddState(kartav1alpha1.InitializingStatus, PhaseAny([]string{"NotReady", "Pending"}, "status", "state")).
			AddState(kartav1alpha1.RunningStatus, PhaseEq("Ready", "status", "state"))
	})

	It("running", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "running", "testdata/nim/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("initializing", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "initializing", "testdata/nim/initializing.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus)).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})
