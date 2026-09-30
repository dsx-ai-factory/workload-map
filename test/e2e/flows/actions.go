// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"fmt"

	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

// Flow actions: each builds a recorder.Action, the merge-patch a flow fires to drive a transition the
// operator will not make itself.

// Suspend sets spec.suspend so a running workload pauses.
func Suspend() *recorder.Action {
	return &recorder.Action{Type: recorder.ActionSuspend, Patch: []byte(`{"spec":{"suspend":true}}`)}
}

// ResumeRunPolicy clears spec.runPolicy.suspend, where Kubeflow jobs (PyTorchJob, MPIJob) keep the
// suspend flag, unlike the top-level spec.suspend that a plain resume patches.
func ResumeRunPolicy() *recorder.Action {
	return &recorder.Action{Type: recorder.ActionResume, Patch: []byte(`{"spec":{"runPolicy":{"suspend":false}}}`)}
}

// ScaleReplicas sets spec.replicas, for any workload with a scalar replica count (Deployment,
// StatefulSet, LeaderWorkerSet, Grove, ...).
func ScaleReplicas(n int) *recorder.Action {
	return &recorder.Action{Type: recorder.ActionScale, Patch: []byte(fmt.Sprintf(`{"spec":{"replicas":%d}}`, n))}
}

// Resume clears spec.suspend so a suspended workload resumes.
func Resume() *recorder.Action {
	return &recorder.Action{Type: recorder.ActionResume, Patch: []byte(`{"spec":{"suspend":false}}`)}
}

// ScaleParallelism sets a batch Job's spec.parallelism.
func ScaleParallelism(n int) *recorder.Action {
	return &recorder.Action{Type: recorder.ActionScale, Patch: []byte(fmt.Sprintf(`{"spec":{"parallelism":%d}}`, n))}
}
