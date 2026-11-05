// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

// State predicates: each reads a workload's own fields to recognise one state, never Karta.

// AllOf matches when every native-object check matches.
func AllOf(checks ...recorder.StateCheck) recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		for _, c := range checks {
			if !c(u) {
				return false
			}
		}
		return len(checks) > 0
	}
}

// CondTrue matches when any of the given condition types is present with status True.
func CondTrue(condTypes ...string) recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
		for _, c := range conds {
			m, ok := c.(map[string]any)
			if !ok || m["status"] != "True" {
				continue
			}
			for _, t := range condTypes {
				if m["type"] == t {
					return true
				}
			}
		}
		return false
	}
}

// CondFalse matches when a condition of the given type is present and False.
func CondFalse(condType string) recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
		for _, c := range conds {
			m, ok := c.(map[string]any)
			if ok && m["type"] == condType && m["status"] == "False" {
				return true
			}
		}
		return false
	}
}

// CondStatus matches when a condition of condType is present with the given status. Useful for the
// "Unknown" status a workload reports while it is still reconciling (Knative Ready=Unknown while deploying).
func CondStatus(condType, status string) recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
		for _, c := range conds {
			if m, ok := c.(map[string]any); ok && m["type"] == condType && m["status"] == status {
				return true
			}
		}
		return false
	}
}

// CondNotTrue matches when no condition of condType is present with status True.
func CondNotTrue(condType string) recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
		for _, c := range conds {
			if m, ok := c.(map[string]any); ok && m["type"] == condType && m["status"] == "True" {
				return false
			}
		}
		return true
	}
}

// CondReason matches when a condition is True with the given reason.
func CondReason(condType, reason string) recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
		for _, c := range conds {
			if m, ok := c.(map[string]any); ok && m["type"] == condType && m["status"] == "True" && m["reason"] == reason {
				return true
			}
		}
		return false
	}
}

// CondsFalse matches when every listed status condition is present and False (the KServe failed mapping:
// PredictorReady, PredictorConfigurationReady, and RoutesReady all False).
func CondsFalse(types ...string) recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
		for _, want := range types {
			ok := false
			for _, c := range conds {
				if m, is := c.(map[string]any); is && m["type"] == want && m["status"] == "False" {
					ok = true
					break
				}
			}
			if !ok {
				return false
			}
		}
		return len(types) > 0
	}
}

func PhaseEq(want string, path ...string) recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		got, _, _ := unstructured.NestedString(u.Object, path...)
		return got == want
	}
}

// PhaseAny matches when the string at the path equals any of wants (some operators map one state from
// several phase strings).
func PhaseAny(wants []string, path ...string) recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		got, _, _ := unstructured.NestedString(u.Object, path...)
		for _, w := range wants {
			if got == w {
				return true
			}
		}
		return false
	}
}

func IntAtLeast(n int64, path ...string) recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		got, found, err := unstructured.NestedInt64(u.Object, path...)
		return err == nil && found && got >= n
	}
}

// IntEq matches when the integer field at the given path is present and exactly n. It gates a scale flow's
// step for a workload whose readiness count is a single field (Grove reports status.availableReplicas).
func IntEq(n int64, path ...string) recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		got, found, err := unstructured.NestedInt64(u.Object, path...)
		return err == nil && found && got == n
	}
}

func BoolTrue(path ...string) recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		got, found, err := unstructured.NestedBool(u.Object, path...)
		return err == nil && found && got
	}
}

// Absent matches when the field at the given path is not present (a CronJob that has not scheduled yet
// has no status.lastScheduleTime).
func Absent(path ...string) recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		_, found, _ := unstructured.NestedFieldNoCopy(u.Object, path...)
		return !found
	}
}

// ReplicasReady matches a workload settled at exactly n replicas: status.replicas and status.readyReplicas
// both equal n. It gates a scale flow's step so each replica count is captured only once the controller
// has finished scaling to it, not mid-rollout.
func ReplicasReady(n int64) recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		replicas, ok, _ := unstructured.NestedInt64(u.Object, "status", "replicas")
		ready, _, _ := unstructured.NestedInt64(u.Object, "status", "readyReplicas")
		return ok && replicas == n && ready == n
	}
}

// PodProgressing matches a scheduled Pod whose startup has not completed.
func PodProgressing() recorder.StateCheck {
	return AllOf(PhaseEq("Pending", "status", "phase"), CondTrue("PodScheduled"), notDeleting)
}

