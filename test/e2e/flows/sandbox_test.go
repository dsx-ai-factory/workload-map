// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

var _ = Describe("Sandbox", Ordered, Label("agent-sandbox", "sandbox"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/agents-x-k8s-io-sandbox-v1beta1.yaml", "agents-x-k8s-io-sandbox-v1beta1")
		fx = recorder.Fixture{Operator: "agent-sandbox", Version: operatorVersion("agent-sandbox"), KartaName: "agents-x-k8s-io-sandbox-v1beta1", KartaFile: "docs/catalog/agents-x-k8s-io-sandbox-v1beta1.yaml"}
		// Each state pairs the requested spec.operatingMode with the controller's conditions, so a
		// condition the controller has not caught up on yet is recorded as the transition it is.
		rec = recorder.New(cfg).
			AddState(kartav1alpha1.InitializingStatus, AllOf(SandboxMode("Running"), CondFalse("Ready"), CondNotTrue("Suspended"))).
			AddState(kartav1alpha1.RunningStatus, AllOf(SandboxMode("Running"), CondTrue("Ready"))).
			AddState(kartav1alpha1.SuspendingStatus, AllOf(SandboxMode("Suspended"), CondNotTrue("Suspended"))).
			AddState(kartav1alpha1.SuspendedStatus, AllOf(SandboxMode("Suspended"), CondTrue("Suspended"))).
			AddState(kartav1alpha1.ResumingStatus, AllOf(SandboxMode("Running"), CondTrue("Suspended")))
	})

	It("running", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "running", "testdata/sandbox/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("suspended", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "suspended", "testdata/sandbox/suspended.yaml").Through(
			recorder.Reaches(kartav1alpha1.SuspendedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("resumed", func(ctx SpecContext) {
		// The controller reports each step: suspending while the pod is deleted, then resuming while
		// the Suspended condition is still set after the mode returns to Running.
		out, err := recorder.NewFlow(rec, "resumed", "testdata/sandbox/resumed.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus).Do(SuspendSandbox()),
			recorder.Reaches(kartav1alpha1.SuspendingStatus),
			recorder.Reaches(kartav1alpha1.SuspendedStatus).Do(ResumeSandbox()),
			recorder.Reaches(kartav1alpha1.ResumingStatus),
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})
