// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	metadatafake "k8s.io/client-go/metadata/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/discovery"
	inventory "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelevaluator"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog"
)

var jobKind = schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
var deploymentKind = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
var replicaSetKind = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"}

func simpleRuntime(t *testing.T, resolver labelresolver.Resolver) (*Runtime, store.PublicationStore) {
	t.Helper()
	scheme := kruntime.NewScheme()
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{podGVK.GroupVersion(), jobKind.GroupVersion(), deploymentKind.GroupVersion(), kartaGVK.GroupVersion()})
	listKinds := map[schema.GroupVersionResource]string{}
	for gvk, resource := range map[schema.GroupVersionKind]string{podGVK: "pods", jobKind: "jobs", deploymentKind: "deployments", replicaSetKind: "replicasets", namespaceGVK: "namespaces", kartaGVK: "kartas"} {
		scope := meta.RESTScopeNamespace
		if gvk == kartaGVK || gvk == namespaceGVK {
			scope = meta.RESTScopeRoot
		}
		gvr := gvk.GroupVersion().WithResource(resource)
		mapper.AddSpecific(gvk, gvr, gvr, scope)
		listKinds[gvr] = gvk.Kind + "List"
	}
	reader, err := NewReader(Clients{Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds), Metadata: metadatafake.NewSimpleMetadataClient(scheme), Mapper: mapper}, ReaderOptions{})
	require.NoError(t, err)
	ownership, err := discovery.New(reader, discovery.Config{})
	require.NoError(t, err)
	definitions, err := inventory.New(nil, inventory.Config{})
	require.NoError(t, err)
	if resolver == nil {
		resolver, err = labelresolver.New(labelresolver.Config{}, labelresolver.Dependencies{Compiler: labelevaluator.NewCompiler()})
		require.NoError(t, err)
	}
	values := store.New()
	r, err := New(Config{}, Dependencies{Discovery: ownership, Inventory: definitions, Labels: resolver, Publications: values}, reader)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	r.state.ctx = ctx
	t.Cleanup(func() {
		cancel()
		r.state.podQueue.ShutDown()
		r.state.workloadQueue.ShutDown()
		r.state.watchSyncQueue.ShutDown()
		r.state.stateMu.Lock()
		for _, w := range r.state.watches {
			w.cancel()
		}
		for _, w := range r.state.replacements {
			w.cancel()
		}
		r.state.stateMu.Unlock()
		r.state.wg.Wait()
	})
	return r, values
}
func simpleObject(gvk schema.GroupVersionKind, name string) *unstructured.Unstructured {
	object := &unstructured.Unstructured{Object: map[string]any{}}
	object.SetGroupVersionKind(gvk)
	object.SetNamespace("ns")
	object.SetName(name)
	object.SetUID(types.UID(name))
	object.SetResourceVersion("1")
	object.SetCreationTimestamp(metav1.NewTime(time.Unix(100, 0)))
	return object
}
func simpleOwner(object, owner *unstructured.Unstructured) {
	object.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: owner.GetAPIVersion(), Kind: owner.GetKind(), Name: owner.GetName(), UID: owner.GetUID(), Controller: ptr.To(true)}})
}
func seedSource(t *testing.T, r *Runtime, gvk schema.GroupVersionKind, full bool, objects ...*unstructured.Unstructured) *resourceWatch {
	t.Helper()
	mapping, err := r.state.reader.restMapping(context.Background(), gvk)
	require.NoError(t, err)
	key := watchKey{Resource: mapping.Resource}
	w := &resourceWatch{key: key, gvk: gvk, full: full, ready: true, objects: map[objectKey]struct{}{}, cancel: func() {}}
	r.state.watches[key] = w
	r.state.reader.sources[key] = cacheSource{gvk: gvk, hasFullObject: full, synced: true}
	r.state.deps.Publications.SetSourceHealth(key, store.SourceHealth{GVK: gvk, Synced: true})
	for _, object := range objects {
		require.NoError(t, r.state.applyWatchEvent(w, object, false))
	}
	return w
}
func selectCatalog(t *testing.T, r *Runtime, names ...string) {
	t.Helper()
	var err error
	r.state.deps.Inventory, err = inventory.New(nil, inventory.Config{Catalog: inventory.SourceConfig{Enabled: true, Include: names}})
	require.NoError(t, err)
	r.state.stateMu.Lock()
	err = r.state.refreshDefinitionsLocked()
	r.state.stateMu.Unlock()
	require.NoError(t, err)
}
func emptyQueue[T comparable](queue workqueue.TypedRateLimitingInterface[T]) {
	for queue.Len() > 0 {
		key, _ := queue.Get()
		queue.Forget(key)
		queue.Done(key)
	}
}
func settle(t *testing.T, r *Runtime) {
	t.Helper()
	for n := 0; n < 2000; n++ {
		if r.state.podQueue.Len() > 0 {
			key, _ := r.state.podQueue.Get()
			err := r.handlePodInput(context.Background(), key)
			r.state.podQueue.Done(key)
			require.NoError(t, err)
			continue
		}
		if r.state.workloadQueue.Len() > 0 {
			key, _ := r.state.workloadQueue.Get()
			err := r.handleWorkloadInput(context.Background(), key)
			r.state.workloadQueue.Done(key)
			require.NoError(t, err)
			continue
		}
		return
	}
	t.Fatal("domain queues did not settle")
}
func seedPair(t *testing.T, r *Runtime) (*unstructured.Unstructured, *unstructured.Unstructured, *resourceWatch, *resourceWatch) {
	t.Helper()
	root, pod := simpleObject(deploymentKind, "root"), simpleObject(podGVK, "pod")
	simpleOwner(pod, root)
	roots := seedSource(t, r, deploymentKind, true, root)
	pods := seedSource(t, r, podGVK, true, pod)
	settle(t, r)
	return root, pod, roots, pods
}

