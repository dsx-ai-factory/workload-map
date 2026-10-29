// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas_test

import (
	"encoding/json"
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog/kartas"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

type operatorStatusCase struct {
	name   string
	object string
	want   []v1alpha1.ResourceStatus
}

func TestOperatorStatusMappings(t *testing.T) {
	const (
		pending     = v1alpha1.PendingStatus
		progressing = v1alpha1.ProgressingStatus
		running     = v1alpha1.RunningStatus
		completed   = v1alpha1.CompletedStatus
		failed      = v1alpha1.FailedStatus
		degraded    = v1alpha1.DegradedStatus
		suspended   = v1alpha1.SuspendedStatus
		undefined   = v1alpha1.UndefinedStatus
	)
	trainingCases := []operatorStatusCase{
		{"absent", `{}`, []v1alpha1.ResourceStatus{undefined}},
		{"created", `{"status":{"conditions":[{"type":"Created","status":"True"}]}}`, []v1alpha1.ResourceStatus{pending}},
		{"partial startup", `{"status":{"conditions":[{"type":"Created","status":"True"}],"replicaStatuses":{"Worker":{"active":1}}}}`, []v1alpha1.ResourceStatus{pending, progressing}},
		{"zero active", `{"status":{"conditions":[{"type":"Created","status":"True"}],"replicaStatuses":{"Worker":{"active":0}}}}`, []v1alpha1.ResourceStatus{pending}},
		{"active without startup condition", `{"status":{"replicaStatuses":{"Worker":{"active":1}}}}`, []v1alpha1.ResourceStatus{undefined}},
		{"running with sticky created", `{"status":{"conditions":[{"type":"Created","status":"True"},{"type":"Running","status":"True"}],"replicaStatuses":{"Worker":{"active":1}}}}`, []v1alpha1.ResourceStatus{running}},
		{"completed with sticky created and count", `{"status":{"conditions":[{"type":"Created","status":"True"},{"type":"Succeeded","status":"True"}],"replicaStatuses":{"Worker":{"active":1}}}}`, []v1alpha1.ResourceStatus{completed}},
		{"failed with sticky created", `{"status":{"conditions":[{"type":"Created","status":"True"},{"type":"Failed","status":"True"}],"replicaStatuses":{"Worker":{"active":1}}}}`, []v1alpha1.ResourceStatus{failed}},
		{"suspended with sticky created", `{"status":{"conditions":[{"type":"Created","status":"True"},{"type":"Suspended","status":"True"}],"replicaStatuses":{"Worker":{"active":1}}}}`, []v1alpha1.ResourceStatus{suspended}},
		{"suspension requested", `{"spec":{"runPolicy":{"suspend":true}},"status":{"conditions":[{"type":"Created","status":"True"}],"replicaStatuses":{"Worker":{"active":1}}}}`, []v1alpha1.ResourceStatus{undefined}},
	}
	dynamoCases := []operatorStatusCase{
		{"absent", `{}`, []v1alpha1.ResourceStatus{undefined}},
		{"schema default is not an observation", `{"status":{"state":"initializing"}}`, []v1alpha1.ResourceStatus{undefined}},
		{"readiness wait", `{"status":{"state":"pending"}}`, []v1alpha1.ResourceStatus{pending}},
		{"rollout pending", `{"status":{"state":"pending","rollingUpdate":{"phase":"Pending"}}}`, []v1alpha1.ResourceStatus{pending, progressing}},
		{"rollout in progress", `{"status":{"state":"pending","rollingUpdate":{"phase":"InProgress"}}}`, []v1alpha1.ResourceStatus{pending, progressing}},
		{"explicit rollout without readiness", `{"status":{"rollingUpdate":{"phase":"InProgress"}}}`, []v1alpha1.ResourceStatus{progressing}},
		{"rollout completed", `{"status":{"state":"successful","rollingUpdate":{"phase":"Completed"}}}`, []v1alpha1.ResourceStatus{running}},
		{"failure overrides stale rollout evidence", `{"status":{"state":"failed","rollingUpdate":{"phase":"InProgress"}}}`, []v1alpha1.ResourceStatus{failed}},
		{"stale observation", `{"metadata":{"generation":2},"status":{"observedGeneration":1,"state":"pending","rollingUpdate":{"phase":"InProgress"}}}`, []v1alpha1.ResourceStatus{undefined}},
	}
	suites := []struct {
		name       string
		definition *v1alpha1.Karta
		cases      []operatorStatusCase
	}{
		{"pytorch", kartas.Pytorch(), trainingCases},
		{"mpijob", kartas.Mpijob(), trainingCases},
		{"dynamo v1alpha1", kartas.Dynamo(), dynamoCases},
		{"dynamo v1beta1", kartas.DynamoV1beta1(), dynamoCases},
		{"raycluster", kartas.Raycluster(), []operatorStatusCase{
			{"absent", `{}`, []v1alpha1.ResourceStatus{undefined}},
			{"head missing", `{"status":{"conditions":[{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"HeadPodNotFound"}]}}`, []v1alpha1.ResourceStatus{pending}},
			{"container startup", `{"status":{"conditions":[{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, []v1alpha1.ResourceStatus{pending, progressing}},
			{"containers not ready", `{"status":{"conditions":[{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainersNotReady"}]}}`, []v1alpha1.ResourceStatus{pending, progressing}},
			{"blocked startup", `{"status":{"conditions":[{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ImagePullBackOff"}]}}`, []v1alpha1.ResourceStatus{pending, progressing}},
			{"unknown head reason", `{"status":{"conditions":[{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"Unschedulable"}]}}`, []v1alpha1.ResourceStatus{pending}},
			{"legacy ready", `{"status":{"state":"ready"}}`, []v1alpha1.ResourceStatus{running}},
			{"sticky provisioned is not startup", `{"status":{"state":"ready","conditions":[{"type":"RayClusterProvisioned","status":"True"},{"type":"HeadPodReady","status":"False","reason":"ContainersNotReady"}]}}`, []v1alpha1.ResourceStatus{running}},
			{"failed startup", `{"status":{"state":"failed","conditions":[{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, []v1alpha1.ResourceStatus{failed}},
			{"suspension requested", `{"spec":{"suspend":true},"status":{"conditions":[{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, []v1alpha1.ResourceStatus{undefined}},
			{"suspension observed", `{"status":{"conditions":[{"type":"RayClusterSuspending","status":"True"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, []v1alpha1.ResourceStatus{undefined}},
			{"resume awaiting head", `{"metadata":{"generation":2},"spec":{"suspend":false},"status":{"observedGeneration":2,"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"False"},{"type":"RayClusterSuspended","status":"False"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"HeadPodNotFound"}]}}`, []v1alpha1.ResourceStatus{pending}},
			{"resume starting head", `{"metadata":{"generation":2},"spec":{"suspend":false},"status":{"observedGeneration":2,"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"False"},{"type":"RayClusterSuspended","status":"False"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, []v1alpha1.ResourceStatus{pending, progressing}},
			{"resume with stale observation", `{"metadata":{"generation":2},"spec":{"suspend":false},"status":{"observedGeneration":1,"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"False"},{"type":"RayClusterSuspended","status":"False"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, []v1alpha1.ResourceStatus{pending}},
			{"resume without observed generation", `{"metadata":{"generation":2},"spec":{"suspend":false},"status":{"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"False"},{"type":"RayClusterSuspended","status":"False"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, []v1alpha1.ResourceStatus{pending}},
			{"resume without desired generation", `{"spec":{"suspend":false},"status":{"observedGeneration":2,"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"False"},{"type":"RayClusterSuspended","status":"False"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, []v1alpha1.ResourceStatus{undefined}},
			{"resume still suspending", `{"metadata":{"generation":2},"spec":{"suspend":false},"status":{"observedGeneration":2,"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"True"},{"type":"RayClusterSuspended","status":"False"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, []v1alpha1.ResourceStatus{pending}},
			{"resume still suspended", `{"metadata":{"generation":2},"spec":{"suspend":false},"status":{"observedGeneration":2,"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"False"},{"type":"RayClusterSuspended","status":"True"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, []v1alpha1.ResourceStatus{pending}},
			{"resume with unknown suspension condition", `{"metadata":{"generation":2},"spec":{"suspend":false},"status":{"observedGeneration":2,"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"False"},{"type":"RayClusterSuspended","status":"Unknown"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, []v1alpha1.ResourceStatus{pending}},
			{"resume without suspension condition", `{"metadata":{"generation":2},"spec":{"suspend":false},"status":{"observedGeneration":2,"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"False"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, []v1alpha1.ResourceStatus{pending}},
			{"suspended without resume intent", `{"metadata":{"generation":2},"status":{"observedGeneration":2,"state":"suspended","conditions":[{"type":"RayClusterSuspending","status":"False"},{"type":"RayClusterSuspended","status":"False"},{"type":"RayClusterProvisioned","status":"False"},{"type":"HeadPodReady","status":"False","reason":"ContainerCreating"}]}}`, []v1alpha1.ResourceStatus{undefined}},
		}},
		{"rayjob", kartas.Rayjob(), []operatorStatusCase{
			{"absent", `{}`, []v1alpha1.ResourceStatus{undefined}},
			{"cluster initialization", `{"status":{"jobDeploymentStatus":"Initializing"}}`, []v1alpha1.ResourceStatus{pending, progressing}},
			{"interactive waiting", `{"status":{"jobDeploymentStatus":"Waiting"}}`, []v1alpha1.ResourceStatus{pending}},
			{"submission pending", `{"status":{"jobDeploymentStatus":"Running","jobStatus":"PENDING"}}`, []v1alpha1.ResourceStatus{pending}},
			{"deployment running awaits application status", `{"status":{"jobDeploymentStatus":"Running"}}`, []v1alpha1.ResourceStatus{pending}},
			{"deployment running with empty application status", `{"status":{"jobDeploymentStatus":"Running","jobStatus":""}}`, []v1alpha1.ResourceStatus{pending}},
			{"deployment running with null application status", `{"status":{"jobDeploymentStatus":"Running","jobStatus":null}}`, []v1alpha1.ResourceStatus{pending}},
			{"deployment running with unknown application status", `{"status":{"jobDeploymentStatus":"Running","jobStatus":"UNKNOWN"}}`, []v1alpha1.ResourceStatus{undefined}},
			{"deployment running with stopped application", `{"status":{"jobDeploymentStatus":"Running","jobStatus":"STOPPED"}}`, []v1alpha1.ResourceStatus{undefined}},
			{"deployment running with suspended intent", `{"spec":{"suspend":true},"status":{"jobDeploymentStatus":"Running"}}`, []v1alpha1.ResourceStatus{undefined}},
			{"application running", `{"status":{"jobDeploymentStatus":"Running","jobStatus":"RUNNING"}}`, []v1alpha1.ResourceStatus{running}},
			{"application completed with old deployment status", `{"status":{"jobDeploymentStatus":"Initializing","jobStatus":"SUCCEEDED"}}`, []v1alpha1.ResourceStatus{completed}},
			{"application failed with old deployment status", `{"status":{"jobDeploymentStatus":"Initializing","jobStatus":"FAILED"}}`, []v1alpha1.ResourceStatus{failed}},
			{"deployment failed with old pending", `{"status":{"jobDeploymentStatus":"Failed","jobStatus":"PENDING"}}`, []v1alpha1.ResourceStatus{undefined}},
			{"deployment complete with old pending", `{"status":{"jobDeploymentStatus":"Complete","jobStatus":"PENDING"}}`, []v1alpha1.ResourceStatus{undefined}},
			{"retry cleanup with old pending", `{"status":{"jobDeploymentStatus":"Retrying","jobStatus":"PENDING"}}`, []v1alpha1.ResourceStatus{undefined}},
			{"suspended with old pending", `{"status":{"jobDeploymentStatus":"Suspended","jobStatus":"PENDING"}}`, []v1alpha1.ResourceStatus{suspended}},
			{"suspending with old pending", `{"status":{"jobDeploymentStatus":"Suspending","jobStatus":"PENDING"}}`, []v1alpha1.ResourceStatus{v1alpha1.SuspendingStatus}},
			{"suspension requested", `{"spec":{"suspend":true},"status":{"jobDeploymentStatus":"Initializing","jobStatus":"PENDING"}}`, []v1alpha1.ResourceStatus{undefined}},
		}},
		{"rayservice", kartas.RayService(), []operatorStatusCase{
			{"absent", `{}`, []v1alpha1.ResourceStatus{undefined}},
			{"initial readiness wait", `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"Initializing"}]}}`, []v1alpha1.ResourceStatus{pending}},
			{"initial target allocated", `{"status":{"pendingServiceStatus":{"rayClusterName":"target"},"conditions":[{"type":"Ready","status":"False","reason":"Initializing"}]}}`, []v1alpha1.ResourceStatus{pending, progressing}},
			{"ready", `{"status":{"conditions":[{"type":"Ready","status":"True"}]}}`, []v1alpha1.ResourceStatus{running}},
			{"serving during upgrade", `{"status":{"conditions":[{"type":"Ready","status":"True"},{"type":"UpgradeInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{running, progressing}},
			{"serving during rollback", `{"status":{"conditions":[{"type":"Ready","status":"True"},{"type":"RollbackInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{running, progressing}},
			{"pending name alone is not progress", `{"status":{"pendingServiceStatus":{"rayClusterName":"target"},"conditions":[{"type":"Ready","status":"True"}]}}`, []v1alpha1.ResourceStatus{running}},
			{"timeout with old upgrade condition", `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"InitializingTimeout"},{"type":"UpgradeInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{failed}},
			{"validation failure with old upgrade condition", `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"ValidationFailed"},{"type":"UpgradeInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{failed}},
			{"suspension requested", `{"spec":{"suspend":true},"status":{"pendingServiceStatus":{"rayClusterName":"target"},"conditions":[{"type":"Ready","status":"False","reason":"Initializing"}]}}`, []v1alpha1.ResourceStatus{undefined}},
			{"suspension observed", `{"status":{"conditions":[{"type":"Suspended","status":"True"},{"type":"Ready","status":"False","reason":"Initializing"},{"type":"UpgradeInProgress","status":"True"}]}}`, []v1alpha1.ResourceStatus{undefined}},
			{"stale observation", `{"metadata":{"generation":2},"status":{"observedGeneration":1,"pendingServiceStatus":{"rayClusterName":"target"},"conditions":[{"type":"Ready","status":"False","reason":"Initializing"}]}}`, []v1alpha1.ResourceStatus{undefined}},
		}},
		{"knative service", kartas.KnativeServing(), []operatorStatusCase{
			{"absent", `{}`, []v1alpha1.ResourceStatus{undefined}},
			{"unresolved readiness", `{"status":{"conditions":[{"type":"Ready","status":"Unknown","reason":"Uninitialized"}]}}`, []v1alpha1.ResourceStatus{pending}},
			{"route transition after equal revisions", `{"metadata":{"generation":1},"status":{"observedGeneration":1,"latestCreatedRevisionName":"service-00001","latestReadyRevisionName":"service-00001","conditions":[{"type":"Ready","status":"Unknown","reason":"IngressNotConfigured"},{"type":"RoutesReady","status":"Unknown","reason":"OutOfDate"}]}}`, []v1alpha1.ResourceStatus{pending, progressing}},
			{"deleting route transition retains only readiness wait", `{"metadata":{"generation":1,"deletionTimestamp":"2026-10-19T00:00:00Z"},"status":{"observedGeneration":1,"latestCreatedRevisionName":"service-00001","latestReadyRevisionName":"service-00001","conditions":[{"type":"Ready","status":"Unknown","reason":"IngressNotConfigured"},{"type":"RoutesReady","status":"Unknown","reason":"OutOfDate"}]}}`, []v1alpha1.ResourceStatus{pending}},
			{"configuration transition", `{"status":{"conditions":[{"type":"Ready","status":"Unknown","reason":"OutOfDate"}]}}`, []v1alpha1.ResourceStatus{pending, progressing}},
			{"traffic transition", `{"status":{"conditions":[{"type":"Ready","status":"Unknown","reason":"TrafficNotMigrated"}]}}`, []v1alpha1.ResourceStatus{pending, progressing}},
			{"new revision", `{"status":{"latestCreatedRevisionName":"new","latestReadyRevisionName":"old","conditions":[{"type":"Ready","status":"Unknown","reason":"RevisionMissing"}]}}`, []v1alpha1.ResourceStatus{pending, progressing}},
			{"ingress waiting after revision ready", `{"status":{"latestCreatedRevisionName":"new","latestReadyRevisionName":"new","conditions":[{"type":"Ready","status":"Unknown","reason":"IngressNotConfigured"}]}}`, []v1alpha1.ResourceStatus{pending}},
			{"ready", `{"status":{"conditions":[{"type":"Ready","status":"True"}]}}`, []v1alpha1.ResourceStatus{running}},
			{"failed revision", `{"status":{"latestCreatedRevisionName":"new","latestReadyRevisionName":"old","conditions":[{"type":"Ready","status":"False","reason":"RevisionFailed"}]}}`, []v1alpha1.ResourceStatus{failed}},
			{"stale observation", `{"metadata":{"generation":2},"status":{"observedGeneration":1,"conditions":[{"type":"Ready","status":"Unknown","reason":"OutOfDate"}]}}`, []v1alpha1.ResourceStatus{undefined}},
		}},
		{"milvus", kartas.Milvus(), []operatorStatusCase{
			{"absent", `{}`, []v1alpha1.ResourceStatus{undefined}},
			{"initial readiness wait", `{"status":{"status":"Pending"}}`, []v1alpha1.ResourceStatus{pending}},
			{"components starting", `{"status":{"status":"Pending","conditions":[{"type":"MilvusUpdated","status":"False","reason":"MilvusComponentsUpdating"}]}}`, []v1alpha1.ResourceStatus{pending, progressing}},
			{"healthy during upgrade", `{"status":{"status":"Healthy","conditions":[{"type":"MilvusUpdated","status":"False","reason":"MilvusUpgradingImage"}]}}`, []v1alpha1.ResourceStatus{running, progressing}},
			{"healthy during downgrade", `{"status":{"status":"Healthy","conditions":[{"type":"MilvusUpdated","status":"False","reason":"MilvusDowngradingImage"}]}}`, []v1alpha1.ResourceStatus{running, progressing}},
			{"update complete", `{"status":{"status":"Healthy","conditions":[{"type":"MilvusUpdated","status":"True","reason":"MilvusComponentsUpdated"}]}}`, []v1alpha1.ResourceStatus{running}},
			{"unhealthy during update", `{"status":{"status":"Unhealthy","conditions":[{"type":"MilvusUpdated","status":"False","reason":"MilvusComponentsUpdating"}]}}`, []v1alpha1.ResourceStatus{degraded, progressing}},
			{"stopped with old update condition", `{"status":{"status":"Stopped","conditions":[{"type":"MilvusUpdated","status":"False","reason":"MilvusComponentsUpdating"}]}}`, []v1alpha1.ResourceStatus{undefined}},
			{"stopping with old pending", `{"status":{"status":"Pending","conditions":[{"type":"MilvusReady","status":"False","reason":"MilvusStopping"},{"type":"MilvusUpdated","status":"False","reason":"MilvusComponentsUpdating"}]}}`, []v1alpha1.ResourceStatus{undefined}},
			{"stale observation", `{"metadata":{"generation":2},"status":{"observedGeneration":1,"status":"Pending","conditions":[{"type":"MilvusUpdated","status":"False","reason":"MilvusComponentsUpdating"}]}}`, []v1alpha1.ResourceStatus{undefined}},
		}},
	}
	for _, suite := range suites {
		t.Run(suite.name, func(t *testing.T) {
			for _, tc := range suite.cases {
				t.Run(tc.name, func(t *testing.T) {
					object := &unstructured.Unstructured{}
					if err := json.Unmarshal([]byte(tc.object), &object.Object); err != nil {
						t.Fatal(err)
					}
					component, err := resource.NewComponentFactoryFromObject(suite.definition, object).GetRootComponent()
					if err != nil {
						t.Fatal(err)
					}
					status, err := component.GetStatus(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					got := slices.Clone(status.MatchedStatuses)
					want := slices.Clone(tc.want)
					slices.Sort(got)
					slices.Sort(want)
					if !slices.Equal(got, want) {
						t.Errorf("matched statuses = %v, want %v", got, want)
					}
				})
			}
		})
	}
}
