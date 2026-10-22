// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

func nimCachePending(u *unstructured.Unstructured) bool {
	return inferenceAwaitingFirstStatus(u) || (inferenceObservationCurrent(u) &&
		PhaseAny([]string{"Pending", "PVC-Created", "Started"}, "status", "state")(u) &&
		!CondTrue("NIM_CACHE_JOB_COMPLETED")(u))
}

func nimCacheProgressing(u *unstructured.Unstructured) bool {
	return nimCachePending(u) &&
		(PhaseAny([]string{"PVC-Created", "Started"}, "status", "state")(u) ||
			CondTrue("NIM_CACHE_JOB_CREATED")(u))
}
