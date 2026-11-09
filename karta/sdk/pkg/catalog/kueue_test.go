// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package catalog

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

var _ = Describe("Kueue Workload operations", func() {
	DescribeTable("changes only active through suspend and resume",
		func(ctx SpecContext, active *bool) {
			object := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "kueue.x-k8s.io/v1beta2",
				"kind":       "Workload",
				"metadata":   map[string]any{"name": "training", "namespace": "default"},
				"spec": map[string]any{
					"queueName": "training",
					"podSets":   []any{map[string]any{"name": "main", "count": int64(1)}},
				},
				"status": map[string]any{
					"conditions": []any{map[string]any{"type": "Admitted", "status": "True"}},
				},
			}}
			if active != nil {
				Expect(unstructured.SetNestedField(object.Object, *active, "spec", "active")).To(Succeed())
			}
			definition, err := Get(object.GroupVersionKind())
			Expect(err).NotTo(HaveOccurred())
			factory := resource.NewComponentFactoryFromObject(definition, object.DeepCopy())
			root, err := factory.GetRootComponent()
			Expect(err).NotTo(HaveOccurred())
			Expect(root.HasSuspendDefinition()).To(BeTrue())

			// Repeating either operation must preserve all other fields.
			for _, wantActive := range []bool{false, false, true, true} {
				if wantActive {
					Expect(root.Resume(ctx)).To(Succeed())
				} else {
					Expect(root.Suspend(ctx)).To(Succeed())
				}
				updated, err := factory.GetResource()
				Expect(err).NotTo(HaveOccurred())
				expected := object.DeepCopy()
				Expect(unstructured.SetNestedField(expected.Object, wantActive, "spec", "active")).To(Succeed())
				actualJSON, err := json.Marshal(updated)
				Expect(err).NotTo(HaveOccurred())
				expectedJSON, err := json.Marshal(expected)
				Expect(err).NotTo(HaveOccurred())
				Expect(actualJSON).To(MatchJSON(expectedJSON))
			}
		},
		Entry("default active", nil),
		Entry("already active", ptr.To(true)),
		Entry("already inactive", ptr.To(false)),
	)
})

