// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

// operatorCurrentObservation excludes a known old observation without requiring a field
// that some operator versions omit.
func operatorCurrentObservation(u *unstructured.Unstructured) bool {
	generation, hasGeneration, _ := unstructured.NestedInt64(u.Object, "metadata", "generation")
	observed, hasObserved, _ := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
	return !hasGeneration || !hasObserved || observed >= generation
}

func operatorConditionReason(condType, status string, reasons ...string) recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		conditions, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
		for _, condition := range conditions {
			c, ok := condition.(map[string]any)
			if !ok || (condType != "" && c["type"] != condType) || (status != "" && c["status"] != status) {
				continue
			}
			for _, reason := range reasons {
				if c["reason"] == reason {
					return true
				}
			}
		}
		return false
	}
}

func rayClusterStarting() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		if BoolTrue("spec", "suspend")(u) || PhaseEq("failed", "status", "state")(u) ||
			CondTrue("RayClusterSuspending", "RayClusterSuspended")(u) {
			return false
		}
		if PhaseEq("suspended", "status", "state")(u) {
			suspend, hasSuspend, _ := unstructured.NestedBool(u.Object, "spec", "suspend")
			generation, hasGeneration, _ := unstructured.NestedInt64(u.Object, "metadata", "generation")
			observed, hasObserved, _ := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
			if !hasSuspend || suspend || !hasGeneration || !hasObserved || observed != generation ||
				!CondFalse("RayClusterSuspending")(u) || !CondFalse("RayClusterSuspended")(u) {
				return false
			}
		}
		return CondFalse("RayClusterProvisioned")(u) && CondFalse("HeadPodReady")(u)
	}
}

func RayClusterPending() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		if !notDeleting(u) {
			return false
		}
		if rayClusterStarting()(u) {
			return true
		}
		if u.GetGeneration() <= 0 {
			return false
		}
		status, _, err := unstructured.NestedMap(u.Object, "status")
		if err != nil {
			return false
		}
		suspend, hasSuspend, _ := unstructured.NestedBool(u.Object, "spec", "suspend")
		return len(status) == 0 || (hasSuspend && !suspend && PhaseEq("suspended", "status", "state")(u))
	}
}

func RayClusterProgressing() recorder.StateCheck {
	return AllOf(notDeleting, rayClusterStarting(), operatorConditionReason("HeadPodReady", "False",
		"ContainerCreating", "ContainersNotReady", "ErrImagePull", "ImagePullBackOff", "CrashLoopBackOff"))
}

func rayJobCanStart(u *unstructured.Unstructured) bool {
	if !notDeleting(u) || BoolTrue("spec", "suspend")(u) || PhaseAny([]string{"RUNNING", "SUCCEEDED", "FAILED", "STOPPED"}, "status", "jobStatus")(u) {
		return false
	}
	deployment, _, _ := unstructured.NestedString(u.Object, "status", "jobDeploymentStatus")
	switch deployment {
	case "", "Initializing", "Running", "Waiting":
		return true
	default:
		return false
	}
}

func RayJobPending() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		jobStatus, _, _ := unstructured.NestedFieldNoCopy(u.Object, "status", "jobStatus")
		deployment, _, _ := unstructured.NestedFieldNoCopy(u.Object, "status", "jobDeploymentStatus")
		reason, _, _ := unstructured.NestedFieldNoCopy(u.Object, "status", "reason")
		message, _, _ := unstructured.NestedFieldNoCopy(u.Object, "status", "message")
		if notDeleting(u) && u.GetGeneration() > 0 && (deployment == nil || deployment == "") &&
			(jobStatus == nil || jobStatus == "") && (reason == nil || reason == "") && (message == nil || message == "") {
			return true
		}
		if notDeleting(u) && u.GetGeneration() > 0 && BoolTrue("spec", "suspend")(u) &&
			deployment == "Initializing" && (jobStatus == nil || jobStatus == "" || jobStatus == "PENDING") &&
			(reason == nil || reason == "") && (message == nil || message == "") {
			return true
		}
		return rayJobCanStart(u) && (PhaseEq("PENDING", "status", "jobStatus")(u) ||
			PhaseAny([]string{"Initializing", "Waiting"}, "status", "jobDeploymentStatus")(u) ||
			(PhaseEq("Running", "status", "jobDeploymentStatus")(u) && (jobStatus == nil || jobStatus == "")))
	}
}

func RayJobSuspending() recorder.StateCheck {
	return AllOf(notDeleting, PhaseEq("Suspending", "status", "jobDeploymentStatus"),
		func(u *unstructured.Unstructured) bool {
			return !PhaseAny([]string{"SUCCEEDED", "FAILED", "STOPPED"}, "status", "jobStatus")(u)
		})
}

func RayJobProgressing() recorder.StateCheck {
	return AllOf(rayJobCanStart, PhaseEq("Initializing", "status", "jobDeploymentStatus"))
}

func DynamoPending() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		if !notDeleting(u) {
			return false
		}
		if operatorCurrentObservation(u) && PhaseEq("pending", "status", "state")(u) {
			return true
		}
		if u.GetGeneration() <= 0 {
			return false
		}
		var components []any
		if u.GetAPIVersion() == "nvidia.com/v1beta1" {
			components, _, _ = unstructured.NestedSlice(u.Object, "spec", "components")
		} else {
			services, _, _ := unstructured.NestedMap(u.Object, "spec", "services")
			for _, service := range services {
				components = append(components, service)
			}
		}
		active := false
		for _, value := range components {
			component, ok := value.(map[string]any)
			if !ok {
				continue
			}
			replicas, found, _ := unstructured.NestedInt64(component, "replicas")
			if !found || replicas > 0 {
				active = true
				break
			}
		}
		if !active {
			return false
		}
		status, _, _ := unstructured.NestedMap(u.Object, "status")
		state, _, _ := unstructured.NestedString(u.Object, "status", "state")
		if (len(status) == 0 || (len(status) == 1 && status["state"] != nil)) &&
			(state == "" || state == "initializing") {
			return true
		}
		return !operatorCurrentObservation(u) &&
			(state == "initializing" || state == "pending" || state == "successful")
	}
}

