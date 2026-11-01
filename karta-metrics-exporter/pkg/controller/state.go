// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"errors"
	"slices"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/discovery"
	inventory "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

// controllerState serializes source events, ownership changes and publication.
// Methods return values or owned snapshots. Mutable membership maps stay here.
// Accepted objects are immutable. Reader shares this lock.
// Discovery and evaluation run unlocked.
type controllerState struct {
	*objectCache
	config Config
	deps   Dependencies
	reader *Reader

	podQueue       workqueue.TypedRateLimitingInterface[objectJobKey]
	workloadQueue  workqueue.TypedRateLimitingInterface[workloadJobKey]
	watchSyncQueue workqueue.TypedRateLimitingInterface[string]

	watches       map[watchKey]*resourceWatch
	replacements  map[watchKey]*resourceWatch
	watchFailures map[discovery.WatchRequest]error
	scopes        map[string]bool

	nextRoot     uint64
	roots        map[types.UID]*rootState
	podRoots     map[types.UID]types.UID
	ancestorPods map[types.UID]bool
	// Objects with a selected Karta definition can discover themselves or an ancestor as root.
	// An empty UID keeps the object tracked until discovery succeeds.
	selectedObjectRoots map[objectJobKey]types.UID

	selectedDefinitions    map[schema.GroupKind]selectedDefinition
	definitionInventoryErr error
	definitionVersion      uint64
	namespaceSelector      labels.Selector
	objectSelector         labels.Selector
	podSelector            labels.Selector

	ctx     context.Context
	started bool
	wg      sync.WaitGroup
}

// Reader and controllerState use this lock for cache reads and state changes.
type objectCache struct {
	mu      sync.RWMutex
	objects map[objectKey]cachedObject
	sources map[watchKey]cacheSource
}

type rootState struct {
	generation      uint64
	ref             discovery.ObjectRef
	resolution      discovery.OwnerResolution
	pods            map[types.UID]objectJobKey
	selectedObjects map[objectJobKey]selectedObjectState
}

type selectedObjectState struct {
	sources []store.SourceKey
	err     error
}

type rootSnapshot struct {
	ref        discovery.ObjectRef
	resolution discovery.OwnerResolution
	generation uint64
	sources    []store.SourceKey
	err        error
}

func newControllerState(objects *objectCache) *controllerState {
	return &controllerState{
		objectCache:         objects,
		watches:             map[watchKey]*resourceWatch{},
		replacements:        map[watchKey]*resourceWatch{},
		watchFailures:       map[discovery.WatchRequest]error{},
		roots:               map[types.UID]*rootState{},
		podRoots:            map[types.UID]types.UID{},
		ancestorPods:        map[types.UID]bool{},
		selectedObjectRoots: map[objectJobKey]types.UID{},
		selectedDefinitions: map[schema.GroupKind]selectedDefinition{},
		scopes:              map[string]bool{},
	}
}

func (r *controllerState) lookupDefinition(gvk schema.GroupVersionKind) (selectedDefinition, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	selected := r.selectedDefinitions[gvk.GroupKind()]
	selected.version = r.definitionVersion
	if r.definitionInventoryErr != nil {
		return selected, r.definitionInventoryErr
	}
	return selected, selected.err
}

func (r *controllerState) namespaceAdmitted(namespace string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.config.NamespaceSelector == nil || namespace == "" || r.scopes[namespace]
}

func (r *controllerState) unregisterPod(key objectJobKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if root := r.roots[r.podRoots[key.UID]]; root != nil {
		delete(root.pods, key.UID)
		r.workloadQueue.Add(workloadJobKey{RootUID: root.ref.UID})
	}
	delete(r.podRoots, key.UID)
	r.deps.Publications.RemovePod(key.UID)
}

func (r *controllerState) failPod(key objectJobKey, path []discovery.ObjectRef, cause error, root ...discovery.ObjectRef) error {
	sources := r.sourcesFor(path)
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failPodLocked(key, sources, cause, root...)
}