func notDeleting(u *unstructured.Unstructured) bool {
	return u.GetDeletionTimestamp() == nil
}

func currentGeneration(u *unstructured.Unstructured) bool {
	observed, found, err := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
	return notDeleting(u) && err == nil && found && observed == u.GetGeneration()
}

func waitingForObservation(u *unstructured.Unstructured) bool {
	observed, found, err := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
	return notDeleting(u) && u.GetGeneration() > 0 && err == nil && (!found || observed < u.GetGeneration())
}

func desiredReplicas(u *unstructured.Unstructured) int64 {
	replicas, found, _ := unstructured.NestedInt64(u.Object, "spec", "replicas")
	if !found {
		return 1
	}
	return replicas
}

func DeploymentPending() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		if !notDeleting(u) || desiredReplicas(u) <= 0 {
			return false
		}
		available, _, _ := unstructured.NestedInt64(u.Object, "status", "availableReplicas")
		return currentGeneration(u) && available < desiredReplicas(u) ||
			waitingForObservation(u) && !CondTrue("ReplicaFailure")(u) &&
				!operatorConditionReason("Progressing", "False", "ProgressDeadlineExceeded")(u)
	}
}

// DeploymentProgressing distinguishes an unfinished rollout from sticky success.
func DeploymentProgressing() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		if !currentGeneration(u) {
			return false
		}
		desired := desiredReplicas(u)
		actual, _, _ := unstructured.NestedInt64(u.Object, "status", "replicas")
		updated, _, _ := unstructured.NestedInt64(u.Object, "status", "updatedReplicas")
		available, _, _ := unstructured.NestedInt64(u.Object, "status", "availableReplicas")
		if actual != desired {
			return true
		}
		// Pausing holds template updates, not replica-count reconciliation.
		if BoolTrue("spec", "paused")(u) {
			return false
		}
		if updated < desired {
			return true
		}
		if available >= desired {
			return false
		}
		conditions, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
		for _, condition := range conditions {
			c, ok := condition.(map[string]any)
			if ok && c["type"] == "Progressing" && ((c["status"] == "True" && c["reason"] != "NewReplicaSetAvailable") || (c["status"] == "False" && c["reason"] == "ProgressDeadlineExceeded")) {
				return true
			}
		}
		return false
	}
}

func DeploymentRunning() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		return currentGeneration(u) && desiredReplicas(u) > 0 && IntAtLeast(1, "status", "availableReplicas")(u) && CondTrue("Available")(u)
	}
}

func DeploymentFailed() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		if !currentGeneration(u) || BoolTrue("spec", "paused")(u) {
			return false
		}
		conditions, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
		for _, condition := range conditions {
			c, ok := condition.(map[string]any)
			if ok && c["type"] == "Progressing" && c["status"] == "False" && c["reason"] == "ProgressDeadlineExceeded" {
				return true
			}
		}
		return false
	}
}

func ReplicasPending() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		if !notDeleting(u) || desiredReplicas(u) <= 0 {
			return false
		}
		ready, _, _ := unstructured.NestedInt64(u.Object, "status", "readyReplicas")
		conditions, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
		return currentGeneration(u) && ready < desiredReplicas(u) || waitingForObservation(u) && len(conditions) == 0
	}
}

// ReplicasProgressing respects intentional OnDelete and partitioned revision gaps.
func ReplicasProgressing() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		if !currentGeneration(u) {
			return false
		}
		desired := desiredReplicas(u)
		actual, _, _ := unstructured.NestedInt64(u.Object, "status", "replicas")
		if actual != desired {
			return true
		}
		ready, _, _ := unstructured.NestedInt64(u.Object, "status", "readyReplicas")
		updated, _, _ := unstructured.NestedInt64(u.Object, "status", "updatedReplicas")
		strategy, _, _ := unstructured.NestedString(u.Object, "spec", "updateStrategy", "type")
		switch strategy {
		case "", "RollingUpdate":
			current, hasCurrent, _ := unstructured.NestedString(u.Object, "status", "currentRevision")
			update, hasUpdate, _ := unstructured.NestedString(u.Object, "status", "updateRevision")
			partition, _, _ := unstructured.NestedInt64(u.Object, "spec", "updateStrategy", "rollingUpdate", "partition")
			return hasCurrent && hasUpdate && current != update && desired > partition && (updated < desired-partition || ready < desired)
		case "Recreate":
			return CondTrue("Progressing")(u) && (updated < desired || ready < desired)
		default:
			return false
		}
	}
}

