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

var _ = Describe("RayCluster", Ordered, Label("kuberay", "raycluster"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/ray-io-raycluster-v1.yaml", "ray-io-raycluster-v1")
		fx = recorder.Fixture{Operator: "kuberay", Version: operatorVersion("kuberay"), KartaName: "ray-io-raycluster-v1", KartaFile: "docs/catalog/ray-io-raycluster-v1.yaml"}
		// The definition maps no Initializing for a RayCluster (bring-up only reports False
		// conditions and an empty state), so the journeys start at the first mapped state and the
		// provisioning frames stay recorded as Undefined.
		rec = recorder.New(cfg).
			SetTimeout(8*time.Minute).
			AddState(kartav1alpha1.RunningStatus, PhaseEq("ready", "status", "state")).
			AddState(kartav1alpha1.SuspendedStatus, RaySuspended())
	})

	It("running", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "running", "testdata/raycluster/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.RunningStatus)).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("suspended", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "suspended", "testdata/raycluster/suspended.yaml").Through(recorder.Reaches(kartav1alpha1.SuspendedStatus)).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("suspended-from-running", func(ctx SpecContext) {
		// KubeRay keeps status.state ready while it deletes the head pod, so those frames read
		// Running, and reports suspended once the pod is gone. The last step is gated on that
		// status.state: the suspended mapping also accepts an absent state, so spec.suspend=true
		// alone must not be able to end the recording.
		out, err := recorder.NewFlow(rec, "suspended-from-running", "testdata/raycluster/suspended-from-running.yaml").Through(
			recorder.Reaches(kartav1alpha1.RunningStatus).Do(Suspend()),
			recorder.Reaches(kartav1alpha1.SuspendedStatus).With(PhaseEq("suspended", "status", "state")),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("resumed", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "resumed", "testdata/raycluster/resumed.yaml").Through(
			recorder.Reaches(kartav1alpha1.SuspendedStatus).Do(Resume()),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})