func (r *controllerState) failPodEvaluation(key objectJobKey, path []discovery.ObjectRef, cause error, root discovery.ObjectRef, selection selectedDefinition) error {
	sources := r.sourcesFor(path)
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.currentSelectionLocked(root, selection) || !r.currentPathLocked(path) {
		return discovery.ErrStale
	}
	if _, _, err := r.readLocked(root, false); err != nil || !r.isRootLocked(root) {
		return discovery.ErrStale
	}
	return r.failPodLocked(key, sources, cause, root)
}

func (r *controllerState) failSelectedObjectDiscovery(key objectJobKey, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if root := r.roots[r.selectedObjectRoots[key]]; root != nil {
		selectedObject := root.selectedObjects[key]
		selectedObject.err = err
		root.selectedObjects[key] = selectedObject
		r.workloadQueue.Add(workloadJobKey{RootUID: root.ref.UID})
	}
	r.deps.Publications.SetDiscoveryFailure(storedRef(key.ref()), failureReason(err))
}

func (r *controllerState) failWorkload(uid types.UID, ref discovery.ObjectRef, cause error) error {
	if errors.Is(cause, context.Canceled) {
		return cause
	}
	sources := r.sourcesFor([]discovery.ObjectRef{ref})
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.roots[uid] == nil {
		return nil
	}
	next := store.WorkloadResult{Record: store.WorkloadRecord{UID: uid, Ref: storedRef(ref)}, Sources: sources, CreationSources: sources}
	if previous := r.deps.Publications.Workload(uid); previous != nil {
		next = *previous
		next.Sources = appendSources(previous.Sources, sources)
		next.CreationSources = appendSources(previous.CreationSources, sources)
	}
	next.Error = failureReason(cause)
	r.deps.Publications.UpsertWorkload(&next)
	return cause
}

func (r *controllerState) registerPodRoot(key objectJobKey, workload *discovery.GenericWorkload) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.currentPathLocked(workload.Path) {
		return discovery.ErrStale
	}
	if !r.isRootLocked(workload.Root) {
		return discovery.ErrUnresolvedOwner
	}
	// Register before reading the root body. Root events use the same lock when queuing members.
	previous := r.podRoots[key.UID]
	root := r.ensureRootLocked(workload)
	if previous != root.ref.UID {
		if old := r.roots[previous]; old != nil {
			delete(old.pods, key.UID)
			r.workloadQueue.Add(workloadJobKey{RootUID: previous})
		}
		r.podRoots[key.UID] = root.ref.UID
		root.pods[key.UID] = key
		r.workloadQueue.Add(workloadJobKey{RootUID: root.ref.UID})
	}
	return nil
}

func (r *controllerState) watchFailed(ref discovery.ObjectRef) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, failed := r.watchFailures[discovery.WatchRequest{GVK: ref.GVK, Namespace: ref.Namespace}]
	return failed
}

func (r *controllerState) publishPod(path []discovery.ObjectRef, rootRef discovery.ObjectRef, selection selectedDefinition, next store.PodResult, labelFailed bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.currentPathLocked(path) || !r.isRootLocked(rootRef) || !r.currentSelectionLocked(rootRef, selection) {
		return discovery.ErrStale
	}
	if current, _, err := r.readLocked(rootRef, false); err != nil || current.GetResourceVersion() != rootRef.ResourceVersion {
		return discovery.ErrStale
	}
	if labelFailed {
		next.Error = store.ReasonEvaluationFailed
		if previous := r.deps.Publications.Pod(next.Record.UID); previous != nil && previous.HasValue {
			next.Record = previous.Record
			next.Sources = appendSources(previous.Sources, next.Sources)
		}
	}
	r.deps.Publications.UpsertPod(&next)
	return nil
}

func (r *controllerState) requeueScope(namespace string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range r.objects {
		if namespace == "" || entry.object.GetNamespace() == namespace {
			r.enqueueObjectLocked(entry.object)
		}
	}
	// Include previous memberships so a removed definition can withdraw its roots.
	for selectedObject := range r.selectedObjectRoots {
		if namespace == "" || selectedObject.Object.Namespace == namespace {
			r.workloadQueue.Add(workloadJobKey{SelectedObject: selectedObject})
		}
	}
	return nil
}

