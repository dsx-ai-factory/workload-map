// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

var _ = Describe("VcJob", Ordered, Label("volcano", "vcjob"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/batch-volcano-sh-job-v1alpha1.yaml", "batch-volcano-sh-job-v1alpha1")
		fx = recorder.Fixture{Operator: "volcano", Version: operatorVersion("volcano"), KartaName: "batch-volcano-sh-job-v1alpha1", KartaFile: "docs/catalog/batch-volcano-sh-job-v1alpha1.yaml"}
		rec = recorder.New(cfg).
			SetTimeout(4*time.Minute).
			AddState(kartav1alpha1.InitializingStatus, vcjobInitializing()).
			AddState(kartav1alpha1.RunningStatus, PhaseEq("Running", "status", "state", "phase")).
			AddState(kartav1alpha1.CompletedStatus, PhaseAny([]string{"Completing", "Completed"}, "status", "state", "phase")).
			AddState(kartav1alpha1.FailedStatus, vcjobFailed()).
			AddState(kartav1alpha1.SuspendingStatus, PhaseEq("Aborting", "status", "state", "phase")).
			AddState(kartav1alpha1.SuspendedStatus, PhaseEq("Aborted", "status", "state", "phase"))
	})

	It("running", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "running", "testdata/vcjob/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("completed", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "completed", "testdata/vcjob/completed.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			// Running may be skipped when the task completes before the controller records the phase.
			recorder.Reaches(kartav1alpha1.RunningStatus).Optional(),
			recorder.Reaches(kartav1alpha1.CompletedStatus).With(PhaseEq("Completed", "status", "state", "phase")),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("failed", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "failed", "testdata/vcjob/failed.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			// Running may be skipped when the task fails before the controller records the phase.
			recorder.Reaches(kartav1alpha1.RunningStatus).Optional(),
			recorder.Reaches(kartav1alpha1.FailedStatus).With(PhaseEq("Failed", "status", "state", "phase")),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("restarting", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "restarting", "testdata/vcjob/restarting.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).With(PhaseEq("Pending", "status", "state", "phase")),
			recorder.Reaches(kartav1alpha1.RunningStatus).Optional(),
			// The job fails and enters Restarting with retries left, mapping to Initializing.
			recorder.Reaches(kartav1alpha1.InitializingStatus).With(vcjobRestartingWithRetries()),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("restarting-exhausted", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "restarting-exhausted", "testdata/vcjob/restarting-exhausted.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus).Optional(),
			// The job exhausts its single retry; Restarting with retryCount >= maxRetry maps to Failed.
			recorder.Reaches(kartav1alpha1.FailedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("terminated", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "terminated", "testdata/vcjob/terminated.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus).Optional(),
			// PodFailed triggers TerminateJob; Terminating is the in-flight phase.
			recorder.Reaches(kartav1alpha1.FailedStatus).With(PhaseEq("Terminating", "status", "state", "phase")),
			// The controller drives Terminating to Terminated; both map to Failed.
			recorder.Reaches(kartav1alpha1.FailedStatus).With(PhaseEq("Terminated", "status", "state", "phase")),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("aborted", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "aborted", "testdata/vcjob/aborted.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus).Optional(),
			// PodFailed triggers AbortJob; Aborting maps to Suspending.
			recorder.Reaches(kartav1alpha1.SuspendingStatus),
			// The controller always completes the abort; Aborted maps to Suspended.
			recorder.Reaches(kartav1alpha1.SuspendedStatus).With(PhaseEq("Aborted", "status", "state", "phase")),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})

func vcjobRestartingWithRetries() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		phase, _, _ := unstructured.NestedString(u.Object, "status", "state", "phase")
		if phase != "Restarting" {
			return false
		}
		maxRetry, found, _ := unstructured.NestedInt64(u.Object, "spec", "maxRetry")
		if !found {
			maxRetry = 3
		}
		retryCount, _, _ := unstructured.NestedInt64(u.Object, "status", "retryCount")
		return retryCount < maxRetry
	}
}

func vcjobInitializing() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		phase, _, _ := unstructured.NestedString(u.Object, "status", "state", "phase")
		if phase == "Pending" {
			return true
		}
		if phase != "Restarting" {
			return false
		}
		maxRetry, found, _ := unstructured.NestedInt64(u.Object, "spec", "maxRetry")
		if !found {
			maxRetry = 3
		}
		retryCount, _, _ := unstructured.NestedInt64(u.Object, "status", "retryCount")
		return retryCount < maxRetry
	}
}

func vcjobFailed() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		phase, _, _ := unstructured.NestedString(u.Object, "status", "state", "phase")
		switch phase {
		case "Terminating", "Terminated", "Failed":
			return true
		case "Restarting":
			maxRetry, found, _ := unstructured.NestedInt64(u.Object, "spec", "maxRetry")
			if !found {
				maxRetry = 3
			}
			retryCount, _, _ := unstructured.NestedInt64(u.Object, "status", "retryCount")
			return retryCount >= maxRetry
		default:
			return false
		}
	}
}
