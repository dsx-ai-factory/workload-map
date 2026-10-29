// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func inferenceObservationCurrent(u *unstructured.Unstructured) bool {
	observed, found, _ := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
	return !found || u.GetGeneration() == 0 || observed >= u.GetGeneration()
}

func inferenceAwaitingFirstStatus(u *unstructured.Unstructured) bool {
	if u.GetGeneration() <= 0 || !notDeleting(u) {
		return false
	}
	value, _, err := unstructured.NestedFieldNoCopy(u.Object, "status")
	if err != nil {
		return false
	}
	if value == nil {
		return true
	}
	status, ok := value.(map[string]any)
	return ok && len(status) == 0
}

func inferenceServiceAwaitingStatus(u *unstructured.Unstructured) bool {
	return inferenceAwaitingFirstStatus(u) && !strings.EqualFold(u.GetAnnotations()["serving.kserve.io/stop"], "true")
}

func nimPending(u *unstructured.Unstructured) bool {
	replicas, found, _ := unstructured.NestedInt64(u.Object, "spec", "replicas")
	return (!found || replicas > 0) && (inferenceAwaitingFirstStatus(u) ||
		(inferenceObservationCurrent(u) && PhaseAny([]string{"Pending", "NotReady", "PVC-Created"}, "status", "state")(u)))
}

func grovePending(u *unstructured.Unstructured) bool {
	desired, _, _ := unstructured.NestedInt64(u.Object, "spec", "replicas")
	if !notDeleting(u) || desired <= 0 || CondTrue("TopologyLevelsUnavailable")(u) {
		return false
	}
	available, hasAvailable, _ := unstructured.NestedInt64(u.Object, "status", "availableReplicas")
	observed, hasObserved, _ := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
	if inferenceObservationCurrent(u) && (hasAvailable || hasObserved) && available < desired {
		return true
	}
	status, _, _ := unstructured.NestedMap(u.Object, "status")
	lastErrors, _, _ := unstructured.NestedSlice(u.Object, "status", "lastErrors")
	return u.GetGeneration() > 0 && len(lastErrors) == 0 &&
		(len(status) == 0 || hasObserved && observed < u.GetGeneration())
}

func groveProgressing(u *unstructured.Unstructured) bool {
	strategy, _, _ := unstructured.NestedString(u.Object, "spec", "updateStrategy", "type")
	ended, _, _ := unstructured.NestedFieldNoCopy(u.Object, "status", "updateProgress", "updateEndedAt")
	started, _, _ := unstructured.NestedString(u.Object, "status", "updateProgress", "updateStartedAt")
	// The root operation begins before child update conditions become active.
	// Its generation watermark advances only after convergence.
	return notDeleting(u) && strategy != "OnDelete" && ended == nil && !CondTrue("TopologyLevelsUnavailable")(u) &&
		(started != "" && started != "0001-01-01T00:00:00Z" ||
			CondTrue("UpdateInProgress")(u) || operatorConditionReason("UpdateInProgress", "Unknown", "ProgressDeadlineExceeded")(u))
}

func groveRunning(u *unstructured.Unstructured) bool {
	status, hasStatus, _ := unstructured.NestedMap(u.Object, "status")
	available, _, _ := unstructured.NestedInt64(status, "availableReplicas")
	desired, _, _ := unstructured.NestedInt64(u.Object, "spec", "replicas")
	return inferenceObservationCurrent(u) && hasStatus && available >= desired
}

func sandboxPending(u *unstructured.Unstructured) bool {
	return inferenceObservationCurrent(u) && inferenceConditionsCurrent(u, "Ready", "Suspended") && SandboxMode("Running")(u) &&
		operatorConditionReason("Ready", "False", "DependenciesNotReady")(u) &&
		CondFalse("Suspended")(u) && !CondTrue("Finished")(u)
}

func sandboxProgressing(u *unstructured.Unstructured) bool {
	return sandboxPending(u) && inferenceConditionsCurrent(u, "PodScheduled") && CondTrue("PodScheduled")(u)
}

func inferenceConditionsCurrent(u *unstructured.Unstructured, conditionTypes ...string) bool {
	conditions, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, item := range conditions {
		condition, ok := item.(map[string]any)
		if !ok {
			continue
		}
		conditionType, _, _ := unstructured.NestedString(condition, "type")
		observed, found, _ := unstructured.NestedInt64(condition, "observedGeneration")
		if slices.Contains(conditionTypes, conditionType) && found && observed < u.GetGeneration() {
			return false
		}
	}
	return true
}

func inferenceServiceProgressing(u *unstructured.Unstructured) bool {
	return inferenceObservationCurrent(u) && !CondsFalse("PredictorReady", "PredictorConfigurationReady", "RoutesReady")(u) &&
		PhaseEq("Loading", "status", "modelStatus", "states", "targetModelState")(u)
}

func inferenceServicePending(u *unstructured.Unstructured) bool {
	if inferenceServiceAwaitingStatus(u) {
		return true
	}
	if !inferenceObservationCurrent(u) || CondTrue("Ready")(u) || CondsFalse("PredictorReady", "PredictorConfigurationReady", "RoutesReady")(u) {
		return false
	}
	for _, name := range []string{"Ready", "PredictorReady", "RoutesReady", "LatestDeploymentReady"} {
		if CondStatus(name, "Unknown")(u) {
			return true
		}
	}
	return inferenceServiceProgressing(u) && operatorConditionReason("PredictorReady", "False", "MinimumReplicasUnavailable")(u)
}
