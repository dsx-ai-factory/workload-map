// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

var _ = Describe("StatefulSet (built-in)", Ordered, Label("statefulset", "builtin"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/apps-statefulset-v1.yaml", "apps-statefulset-v1")
		fx = recorder.Fixture{Operator: "statefulset", Version: operatorVersion("statefulset"), KartaName: "apps-statefulset-v1", KartaFile: "docs/catalog/apps-statefulset-v1.yaml"}
		rec = recorder.New(cfg).
			AddState(kartav1alpha1.InitializingStatus, ReplicasInitializing()).
			AddState(kartav1alpha1.RunningStatus, FullyAvailable()).
			AddState(kartav1alpha1.DegradedStatus, ReplicasDegraded())
	})

	// A StatefulSet takes transient Initializing/Degraded dips while scaling; the Optional steps declare them
	// so the order check tolerates them, and the recorder only stops at the ReplicasReady gates.
	It("scaled", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "scaled", "testdata/statefulset/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(1)).Do(ScaleReplicas(3)),
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.DegradedStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(3)).Do(ScaleReplicas(1)),
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(1)),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	// 3 replicas + hostname antiAffinity on 2 nodes: one stays pending, so readyReplicas settles below
	// replicas with updatedReplicas == replicas, read as Degraded.
	It("degraded", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "degraded", "testdata/statefulset/degraded.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.DegradedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})