var _ = Describe("Kueue Workload status", func() {
	DescribeTable("reads admission and readiness without inferring execution success",
		func(ctx SpecContext, active *bool, conditions []map[string]any, want v1alpha1.ResourceStatus) {
			karta, err := Get(schema.GroupVersionKind{Group: "kueue.x-k8s.io", Version: "v1beta2", Kind: "Workload"})
			Expect(err).NotTo(HaveOccurred())
			object := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "kueue.x-k8s.io/v1beta2",
				"kind":       "Workload",
			}}
			if active != nil {
				Expect(unstructured.SetNestedField(object.Object, *active, "spec", "active")).To(Succeed())
			}
			if conditions != nil {
				values := make([]any, len(conditions))
				for i, condition := range conditions {
					values[i] = condition
				}
				Expect(unstructured.SetNestedSlice(object.Object, values, "status", "conditions")).To(Succeed())
			}
			root, err := resource.NewComponentFactoryFromObject(karta, object).GetRootComponent()
			Expect(err).NotTo(HaveOccurred())
			status, err := root.GetStatus(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(status.MatchedStatuses).To(Equal([]v1alpha1.ResourceStatus{want}))
		},
		Entry("missing conditions", nil, nil, v1alpha1.UndefinedStatus),
		Entry("waiting for admission", nil, []map[string]any{
			{"type": "Admitted", "status": "False"},
		}, v1alpha1.InitializingStatus),
		Entry("unknown admission", nil, []map[string]any{
			{"type": "Admitted", "status": "Unknown"},
		}, v1alpha1.UndefinedStatus),
		Entry("admitted without readiness reporting", nil, []map[string]any{
			{"type": "Admitted", "status": "True"},
		}, v1alpha1.UndefinedStatus),
		Entry("admitted before Pods are ready", nil, []map[string]any{
			{"type": "Admitted", "status": "True"},
			{"type": "PodsReady", "status": "False"},
		}, v1alpha1.UndefinedStatus),
		Entry("admitted and ready", nil, []map[string]any{
			{"type": "Admitted", "status": "True"},
			{"type": "PodsReady", "status": "True"},
		}, v1alpha1.RunningStatus),
		Entry("readiness retained after admission is withdrawn", nil, []map[string]any{
			{"type": "Admitted", "status": "False"},
			{"type": "PodsReady", "status": "True"},
		}, v1alpha1.InitializingStatus),
		Entry("readiness retained during eviction", nil, []map[string]any{
			{"type": "Admitted", "status": "True"},
			{"type": "PodsReady", "status": "True"},
			{"type": "Evicted", "status": "True"},
		}, v1alpha1.UndefinedStatus),
		Entry("readiness retained while waiting for replacement Pods", nil, []map[string]any{
			{"type": "Admitted", "status": "True"},
			{"type": "PodsReady", "status": "True"},
			{"type": "WaitingForReplacementPods", "status": "True"},
		}, v1alpha1.UndefinedStatus),
		Entry("finished successful workload", nil, []map[string]any{
			{"type": "Finished", "status": "True", "reason": "Succeeded"},
		}, v1alpha1.CompletedStatus),
		Entry("success reason before completion", nil, []map[string]any{
			{"type": "Admitted", "status": "False"},
			{"type": "Finished", "status": "False", "reason": "Succeeded"},
		}, v1alpha1.InitializingStatus),
		Entry("finished mixed Pod group with stale readiness", nil, []map[string]any{
			{"type": "Admitted", "status": "True"},
			{"type": "PodsReady", "status": "True"},
			{"type": "Finished", "status": "True", "reason": "Succeeded", "message": "Pods succeeded: 1/3."},
		}, v1alpha1.CompletedStatus),
		Entry("finished after quota was released", nil, []map[string]any{
			{"type": "Admitted", "status": "False"},
			{"type": "Finished", "status": "True", "reason": "Failed"},
		}, v1alpha1.FailedStatus),
		Entry("recovered after eviction and replacement", nil, []map[string]any{
			{"type": "Admitted", "status": "True"},
			{"type": "PodsReady", "status": "True"},
			{"type": "Evicted", "status": "False"},
			{"type": "WaitingForReplacementPods", "status": "False"},
			{"type": "Finished", "status": "False"},
		}, v1alpha1.RunningStatus),
		Entry("explicitly active and waiting for admission", ptr.To(true), []map[string]any{
			{"type": "Admitted", "status": "False"},
		}, v1alpha1.InitializingStatus),
		Entry("explicitly active and ready", ptr.To(true), []map[string]any{
			{"type": "Admitted", "status": "True"},
			{"type": "PodsReady", "status": "True"},
		}, v1alpha1.RunningStatus),
		Entry("inactive without conditions", ptr.To(false), nil, v1alpha1.SuspendedStatus),
		Entry("inactive without reserved quota", ptr.To(false), []map[string]any{
			{"type": "Admitted", "status": "False"},
			{"type": "QuotaReserved", "status": "False"},
		}, v1alpha1.SuspendedStatus),
		Entry("inactive with unknown quota reservation", ptr.To(false), []map[string]any{
			{"type": "QuotaReserved", "status": "Unknown"},
		}, v1alpha1.SuspendedStatus),
		Entry("inactive before admission with reserved quota", ptr.To(false), []map[string]any{
			{"type": "Admitted", "status": "False"},
			{"type": "QuotaReserved", "status": "True"},
		}, v1alpha1.UndefinedStatus),
		Entry("inactive with reserved quota and stale readiness", ptr.To(false), []map[string]any{
			{"type": "Admitted", "status": "True"},
			{"type": "PodsReady", "status": "True"},
			{"type": "QuotaReserved", "status": "True"},
		}, v1alpha1.UndefinedStatus),
		Entry("inactive after quota release with stale readiness", ptr.To(false), []map[string]any{
			{"type": "Admitted", "status": "True"},
			{"type": "PodsReady", "status": "True"},
			{"type": "QuotaReserved", "status": "False"},
		}, v1alpha1.SuspendedStatus),
		Entry("unknown terminal reason with stale readiness", nil, []map[string]any{
			{"type": "Admitted", "status": "True"},
			{"type": "PodsReady", "status": "True"},
			{"type": "Finished", "status": "True", "reason": "Unrecognized"},
		}, v1alpha1.UndefinedStatus),
		Entry("inactive finished group with quota and stale readiness", ptr.To(false), []map[string]any{
			{"type": "Admitted", "status": "True"},
			{"type": "PodsReady", "status": "True"},
			{"type": "QuotaReserved", "status": "True"},
			{"type": "Finished", "status": "True", "reason": "Succeeded"},
		}, v1alpha1.CompletedStatus),
		Entry("inactive unknown terminal reason after quota release", ptr.To(false), []map[string]any{
			{"type": "Admitted", "status": "False"},
			{"type": "QuotaReserved", "status": "False"},
			{"type": "Finished", "status": "True", "reason": "Unrecognized"},
		}, v1alpha1.UndefinedStatus),
		Entry("failed to start with stale readiness", nil, []map[string]any{
			{"type": "Admitted", "status": "True"},
			{"type": "PodsReady", "status": "True"},
			{"type": "Finished", "status": "True", "reason": "FailedToStart"},
		}, v1alpha1.FailedStatus),
		Entry("inactive failed workload with reserved quota", ptr.To(false), []map[string]any{
			{"type": "Admitted", "status": "True"},
			{"type": "PodsReady", "status": "True"},
			{"type": "QuotaReserved", "status": "True"},
			{"type": "Finished", "status": "True", "reason": "Failed"},
		}, v1alpha1.FailedStatus),
		Entry("failure reason without a finished condition", nil, []map[string]any{
			{"type": "Admitted", "status": "False"},
			{"type": "Finished", "status": "False", "reason": "Failed"},
		}, v1alpha1.InitializingStatus),
		Entry("out-of-sync admission request", nil, []map[string]any{
			{"type": "Admitted", "status": "False"},
			{"type": "Finished", "status": "True", "reason": "OutOfSync"},
		}, v1alpha1.UndefinedStatus),
		Entry("orphan cleanup after owner deletion", ptr.To(false), []map[string]any{
			{"type": "QuotaReserved", "status": "False"},
			{"type": "Finished", "status": "True", "reason": "OwnerNotFound"},
		}, v1alpha1.UndefinedStatus),
	)
})
