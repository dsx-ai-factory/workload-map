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

var _ = Describe("CloneSet", Ordered, Label("openkruise", "cloneset"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/apps-kruise-io-cloneset-v1alpha1.yaml", "apps-kruise-io-cloneset-v1alpha1")
		fx = recorder.Fixture{Operator: "openkruise", Version: operatorVersion("openkruise"), KartaName: "apps-kruise-io-cloneset-v1alpha1", KartaFile: "docs/catalog/apps-kruise-io-cloneset-v1alpha1.yaml"}
		rec = recorder.New(cfg).
			SetTimeout(5*time.Minute).
			AddState(kartav1alpha1.InitializingStatus, AllOf(
				IntAtLeast(1, "status", "observedGeneration"),
				NotFullyAvailable(),
			)).
			AddState(kartav1alpha1.RunningStatus, AllOf(
				IntAtLeast(1, "status", "observedGeneration"),
				GenerationCurrent(),
				FullyAvailable(),
				AvailableReady(),
				RevisionsCurrent(),
			)).
			AddState(kartav1alpha1.RunningStatus, AllOf(
				IntAtLeast(1, "status", "observedGeneration"),
				GenerationCurrent(),
				PartitionReady(),
			)).
			AddState(kartav1alpha1.RunningStatus, AllOf(
				IntAtLeast(1, "status", "observedGeneration"),
				GenerationCurrent(),
				PausedReady(),
			))
	})

	It("running", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "running", "testdata/cloneset/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(1)),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("scaled", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "scaled", "testdata/cloneset/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(1)).Do(ScaleReplicas(3)),
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(3)).Do(ScaleReplicas(1)),
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(1)).Do(ScaleReplicas(0)),
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(), // draining toward zero
			recorder.Reaches(kartav1alpha1.RunningStatus).With(IntEq(0, "spec", "replicas")).Do(ScaleReplicas(1)),
			recorder.Reaches(kartav1alpha1.InitializingStatus), // coming back up
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(1)),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("updated", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "updated", "testdata/cloneset/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(1)).Do(RolloutImage(
				`{"name":"worker","image":"busybox:1.37","command":["sh","-c","sleep infinity"],"resources":{"requests":{"cpu":"20m","memory":"32Mi"},"limits":{"cpu":"20m","memory":"32Mi"}}}`,
			)),
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(1)),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("stuck", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "stuck", "testdata/cloneset/stuck.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).With(AllOf(
				IntAtLeast(1, "status", "replicas"),
				IntAtLeast(1, "status", "updatedReplicas"),
				IntEq(0, "status", "readyReplicas"),
			)),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("unready", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "unready", "testdata/cloneset/unready.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).With(AllOf(IntAtLeast(1, "status", "replicas"), IntEq(0, "status", "readyReplicas"))),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("partition", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "partition", "testdata/cloneset/partition.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(3)).Do(PartitionRollout(2,
				`{"name":"worker","image":"busybox:1.37","command":["sh","-c","sleep infinity"],"resources":{"requests":{"cpu":"20m","memory":"32Mi"},"limits":{"cpu":"20m","memory":"32Mi"}}}`,
			)),
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(PartitionReady()),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("paused", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "paused", "testdata/cloneset/paused.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(1)).Do(PauseRollout(
				`{"name":"worker","image":"busybox:1.37","command":["sh","-c","sleep infinity"],"resources":{"requests":{"cpu":"20m","memory":"32Mi"},"limits":{"cpu":"20m","memory":"32Mi"}}}`,
			)),
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(PausedReady()),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})