func ReplicasRunning() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		actual, _, _ := unstructured.NestedInt64(u.Object, "status", "replicas")
		ready, _, _ := unstructured.NestedInt64(u.Object, "status", "readyReplicas")
		return currentGeneration(u) && desiredReplicas(u) > 0 && actual >= desiredReplicas(u) && ready >= desiredReplicas(u)
	}
}

func ReplicasDegraded() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		desired := desiredReplicas(u)
		ready, _, _ := unstructured.NestedInt64(u.Object, "status", "readyReplicas")
		updated, _, _ := unstructured.NestedInt64(u.Object, "status", "updatedReplicas")
		current, _, _ := unstructured.NestedString(u.Object, "status", "currentRevision")
		update, _, _ := unstructured.NestedString(u.Object, "status", "updateRevision")
		return currentGeneration(u) && updated == desired && ready > 0 && ready < desired && current == update
	}
}

func CronjobPending() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		_, hasStatus, _ := unstructured.NestedMap(u.Object, "status")
		scheduled, _, _ := unstructured.NestedFieldNoCopy(u.Object, "status", "lastScheduleTime")
		return notDeleting(u) && !BoolTrue("spec", "suspend")(u) && hasStatus && scheduled == nil
	}
}

// CronjobFired describes an enabled established schedule, including idle intervals.
func CronjobFired() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		scheduled, _, _ := unstructured.NestedFieldNoCopy(u.Object, "status", "lastScheduleTime")
		return notDeleting(u) && !BoolTrue("spec", "suspend")(u) && scheduled != nil
	}
}

func jobActive(u *unstructured.Unstructured) bool {
	parallelism, found, _ := unstructured.NestedInt64(u.Object, "spec", "parallelism")
	return notDeleting(u) && !BoolTrue("spec", "suspend")(u) && (!found || parallelism > 0) && !CondTrue("Complete", "SuccessCriteriaMet", "Failed", "FailureTarget", "Suspended")(u)
}

func jobWantedActive(u *unstructured.Unstructured) int64 {
	parallelism, found, _ := unstructured.NestedInt64(u.Object, "spec", "parallelism")
	if !found {
		parallelism = 1
	}
	succeeded, _, _ := unstructured.NestedInt64(u.Object, "status", "succeeded")
	if completions, found, _ := unstructured.NestedInt64(u.Object, "spec", "completions"); found {
		return min(parallelism, max(int64(0), completions-succeeded))
	}
	if succeeded > 0 {
		active, _, _ := unstructured.NestedInt64(u.Object, "status", "active")
		return active
	}
	return parallelism
}

func JobPending() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		ready, found, _ := unstructured.NestedInt64(u.Object, "status", "ready")
		status, _, _ := unstructured.NestedMap(u.Object, "status")
		return jobActive(u) && (found && ready < jobWantedActive(u) ||
			u.GetGeneration() > 0 && len(status) == 0 && jobWantedActive(u) > 0)
	}
}

func JobRunning() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		return jobActive(u) && IntAtLeast(1, "status", "active")(u) && IntAtLeast(1, "status", "ready")(u)
	}
}

func JobDegraded() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		active, _, _ := unstructured.NestedInt64(u.Object, "status", "active")
		ready, _, _ := unstructured.NestedInt64(u.Object, "status", "ready")
		failed, _, _ := unstructured.NestedInt64(u.Object, "status", "failed")
		uncountedFailed, _, _ := unstructured.NestedSlice(u.Object, "status", "uncountedTerminatedPods", "failed")
		return jobActive(u) && active > 0 && ready > 0 && (failed > 0 || len(uncountedFailed) > 0) && ready < jobWantedActive(u)
	}
}

func jobsetActive(u *unstructured.Unstructured) bool {
	return notDeleting(u) && !BoolTrue("spec", "suspend")(u) && !CondTrue("Completed", "Failed")(u)
}

// JobsetRunning requires the operator's ready-Job threshold, not merely active Jobs.
func JobsetRunning() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		if !jobsetActive(u) {
			return false
		}
		groups, _, _ := unstructured.NestedSlice(u.Object, "status", "replicatedJobsStatus")
		anyReady := false
		for _, group := range groups {
			observed, ok := group.(map[string]any)
			if !ok {
				continue
			}
			failed, _, _ := unstructured.NestedInt64(observed, "failed")
			if failed != 0 {
				return false
			}
			ready, _, _ := unstructured.NestedInt64(observed, "ready")
			active, _, _ := unstructured.NestedInt64(observed, "active")
			anyReady = anyReady || ready > 0 && active > 0
		}
		return anyReady
	}
}

