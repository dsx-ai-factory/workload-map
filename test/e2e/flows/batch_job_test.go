// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

var _ = Describe("BatchJob (built-in)", Ordered, Label("batch-job", "builtin"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/batch-job-v1.yaml", "batch-job-v1")
		fx = recorder.Fixture{Operator: "batch-job", Version: operatorVersion("batch-job"), KartaName: "batch-job-v1", KartaFile: "docs/catalog/batch-job-v1.yaml"}
		rec = recorder.New(cfg).
			AddState(kartav1alpha1.SuspendedStatus, CondTrue("Suspended")).
			AddState(kartav1alpha1.InitializingStatus, IntAtLeast(1, "status", "active")).
			AddState(kartav1alpha1.RunningStatus, IntAtLeast(1, "status", "ready")).
			AddState(kartav1alpha1.CompletedStatus, CondTrue("Complete", "SuccessCriteriaMet")).
			AddState(kartav1alpha1.FailedStatus, CondTrue("Failed", "FailureTarget")).
			AddState(kartav1alpha1.DegradedStatus, JobDegraded())
	})

	It("running", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "running", "testdata/batch-job/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("completed", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "completed", "testdata/batch-job/completed.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus),
			recorder.Reaches(kartav1alpha1.InitializingStatus), // active-not-ready dip as the pod terminates
			recorder.Reaches(kartav1alpha1.CompletedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("failed", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "failed", "testdata/batch-job/failed.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.FailedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("resumed", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "resumed", "testdata/batch-job/resumed.yaml").Through(
			recorder.Reaches(kartav1alpha1.SuspendedStatus).Do(Resume()),
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus),
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.CompletedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("degraded", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "degraded", "testdata/batch-job/degraded.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus),
			recorder.Reaches(kartav1alpha1.DegradedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("suspended", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "suspended", "testdata/batch-job/suspended.yaml").
			Through(recorder.Reaches(kartav1alpha1.SuspendedStatus)).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("scaled", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "scaled", "testdata/batch-job/scaled.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(IntEq(1, "status", "ready")).Do(ScaleParallelism(3)),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(IntEq(3, "status", "ready")).Do(ScaleParallelism(1)),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(IntEq(1, "status", "ready")),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})
