// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"testing"

	"github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/json"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

func TestBuiltinFlowPredicates(t *testing.T) {
	checks := map[string]map[v1alpha1.ResourceStatus]recorder.StateCheck{
		"Pod": {
			v1alpha1.PendingStatus:     PhaseEq("Pending", "status", "phase"),
			v1alpha1.ProgressingStatus: PodProgressing(),
			v1alpha1.RunningStatus:     PhaseEq("Running", "status", "phase"),
			v1alpha1.CompletedStatus:   PhaseEq("Succeeded", "status", "phase"),
			v1alpha1.FailedStatus:      PhaseEq("Failed", "status", "phase"),
		},
		"Deployment": {
			v1alpha1.PendingStatus:     DeploymentPending(),
			v1alpha1.ProgressingStatus: DeploymentProgressing(),
			v1alpha1.RunningStatus:     DeploymentRunning(),
			v1alpha1.FailedStatus:      DeploymentFailed(),
		},
		"StatefulSet": {
			v1alpha1.PendingStatus:     ReplicasPending(),
			v1alpha1.ProgressingStatus: ReplicasProgressing(),
			v1alpha1.RunningStatus:     ReplicasRunning(),
			v1alpha1.DegradedStatus:    ReplicasDegraded(),
		},
		"CronJob": {
			v1alpha1.PendingStatus:   CronjobPending(),
			v1alpha1.RunningStatus:   CronjobFired(),
			v1alpha1.SuspendedStatus: BoolTrue("spec", "suspend"),
		},
		"BatchJob": {
			v1alpha1.PendingStatus:   JobPending(),
			v1alpha1.RunningStatus:   JobRunning(),
			v1alpha1.CompletedStatus: CondTrue("Complete", "SuccessCriteriaMet"),
			v1alpha1.FailedStatus:    CondTrue("Failed", "FailureTarget"),
			v1alpha1.SuspendedStatus: CondTrue("Suspended"),
			v1alpha1.DegradedStatus:  JobDegraded(),
		},
		"Jobset": {
			v1alpha1.PendingStatus:     JobsetPending(),
			v1alpha1.ProgressingStatus: JobsetProgressing(),
			v1alpha1.RunningStatus:     JobsetRunning(),
			v1alpha1.CompletedStatus:   CondTrue("Completed"),
			v1alpha1.FailedStatus:      CondTrue("Failed"),
			v1alpha1.SuspendedStatus:   JobsetSuspended(),
		},
		"Grove": {
			v1alpha1.PendingStatus:     grovePending,
			v1alpha1.ProgressingStatus: groveProgressing,
			v1alpha1.RunningStatus:     groveRunning,
			v1alpha1.FailedStatus:      CondTrue("TopologyLevelsUnavailable"),
		},
		"LWS": {
			v1alpha1.PendingStatus:     LWSPending(),
			v1alpha1.ProgressingStatus: LWSProgressing(),
			v1alpha1.RunningStatus:     LWSRunning(),
			v1alpha1.FailedStatus:      LWSFailed(),
		},
	}
	tests := []struct {
		name   string
		kind   string
		object string
		want   []v1alpha1.ResourceStatus
	}{
		{
			name:   "Pod/absent status",
			kind:   "Pod",
			object: `{"metadata":{"generation":1},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Deployment/absent status",
			kind:   "Deployment",
			object: `{"metadata":{"generation":1},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "StatefulSet/absent status",
			kind:   "StatefulSet",
			object: `{"metadata":{"generation":1},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "CronJob/absent status",
			kind:   "CronJob",
			object: `{"metadata":{"generation":1},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/absent status",
			kind:   "BatchJob",
			object: `{"metadata":{"generation":1},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Jobset/absent status",
			kind:   "Jobset",
			object: `{"metadata":{"generation":1},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "LWS/absent status",
			kind:   "LWS",
			object: `{"metadata":{"generation":1},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Pod/unscheduled",
			kind:   "Pod",
			object: `{"status":{"phase":"Pending","conditions":[{"type":"PodScheduled","status":"False","reason":"Unschedulable"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Pod/scheduled image pull waiting",
			kind:   "Pod",
			object: `{"status":{"phase":"Pending","conditions":[{"type":"PodScheduled","status":"True"}],"containerStatuses":[{"state":{"waiting":{"reason":"ImagePullBackOff"}}}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "Pod/deleting pending pod",
			kind:   "Pod",
			object: `{"metadata":{"deletionTimestamp":"2026-01-01T00:00:00Z"},"status":{"phase":"Pending","conditions":[{"type":"PodScheduled","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Deployment/startup",
			kind:   "Deployment",
			object: `{"metadata":{"generation":1},"spec":{"replicas":1,"paused":false},"status":{"observedGeneration":1,"replicas":0,"updatedReplicas":0,"availableReplicas":0,"conditions":[{"type":"Progressing","status":"True","reason":"NewReplicaSetCreated"},{"type":"Available","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "Deployment/blocked rollout",
			kind:   "Deployment",
			object: `{"metadata":{"generation":1},"spec":{"replicas":1,"paused":false},"status":{"observedGeneration":1,"replicas":1,"updatedReplicas":1,"availableReplicas":0,"conditions":[{"type":"Progressing","status":"False","reason":"ProgressDeadlineExceeded"},{"type":"Available","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus, v1alpha1.FailedStatus},
		},
		{
			name:   "Deployment/settled sticky success",
			kind:   "Deployment",
			object: `{"metadata":{"generation":1},"spec":{"replicas":1,"paused":false},"status":{"observedGeneration":1,"replicas":1,"updatedReplicas":1,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"True","reason":"NewReplicaSetAvailable"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "Deployment/partial capacity rollout",
			kind:   "Deployment",
			object: `{"metadata":{"generation":1},"spec":{"replicas":2,"paused":false},"status":{"observedGeneration":1,"replicas":2,"updatedReplicas":1,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"True","reason":"ReplicaSetUpdated"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.RunningStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "Deployment/availability lost after success",
			kind:   "Deployment",
			object: `{"metadata":{"generation":1},"spec":{"replicas":1,"paused":false},"status":{"observedGeneration":1,"replicas":1,"updatedReplicas":1,"availableReplicas":0,"conditions":[{"type":"Progressing","status":"True","reason":"NewReplicaSetAvailable"},{"type":"Available","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Deployment/paused scale up",
			kind:   "Deployment",
			object: `{"metadata":{"generation":2},"spec":{"replicas":3,"paused":true},"status":{"observedGeneration":2,"replicas":1,"updatedReplicas":0,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"Unknown","reason":"DeploymentPaused"},{"type":"Available","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "Deployment/paused scale down",
			kind:   "Deployment",
			object: `{"metadata":{"generation":2},"spec":{"replicas":1,"paused":true},"status":{"observedGeneration":2,"replicas":3,"updatedReplicas":0,"availableReplicas":3,"conditions":[{"type":"Progressing","status":"Unknown","reason":"DeploymentPaused"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "Deployment/paused template update only",
			kind:   "Deployment",
			object: `{"metadata":{"generation":2},"spec":{"replicas":3,"paused":true},"status":{"observedGeneration":2,"replicas":3,"updatedReplicas":0,"availableReplicas":3,"conditions":[{"type":"Progressing","status":"Unknown","reason":"DeploymentPaused"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "Deployment/paused readiness wait after counts converge",
			kind:   "Deployment",
			object: `{"metadata":{"generation":2},"spec":{"replicas":3,"paused":true},"status":{"observedGeneration":2,"replicas":3,"updatedReplicas":3,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"Unknown","reason":"DeploymentPaused"},{"type":"Available","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Deployment/paused scale to zero draining",
			kind:   "Deployment",
			object: `{"metadata":{"generation":2},"spec":{"replicas":0,"paused":true},"status":{"observedGeneration":2,"replicas":1,"updatedReplicas":0,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"Unknown","reason":"DeploymentPaused"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.ProgressingStatus},
		},
		{
			name:   "Deployment/paused settled zero",
			kind:   "Deployment",
			object: `{"metadata":{"generation":2},"spec":{"replicas":0,"paused":true},"status":{"observedGeneration":2,"replicas":0,"updatedReplicas":0,"availableReplicas":0,"conditions":[{"type":"Progressing","status":"Unknown","reason":"DeploymentPaused"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Deployment/paused scale with stale status",
			kind:   "Deployment",
			object: `{"metadata":{"generation":2},"spec":{"replicas":3,"paused":true},"status":{"observedGeneration":1,"replicas":1,"updatedReplicas":0,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"Unknown","reason":"DeploymentPaused"},{"type":"Available","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Deployment/deleting paused scale",
			kind:   "Deployment",
			object: `{"metadata":{"generation":2,"deletionTimestamp":"2026-01-01T00:00:00Z"},"spec":{"replicas":3,"paused":true},"status":{"observedGeneration":2,"replicas":1,"updatedReplicas":0,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"Unknown","reason":"DeploymentPaused"},{"type":"Available","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Deployment/stale generation",
			kind:   "Deployment",
			object: `{"metadata":{"generation":1},"spec":{"replicas":1,"paused":false},"status":{"observedGeneration":0,"replicas":1,"updatedReplicas":1,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"True","reason":"NewReplicaSetAvailable"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Deployment/scale to zero draining",
			kind:   "Deployment",
			object: `{"metadata":{"generation":1},"spec":{"replicas":0,"paused":false},"status":{"observedGeneration":1,"replicas":1,"updatedReplicas":1,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"True","reason":"NewReplicaSetAvailable"},{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.ProgressingStatus},
		},
		{
			name:   "StatefulSet/startup readiness only",
			kind:   "StatefulSet",
			object: `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{"observedGeneration":1,"replicas":1,"readyReplicas":0,"updatedReplicas":1,"currentRevision":"a","updateRevision":"a"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "StatefulSet/healthy rolling update",
			kind:   "StatefulSet",
			object: `{"metadata":{"generation":1},"spec":{"replicas":2},"status":{"observedGeneration":1,"replicas":2,"readyReplicas":2,"updatedReplicas":1,"currentRevision":"a","updateRevision":"b"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "StatefulSet/readiness regression",
			kind:   "StatefulSet",
			object: `{"metadata":{"generation":1},"spec":{"replicas":2},"status":{"observedGeneration":1,"replicas":2,"readyReplicas":1,"updatedReplicas":2,"currentRevision":"a","updateRevision":"a"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.DegradedStatus},
		},
		{
			name:   "StatefulSet/partition fulfilled",
			kind:   "StatefulSet",
			object: `{"metadata":{"generation":1},"spec":{"replicas":3,"updateStrategy":{"type":"RollingUpdate","rollingUpdate":{"partition":2}}},"status":{"observedGeneration":1,"replicas":3,"readyReplicas":3,"updatedReplicas":1,"currentRevision":"a","updateRevision":"b"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "StatefulSet/OnDelete awaits explicit deletion",
			kind:   "StatefulSet",
			object: `{"metadata":{"generation":1},"spec":{"replicas":1,"updateStrategy":{"type":"OnDelete"}},"status":{"observedGeneration":1,"replicas":1,"readyReplicas":1,"updatedReplicas":0,"currentRevision":"a","updateRevision":"b"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "StatefulSet/stale generation",
			kind:   "StatefulSet",
			object: `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{"observedGeneration":0,"replicas":1,"readyReplicas":1,"updatedReplicas":1,"currentRevision":"a","updateRevision":"a"}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "CronJob/waiting first schedule",
			kind:   "CronJob",
			object: `{"status":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "CronJob/idle established schedule",
			kind:   "CronJob",
			object: `{"status":{"lastScheduleTime":"2026-01-01T00:00:00Z","active":[]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "CronJob/suspended before first schedule",
			kind:   "CronJob",
			object: `{"spec":{"suspend":true},"status":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.SuspendedStatus},
		},
		{
			name:   "BatchJob/active without readiness field",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":1},"status":{"active":1,"conditions":[]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/partially ready",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2},"status":{"active":2,"conditions":[],"ready":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus, v1alpha1.PendingStatus},
		},
		{
			name:   "BatchJob/Complete suppresses active counters",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2},"status":{"active":2,"conditions":[{"type":"Complete","status":"True"}],"ready":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.CompletedStatus},
		},
		{
			name:   "BatchJob/retry with ready peer",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2},"status":{"active":2,"ready":1,"failed":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.RunningStatus, v1alpha1.DegradedStatus},
		},
		{
			name:   "Jobset/active jobs are not ready",
			kind:   "Jobset",
			object: `{"spec":{"suspend":false,"replicatedJobs":[{"name":"workers","replicas":1}]},"status":{"replicatedJobsStatus":[{"name":"workers","active":1,"ready":0,"succeeded":0,"failed":0}],"conditions":[]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Jobset/dependent jobs not created",
			kind:   "Jobset",
			object: `{"spec":{"suspend":false,"replicatedJobs":[{"name":"workers","replicas":1}]},"status":{"replicatedJobsStatus":[{"name":"workers","active":0,"ready":0,"succeeded":0,"failed":0}],"conditions":[]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Jobset/ordered startup blocked",
			kind:   "Jobset",
			object: `{"spec":{"suspend":false,"replicatedJobs":[{"name":"workers","replicas":1}],"startupPolicy":{"startupPolicyOrder":"InOrder"}},"status":{"replicatedJobsStatus":[{"name":"workers","active":1,"ready":0,"succeeded":0,"failed":0}],"conditions":[{"type":"StartupPolicyInProgress","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "Jobset/ready jobs",
			kind:   "Jobset",
			object: `{"spec":{"suspend":false,"replicatedJobs":[{"name":"workers","replicas":1}]},"status":{"replicatedJobsStatus":[{"name":"workers","active":1,"ready":1,"succeeded":0,"failed":0}],"conditions":[]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "Jobset/completed guards startup condition",
			kind:   "Jobset",
			object: `{"spec":{"suspend":false,"replicatedJobs":[{"name":"workers","replicas":1}],"startupPolicy":{"startupPolicyOrder":"InOrder"}},"status":{"replicatedJobsStatus":[{"name":"workers","active":1,"ready":1,"succeeded":0,"failed":0}],"conditions":[{"type":"Completed","status":"True"},{"type":"StartupPolicyInProgress","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.CompletedStatus},
		},
		{
			name:   "Jobset/resumed ignores stale suspended condition",
			kind:   "Jobset",
			object: `{"spec":{"suspend":false,"replicatedJobs":[{"name":"workers","replicas":1}],"startupPolicy":{"startupPolicyOrder":"InOrder"}},"status":{"replicatedJobsStatus":[{"name":"workers","active":1,"ready":0,"succeeded":0,"failed":0}],"conditions":[{"type":"Suspended","status":"True"},{"type":"StartupPolicyInProgress","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "LWS/startup",
			kind:   "LWS",
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"conditions":[{"type":"Progressing","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "LWS/healthy update",
			kind:   "LWS",
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"conditions":[{"type":"Available","status":"False"},{"type":"Progressing","status":"False"},{"type":"UpdateInProgress","status":"True"}],"replicas":1,"readyReplicas":1,"updatedReplicas":0}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "LWS/stale counts during scale up",
			kind:   "LWS",
			object: `{"metadata":{"generation":2},"spec":{"replicas":2},"status":{"conditions":[{"type":"Progressing","status":"True"}],"replicas":1,"readyReplicas":1,"updatedReplicas":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "LWS/explicit stale generation",
			kind:   "LWS",
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"conditions":[{"type":"Available","status":"True"},{"type":"UpdateInProgress","status":"True"}],"replicas":1,"readyReplicas":1,"updatedReplicas":1,"observedGeneration":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "LWS/zero scale transition",
			kind:   "LWS",
			object: `{"metadata":{"generation":2},"spec":{"replicas":0},"status":{"conditions":[{"type":"Progressing","status":"True"}],"replicas":1,"readyReplicas":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.ProgressingStatus},
		},
		{
			name:   "Deployment/unversioned empty object is unknown",
			kind:   "Deployment",
			object: `{"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Deployment/zero generation is unknown",
			kind:   "Deployment",
			object: `{"metadata":{"generation":0},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Deployment/deleting before first status",
			kind:   "Deployment",
			object: `{"metadata":{"generation":1,"deletionTimestamp":"2026-01-01T00:00:00Z"},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Deployment/zero desired before observation",
			kind:   "Deployment",
			object: `{"metadata":{"generation":2},"spec":{"replicas":0},"status":{"observedGeneration":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "StatefulSet/unversioned empty object is unknown",
			kind:   "StatefulSet",
			object: `{"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "StatefulSet/zero generation is unknown",
			kind:   "StatefulSet",
			object: `{"metadata":{"generation":0},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "StatefulSet/deleting before first status",
			kind:   "StatefulSet",
			object: `{"metadata":{"generation":1,"deletionTimestamp":"2026-01-01T00:00:00Z"},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "StatefulSet/zero desired before observation",
			kind:   "StatefulSet",
			object: `{"metadata":{"generation":2},"spec":{"replicas":0},"status":{"observedGeneration":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/unversioned empty object is unknown",
			kind:   "BatchJob",
			object: `{"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/zero generation is unknown",
			kind:   "BatchJob",
			object: `{"metadata":{"generation":0},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/deleting before first status",
			kind:   "BatchJob",
			object: `{"metadata":{"generation":1,"deletionTimestamp":"2026-01-01T00:00:00Z"},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "LWS/unversioned empty object is unknown",
			kind:   "LWS",
			object: `{"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "LWS/zero generation is unknown",
			kind:   "LWS",
			object: `{"metadata":{"generation":0},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "LWS/deleting before first status",
			kind:   "LWS",
			object: `{"metadata":{"generation":1,"deletionTimestamp":"2026-01-01T00:00:00Z"},"spec":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "LWS/zero desired before observation",
			kind:   "LWS",
			object: `{"metadata":{"generation":2},"spec":{"replicas":0},"status":{"observedGeneration":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Deployment/stale failure is not normal observation wait",
			kind:   "Deployment",
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"conditions":[{"type":"ReplicaFailure","status":"True","reason":"FailedCreate"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Deployment/stale deadline is not normal observation wait",
			kind:   "Deployment",
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"conditions":[{"type":"Progressing","status":"False","reason":"ProgressDeadlineExceeded"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "StatefulSet/unknown condition blocks new observation fallback",
			kind:   "StatefulSet",
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"conditions":[{"type":"UnexpectedError","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/zero completions before first status",
			kind:   "BatchJob",
			object: `{"metadata":{"generation":1},"spec":{"completions":0}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/suspended before first status",
			kind:   "BatchJob",
			object: `{"metadata":{"generation":1},"spec":{"suspend":true}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/zero parallelism before first status",
			kind:   "BatchJob",
			object: `{"metadata":{"generation":1},"spec":{"parallelism":0}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/healthy remaining completion",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":1,"ready":1,"succeeded":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "BatchJob/remaining work below parallelism",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":3,"completions":2},"status":{"active":2,"ready":2}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "BatchJob/recovered failed attempt is not degradation",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":2,"ready":2,"failed":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "BatchJob/failed retry waits beside healthy peer",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":1,"ready":1,"failed":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.RunningStatus, v1alpha1.DegradedStatus},
		},

		{
			name:   "BatchJob/uncounted failed pod beside ready peer",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":2,"ready":1,"uncountedTerminatedPods":{"failed":["failed-pod"]}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.RunningStatus, v1alpha1.DegradedStatus},
		},
		{
			name:   "BatchJob/uncounted succeeded pod is not failure evidence",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":2,"ready":1,"uncountedTerminatedPods":{"succeeded":["succeeded-pod"]}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.RunningStatus},
		},
		{
			name:   "BatchJob/empty uncounted failure list is not failure evidence",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":2,"ready":1,"uncountedTerminatedPods":{"failed":[]}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.RunningStatus},
		},
		{
			name:   "BatchJob/uncounted failed pod with restored readiness",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":2,"ready":2,"uncountedTerminatedPods":{"failed":["failed-pod"]}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "BatchJob/uncounted failed pod without active peer",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":0,"ready":1,"uncountedTerminatedPods":{"failed":["failed-pod"]}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "BatchJob/uncounted failed pod without ready peer",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":1,"ready":0,"uncountedTerminatedPods":{"failed":["failed-pod"]}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "BatchJob/uncounted failed pod after failure target",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":2,"ready":1,"uncountedTerminatedPods":{"failed":["failed-pod"]},"conditions":[{"type":"FailureTarget","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.FailedStatus},
		},
		{
			name:   "BatchJob/uncounted failed pod after success criteria met",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":2,"ready":1,"uncountedTerminatedPods":{"failed":["failed-pod"]},"conditions":[{"type":"SuccessCriteriaMet","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.CompletedStatus},
		},
		{
			name:   "BatchJob/uncounted failed pod while suspending",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2,"completions":2,"suspend":true},"status":{"active":2,"ready":1,"uncountedTerminatedPods":{"failed":["failed-pod"]}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/uncounted failed pod after suspension acknowledged",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":2,"ready":1,"uncountedTerminatedPods":{"failed":["failed-pod"]},"conditions":[{"type":"Suspended","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.SuspendedStatus},
		},
		{
			name:   "BatchJob/uncounted failed pod while deleting",
			kind:   "BatchJob",
			object: `{"metadata":{"deletionTimestamp":"2026-01-01T00:00:00Z"},"spec":{"parallelism":2,"completions":2},"status":{"active":2,"ready":1,"uncountedTerminatedPods":{"failed":["failed-pod"]}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/uncounted failed pod with zero parallelism",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":0,"completions":2},"status":{"active":2,"ready":1,"uncountedTerminatedPods":{"failed":["failed-pod"]}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "BatchJob/all retries waiting have no running capacity",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":0,"ready":0,"failed":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "BatchJob/work queue drains after success",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2},"status":{"active":1,"ready":1,"succeeded":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "BatchJob/work queue remaining active pod not ready",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2},"status":{"active":1,"ready":0,"succeeded":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "BatchJob/scale requested before active count catches up",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":3},"status":{"active":1,"ready":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.RunningStatus},
		},
		{
			name:   "BatchJob/all completions await terminal condition",
			kind:   "BatchJob",
			object: `{"spec":{"parallelism":2,"completions":2},"status":{"active":0,"ready":0,"succeeded":2}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Jobset/first desired group awaits observation",
			kind:   "Jobset",
			object: `{"metadata":{"generation":1},"spec":{"replicatedJobs":[{"name":"workers","replicas":1}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Jobset/zero desired group before observation",
			kind:   "Jobset",
			object: `{"metadata":{"generation":1},"spec":{"replicatedJobs":[{"name":"workers","replicas":0}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Jobset/deleted group before observation",
			kind:   "Jobset",
			object: `{"metadata":{"generation":1,"deletionTimestamp":"2026-01-01T00:00:00Z"},"spec":{"replicatedJobs":[{"name":"workers","replicas":1}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Jobset/suspended group before observation",
			kind:   "Jobset",
			object: `{"metadata":{"generation":1},"spec":{"suspend":true,"replicatedJobs":[{"name":"workers","replicas":1}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Jobset/ready from completed pods is not active",
			kind:   "Jobset",
			object: `{"spec":{"replicatedJobs":[{"name":"workers","replicas":1}]},"status":{"replicatedJobsStatus":[{"name":"workers","active":0,"ready":1,"succeeded":0}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Jobset/ready and active must describe same group",
			kind:   "Jobset",
			object: `{"spec":{"replicatedJobs":[{"name":"a","replicas":1},{"name":"b","replicas":1}]},"status":{"replicatedJobsStatus":[{"name":"a","active":0,"ready":1},{"name":"b","active":1,"ready":0}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Jobset/unobserved group alongside ready group",
			kind:   "Jobset",
			object: `{"metadata":{"generation":1},"spec":{"replicatedJobs":[{"name":"a","replicas":1},{"name":"b","replicas":1}]},"status":{"replicatedJobsStatus":[{"name":"a","active":1,"ready":1}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.RunningStatus},
		},
		{
			name:   "Jobset/succeeded groups reduce remaining work",
			kind:   "Jobset",
			object: `{"spec":{"replicatedJobs":[{"name":"workers","replicas":2}]},"status":{"replicatedJobsStatus":[{"name":"workers","active":1,"ready":1,"succeeded":1}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.RunningStatus},
		},
		{
			name:   "LWS/sticky Available cannot satisfy new replica target",
			kind:   "LWS",
			object: `{"metadata":{"generation":2},"spec":{"replicas":2},"status":{"replicas":1,"readyReplicas":1,"conditions":[{"type":"Available","status":"True"},{"type":"Progressing","status":"False"},{"type":"UpdateInProgress","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "LWS/Available without counters does not prove running",
			kind:   "LWS",
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"conditions":[{"type":"Available","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "LWS/stale explicit failure is not normal observation wait",
			kind:   "LWS",
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"conditions":[{"type":"Available","status":"False"},{"type":"Progressing","status":"False"},{"type":"UpdateInProgress","status":"False"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Grove/first observation wait",
			kind:   "Grove",
			object: `{"metadata":{"generation":1},"spec":{"replicas":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Grove/empty first status wait",
			kind:   "Grove",
			object: `{"metadata":{"generation":1},"spec":{"replicas":1},"status":{}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Grove/first status without generation stays unknown",
			kind:   "Grove",
			object: `{"spec":{"replicas":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Grove/deleted first status stays unknown",
			kind:   "Grove",
			object: `{"metadata":{"generation":1,"deletionTimestamp":"2026-01-01T00:00:00Z"},"spec":{"replicas":1}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Grove/zero desired before first status stays unknown",
			kind:   "Grove",
			object: `{"metadata":{"generation":1},"spec":{"replicas":0}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Grove/stale reconcile error blocks observation fallback",
			kind:   "Grove",
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"lastErrors":[{"code":"ERR","description":"failed create"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus},
		},
		{
			name:   "Grove/zero timestamp does not prove root update",
			kind:   "Grove",
			object: `{"spec":{"replicas":1},"status":{"availableReplicas":0,"updateProgress":{"updateStartedAt":"0001-01-01T00:00:00Z"}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Grove/empty timestamp does not prove root update",
			kind:   "Grove",
			object: `{"spec":{"replicas":1},"status":{"availableReplicas":0,"updateProgress":{"updateStartedAt":""}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Grove/root update before child updates",
			kind:   "Grove",
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"availableReplicas":1,"updateProgress":{"updateStartedAt":"2026-01-01T00:00:00Z"},"conditions":[{"type":"UpdateInProgress","status":"False","reason":"NoActiveUpdate"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus, v1alpha1.ProgressingStatus},
		},
		{
			name:   "Grove/finished stale progress cannot imply active update",
			kind:   "Grove",
			object: `{"metadata":{"generation":3},"spec":{"replicas":1},"status":{"observedGeneration":2,"availableReplicas":1,"updateProgress":{"updateStartedAt":"2026-01-01T00:00:00Z","updateEndedAt":"2026-01-01T00:01:00Z"}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Grove/OnDelete timestamp is not automatic update",
			kind:   "Grove",
			object: `{"metadata":{"generation":2},"spec":{"replicas":1,"updateStrategy":{"type":"OnDelete"}},"status":{"observedGeneration":1,"availableReplicas":1,"updateProgress":{"updateStartedAt":"2026-01-01T00:00:00Z"}}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.PendingStatus},
		},
		{
			name:   "Grove/topology failure suppresses root update",
			kind:   "Grove",
			object: `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"availableReplicas":0,"updateProgress":{"updateStartedAt":"2026-01-01T00:00:00Z"},"conditions":[{"type":"TopologyLevelsUnavailable","status":"True"}]}}`,
			want:   []v1alpha1.ResourceStatus{v1alpha1.FailedStatus},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := gomega.NewWithT(t)
			object := &unstructured.Unstructured{}
			g.Expect(json.Unmarshal([]byte(tt.object), &object.Object)).To(gomega.Succeed())
			var matched []v1alpha1.ResourceStatus
			for status, check := range checks[tt.kind] {
				if check(object) {
					matched = append(matched, status)
				}
			}
			if len(matched) == 0 {
				matched = []v1alpha1.ResourceStatus{v1alpha1.UndefinedStatus}
			}
			g.Expect(matched).To(gomega.ConsistOf(tt.want))
		})
	}
}