func TestIndependentPodAndWorkloadPublication(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	root, pod, _, pods := seedPair(t, r)
	oldRoot := values.Workload(root.GetUID())
	require.NotNil(t, oldRoot)
	pod = pod.DeepCopy()
	pod.SetResourceVersion("2")
	pod.SetLabels(map[string]string{"changed": "yes"})
	require.NoError(t, r.state.applyWatchEvent(pods, pod, false))
	require.NoError(t, r.reconcilePod(context.Background(), objectJobFor(refOf(pod))))
	require.Same(t, oldRoot, values.Workload(root.GetUID()))
	require.Equal(t, "2", values.Pod(pod.GetUID()).Input.ResourceVersion)
	require.NoError(t, r.reconcileWorkload(context.Background(), root.GetUID()))
	require.Equal(t, "2", values.Pod(pod.GetUID()).Input.ResourceVersion)
}
func TestTombstoneAndSameNameReplacement(t *testing.T) {
	for _, kind := range []schema.GroupVersionKind{podGVK, deploymentKind} {
		t.Run(kind.Kind, func(t *testing.T) {
			r, values := simpleRuntime(t, nil)
			root, pod, roots, pods := seedPair(t, r)
			old, source := pod, pods
			if kind == deploymentKind {
				old, source = root, roots
			}
			replacement := old.DeepCopy()
			replacement.SetUID(old.GetUID() + "-new")
			replacement.SetResourceVersion("2")
			require.NoError(t, r.state.applyWatchEvent(source, replacement, false))
			require.NoError(t, r.state.applyWatchEvent(source, cache.DeletedFinalStateUnknown{Obj: old}, true))
			current, ok := r.state.reader.Cached(refOf(replacement))
			require.True(t, ok)
			require.Equal(t, replacement.GetUID(), current.GetUID())
			if kind == podGVK {
				settle(t, r)
				require.Nil(t, values.Pod(old.GetUID()))
				require.NotNil(t, values.Pod(replacement.GetUID()))
			} else {
				require.NoError(t, r.reconcileWorkload(context.Background(), old.GetUID()))
				require.Nil(t, values.Workload(old.GetUID()))
				require.Error(t, r.reconcilePod(context.Background(), objectJobFor(refOf(pod))))
				require.Equal(t, store.ReasonUnresolvedOwner, values.Pod(pod.GetUID()).Error)
			}
		})
	}
}
func TestIntermediateOwnershipRescanAndQuietStatus(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	a, b := simpleObject(deploymentKind, "a"), simpleObject(deploymentKind, "b")
	middle, pod := simpleObject(replicaSetKind, "middle"), simpleObject(podGVK, "pod")
	simpleOwner(middle, a)
	simpleOwner(pod, middle)
	seedSource(t, r, deploymentKind, true, a, b)
	source := seedSource(t, r, replicaSetKind, false, middle)
	seedSource(t, r, podGVK, true, pod)
	settle(t, r)
	require.Equal(t, a.GetUID(), values.Pod(pod.GetUID()).Record.Workload.UID)
	changed := middle.DeepCopy()
	changed.SetResourceVersion("2")
	changed.Object["status"] = map[string]any{"readyReplicas": int64(5)}
	require.NoError(t, r.state.applyWatchEvent(source, changed, false))
	require.Zero(t, r.state.podQueue.Len())
	require.Zero(t, r.state.workloadQueue.Len())
	changed.SetResourceVersion("3")
	simpleOwner(changed, b)
	for i := 0; i < 20; i++ {
		require.NoError(t, r.state.applyWatchEvent(source, changed, false))
	}
	require.Equal(t, 1, r.state.podQueue.Len())
	settle(t, r)
	require.Equal(t, b.GetUID(), values.Pod(pod.GetUID()).Record.Workload.UID)
	require.Nil(t, values.Workload(a.GetUID()))
	require.NotNil(t, values.Workload(b.GetUID()))
}
func TestZeroPodCandidateFindsGenericParentAndMoves(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	selectCatalog(t, r, "batch-job-v1")
	a, b := simpleObject(deploymentKind, "a"), simpleObject(deploymentKind, "b")
	middle, child := simpleObject(replicaSetKind, "middle"), simpleObject(jobKind, "child")
	simpleOwner(middle, a)
	simpleOwner(child, middle)
	seedSource(t, r, deploymentKind, true, a, b)
	source := seedSource(t, r, replicaSetKind, false, middle)
	seedSource(t, r, jobKind, true, child)
	seedSource(t, r, podGVK, true)
	settle(t, r)
	require.Len(t, values.Capture().Workloads, 1)
	require.NotNil(t, values.Workload(a.GetUID()))
	require.Empty(t, values.Capture().Pods)
	changed := middle.DeepCopy()
	changed.SetResourceVersion("2")
	simpleOwner(changed, b)
	require.NoError(t, r.state.applyWatchEvent(source, changed, false))
	settle(t, r)
	require.Nil(t, values.Workload(a.GetUID()))
	require.NotNil(t, values.Workload(b.GetUID()))
}
func TestCandidateFailureDoesNotFabricateRoot(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	selectCatalog(t, r, "batch-job-v1")
	child, parent := simpleObject(jobKind, "child"), simpleObject(deploymentKind, "parent")
	simpleOwner(child, parent)
	source := seedSource(t, r, deploymentKind, true)
	seedSource(t, r, jobKind, true, child)
	require.Error(t, r.resolveSelectedObjectRoot(context.Background(), objectJobFor(refOf(child))))
	require.Empty(t, values.Capture().Workloads)
	require.Len(t, values.Capture().Failures, 1)
	require.NoError(t, r.state.applyWatchEvent(source, parent, false))
	require.NoError(t, r.resolveSelectedObjectRoot(context.Background(), objectJobFor(refOf(child))))
	require.Empty(t, values.Capture().Failures)
	require.NoError(t, r.reconcileWorkload(context.Background(), parent.GetUID()))
	require.NotNil(t, values.Workload(parent.GetUID()))
}
func TestCandidateAdmissionFailuresPreserveCause(t *testing.T) {
	cause := errors.New("candidate source failed")
	for _, tc := range []struct {
		name         string
		readErr      error
		discoveryErr error
		want         error
	}{
		{name: "cache read", readErr: cause, want: cause},
		{name: "discovery", discoveryErr: cause, want: cause},
		{name: "empty discovery", want: discovery.ErrUnresolvedOwner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, values := simpleRuntime(t, nil)
			selectCatalog(t, r, "batch-job-v1")
			child := simpleObject(jobKind, "child")
			source := seedSource(t, r, jobKind, true, child)
			discoverer := discovery.NewMockDiscovery(t)
			r.state.deps.Discovery = discoverer
			ctx := context.Background()
			if tc.readErr != nil {
				r.state.stateMu.Lock()
				r.state.setWatchHealthLocked(source, tc.readErr)
				r.state.stateMu.Unlock()
			} else {
				discoverer.EXPECT().Get(ctx, refOf(child)).Return(discovery.Result{}, tc.discoveryErr).Once()
			}
			err := r.resolveSelectedObjectRoot(ctx, objectJobFor(refOf(child)))
			require.Same(t, tc.want, err)
			require.Empty(t, values.Capture().Workloads)
			require.Equal(t, []store.DiscoveryFailure{{Input: storedRef(objectJobFor(refOf(child)).ref()), Error: failureReason(tc.want)}}, values.Capture().Failures)
		})
	}
}