func (r *controllerState) trackSelectedObject(key objectJobKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, known := r.selectedObjectRoots[key]; !known {
		r.selectedObjectRoots[key] = ""
	}
}

func (r *controllerState) replaceSelectedObjectTracking(key objectJobKey, object *unstructured.Unstructured) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if object != nil {
		replacement := objectJobFor(refOf(object))
		r.workloadQueue.Add(workloadJobKey{SelectedObject: replacement})
		if r.selectedObjectRoots[replacement] == "" {
			return discovery.ErrPending
		}
	}
	r.unregisterSelectedObjectLocked(key)
	return nil
}

func (r *controllerState) registerSelectedObjectRoot(key objectJobKey, workload *discovery.GenericWorkload, selection, rootSelection selectedDefinition, sources []store.SourceKey) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.currentPathLocked(workload.Path) || !r.currentSelectionLocked(key.ref(), selection) || !r.currentSelectionLocked(workload.Root, rootSelection) {
		return discovery.ErrStale
	}
	previous := r.selectedObjectRoots[key]
	root := r.ensureRootLocked(workload)
	if previous != root.ref.UID {
		if old := r.roots[previous]; old != nil {
			delete(old.selectedObjects, key)
			r.workloadQueue.Add(workloadJobKey{RootUID: previous})
		}
	}
	r.selectedObjectRoots[key] = root.ref.UID
	root.selectedObjects[key] = selectedObjectState{sources: slices.Clone(sources)}
	r.workloadQueue.Add(workloadJobKey{RootUID: root.ref.UID})
	r.deps.Publications.RemoveDiscoveryFailure(key.UID)
	return nil
}

// unregisterSelectedObject removes exporter tracking, not the Kubernetes object.
func (r *controllerState) unregisterSelectedObject(key objectJobKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.unregisterSelectedObjectLocked(key)
}

func (r *controllerState) snapshotRoot(uid types.UID) (rootSnapshot, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	root := r.roots[uid]
	if root == nil {
		return rootSnapshot{}, false
	}
	if len(root.pods) == 0 && len(root.selectedObjects) == 0 {
		delete(r.roots, uid)
		r.deps.Publications.RemoveWorkload(uid)
		return rootSnapshot{}, false
	}
	snapshot := rootSnapshot{ref: root.ref, resolution: root.resolution, generation: root.generation}
	for _, selectedObject := range root.selectedObjects {
		snapshot.sources = appendSources(snapshot.sources, selectedObject.sources)
		snapshot.err = errors.Join(snapshot.err, selectedObject.err)
	}
	return snapshot, true
}

func (r *controllerState) withdrawWorkload(uid types.UID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if root := r.roots[uid]; root != nil {
		r.enqueueMembersLocked(root)
	}
	r.deps.Publications.RemoveWorkload(uid)
}

func (r *controllerState) isRoot(ref discovery.ObjectRef) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.isRootLocked(ref)
}

func (r *controllerState) publishWorkload(uid types.UID, generation uint64, ref discovery.ObjectRef, selection selectedDefinition, next store.WorkloadResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.roots[uid]
	if state == nil || state.generation != generation || !r.currentPathLocked([]discovery.ObjectRef{ref}) || !r.isRootLocked(ref) {
		return discovery.ErrStale
	}
	if !r.currentSelectionLocked(ref, selection) {
		return discovery.ErrStale
	}
	if next.StatusError != "" {
		if previous := r.deps.Publications.Workload(uid); previous != nil && previous.HasValue {
			next.Record = previous.Record
			next.Sources = appendSources(previous.Sources, next.Sources)
		}
	}
	state.ref = ref
	r.deps.Publications.UpsertWorkload(&next)
	return nil
}

