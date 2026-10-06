// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

var _ = Describe("Pod (built-in)", Ordered, Label("pod", "builtin"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/core-pod-v1.yaml", "core-pod-v1")
		fx = recorder.Fixture{Operator: "pod", Version: operatorVersion("pod"), KartaName: "core-pod-v1", KartaFile: "docs/catalog/core-pod-v1.yaml"}
		rec = recorder.New(cfg).
			AddState(kartav1alpha1.InitializingStatus, PhaseEq("Pending", "status", "phase")).
			AddState(kartav1alpha1.RunningStatus, PhaseEq("Running", "status", "phase")).
			AddState(kartav1alpha1.CompletedStatus, PhaseEq("Succeeded", "status", "phase")).
			AddState(kartav1alpha1.FailedStatus, PhaseEq("Failed", "status", "phase"))
	})

	It("running", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "running", "testdata/pod/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).NotTo(HaveOccurred(), "run pod flow running")
	})

	It("completed", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "completed", "testdata/pod/completed.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus),
			recorder.Reaches(kartav1alpha1.CompletedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).NotTo(HaveOccurred(), "run pod flow completed")
	})

	It("failed", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "failed", "testdata/pod/failed.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus),
			recorder.Reaches(kartav1alpha1.FailedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).NotTo(HaveOccurred(), "run pod flow failed")
	})

	It("initializing", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "initializing", "testdata/pod/initializing.yaml").
			Through(recorder.Reaches(kartav1alpha1.InitializingStatus)).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).NotTo(HaveOccurred(), "run pod flow initializing")
	})
})
