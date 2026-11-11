// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onsi/gomega"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog/kartas"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/jq/execution"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

func TestBuiltinStatusMappings(t *testing.T) {
	tests := []struct {
		name   string
		karta  func() *v1alpha1.Karta
		object string
		want   []v1alpha1.ResourceStatus
	}{
		{
			name:   "Pod/absent status",
			karta:  kartas.Pod,
			object: `{"metadata":{"generation":1},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Deployment/absent status",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":1},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "StatefulSet/absent status",
			karta:  kartas.StatefulSet,
			object: `{"metadata":{"generation":1},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "CronJob/absent status",
			karta:  kartas.CronJob,
			object: `{"metadata":{"generation":1},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/absent status",
			karta:  kartas.BatchJob,
			object: `{"metadata":{"generation":1},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Jobset/absent status",
			karta:  kartas.Jobset,
			object: `{"metadata":{"generation":1},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "LWS/absent status",
			karta:  kartas.LWS,
			object: `{"metadata":{"generation":1},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Pod/unscheduled",
			karta:  kartas.Pod,
			object: `{"status":{"phase":"Pending","conditions":[{"type":"PodScheduled","status":"False","reason":"Unschedulable"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Pod/scheduled image pull waiting",
			karta:  kartas.Pod,
			object: `{"status":{"phase":"Pending","conditions":[{"type":"PodScheduled","status":"True"}],"containerStatuses":[{"state":{"waiting":{"reason":"ImagePullBackOff"}}}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "Pod/pending without scheduling evidence",
			karta:  kartas.Pod,
			object: `{"status":{"phase":"Pending"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Pod/deleting pending pod",
			karta:  kartas.Pod,
			object: `{"metadata":{"deletionTimestamp":"2026-01-01T00:00:00Z"},"status":{"phase":"Pending","conditions":[{"type":"PodScheduled","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Pod/Running",
			karta:  kartas.Pod,
			object: `{"status":{"phase":"Running","conditions":[{"type":"PodScheduled","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "Pod/Succeeded",
			karta:  kartas.Pod,
			object: `{"status":{"phase":"Succeeded","conditions":[{"type":"PodScheduled","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.CompletedStatus},
		},
		{
			name:   "Pod/Failed",
			karta:  kartas.Pod,
			object: `{"status":{"phase":"Failed","conditions":[{"type":"PodScheduled","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.FailedStatus},
		},
		{
			name:   "Pod/Unknown",
			karta:  kartas.Pod,
			object: `{"status":{"phase":"Unknown","conditions":[{"type":"PodScheduled","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Deployment/startup",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":1},"spec":{"replicas":1,"paused":false},"status":{"observedGeneration":1,"replicas":0,"updatedReplicas":0,"availableReplicas":0,"conditions":[{"type":"Progressing","status":"True","reason":"NewReplicaSetCreated"},{"type":"Available","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "Deployment/blocked rollout",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":1},"spec":{"replicas":1,"paused":false},"status":{"observedGeneration":1,"replicas":1,"updatedReplicas":1,"availableReplicas":0,"conditions":[{"type":"Progressing","status":"False","reason":"ProgressDeadlineExceeded"},{"type":"Available","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus, v1alpha1.FailedStatus},
		},
		{
			name:   "Deployment/settled sticky success",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":1},"spec":{"replicas":1,"paused":false},"status":{"observedGeneration":1,"replicas":1,"updatedReplicas":1,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"True","reason":"NewReplicaSetAvailable"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "Deployment/rolling while available",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":1},"spec":{"replicas":2,"paused":false},"status":{"observedGeneration":1,"replicas":3,"updatedReplicas":1,"availableReplicas":2,"conditions":[{"type":"Progressing","status":"True","reason":"ReplicaSetUpdated"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "Deployment/partial capacity rollout",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":1},"spec":{"replicas":2,"paused":false},"status":{"observedGeneration":1,"replicas":2,"updatedReplicas":1,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"True","reason":"ReplicaSetUpdated"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.RunningStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "Deployment/availability lost after success",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":1},"spec":{"replicas":1,"paused":false},"status":{"observedGeneration":1,"replicas":1,"updatedReplicas":1,"availableReplicas":0,"conditions":[{"type":"Progressing","status":"True","reason":"NewReplicaSetAvailable"},{"type":"Available","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Deployment/scale up sticky condition",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":1},"spec":{"replicas":2,"paused":false},"status":{"observedGeneration":1,"replicas":1,"updatedReplicas":1,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"True","reason":"NewReplicaSetAvailable"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.RunningStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "Deployment/paused rollout",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":1},"spec":{"replicas":2,"paused":true},"status":{"observedGeneration":1,"replicas":2,"updatedReplicas":1,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"True","reason":"ReplicaSetUpdated"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.RunningStatus},
		},
		{
			name:   "Deployment/paused scale up",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":2},"spec":{"replicas":3,"paused":true},"status":{"observedGeneration":2,"replicas":1,"updatedReplicas":0,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"Unknown","reason":"DeploymentPaused"},{"type":"Available","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "Deployment/paused scale down",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":2},"spec":{"replicas":1,"paused":true},"status":{"observedGeneration":2,"replicas":3,"updatedReplicas":0,"availableReplicas":3,"conditions":[{"type":"Progressing","status":"Unknown","reason":"DeploymentPaused"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "Deployment/paused template update only",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":2},"spec":{"replicas":3,"paused":true},"status":{"observedGeneration":2,"replicas":3,"updatedReplicas":0,"availableReplicas":3,"conditions":[{"type":"Progressing","status":"Unknown","reason":"DeploymentPaused"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "Deployment/paused readiness wait after counts converge",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":2},"spec":{"replicas":3,"paused":true},"status":{"observedGeneration":2,"replicas":3,"updatedReplicas":3,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"Unknown","reason":"DeploymentPaused"},{"type":"Available","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Deployment/paused scale to zero draining",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":2},"spec":{"replicas":0,"paused":true},"status":{"observedGeneration":2,"replicas":1,"updatedReplicas":0,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"Unknown","reason":"DeploymentPaused"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.ProgressingStatus},
		},
		{
			name:   "Deployment/paused settled zero",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":2},"spec":{"replicas":0,"paused":true},"status":{"observedGeneration":2,"replicas":0,"updatedReplicas":0,"availableReplicas":0,"conditions":[{"type":"Progressing","status":"Unknown","reason":"DeploymentPaused"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Deployment/paused scale with stale status",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":2},"spec":{"replicas":3,"paused":true},"status":{"observedGeneration":1,"replicas":1,"updatedReplicas":0,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"Unknown","reason":"DeploymentPaused"},{"type":"Available","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Deployment/deleting paused scale",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":2,"deletionTimestamp":"2026-01-01T00:00:00Z"},"spec":{"replicas":3,"paused":true},"status":{"observedGeneration":2,"replicas":1,"updatedReplicas":0,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"Unknown","reason":"DeploymentPaused"},{"type":"Available","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Deployment/stale generation",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":1},"spec":{"replicas":1,"paused":false},"status":{"observedGeneration":0,"replicas":1,"updatedReplicas":1,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"True","reason":"NewReplicaSetAvailable"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Deployment/scale to zero draining",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":1},"spec":{"replicas":0,"paused":false},"status":{"observedGeneration":1,"replicas":1,"updatedReplicas":1,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"True","reason":"NewReplicaSetAvailable"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.ProgressingStatus},
		},
		{
			name:   "Deployment/settled zero",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":1},"spec":{"replicas":0,"paused":false},"status":{"observedGeneration":1,"replicas":0,"updatedReplicas":0,"availableReplicas":0,"conditions":[{"type":"Progressing","status":"True","reason":"NewReplicaSetAvailable"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Deployment/new spec without observed status",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":2},"spec":{"replicas":2},"status":{"conditions":[{"type":"Progressing","status":"True","reason":"NewReplicaSetCreated"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "StatefulSet/startup scaling",
			karta:  kartas.StatefulSet,
			object: `{"metadata":{"generation":1},"spec":{"replicas":2},"status":{"observedGeneration":1,"replicas":1,"readyReplicas":0,"updatedReplicas":1,"currentRevision":"a","updateRevision":"a"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "StatefulSet/startup readiness only",
			karta:  kartas.StatefulSet,
			object: `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{"observedGeneration":1,"replicas":1,"readyReplicas":0,"updatedReplicas":1,"currentRevision":"a","updateRevision":"a"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "StatefulSet/settled",
			karta:  kartas.StatefulSet,
			object: `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{"observedGeneration":1,"replicas":1,"readyReplicas":1,"updatedReplicas":1,"currentRevision":"a","updateRevision":"a"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "StatefulSet/healthy rolling update",
			karta:  kartas.StatefulSet,
			object: `{"metadata":{"generation":1},"spec":{"replicas":2},"status":{"observedGeneration":1,"replicas":2,"readyReplicas":2,"updatedReplicas":1,"currentRevision":"a","updateRevision":"b"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "StatefulSet/blocked rolling update",
			karta:  kartas.StatefulSet,
			object: `{"metadata":{"generation":1},"spec":{"replicas":2},"status":{"observedGeneration":1,"replicas":2,"readyReplicas":0,"updatedReplicas":1,"currentRevision":"a","updateRevision":"b"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "StatefulSet/readiness regression",
			karta:  kartas.StatefulSet,
			object: `{"metadata":{"generation":1},"spec":{"replicas":2},"status":{"observedGeneration":1,"replicas":2,"readyReplicas":1,"updatedReplicas":2,"currentRevision":"a","updateRevision":"a"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.DegradedStatus},
		},
		{
			name:   "StatefulSet/partition fulfilled",
			karta:  kartas.StatefulSet,
			object: `{"metadata":{"generation":1},"spec":{"replicas":3,"updateStrategy":{"type":"RollingUpdate","rollingUpdate":{"partition":2}}},"status":{"observedGeneration":1,"replicas":3,"readyReplicas":3,"updatedReplicas":1,"currentRevision":"a","updateRevision":"b"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "StatefulSet/partition excludes all pods",
			karta:  kartas.StatefulSet,
			object: `{"metadata":{"generation":1},"spec":{"replicas":3,"updateStrategy":{"type":"RollingUpdate","rollingUpdate":{"partition":3}}},"status":{"observedGeneration":1,"replicas":3,"readyReplicas":3,"updatedReplicas":0,"currentRevision":"a","updateRevision":"b"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "StatefulSet/OnDelete awaits explicit deletion",
			karta:  kartas.StatefulSet,
			object: `{"metadata":{"generation":1},"spec":{"replicas":1,"updateStrategy":{"type":"OnDelete"}},"status":{"observedGeneration":1,"replicas":1,"readyReplicas":1,"updatedReplicas":0,"currentRevision":"a","updateRevision":"b"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "StatefulSet/scale down while serving",
			karta:  kartas.StatefulSet,
			object: `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{"observedGeneration":1,"replicas":2,"readyReplicas":2,"updatedReplicas":2,"currentRevision":"a","updateRevision":"a"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "StatefulSet/scale zero draining",
			karta:  kartas.StatefulSet,
			object: `{"metadata":{"generation":1},"spec":{"replicas":0},"status":{"observedGeneration":1,"replicas":1,"readyReplicas":1,"updatedReplicas":1,"currentRevision":"a","updateRevision":"a"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.ProgressingStatus},
		},
		{
			name:   "StatefulSet/settled zero",
			karta:  kartas.StatefulSet,
			object: `{"metadata":{"generation":1},"spec":{"replicas":0},"status":{"observedGeneration":1,"replicas":0,"readyReplicas":0,"updatedReplicas":0,"currentRevision":"a","updateRevision":"a"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "StatefulSet/stale generation",
			karta:  kartas.StatefulSet,
			object: `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{"observedGeneration":0,"replicas":1,"readyReplicas":1,"updatedReplicas":1,"currentRevision":"a","updateRevision":"a"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "CronJob/waiting first schedule",
			karta:  kartas.CronJob,
			object: `{"status":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "CronJob/idle established schedule",
			karta:  kartas.CronJob,
			object: `{"status":{"lastScheduleTime":"2026-01-01T00:00:00Z","active":[]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "CronJob/active schedule",
			karta:  kartas.CronJob,
			object: `{"status":{"lastScheduleTime":"2026-01-01T00:00:00Z","active":[{"name":"job"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "CronJob/suspended before first schedule",
			karta:  kartas.CronJob,
			object: `{"spec":{"suspend":true},"status":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.SuspendedStatus},
		},
		{
			name:   "CronJob/suspended established schedule",
			karta:  kartas.CronJob,
			object: `{"spec":{"suspend":true},"status":{"lastScheduleTime":"2026-01-01T00:00:00Z"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.SuspendedStatus},
		},
		{
			name:   "BatchJob/active pods not ready",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":1},"status":{"active":1,"conditions":[],"ready":0}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "BatchJob/active without readiness field",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":1},"status":{"active":1,"conditions":[]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/ready pod",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":1},"status":{"active":1,"conditions":[],"ready":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "BatchJob/partially ready",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2},"status":{"active":2,"conditions":[],"ready":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus, v1alpha1.PendingStatus},
		},
		{
			name:   "BatchJob/zero parallelism",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":0},"status":{"active":1,"conditions":[],"ready":0}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/suspend requested",
			karta:  kartas.BatchJob,
			object: `{"spec":{"suspend":true},"status":{"active":1,"conditions":[],"ready":0}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/suspended historical active",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":1},"status":{"active":1,"conditions":[{"type":"Suspended","status":"True"}],"ready":0}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.SuspendedStatus},
		},
		{
			name:   "BatchJob/Complete suppresses active counters",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2},"status":{"active":2,"conditions":[{"type":"Complete","status":"True"}],"ready":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.CompletedStatus},
		},
		{
			name:   "BatchJob/SuccessCriteriaMet suppresses active counters",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2},"status":{"active":2,"conditions":[{"type":"SuccessCriteriaMet","status":"True"}],"ready":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.CompletedStatus},
		},
		{
			name:   "BatchJob/Failed suppresses active counters",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2},"status":{"active":2,"conditions":[{"type":"Failed","status":"True"}],"ready":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.FailedStatus},
		},
		{
			name:   "BatchJob/FailureTarget suppresses active counters",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2},"status":{"active":2,"conditions":[{"type":"FailureTarget","status":"True"}],"ready":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.FailedStatus},
		},
		{
			name:   "BatchJob/completed suppresses degraded",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2},"status":{"active":1,"ready":0,"succeeded":2,"conditions":[{"type":"Complete","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.CompletedStatus},
		},
		{
			name:   "BatchJob/retry with ready peer",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2},"status":{"active":2,"ready":1,"failed":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.RunningStatus, v1alpha1.DegradedStatus},
		},
		{
			name:   "Jobset/active jobs are not ready",
			karta:  kartas.Jobset,
			object: `{"spec":{"suspend":false,"replicatedJobs":[{"name":"workers","replicas":1}]},"status":{"replicatedJobsStatus":[{"name":"workers","active":1,"ready":0,"succeeded":0,"failed":0}],"conditions":[]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Jobset/dependent jobs not created",
			karta:  kartas.Jobset,
			object: `{"spec":{"suspend":false,"replicatedJobs":[{"name":"workers","replicas":1}]},"status":{"replicatedJobsStatus":[{"name":"workers","active":0,"ready":0,"succeeded":0,"failed":0}],"conditions":[]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Jobset/ordered startup blocked",
			karta:  kartas.Jobset,
			object: `{"spec":{"suspend":false,"replicatedJobs":[{"name":"workers","replicas":1}],"startupPolicy":{"startupPolicyOrder":"InOrder"}},"status":{"replicatedJobsStatus":[{"name":"workers","active":1,"ready":0,"succeeded":0,"failed":0}],"conditions":[{"type":"StartupPolicyInProgress","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "Jobset/startup condition stale under AnyOrder",
			karta:  kartas.Jobset,
			object: `{"spec":{"suspend":false,"replicatedJobs":[{"name":"workers","replicas":1}],"startupPolicy":{"startupPolicyOrder":"AnyOrder"}},"status":{"replicatedJobsStatus":[{"name":"workers","active":1,"ready":0,"succeeded":0,"failed":0}],"conditions":[{"type":"StartupPolicyInProgress","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Jobset/partial ordered startup",
			karta:  kartas.Jobset,
			object: `{"spec":{"suspend":false,"replicatedJobs":[{"name":"workers","replicas":2}],"startupPolicy":{"startupPolicyOrder":"InOrder"}},"status":{"replicatedJobsStatus":[{"name":"workers","active":2,"ready":1,"succeeded":0,"failed":0}],"conditions":[{"type":"StartupPolicyInProgress","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus, v1alpha1.RunningStatus},
		},
		{
			name:   "Jobset/ready jobs",
			karta:  kartas.Jobset,
			object: `{"spec":{"suspend":false,"replicatedJobs":[{"name":"workers","replicas":1}]},"status":{"replicatedJobsStatus":[{"name":"workers","active":1,"ready":1,"succeeded":0,"failed":0}],"conditions":[]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "Jobset/restart blocked",
			karta:  kartas.Jobset,
			object: `{"spec":{"suspend":false,"replicatedJobs":[{"name":"workers","replicas":1}]},"status":{"replicatedJobsStatus":[{"name":"workers","active":0,"ready":0,"succeeded":0,"failed":0}],"conditions":[{"type":"RestartingJobSet","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "Jobset/completed guards startup condition",
			karta:  kartas.Jobset,
			object: `{"spec":{"suspend":false,"replicatedJobs":[{"name":"workers","replicas":1}],"startupPolicy":{"startupPolicyOrder":"InOrder"}},"status":{"replicatedJobsStatus":[{"name":"workers","active":1,"ready":1,"succeeded":0,"failed":0}],"conditions":[{"type":"Completed","status":"True"},{"type":"StartupPolicyInProgress","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.CompletedStatus},
		},
		{
			name:   "Jobset/failed guards restart condition",
			karta:  kartas.Jobset,
			object: `{"spec":{"suspend":false,"replicatedJobs":[{"name":"workers","replicas":1}]},"status":{"replicatedJobsStatus":[{"name":"workers","active":1,"ready":0,"succeeded":0,"failed":0}],"conditions":[{"type":"Failed","status":"True"},{"type":"RestartingJobSet","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.FailedStatus},
		},
		{
			name:   "Jobset/suspended guards startup condition",
			karta:  kartas.Jobset,
			object: `{"spec":{"suspend":true,"replicatedJobs":[{"name":"workers","replicas":1}],"startupPolicy":{"startupPolicyOrder":"InOrder"}},"status":{"replicatedJobsStatus":[{"name":"workers","active":1,"ready":0,"succeeded":0,"failed":0}],"conditions":[{"type":"Suspended","status":"True"},{"type":"StartupPolicyInProgress","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.SuspendedStatus},
		},
		{
			name:   "Jobset/resumed ignores stale suspended condition",
			karta:  kartas.Jobset,
			object: `{"spec":{"suspend":false,"replicatedJobs":[{"name":"workers","replicas":1}],"startupPolicy":{"startupPolicyOrder":"InOrder"}},"status":{"replicatedJobsStatus":[{"name":"workers","active":1,"ready":0,"succeeded":0,"failed":0}],"conditions":[{"type":"Suspended","status":"True"},{"type":"StartupPolicyInProgress","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "Jobset/succeeded jobs do not wait for readiness",
			karta:  kartas.Jobset,
			object: `{"spec":{"suspend":false,"replicatedJobs":[{"name":"workers","replicas":1}]},"status":{"replicatedJobsStatus":[{"name":"workers","active":0,"ready":0,"succeeded":1,"failed":0}],"conditions":[]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Jobset/zero replicas",
			karta:  kartas.Jobset,
			object: `{"spec":{"suspend":false,"replicatedJobs":[{"name":"workers","replicas":0}]},"status":{"replicatedJobsStatus":[{"name":"workers","active":0,"ready":0,"succeeded":0,"failed":0}],"conditions":[]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Jobset/empty observed groups",
			karta:  kartas.Jobset,
			object: `{"spec":{"replicatedJobs":[{"name":"workers","replicas":1}]},"status":{"replicatedJobsStatus":[]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "LWS/startup",
			karta:  kartas.LWS,
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"conditions":[{"type":"Progressing","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "LWS/blocked update no ready groups",
			karta:  kartas.LWS,
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"conditions":[{"type":"Progressing","status":"False"},{"type":"UpdateInProgress","status":"True"}],"replicas":1,"readyReplicas":0}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "LWS/settled available",
			karta:  kartas.LWS,
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"conditions":[{"type":"Available","status":"True"},{"type":"Progressing","status":"False"},{"type":"UpdateInProgress","status":"False"}],"replicas":1,"readyReplicas":1,"updatedReplicas":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "LWS/healthy update",
			karta:  kartas.LWS,
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"conditions":[{"type":"Available","status":"True"},{"type":"Progressing","status":"False"},{"type":"UpdateInProgress","status":"True"}],"replicas":1,"readyReplicas":1,"updatedReplicas":0}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "LWS/counts fallback",
			karta:  kartas.LWS,
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"conditions":[],"replicas":1,"readyReplicas":1,"updatedReplicas":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "LWS/stale counts during scale up",
			karta:  kartas.LWS,
			object: `{"metadata":{"generation":2},"spec":{"replicas":2},"status":{"conditions":[{"type":"Progressing","status":"True"}],"replicas":1,"readyReplicas":1,"updatedReplicas":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "LWS/explicit stale generation",
			karta:  kartas.LWS,
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"conditions":[{"type":"Available","status":"True"},{"type":"UpdateInProgress","status":"True"}],"replicas":1,"readyReplicas":1,"updatedReplicas":1,"observedGeneration":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "LWS/settled zero",
			karta:  kartas.LWS,
			object: `{"metadata":{"generation":2},"spec":{"replicas":0},"status":{"conditions":[{"type":"Available","status":"True"}],"replicas":0,"readyReplicas":0}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "LWS/zero scale transition",
			karta:  kartas.LWS,
			object: `{"metadata":{"generation":2},"spec":{"replicas":0},"status":{"conditions":[{"type":"Progressing","status":"True"}],"replicas":1,"readyReplicas":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.ProgressingStatus},
		},
		{
			name:   "LWS/failed",
			karta:  kartas.LWS,
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"conditions":[{"type":"Available","status":"False"},{"type":"Progressing","status":"False"},{"type":"UpdateInProgress","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.FailedStatus},
		},
		{
			name:   "Deployment/unversioned empty object is unknown",
			karta:  kartas.Deployment,
			object: `{"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Deployment/zero generation is unknown",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":0},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Deployment/deleting before first status",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":1,"deletionTimestamp":"2026-01-01T00:00:00Z"},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Deployment/zero desired before observation",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":2},"spec":{"replicas":0},"status":{"observedGeneration":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "StatefulSet/unversioned empty object is unknown",
			karta:  kartas.StatefulSet,
			object: `{"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "StatefulSet/zero generation is unknown",
			karta:  kartas.StatefulSet,
			object: `{"metadata":{"generation":0},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "StatefulSet/deleting before first status",
			karta:  kartas.StatefulSet,
			object: `{"metadata":{"generation":1,"deletionTimestamp":"2026-01-01T00:00:00Z"},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "StatefulSet/zero desired before observation",
			karta:  kartas.StatefulSet,
			object: `{"metadata":{"generation":2},"spec":{"replicas":0},"status":{"observedGeneration":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/unversioned empty object is unknown",
			karta:  kartas.BatchJob,
			object: `{"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/zero generation is unknown",
			karta:  kartas.BatchJob,
			object: `{"metadata":{"generation":0},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/deleting before first status",
			karta:  kartas.BatchJob,
			object: `{"metadata":{"generation":1,"deletionTimestamp":"2026-01-01T00:00:00Z"},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "LWS/unversioned empty object is unknown",
			karta:  kartas.LWS,
			object: `{"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "LWS/zero generation is unknown",
			karta:  kartas.LWS,
			object: `{"metadata":{"generation":0},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "LWS/deleting before first status",
			karta:  kartas.LWS,
			object: `{"metadata":{"generation":1,"deletionTimestamp":"2026-01-01T00:00:00Z"},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "LWS/zero desired before observation",
			karta:  kartas.LWS,
			object: `{"metadata":{"generation":2},"spec":{"replicas":0},"status":{"observedGeneration":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Deployment/stale failure is not normal observation wait",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"conditions":[{"type":"ReplicaFailure","status":"True","reason":"FailedCreate"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Deployment/stale deadline is not normal observation wait",
			karta:  kartas.Deployment,
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"conditions":[{"type":"Progressing","status":"False","reason":"ProgressDeadlineExceeded"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "StatefulSet/unknown condition blocks new observation fallback",
			karta:  kartas.StatefulSet,
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"conditions":[{"type":"UnexpectedError","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/zero completions before first status",
			karta:  kartas.BatchJob,
			object: `{"metadata":{"generation":1},"spec":{"completions":0}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/suspended before first status",
			karta:  kartas.BatchJob,
			object: `{"metadata":{"generation":1},"spec":{"suspend":true}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/zero parallelism before first status",
			karta:  kartas.BatchJob,
			object: `{"metadata":{"generation":1},"spec":{"parallelism":0}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/healthy remaining completion",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":1,"ready":1,"succeeded":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "BatchJob/remaining work below parallelism",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":3,"completions":2},"status":{"active":2,"ready":2}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "BatchJob/recovered failed attempt is not degradation",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":2,"ready":2,"failed":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "BatchJob/failed retry waits beside healthy peer",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":1,"ready":1,"failed":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.RunningStatus, v1alpha1.DegradedStatus},
		},

		{
			name:   "BatchJob/uncounted failed pod beside ready peer",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":2,"ready":1,"uncountedTerminatedPods":{"failed":["failed-pod"]}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.RunningStatus, v1alpha1.DegradedStatus},
		},
		{
			name:   "BatchJob/uncounted succeeded pod is not failure evidence",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":2,"ready":1,"uncountedTerminatedPods":{"succeeded":["succeeded-pod"]}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.RunningStatus},
		},
		{
			name:   "BatchJob/empty uncounted failure list is not failure evidence",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":2,"ready":1,"uncountedTerminatedPods":{"failed":[]}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.RunningStatus},
		},
		{
			name:   "BatchJob/uncounted failed pod with restored readiness",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":2,"ready":2,"uncountedTerminatedPods":{"failed":["failed-pod"]}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "BatchJob/uncounted failed pod without active peer",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":0,"ready":1,"uncountedTerminatedPods":{"failed":["failed-pod"]}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "BatchJob/uncounted failed pod without ready peer",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":1,"ready":0,"uncountedTerminatedPods":{"failed":["failed-pod"]}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "BatchJob/uncounted failed pod after failure target",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":2,"ready":1,"uncountedTerminatedPods":{"failed":["failed-pod"]},"conditions":[{"type":"FailureTarget","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.FailedStatus},
		},
		{
			name:   "BatchJob/uncounted failed pod after success criteria met",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":2,"ready":1,"uncountedTerminatedPods":{"failed":["failed-pod"]},"conditions":[{"type":"SuccessCriteriaMet","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.CompletedStatus},
		},
		{
			name:   "BatchJob/uncounted failed pod while suspending",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2,"completions":2,"suspend":true},"status":{"active":2,"ready":1,"uncountedTerminatedPods":{"failed":["failed-pod"]}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/uncounted failed pod after suspension acknowledged",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":2,"ready":1,"uncountedTerminatedPods":{"failed":["failed-pod"]},"conditions":[{"type":"Suspended","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.SuspendedStatus},
		},
		{
			name:   "BatchJob/uncounted failed pod while deleting",
			karta:  kartas.BatchJob,
			object: `{"metadata":{"deletionTimestamp":"2026-01-01T00:00:00Z"},"spec":{"parallelism":2,"completions":2},"status":{"active":2,"ready":1,"uncountedTerminatedPods":{"failed":["failed-pod"]}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/uncounted failed pod with zero parallelism",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":0,"completions":2},"status":{"active":2,"ready":1,"uncountedTerminatedPods":{"failed":["failed-pod"]}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/all retries waiting have no running capacity",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":0,"ready":0,"failed":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "BatchJob/work queue drains after success",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2},"status":{"active":1,"ready":1,"succeeded":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "BatchJob/work queue remaining active pod not ready",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2},"status":{"active":1,"ready":0,"succeeded":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "BatchJob/scale requested before active count catches up",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":3},"status":{"active":1,"ready":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.RunningStatus},
		},
		{
			name:   "BatchJob/all completions await terminal condition",
			karta:  kartas.BatchJob,
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":0,"ready":0,"succeeded":2}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Jobset/first desired group awaits observation",
			karta:  kartas.Jobset,
			object: `{"metadata":{"generation":1},"spec":{"replicatedJobs":[{"name":"workers","replicas":1}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Jobset/zero desired group before observation",
			karta:  kartas.Jobset,
			object: `{"metadata":{"generation":1},"spec":{"replicatedJobs":[{"name":"workers","replicas":0}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Jobset/deleted group before observation",
			karta:  kartas.Jobset,
			object: `{"metadata":{"generation":1,"deletionTimestamp":"2026-01-01T00:00:00Z"},"spec":{"replicatedJobs":[{"name":"workers","replicas":1}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Jobset/suspended group before observation",
			karta:  kartas.Jobset,
			object: `{"metadata":{"generation":1},"spec":{"suspend":true,"replicatedJobs":[{"name":"workers","replicas":1}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Jobset/ready from completed pods is not active",
			karta:  kartas.Jobset,
			object: `{"spec":{"replicatedJobs":[{"name":"workers","replicas":1}]},"status":{"replicatedJobsStatus":[{"name":"workers","active":0,"ready":1,"succeeded":0}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Jobset/ready and active must describe same group",
			karta:  kartas.Jobset,
			object: `{"spec":{"replicatedJobs":[{"name":"a","replicas":1},{"name":"b","replicas":1}]},"status":{"replicatedJobsStatus":[{"name":"a","active":0,"ready":1},{"name":"b","active":1,"ready":0}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Jobset/unobserved group alongside ready group",
			karta:  kartas.Jobset,
			object: `{"metadata":{"generation":1},"spec":{"replicatedJobs":[{"name":"a","replicas":1},{"name":"b","replicas":1}]},"status":{"replicatedJobsStatus":[{"name":"a","active":1,"ready":1}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.RunningStatus},
		},
		{
			name:   "Jobset/succeeded groups reduce remaining work",
			karta:  kartas.Jobset,
			object: `{"spec":{"replicatedJobs":[{"name":"workers","replicas":2}]},"status":{"replicatedJobsStatus":[{"name":"workers","active":1,"ready":1,"succeeded":1}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "LWS/sticky Available cannot satisfy new replica target",
			karta:  kartas.LWS,
			object: `{"metadata":{"generation":2},"spec":{"replicas":2},"status":{"replicas":1,"readyReplicas":1,"conditions":[{"type":"Available","status":"True"},{"type":"Progressing","status":"False"},{"type":"UpdateInProgress","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "LWS/Available without counters does not prove running",
			karta:  kartas.LWS,
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"conditions":[{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "LWS/stale explicit failure is not normal observation wait",
			karta:  kartas.LWS,
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"conditions":[{"type":"Available","status":"False"},{"type":"Progressing","status":"False"},{"type":"UpdateInProgress","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Grove/first observation wait",
			karta:  kartas.GrovePodCliqueSet,
			object: `{"metadata":{"generation":1},"spec":{"replicas":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Grove/empty first status wait",
			karta:  kartas.GrovePodCliqueSet,
			object: `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Grove/first status without generation stays unknown",
			karta:  kartas.GrovePodCliqueSet,
			object: `{"spec":{"replicas":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Grove/deleted first status stays unknown",
			karta:  kartas.GrovePodCliqueSet,
			object: `{"metadata":{"generation":1,"deletionTimestamp":"2026-01-01T00:00:00Z"},"spec":{"replicas":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Grove/zero desired before first status stays unknown",
			karta:  kartas.GrovePodCliqueSet,
			object: `{"metadata":{"generation":1},"spec":{"replicas":0}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Grove/stale reconcile error blocks observation fallback",
			karta:  kartas.GrovePodCliqueSet,
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"lastErrors":[{"code":"ERR","description":"failed create"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Grove/zero timestamp does not prove root update",
			karta:  kartas.GrovePodCliqueSet,
			object: `{"spec":{"replicas":1},"status":{"availableReplicas":0,"updateProgress":{"updateStartedAt":"0001-01-01T00:00:00Z"}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Grove/empty timestamp does not prove root update",
			karta:  kartas.GrovePodCliqueSet,
			object: `{"spec":{"replicas":1},"status":{"availableReplicas":0,"updateProgress":{"updateStartedAt":""}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Grove/root update before child updates",
			karta:  kartas.GrovePodCliqueSet,
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"availableReplicas":1,"updateProgress":{"updateStartedAt":"2026-01-01T00:00:00Z"},"conditions":[{"type":"UpdateInProgress","status":"False","reason":"NoActiveUpdate"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "Grove/finished stale progress cannot imply active update",
			karta:  kartas.GrovePodCliqueSet,
			object: `{"metadata":{"generation":3},"spec":{"replicas":1},"status":{"observedGeneration":2,"availableReplicas":1,"updateProgress":{"updateStartedAt":"2026-01-01T00:00:00Z","updateEndedAt":"2026-01-01T00:01:00Z"}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Grove/OnDelete timestamp is not automatic update",
			karta:  kartas.GrovePodCliqueSet,
			object: `{"metadata":{"generation":2},"spec":{"replicas":1,"updateStrategy":{"type":"OnDelete"}},"status":{"observedGeneration":1,"availableReplicas":1,"updateProgress":{"updateStartedAt":"2026-01-01T00:00:00Z"}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Grove/topology failure suppresses root update",
			karta:  kartas.GrovePodCliqueSet,
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"availableReplicas":0,"updateProgress":{"updateStartedAt":"2026-01-01T00:00:00Z"},"conditions":[{"type":"TopologyLevelsUnavailable","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.FailedStatus},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := gomega.NewWithT(t)
			var object map[string]any
			g.Expect(json.Unmarshal([]byte(tt.object), &object)).To(gomega.Succeed())
			accessor := resource.NewAccessor(execution.NewDefaultRunner(object))
			definition := tt.karta().Spec.StructureDefinition.RootComponent
			status, err := accessor.ExtractStatus(context.Background(), definition)
			g.Expect(err).NotTo(gomega.HaveOccurred())
			g.Expect(status.MatchedStatuses).To(gomega.ConsistOf(tt.want))
		})
	}
}