func TestSourceFailureRetainsLastGoodAndRecoveryDoesNotClearEvaluationError(t *testing.T) {
	resolver := &controlledLabels{}
	r, values := simpleRuntime(t, resolver)
	root, pod, roots, _ := seedPair(t, r)
	before := values.Pod(pod.GetUID())
	denied := apierrors.NewForbidden(roots.key.Resource.GroupResource(), "", errors.New("denied"))
	r.state.stateMu.Lock()
	r.state.setWatchHealthLocked(roots, denied)
	r.state.stateMu.Unlock()
	valid, reason := values.Capture().Valid(before.Sources, before.Error)
	require.False(t, valid)
	require.Equal(t, store.ReasonPermissionDenied, reason)
	require.Error(t, r.reconcilePod(context.Background(), objectJobFor(refOf(pod))))
	require.Equal(t, before.Record, values.Pod(pod.GetUID()).Record)
	r.state.stateMu.Lock()
	r.state.setWatchHealthLocked(roots, nil)
	r.state.stateMu.Unlock()
	resolver.mu.Lock()
	resolver.fail = true
	resolver.mu.Unlock()
	require.NoError(t, r.reconcilePod(context.Background(), objectJobFor(refOf(pod))))
	require.Equal(t, store.ReasonEvaluationFailed, values.Pod(pod.GetUID()).Error)
	require.NoError(t, r.reconcileWorkload(context.Background(), root.GetUID()))
	r.state.stateMu.Lock()
	r.state.setWatchHealthLocked(roots, denied)
	r.state.setWatchHealthLocked(roots, nil)
	r.state.stateMu.Unlock()
	require.Equal(t, store.ReasonEvaluationFailed, values.Pod(pod.GetUID()).Error)
	resolver.mu.Lock()
	resolver.fail = false
	resolver.mu.Unlock()
	require.NoError(t, r.reconcilePod(context.Background(), objectJobFor(refOf(pod))))
	require.Empty(t, values.Pod(pod.GetUID()).Error)
}
func TestCompleteRelistDeletionAndMalformedList(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	_, pod, _, pods := seedPair(t, r)
	replacement := &reflectorStore{controller: r.state, watch: pods}
	require.Error(t, replacement.Replace([]any{&unstructured.Unstructured{}}, "2"))
	require.NotNil(t, values.Pod(pod.GetUID()))
	require.NotNil(t, r.state.reader.objects[keyOf(refOf(pod))].object)
	require.NotEmpty(t, values.Capture().Sources[pods.key].Error)
	require.NoError(t, replacement.Replace(nil, "3"))
	settle(t, r)
	require.Nil(t, values.Pod(pod.GetUID()))
	require.Empty(t, values.Capture().Workloads)
}
func TestRetiredCallbacksCannotRepopulateCache(t *testing.T) {
	r, _ := simpleRuntime(t, nil)
	pod := simpleObject(podGVK, "pod")
	old := seedSource(t, r, podGVK, true, pod)
	r.state.stateMu.Lock()
	r.state.retireWatchLocked(old)
	r.state.stateMu.Unlock()
	current := seedSource(t, r, podGVK, true)
	require.NoError(t, r.state.applyWatchEvent(old, pod, false))
	require.NoError(t, (&reflectorStore{controller: r.state, watch: old}).Replace([]any{pod}, "2"))
	require.Empty(t, current.objects)
	_, ok := r.state.reader.Cached(refOf(pod))
	require.False(t, ok)
}
func TestNamespaceRemovalCleansBothDomains(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	root, pod, _, _ := seedPair(t, r)
	r.state.config.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"include": "yes"}}
	r.state.scopes = map[string]bool{}
	require.NoError(t, r.reconcilePod(context.Background(), objectJobFor(refOf(pod))))
	require.NoError(t, r.reconcileWorkload(context.Background(), root.GetUID()))
	require.Empty(t, values.Capture().Pods)
	require.Empty(t, values.Capture().Workloads)
}
func TestStandalonePodAdoptionKeepsOnlyPodRole(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	definition, err := catalog.Get(podGVK)
	require.NoError(t, err)
	selectCatalog(t, r, definition.Name)
	pod, root := simpleObject(podGVK, "pod"), simpleObject(deploymentKind, "root")
	source := seedSource(t, r, podGVK, true, pod)
	seedSource(t, r, deploymentKind, true, root)
	settle(t, r)
	require.NotNil(t, values.Workload(pod.GetUID()))
	require.NotNil(t, values.Pod(pod.GetUID()))
	changed := pod.DeepCopy()
	changed.SetResourceVersion("2")
	simpleOwner(changed, root)
	require.NoError(t, r.state.applyWatchEvent(source, changed, false))
	settle(t, r)
	require.Nil(t, values.Workload(pod.GetUID()))
	require.Equal(t, root.GetUID(), values.Pod(pod.GetUID()).Record.Workload.UID)
}
func TestBarePodIsExcludedByDefault(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	seedSource(t, r, podGVK, true, simpleObject(podGVK, "pod"))
	settle(t, r)
	require.Empty(t, values.Capture().Pods)
	require.Empty(t, values.Capture().Workloads)
}

