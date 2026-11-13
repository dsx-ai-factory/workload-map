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

var _ = Describe("Rollout", Ordered, Label("argo-rollouts", "rollout"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/argoproj-io-rollout-v1alpha1.yaml", "argoproj-io-rollout-v1alpha1")
		fx = recorder.Fixture{Operator: "argo-rollouts", Version: operatorVersion("argo-rollouts"), KartaName: "argoproj-io-rollout-v1alpha1", KartaFile: "docs/catalog/argoproj-io-rollout-v1alpha1.yaml"}
		// 5m: canary rollouts create new ReplicaSets and wait for readiness probes.
		runningCheck := AllOf(
			PhaseEq("Healthy", "status", "phase"),
			StringGenerationCurrent(),
			BoolFalseOrAbsent("spec", "paused"),
		)
		degradedCheck := AllOf(
			PhaseEq("Degraded", "status", "phase"),
			StringGenerationCurrent(),
		)
		suspendedCheck := AllOf(
			BoolTrue("spec", "paused"),
			PhaseExclude("Degraded", "status", "phase"),
		)
		rec = recorder.New(cfg).
			SetTimeout(5*time.Minute).
			AddState(kartav1alpha1.InitializingStatus, NoneMatch(runningCheck, degradedCheck, suspendedCheck)).
			AddState(kartav1alpha1.SuspendedStatus, suspendedCheck).
			AddState(kartav1alpha1.RunningStatus, runningCheck).
			AddState(kartav1alpha1.DegradedStatus, degradedCheck)
	})

	// Scaling may enter Progressing between each settled replica count, unlike Deployment
	// which stays Running while scaling; the recording shows scale-down staying Healthy.
	It("scaled", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "scaled", "testdata/rollout/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(), // initial rollout
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(1)).Do(ScaleReplicas(3)),
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(), // scale-up rollout
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(3)).Do(ScaleReplicas(1)),
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(), // scale-down rollout
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(1)),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("updated", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "updated", "testdata/rollout/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(), // initial rollout can settle before first poll
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(1)).Do(RolloutImage(
				`{"name":"worker","image":"busybox:1.37","command":["sh","-c","sleep infinity"],"resources":{"requests":{"cpu":"20m","memory":"32Mi"},"limits":{"cpu":"20m","memory":"32Mi"}}}`,
			)),
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(1)),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("degraded", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "degraded", "testdata/rollout/degraded.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(), // initial rollout can settle before first poll
			recorder.Reaches(kartav1alpha1.DegradedStatus).Do(RolloutImage(
				`{"name":"worker","image":"busybox:1.36","command":["sh","-c","sleep infinity"],"resources":{"requests":{"cpu":"20m","memory":"32Mi"},"limits":{"cpu":"20m","memory":"32Mi"}}}`,
			)),
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(), // recovery rollout can settle before first poll
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	// Canary timed pause: the initial deploy skips canary steps; the image update triggers the
	// pause step, which holds the Rollout at phase=Paused for 10s before auto-resuming.
	It("canary-pause", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "canary-pause", "testdata/rollout/canary-pause.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(), // initial rollout can settle before first poll
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(1)).Do(RolloutImage(
				`{"name":"worker","image":"busybox:1.37","command":["sh","-c","sleep infinity"],"resources":{"requests":{"cpu":"20m","memory":"32Mi"},"limits":{"cpu":"20m","memory":"32Mi"}}}`,
			)),
			recorder.Reaches(kartav1alpha1.InitializingStatus), // canary pause step (phase=Paused, spec.paused=false)
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(1)),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	// Suspend and resume via spec.paused, the same field kubectl argo rollouts pause/resume sets.
	It("suspended", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "suspended", "testdata/rollout/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(), // initial rollout can settle before first poll
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(1)).Do(SuspendPaused()),
			recorder.Reaches(kartav1alpha1.SuspendedStatus).Do(ResumePaused()),
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(), // resume can trigger brief Progressing
			recorder.Reaches(kartav1alpha1.RunningStatus).With(AllOf(
				ReplicasReady(1),
				StringGenerationCurrent(), // generation bumps from spec.paused changes
			)),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})