func JobsetPending() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		if !jobsetActive(u) {
			return false
		}
		desired, _, _ := unstructured.NestedSlice(u.Object, "spec", "replicatedJobs")
		groups, _, _ := unstructured.NestedSlice(u.Object, "status", "replicatedJobsStatus")
		for _, entry := range desired {
			spec, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			replicas, found, _ := unstructured.NestedInt64(spec, "replicas")
			if !found {
				replicas = 1
			}
			if replicas <= 0 {
				continue
			}
			observedGroup := false
			for _, group := range groups {
				observed, ok := group.(map[string]any)
				if !ok || spec["name"] != observed["name"] {
					continue
				}
				observedGroup = true
				ready, _, _ := unstructured.NestedInt64(observed, "ready")
				active, _, _ := unstructured.NestedInt64(observed, "active")
				succeeded, _, _ := unstructured.NestedInt64(observed, "succeeded")
				if replicas > min(ready, active)+succeeded {
					return true
				}
				break
			}
			if !observedGroup && u.GetGeneration() > 0 {
				return true
			}
		}
		return false
	}
}

func JobsetProgressing() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		ordered := PhaseEq("InOrder", "spec", "startupPolicy", "startupPolicyOrder")(u)
		return jobsetActive(u) && (CondTrue("RestartingJobSet")(u) || ordered && CondTrue("StartupPolicyInProgress")(u))
	}
}

func JobsetSuspended() recorder.StateCheck {
	return AllOf(BoolTrue("spec", "suspend"), CondTrue("Suspended"))
}

func lwsCurrent(u *unstructured.Unstructured) bool {
	observed, found, _ := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
	_, hasGeneration, _ := unstructured.NestedInt64(u.Object, "metadata", "generation")
	return notDeleting(u) && (!found || !hasGeneration || observed == u.GetGeneration())
}

func LWSPending() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		if !notDeleting(u) || desiredReplicas(u) <= 0 || CondsFalse("Available", "Progressing", "UpdateInProgress")(u) {
			return false
		}
		ready, hasReady, _ := unstructured.NestedInt64(u.Object, "status", "readyReplicas")
		if lwsCurrent(u) && ready < desiredReplicas(u) && (hasReady || CondTrue("Progressing", "UpdateInProgress")(u)) {
			return true
		}
		status, _, _ := unstructured.NestedMap(u.Object, "status")
		observed, hasObserved, _ := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
		return u.GetGeneration() > 0 && (len(status) == 0 || hasObserved && observed < u.GetGeneration())
	}
}

func LWSProgressing() recorder.StateCheck {
	return AllOf(lwsCurrent, CondTrue("Progressing", "UpdateInProgress"))
}

func LWSRunning() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		if !lwsCurrent(u) || desiredReplicas(u) <= 0 {
			return false
		}
		actual, _, _ := unstructured.NestedInt64(u.Object, "status", "replicas")
		ready, _, _ := unstructured.NestedInt64(u.Object, "status", "readyReplicas")
		return actual >= desiredReplicas(u) && ready >= desiredReplicas(u)
	}
}

func LWSFailed() recorder.StateCheck {
	return AllOf(lwsCurrent, CondsFalse("Available", "Progressing", "UpdateInProgress"))
}

// RaySuspended requires the controller to acknowledge suspension for the current generation.
func RaySuspended() recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		generation, hasGeneration, _ := unstructured.NestedInt64(u.Object, "metadata", "generation")
		observed, hasObserved, _ := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
		return notDeleting(u) && BoolTrue("spec", "suspend")(u) &&
			PhaseEq("suspended", "status", "state")(u) &&
			hasGeneration && hasObserved && generation == observed
	}
}

// SandboxMode matches an Agent Sandbox whose requested spec.operatingMode is want. The field defaults to
// Running, so an unset mode counts as Running.
func SandboxMode(want string) recorder.StateCheck {
	return func(u *unstructured.Unstructured) bool {
		mode, found, _ := unstructured.NestedString(u.Object, "spec", "operatingMode")
		if !found || mode == "" {
			mode = "Running"
		}
		return mode == want
	}
}
