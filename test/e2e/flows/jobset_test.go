// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

var _ = Describe("JobSet", Ordered, Label("jobset"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/jobset-x-k8s-io-jobset-v1alpha2.yaml", "jobset-x-k8s-io-jobset-v1alpha2")
		// Suspended first so a lingering Suspended condition never masks real progress after a resume.
		fx = recorder.Fixture{Operator: "jobset", Version: operatorVersion("jobset"), KartaName: "jobset-x-k8s-io-jobset-v1alpha2", KartaFile: "docs/catalog/jobset-x-k8s-io-jobset-v1alpha2.yaml"}
		rec = recorder.New(cfg).
			AddState(kartav1alpha1.SuspendedStatus, CondTrue("Suspended")).
			AddState(kartav1alpha1.InitializingStatus, JobsetInitializing()).
			AddState(kartav1alpha1.RunningStatus, JobsetRunning()).
			AddState(kartav1alpha1.CompletedStatus, CondTrue("Completed")).
			AddState(kartav1alpha1.FailedStatus, CondTrue("Failed"))
	})

	// A fast JobSet can go ready before the bring-up window (active pods, none ready) produces a
	// frame, so Initializing is Optional everywhere.
	It("running", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "running", "testdata/jobset/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("completed", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "completed", "testdata/jobset/completed.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus),
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.CompletedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("failed", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "failed", "testdata/jobset/failed.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).Optional(),
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.FailedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("resumed", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "resumed", "testdata/jobset/resumed.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.SuspendedStatus).Do(Resume()),
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("suspended", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "suspended", "testdata/jobset/suspended.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.SuspendedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})