type controlledLabels struct {
	mu              sync.Mutex
	fail            bool
	err             error
	blockWorkload   bool
	entered, resume chan struct{}
	once            sync.Once
}

func (l *controlledLabels) Resolve(ctx context.Context, in labelresolver.Input) ([]labelresolver.ResolvedMetricLabels, error) {
	if (in.Pod != nil || in.PodObject != nil || l.blockWorkload) && l.entered != nil {
		l.once.Do(func() {
			close(l.entered)
			select {
			case <-l.resume:
			case <-ctx.Done():
			}
		})
	}
	l.mu.Lock()
	fail := l.fail
	err := l.err
	l.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if fail && (in.Pod != nil || in.PodObject != nil) {
		return []labelresolver.ResolvedMetricLabels{{Metric: in.Metrics[0], Errors: map[string]error{"team": errors.New("invalid label")}}}, nil
	}
	return []labelresolver.ResolvedMetricLabels{{Metric: in.Metrics[0], Labels: map[string]string{"revision": in.Workload.GetResourceVersion()}}}, nil
}
func TestRootChangeDuringPodEvaluationRequeuesRegisteredMembership(t *testing.T) {
	labels := &controlledLabels{entered: make(chan struct{}), resume: make(chan struct{})}
	r, values := simpleRuntime(t, labels)
	root, pod := simpleObject(deploymentKind, "root"), simpleObject(podGVK, "pod")
	simpleOwner(pod, root)
	roots := seedSource(t, r, deploymentKind, true, root)
	seedSource(t, r, podGVK, true, pod)
	emptyQueue(r.state.podQueue)
	emptyQueue(r.state.workloadQueue)
	key := objectJobFor(refOf(pod))
	r.state.podQueue.Add(key)
	key, _ = r.state.podQueue.Get()
	done := make(chan error, 1)
	go func() { done <- r.reconcilePod(context.Background(), key); r.state.podQueue.Done(key) }()
	select {
	case <-labels.entered:
	case <-time.After(time.Second):
		t.Fatal("Pod evaluation did not start")
	}
	changed := root.DeepCopy()
	changed.SetResourceVersion("2")
	changed.Object["status"] = map[string]any{"availableReplicas": int64(1)}
	require.NoError(t, r.state.applyWatchEvent(roots, changed, false))
	close(labels.resume)
	require.ErrorIs(t, <-done, discovery.ErrStale)
	require.Eventually(t, func() bool { return r.state.podQueue.Len() > 0 }, time.Second, time.Millisecond)
	settle(t, r)
	require.Equal(t, "2", values.Pod(pod.GetUID()).Record.Labels["revision"])
}
func TestPodDeletionDuringEvaluationCannotPublish(t *testing.T) {
	labels := &controlledLabels{entered: make(chan struct{}), resume: make(chan struct{})}
	r, values := simpleRuntime(t, labels)
	root, pod := simpleObject(deploymentKind, "root"), simpleObject(podGVK, "pod")
	simpleOwner(pod, root)
	seedSource(t, r, deploymentKind, true, root)
	pods := seedSource(t, r, podGVK, true, pod)
	done := make(chan error, 1)
	go func() { done <- r.reconcilePod(context.Background(), objectJobFor(refOf(pod))) }()
	<-labels.entered
	require.NoError(t, r.state.applyWatchEvent(pods, pod, true))
	close(labels.resume)
	require.ErrorIs(t, <-done, discovery.ErrStale)
	settle(t, r)
	require.Nil(t, values.Pod(pod.GetUID()))
	require.Empty(t, values.Capture().Workloads)
}
func TestConcurrentLabelResolutionAndFullPodInputs(t *testing.T) {
	labels, err := labelresolver.New(labelresolver.Config{Rules: []labelresolver.LabelRule{
		{Name: "node", Metrics: []string{"karta_pod_workload_info"}, Sources: []labelresolver.Source{labelresolver.SourcePod}, Expression: "object.spec.nodeName"},
		{Name: "pod_phase", Metrics: []string{"karta_pod_workload_info"}, Sources: []labelresolver.Source{labelresolver.SourcePod}, Expression: "object.status.phase"},
		{Name: "extension", Metrics: []string{"karta_pod_workload_info"}, Sources: []labelresolver.Source{labelresolver.SourcePod}, Expression: `has(object.spec.containers) ? "present" : object.spec.futureField`},
		{Name: "revision", Metrics: []string{"karta_pod_workload_info", "karta_workload_status"}, Sources: []labelresolver.Source{labelresolver.SourceWorkload}, Expression: "object.metadata.resourceVersion"},
	}}, labelresolver.Dependencies{Compiler: labelevaluator.NewCompiler()})
	require.NoError(t, err)
	r, values := simpleRuntime(t, labels)
	root := simpleObject(deploymentKind, "root")
	seedSource(t, r, deploymentKind, true, root)
	pods := make([]*unstructured.Unstructured, 40)
	for i := range pods {
		pod := simpleObject(podGVK, fmt.Sprintf("pod-%d", i))
		simpleOwner(pod, root)
		pod.Object["spec"] = map[string]any{"nodeName": "worker", "futureField": "raw-value"}
		pod.Object["status"] = map[string]any{"phase": "Running"}
		pods[i] = pod
	}
	seedSource(t, r, podGVK, true, pods...)
	var wg sync.WaitGroup
	for _, pod := range pods {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, r.reconcilePod(context.Background(), objectJobFor(refOf(pod))))
		}()
	}
	wg.Wait()
	require.NoError(t, r.reconcileWorkload(context.Background(), root.GetUID()))
	for _, pod := range values.Capture().Pods {
		require.Equal(t, "worker", pod.Record.Labels["node"])
		require.Equal(t, "Running", pod.Record.Labels["pod_phase"])
		require.Equal(t, "raw-value", pod.Record.Labels["extension"])
	}
	require.Len(t, values.Capture().Pods, len(pods))
}
func TestFailedListRetainsCacheAndLastGoodRows(t *testing.T) {
	r, values := simpleRuntime(t, nil)
	_, pod, _, pods := seedPair(t, r)
	client := r.state.reader.clients.Dynamic.(*dynamicfake.FakeDynamicClient)
	client.PrependReactor("list", "pods", func(clienttesting.Action) (bool, kruntime.Object, error) {
		return true, nil, errors.New("failed second page")
	})
	_, err := r.state.listResource(context.Background(), pods, metav1.ListOptions{Continue: "second"})
	require.Error(t, err)
	_, ok := r.state.reader.Cached(refOf(pod))
	require.False(t, ok)
	require.NotNil(t, r.state.reader.objects[keyOf(refOf(pod))].object)
	require.NotNil(t, values.Pod(pod.GetUID()))
}