func (r *controllerState) start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return errors.New("runtime already started")
	}
	r.started = true
	r.ctx = ctx
	return nil
}

func (r *controllerState) stopWatches() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range r.watches {
		w.cancel()
	}
	for _, w := range r.replacements {
		w.cancel()
	}
}
func (r *controllerState) enqueueScopeRefreshLocked(namespace string) {
	r.podQueue.Add(objectJobKey{Object: objectKey{Namespace: namespace}, Rescan: true})
}

func (r *controllerState) enqueueObjectLocked(object *unstructured.Unstructured) {
	r.enqueuePodLocked(object)
	if r.hasSelectedDefinitionLocked(object.GroupVersionKind()) {
		r.workloadQueue.Add(workloadJobKey{SelectedObject: objectJobFor(refOf(object))})
	}
}

func (r *controllerState) enqueuePodLocked(object *unstructured.Unstructured) {
	if object == nil || object.GroupVersionKind() != podGVK {
		return
	}
	uid := object.GetUID()
	_, member := r.podRoots[uid]
	// Previously tracked Pods still need a worker to remove their result.
	if r.podSelector.Empty() || r.podSelector.Matches(labelsFor(object)) || member || r.deps.Publications.Pod(uid) != nil {
		r.podQueue.Add(objectJobFor(refOf(object)))
	}
}

func (r *controllerState) hasSelectedDefinitionLocked(gvk schema.GroupVersionKind) bool {
	selection := r.selectedDefinitions[gvk.GroupKind()]
	return selection.Definition != nil && selection.GVK == gvk
}

func (r *controllerState) enqueueRootAndMembersLocked(uid types.UID) {
	root := r.roots[uid]
	if root == nil {
		return
	}
	r.workloadQueue.Add(workloadJobKey{RootUID: uid})
	r.enqueueMembersLocked(root)
}

func (r *controllerState) enqueueMembersLocked(root *rootState) {
	for _, key := range root.pods {
		r.podQueue.Add(key)
	}
	for key := range root.selectedObjects {
		r.workloadQueue.Add(workloadJobKey{SelectedObject: key})
	}
}

func (r *controllerState) ensureRootLocked(workload *discovery.GenericWorkload) *rootState {
	for _, ref := range workload.Path[1:] {
		if ref.GVK == podGVK {
			r.ancestorPods[ref.UID] = true
		}
	}
	root := r.roots[workload.Root.UID]
	if root == nil {
		r.nextRoot++
		root = &rootState{generation: r.nextRoot, ref: workload.Root, pods: map[types.UID]objectJobKey{}, selectedObjects: map[objectJobKey]selectedObjectState{}}
		r.roots[workload.Root.UID] = root
	}
	root.resolution = workload.Resolution
	if selection := r.selectedDefinitions[workload.Root.GVK.GroupKind()]; selection.Definition != nil {
		root.ref.GVK = selection.GVK
	} else if root.ref.GVK != workload.Root.GVK {
		current := root.ref
		current.ResourceVersion = ""
		if _, _, err := r.readLocked(current, false); err != nil {
			root.ref = workload.Root
			r.workloadQueue.Add(workloadJobKey{RootUID: root.ref.UID})
		}
	}
	return root
}

func (r *controllerState) unregisterSelectedObjectLocked(key objectJobKey) {
	if root := r.roots[r.selectedObjectRoots[key]]; root != nil {
		delete(root.selectedObjects, key)
		r.workloadQueue.Add(workloadJobKey{RootUID: root.ref.UID})
	}
	delete(r.selectedObjectRoots, key)
	r.deps.Publications.RemoveDiscoveryFailure(key.UID)
}

