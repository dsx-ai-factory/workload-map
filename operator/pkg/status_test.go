// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package pkg

import (
	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type conditionInputs struct {
	validated metav1.ConditionStatus
	crdExists metav1.ConditionStatus
}

var _ = Describe("status.go test", func() {
	It("setDefaultConditions fills absent conditions with False/Pending", func() {
		status := &kartav1alpha1.KartaStatus{}
		setDefaultConditions(status, 7)

		for _, t := range []kartav1alpha1.ConditionType{
			kartav1alpha1.ConditionValidated,
			kartav1alpha1.ConditionCRDExists,
			kartav1alpha1.ConditionReady,
		} {
			c := apimeta.FindStatusCondition(status.Conditions, string(t))
			Expect(c).NotTo(BeNil(), "condition %s", t)
			Expect(c.Status).To(Equal(metav1.ConditionFalse), "condition %s", t)
			Expect(c.Reason).To(Equal(ReasonPending), "condition %s", t)
			Expect(c.ObservedGeneration).To(Equal(int64(7)), "condition %s", t)
		}
	})

	It("setDefaultConditions does not overwrite existing conditions", func() {
		status := &kartav1alpha1.KartaStatus{}
		setValidated(status, 1, metav1.ConditionTrue, "")

		setDefaultConditions(status, 1)

		c := apimeta.FindStatusCondition(status.Conditions, string(kartav1alpha1.ConditionValidated))
		Expect(c).NotTo(BeNil())
		Expect(c.Status).To(Equal(metav1.ConditionTrue))
		Expect(c.Reason).To(Equal(ReasonValidationSucceeded))
	})

	It("derives Ready=True only when both Validated and CRDExists are True", func() {
		for _, tc := range []struct {
			in    conditionInputs
			ready metav1.ConditionStatus
		}{
			{conditionInputs{metav1.ConditionTrue, metav1.ConditionTrue}, metav1.ConditionTrue},
			{conditionInputs{metav1.ConditionTrue, metav1.ConditionFalse}, metav1.ConditionFalse},
			{conditionInputs{metav1.ConditionFalse, metav1.ConditionTrue}, metav1.ConditionFalse},
			{conditionInputs{metav1.ConditionFalse, metav1.ConditionFalse}, metav1.ConditionFalse},
		} {
			status := &kartav1alpha1.KartaStatus{}
			setConditions(status, tc.in)
			c := apimeta.FindStatusCondition(status.Conditions, string(kartav1alpha1.ConditionReady))
			Expect(c).NotTo(BeNil())
			Expect(c.Status).To(Equal(tc.ready))
		}
	})
})

func setConditions(status *kartav1alpha1.KartaStatus, in conditionInputs) {
	setValidated(status, 0, in.validated, "")
	setCRDExists(status, 0, in.crdExists, "")
	setReady(status, 0)
}