func DynamoProgressing() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		return notDeleting(u) && operatorCurrentObservation(u) && !PhaseEq("failed", "status", "state")(u) &&
			PhaseAny([]string{"Pending", "InProgress"}, "status", "rollingUpdate", "phase")(u)
	}
}

func KnativePending() recorder.StateCheck {
	return AllOf(operatorCurrentObservation, CondStatus("Ready", "Unknown"))
}

func KnativeProgressing() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		if !notDeleting(u) || !KnativePending()(u) {
			return false
		}
		created, _, _ := unstructured.NestedString(u.Object, "status", "latestCreatedRevisionName")
		ready, _, _ := unstructured.NestedString(u.Object, "status", "latestReadyRevisionName")
		if created != "" && created != ready {
			return true
		}
		for _, condition := range []string{"Ready", "ConfigurationsReady", "RoutesReady"} {
			if operatorConditionReason(condition, "Unknown", "OutOfDate", "TrafficNotMigrated")(u) {
				return true
			}
		}
		return false
	}
}

func milvusCanTransition(u *unstructured.Unstructured) bool {
	return operatorCurrentObservation(u) && !PhaseEq("Stopped", "status", "status")(u) &&
		!operatorConditionReason("", "", "MilvusStopped", "MilvusStopping")(u)
}

func MilvusPending() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		if milvusCanTransition(u) && PhaseEq("Pending", "status", "status")(u) {
			return true
		}
		replicas, found, _ := unstructured.NestedInt64(u.Object, "spec", "components", "standalone", "replicas")
		return inferenceAwaitingFirstStatus(u) && PhaseEq("standalone", "spec", "mode")(u) &&
			(!found || replicas > 0) && !BoolTrue("spec", "components", "enableManualMode")(u)
	}
}

func MilvusProgressing() recorder.StateCheck {
	return AllOf(milvusCanTransition, operatorConditionReason("MilvusUpdated", "False",
		"MilvusUpgradingImage", "MilvusDowngradingImage", "MilvusComponentsUpdating"))
}

func TrainingPending() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		return !BoolTrue("spec", "runPolicy", "suspend")(u) && CondTrue("Created")(u) &&
			!CondTrue("Running", "Succeeded", "Failed", "Suspended")(u)
	}
}

func TrainingProgressing() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		if !TrainingPending()(u) {
			return false
		}
		replicas, _, _ := unstructured.NestedMap(u.Object, "status", "replicaStatuses")
		for _, replica := range replicas {
			if fields, ok := replica.(map[string]any); ok {
				if active, _, _ := unstructured.NestedInt64(fields, "active"); active > 0 {
					return true
				}
			}
		}
		return false
	}
}

// A running master does not imply that every requested worker has started.
func PyTorchPending() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		if TrainingPending()(u) {
			return true
		}
		if !notDeleting(u) || BoolTrue("spec", "runPolicy", "suspend")(u) || !CondTrue("Running")(u) ||
			CondTrue("Succeeded", "Failed", "Suspended", "Restarting")(u) {
			return false
		}
		elastic, _, _ := unstructured.NestedFieldNoCopy(u.Object, "spec", "elasticPolicy")
		workerSpec, foundSpec, _ := unstructured.NestedMap(u.Object, "spec", "pytorchReplicaSpecs", "Worker")
		workerStatus, foundStatus, _ := unstructured.NestedMap(u.Object, "status", "replicaStatuses", "Worker")
		masterActive, _, _ := unstructured.NestedInt64(u.Object, "status", "replicaStatuses", "Master", "active")
		if elastic != nil || !foundSpec || !foundStatus || masterActive <= 0 {
			return false
		}
		desired, found, _ := unstructured.NestedInt64(workerSpec, "replicas")
		if !found {
			desired = 1
		}
		active, _, _ := unstructured.NestedInt64(workerStatus, "active")
		succeeded, _, _ := unstructured.NestedInt64(workerStatus, "succeeded")
		failed, _, _ := unstructured.NestedInt64(workerStatus, "failed")
		return failed == 0 && active+succeeded < desired
	}
}

func MPIJobDegraded() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		if !notDeleting(u) || BoolTrue("spec", "runPolicy", "suspend")(u) || !CondTrue("Running")(u) ||
			CondTrue("Succeeded", "Failed", "Suspended")(u) {
			return false
		}
		limit, found, _ := unstructured.NestedInt64(u.Object, "spec", "runPolicy", "backoffLimit")
		failed, _, _ := unstructured.NestedInt64(u.Object, "status", "replicaStatuses", "Launcher", "failed")
		if !found || limit != 0 || failed <= 0 {
			return false
		}
		replicas, _, _ := unstructured.NestedMap(u.Object, "status", "replicaStatuses")
		for _, replica := range replicas {
			if fields, ok := replica.(map[string]any); ok {
				if active, _, _ := unstructured.NestedInt64(fields, "active"); active > 0 {
					return true
				}
			}
		}
		return false
	}
}
