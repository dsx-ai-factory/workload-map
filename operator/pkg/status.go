// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package pkg

import (
	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ReasonValidationSucceeded = "ValidationSucceeded"
	ReasonValidationFailed    = "ValidationFailed"
	ReasonCRDFound            = "CRDFound"
	ReasonCRDNotFound         = "CRDNotFound"
	ReasonReady               = "Ready"
	ReasonNotReady            = "NotReady"
	ReasonPending             = "Pending"
)

const (
	msgValidationFailed = "Karta validation failed"
	msgCRDNotFound      = "CustomResourceDefinition for the root component GVK does not exist in the cluster or does not serve the referenced version"
	msgNotReady         = "Validated and CRDExists must both be True"
	msgPending          = "Condition has not been evaluated yet"
)

func setValidated(status *kartav1alpha1.KartaStatus, generation int64, s metav1.ConditionStatus, msg string) {
	if s == metav1.ConditionFalse && msg == "" {
		msg = msgValidationFailed
	}
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               string(kartav1alpha1.ConditionValidated),
		Status:             s,
		Reason:             reasonForBool(s, ReasonValidationSucceeded, ReasonValidationFailed),
		Message:            msgWhenFalse(s, msg),
		ObservedGeneration: generation,
	})
}

func setCRDExists(status *kartav1alpha1.KartaStatus, generation int64, s metav1.ConditionStatus, msg string) {
	if s == metav1.ConditionFalse && msg == "" {
		msg = msgCRDNotFound
	}
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               string(kartav1alpha1.ConditionCRDExists),
		Status:             s,
		Reason:             reasonForBool(s, ReasonCRDFound, ReasonCRDNotFound),
		Message:            msgWhenFalse(s, msg),
		ObservedGeneration: generation,
	})
}

func setReady(status *kartav1alpha1.KartaStatus, generation int64) metav1.ConditionStatus {
	statusOf := func(t kartav1alpha1.ConditionType) metav1.ConditionStatus {
		if c := apimeta.FindStatusCondition(status.Conditions, string(t)); c != nil {
			return c.Status
		}
		return metav1.ConditionFalse
	}

	readyStatus := metav1.ConditionFalse
	readyReason := ReasonNotReady
	readyMsg := msgNotReady
	if statusOf(kartav1alpha1.ConditionValidated) == metav1.ConditionTrue &&
		statusOf(kartav1alpha1.ConditionCRDExists) == metav1.ConditionTrue {
		readyStatus = metav1.ConditionTrue
		readyReason = ReasonReady
		readyMsg = ""
	}
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               string(kartav1alpha1.ConditionReady),
		Status:             readyStatus,
		Reason:             readyReason,
		Message:            readyMsg,
		ObservedGeneration: generation,
	})
	return readyStatus
}

func setDefaultConditions(status *kartav1alpha1.KartaStatus, generation int64) {
	for _, t := range []kartav1alpha1.ConditionType{
		kartav1alpha1.ConditionValidated,
		kartav1alpha1.ConditionCRDExists,
		kartav1alpha1.ConditionReady,
	} {
		if apimeta.FindStatusCondition(status.Conditions, string(t)) != nil {
			continue
		}
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               string(t),
			Status:             metav1.ConditionFalse,
			Reason:             ReasonPending,
			Message:            msgPending,
			ObservedGeneration: generation,
		})
	}
}

func reasonForBool(status metav1.ConditionStatus, trueReason, falseReason string) string {
	if status == metav1.ConditionTrue {
		return trueReason
	}
	return falseReason
}

func msgWhenFalse(status metav1.ConditionStatus, fallback string) string {
	if status == metav1.ConditionTrue {
		return ""
	}
	return fallback
}
