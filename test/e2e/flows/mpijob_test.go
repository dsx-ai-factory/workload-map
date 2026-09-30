// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

var _ = Describe("MPIJob", Ordered, Label("kubeflow", "mpijob"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/kubeflow-org-mpijob-v2beta1.yaml", "kubeflow-org-mpijob-v2beta1")
		fx = recorder.Fixture{Operator: "kubeflow", Version: operatorVersion("kubeflow"), KartaName: "kubeflow-org-mpijob-v2beta1", KartaFile: "docs/catalog/kubeflow-org-mpijob-v2beta1.yaml"}
		rec = recorder.New(cfg).
			AddState(kartav1alpha1.InitializingStatus, CondTrue("Created")).
			AddState(kartav1alpha1.RunningStatus, CondTrue("Running")).
			AddState(kartav1alpha1.CompletedStatus, CondTrue("Succeeded")).
			AddState(kartav1alpha1.FailedStatus, CondTrue("Failed")).
			AddState(kartav1alpha1.SuspendedStatus, CondTrue("Suspended"))
	})

	It("running", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "running", "testdata/mpijob/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	// The launcher can finish before Running is observed (Optional), and Kubeflow keeps Created set so the
	// CR reads Initializing again for a tick before the terminal.
	It("completed", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "completed", "testdata/mpijob/completed.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus).Optional(),
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.CompletedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("failed", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "failed", "testdata/mpijob/failed.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus).Optional(),
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.FailedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("suspended", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "suspended", "testdata/mpijob/suspended.yaml").
			Through(recorder.Reaches(kartav1alpha1.SuspendedStatus)).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("resumed", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "resumed", "testdata/mpijob/resumed.yaml").Through(
			recorder.Reaches(kartav1alpha1.SuspendedStatus).Do(ResumeRunPolicy()),
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})