func (r *controllerState) failPodLocked(key objectJobKey, sources []store.SourceKey, cause error, root ...discovery.ObjectRef) error {
	if errors.Is(cause, context.Canceled) {
		return cause
	}
	next := store.PodResult{Record: store.PodRecord{UID: key.UID}, Input: storedRef(key.ref())}
	if len(root) > 0 {
		next.Record.Workload = storedRef(root[0])
	}

	if previous := r.deps.Publications.Pod(key.UID); previous != nil {
		if previous.HasValue || len(root) == 0 {
			next = *previous
		}
		sources = appendSources(previous.Sources, sources)
	}
	next.Error = failureReason(cause)
	next.Sources = sources
	r.deps.Publications.UpsertPod(&next)
	if errors.Is(cause, discovery.ErrEvaluation) || errors.Is(cause, inventory.ErrInvalidDefinition) || errors.Is(cause, discovery.ErrInvalidDefinition) {
		return nil
	}
	return cause
}

func (r *controllerState) currentPathLocked(path []discovery.ObjectRef) bool {
	for _, ref := range path {
		if _, _, err := r.readLocked(ref, false); err != nil {
			return false
		}
	}
	return true
}

func (r *controllerState) currentSelectionLocked(ref discovery.ObjectRef, selection selectedDefinition) bool {
	if r.definitionVersion != selection.version {
		return false
	}
	current := r.selectedDefinitions[ref.GVK.GroupKind()]
	return current.GVK == selection.GVK && current.Origin == selection.Origin
}

func (r *controllerState) isRootLocked(ref discovery.ObjectRef) bool {
	selected := r.selectedDefinitions[ref.GVK.GroupKind()]
	if selected.Definition != nil && selected.err == nil {
		entry := r.objects[objectKey{GVK: selected.GVK, Namespace: ref.Namespace, Name: ref.Name}]
		source := r.sources[entry.source]
		if entry.object != nil && entry.object.GetUID() == ref.UID && source.hasFullObject && source.synced && source.err == nil {
			owner, valid := discovery.ControllerOwner(entry.object)
			return valid && (owner.UID == "" || slices.Contains(r.config.IgnoredOwnerKinds, owner.GVK))
		}
	}
	for _, source := range r.sources {
		if !source.synced || source.err != nil || source.gvk.GroupKind() != ref.GVK.GroupKind() {
			continue
		}
		entry := r.objects[objectKey{GVK: source.gvk, Namespace: ref.Namespace, Name: ref.Name}]
		if entry.object == nil || entry.object.GetUID() != ref.UID {
			continue
		}
		owner, valid := discovery.ControllerOwner(entry.object)
		if !valid || (owner.UID != "" && !slices.Contains(r.config.IgnoredOwnerKinds, owner.GVK)) {
			return false
		}
	}
	return true
}

// readLocked requires the state read or write lock.
func (r *objectCache) readLocked(ref discovery.ObjectRef, requireFullObject bool) (*unstructured.Unstructured, watchKey, error) {
	key := keyOf(ref)
	entry, ok := r.objects[key]
	if !ok && key.Namespace != "" {
		key.Namespace = ""
		entry, ok = r.objects[key]
	}
	if ok {
		source := r.sources[entry.source]
		if source.err != nil {
			return nil, entry.source, source.err
		}
		if !source.synced || (requireFullObject && !source.hasFullObject) {
			return nil, entry.source, discovery.ErrPending
		}
		object := entry.object
		if ref.UID != "" && object.GetUID() != ref.UID {
			return nil, entry.source, apierrors.NewNotFound(entry.source.Resource.GroupResource(), ref.Name)
		}
		if ref.ResourceVersion != "" && object.GetResourceVersion() != ref.ResourceVersion {
			return nil, entry.source, discovery.ErrStale
		}
		return object, entry.source, nil
	}
	for sourceKey, source := range r.sources {
		if source.gvk != ref.GVK || (sourceKey.Namespace != "" && sourceKey.Namespace != ref.Namespace) {
			continue
		}
		if source.err != nil {
			return nil, sourceKey, source.err
		}
		if !source.synced || (requireFullObject && !source.hasFullObject) {
			return nil, sourceKey, discovery.ErrPending
		}
		return nil, sourceKey, apierrors.NewNotFound(sourceKey.Resource.GroupResource(), ref.Name)
	}
	return nil, watchKey{}, discovery.ErrPending
}
