// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package otel

import "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"

const (
	MetricPodWorkloadInfo          = "karta_pod_workload_info"
	MetricWorkloadStatus           = "karta_workload_status"
	MetricWorkloadCreatedTimestamp = "karta_workload_created_timestamp_seconds"
	MetricExporterObservationValid = "karta_exporter_observation_valid"
)

const (
	LabelNamespace         = "namespace"
	LabelPod               = "pod_name"
	LabelPodUID            = "pod_uid"
	LabelWorkload          = "workload_name"
	LabelWorkloadUID       = "workload_uid"
	LabelWorkloadKind      = "workload_kind"
	LabelWorkloadGroup     = "workload_group"
	LabelWorkloadVersion   = "workload_version"
	LabelOwnerResolution   = "owner_resolution"
	LabelKarta             = "karta"
	LabelKartaSource       = "karta_source"
	LabelComponent         = "component"
	LabelComponentInstance = "component_instance"
	LabelReplicaGroupID    = "replica_group_id"
	LabelPhase             = "phase"
	LabelReason            = "reason"
	LabelObservationValid  = "observation_valid"
	LabelResourceGroup     = "resource_group"
	LabelResourceVersion   = "resource_version"
	LabelResourceKind      = "resource_kind"
)

// allPhases contains the normalized phases emitted as dense 0/1 observations.
var allPhases = []v1alpha1.ResourceStatus{
	v1alpha1.InitializingStatus,
	v1alpha1.RunningStatus,
	v1alpha1.CompletedStatus,
	v1alpha1.FailedStatus,
	v1alpha1.DegradedStatus,
	v1alpha1.SuspendedStatus,
	v1alpha1.SuspendingStatus,
	v1alpha1.ResumingStatus,
	v1alpha1.UndefinedStatus,
}
