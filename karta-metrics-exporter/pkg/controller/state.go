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
// Accepted objects are immutable. Reader shares stateMu with this state.
// State operations acquire the lock; their helpers reuse it for the full operation.
// Discovery and evaluation run outside stateMu.
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

// stateMu guards both these cache maps and controllerState membership, source
// and publication changes. Reader and controllerState share this objectCache.
type objectCache struct {
	stateMu sync.RWMutex
	objects map[objectKey]cachedObject
	sources map[watchKey]cacheSource
}

type rootState struct {
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
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	selected := r.selectedDefinitions[gvk.GroupKind()]
	selected.version = r.definitionVersion
	if r.definitionInventoryErr != nil {
		return selected, r.definitionInventoryErr
	}
	return selected, selected.err
}

func (r *controllerState) namespaceAdmitted(namespace string) bool {
	if r.config.NamespaceSelector == nil || namespace == "" {
		return true
	}
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.scopes[namespace]
}

func (r *controllerState) unregisterPod(key objectJobKey) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if root := r.roots[r.podRoots[key.UID]]; root != nil {
		delete(root.pods, key.UID)
		r.workloadQueue.Add(workloadJobKey{RootUID: root.ref.UID})
	}
	delete(r.podRoots, key.UID)
	r.deps.Publications.RemovePod(key.UID)
}

func (r *controllerState) failPod(key objectJobKey, path []discovery.ObjectRef, cause error, root ...discovery.ObjectRef) error {
	sources := r.sourcesFor(path)
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	return r.publishPodFailure(key, sources, cause, root...)
}

func (r *controllerState) failPodEvaluation(key objectJobKey, path []discovery.ObjectRef, cause error, root discovery.ObjectRef, selection selectedDefinition) error {
	sources := r.sourcesFor(path)
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if !r.isDefinitionCurrent(root, selection) || !r.isPathCurrent(path) {
		return discovery.ErrStale
	}
	if _, _, err := r.lookupObject(root, false); err != nil || !r.hasRootOwnership(root) {
		return discovery.ErrStale
	}
	return r.publishPodFailure(key, sources, cause, root)
}

func (r *controllerState) failSelectedObjectDiscovery(key objectJobKey, err error) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
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
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if r.roots[uid] == nil {
		return nil
	}
	next := store.WorkloadResult{
		Record:          store.WorkloadRecord{UID: uid, Ref: storedRef(ref)},
		Sources:         sources,
		CreationSources: sources,
	}
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
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if !r.isPathCurrent(workload.Path) {
		return discovery.ErrStale
	}
	if !r.hasRootOwnership(workload.Root) {
		return discovery.ErrUnresolvedOwner
	}
	// Register before reading the root body. Root events use the same lock when queuing members.
	previous := r.podRoots[key.UID]
	root := r.ensureRoot(workload)
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
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	_, failed := r.watchFailures[discovery.WatchRequest{GVK: ref.GVK, Namespace: ref.Namespace}]
	return failed
}

func (r *controllerState) publishPod(path []discovery.ObjectRef, rootRef discovery.ObjectRef, selection selectedDefinition, next store.PodResult, labelFailed bool) error {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if !r.isPathCurrent(path) || !r.hasRootOwnership(rootRef) || !r.isDefinitionCurrent(rootRef, selection) {
		return discovery.ErrStale
	}
	if current, _, err := r.lookupObject(rootRef, false); err != nil || current.GetResourceVersion() != rootRef.ResourceVersion {
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
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	for _, entry := range r.objects {
		if namespace == "" || entry.object.GetNamespace() == namespace {
			r.enqueueObject(entry.object)
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
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if _, known := r.selectedObjectRoots[key]; !known {
		r.selectedObjectRoots[key] = ""
	}
}

func (r *controllerState) replaceSelectedObjectTracking(key objectJobKey, object *unstructured.Unstructured) error {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if object != nil {
		replacement := objectJobFor(refOf(object))
		r.workloadQueue.Add(workloadJobKey{SelectedObject: replacement})
		if r.selectedObjectRoots[replacement] == "" {
			return discovery.ErrPending
		}
	}
	r.removeSelectedObjectMembership(key)
	return nil
}

func (r *controllerState) registerSelectedObjectRoot(key objectJobKey, workload *discovery.GenericWorkload, selection, rootSelection selectedDefinition, sources []store.SourceKey) error {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if !r.isPathCurrent(workload.Path) || !r.isDefinitionCurrent(key.ref(), selection) || !r.isDefinitionCurrent(workload.Root, rootSelection) {
		return discovery.ErrStale
	}
	previous := r.selectedObjectRoots[key]
	root := r.ensureRoot(workload)
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
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.removeSelectedObjectMembership(key)
}

// snapshotRoot is called only by the root-UID job, which owns its queue key
// through publication. Only this operation removes empty root entries.
func (r *controllerState) snapshotRoot(uid types.UID) (rootSnapshot, bool) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	root := r.roots[uid]
	if root == nil {
		return rootSnapshot{}, false
	}
	if len(root.pods) == 0 && len(root.selectedObjects) == 0 {
		delete(r.roots, uid)
		r.deps.Publications.RemoveWorkload(uid)
		return rootSnapshot{}, false
	}
	snapshot := rootSnapshot{ref: root.ref, resolution: root.resolution}
	for _, selectedObject := range root.selectedObjects {
		snapshot.sources = appendSources(snapshot.sources, selectedObject.sources)
		snapshot.err = errors.Join(snapshot.err, selectedObject.err)
	}
	return snapshot, true
}

func (r *controllerState) withdrawWorkload(uid types.UID) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if root := r.roots[uid]; root != nil {
		r.enqueueMembers(root)
	}
	r.deps.Publications.RemoveWorkload(uid)
}

func (r *controllerState) isRoot(ref discovery.ObjectRef) bool {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.hasRootOwnership(ref)
}

func (r *controllerState) publishWorkload(uid types.UID, ref discovery.ObjectRef, selection selectedDefinition, next store.WorkloadResult) error {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	root := r.roots[uid]
	if root == nil {
		return discovery.ErrStale
	}
	if !r.isPathCurrent([]discovery.ObjectRef{ref}) || !r.hasRootOwnership(ref) {
		return discovery.ErrStale
	}
	if !r.isDefinitionCurrent(ref, selection) {
		return discovery.ErrStale
	}
	if next.StatusError != "" {
		if previous := r.deps.Publications.Workload(uid); previous != nil && previous.HasValue {
			next.Record = previous.Record
			next.Sources = appendSources(previous.Sources, next.Sources)
		}
	}
	root.ref = ref
	r.deps.Publications.UpsertWorkload(&next)
	return nil
}

func (r *controllerState) start(ctx context.Context) error {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if r.started {
		return errors.New("runtime already started")
	}
	r.started = true
	r.ctx = ctx
	return nil
}

func (r *controllerState) stopWatches() {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	for _, w := range r.watches {
		w.cancel()
	}
	for _, w := range r.replacements {
		w.cancel()
	}
}

// State helpers below run inside the caller's stateMu critical section.
// They keep validation, membership updates and publication in one operation.
func (r *controllerState) enqueueScopeRefresh(namespace string) {
	r.podQueue.Add(objectJobKey{Object: objectKey{Namespace: namespace}, Rescan: true})
}

func (r *controllerState) enqueueObject(object *unstructured.Unstructured) {
	r.enqueuePod(object)
	if r.hasSelectedDefinition(object.GroupVersionKind()) {
		r.workloadQueue.Add(workloadJobKey{SelectedObject: objectJobFor(refOf(object))})
	}
}

func (r *controllerState) enqueuePod(object *unstructured.Unstructured) {
	if object == nil || object.GroupVersionKind() != podGVK {
		return
	}
	uid := object.GetUID()
	_, member := r.podRoots[uid]
	// Previously tracked Pods still need a worker to remove their result.
	if r.podSelector.Matches(labelsFor(object)) || member || r.deps.Publications.Pod(uid) != nil {
		r.podQueue.Add(objectJobFor(refOf(object)))
	}
}

func (r *controllerState) hasSelectedDefinition(gvk schema.GroupVersionKind) bool {
	selection := r.selectedDefinitions[gvk.GroupKind()]
	return selection.Definition != nil && selection.GVK == gvk
}

func (r *controllerState) enqueueRootAndMembers(uid types.UID) {
	root := r.roots[uid]
	if root == nil {
		return
	}
	r.workloadQueue.Add(workloadJobKey{RootUID: uid})
	r.enqueueMembers(root)
}

func (r *controllerState) enqueueMembers(root *rootState) {
	for _, key := range root.pods {
		r.podQueue.Add(key)
	}
	for key := range root.selectedObjects {
		r.workloadQueue.Add(workloadJobKey{SelectedObject: key})
	}
}

func (r *controllerState) ensureRoot(workload *discovery.GenericWorkload) *rootState {
	for _, ref := range workload.Path[1:] {
		if ref.GVK == podGVK {
			r.ancestorPods[ref.UID] = true
		}
	}
	root := r.roots[workload.Root.UID]
	if root == nil {
		root = &rootState{
			ref:             workload.Root,
			pods:            map[types.UID]objectJobKey{},
			selectedObjects: map[objectJobKey]selectedObjectState{},
		}
		r.roots[workload.Root.UID] = root
	}
	root.resolution = workload.Resolution
	if selection := r.selectedDefinitions[workload.Root.GVK.GroupKind()]; selection.Definition != nil {
		root.ref.GVK = selection.GVK
	} else if root.ref.GVK != workload.Root.GVK {
		current := root.ref
		current.ResourceVersion = ""
		if _, _, err := r.lookupObject(current, false); err != nil {
			root.ref = workload.Root
			r.workloadQueue.Add(workloadJobKey{RootUID: root.ref.UID})
		}
	}
	return root
}

func (r *controllerState) removeSelectedObjectMembership(key objectJobKey) {
	if root := r.roots[r.selectedObjectRoots[key]]; root != nil {
		delete(root.selectedObjects, key)
		r.workloadQueue.Add(workloadJobKey{RootUID: root.ref.UID})
	}
	delete(r.selectedObjectRoots, key)
	r.deps.Publications.RemoveDiscoveryFailure(key.UID)
}

func (r *controllerState) publishPodFailure(key objectJobKey, sources []store.SourceKey, cause error, root ...discovery.ObjectRef) error {
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

// isPathCurrent requires the caller to hold stateMu in read or write mode.
func (r *controllerState) isPathCurrent(path []discovery.ObjectRef) bool {
	for _, ref := range path {
		if _, _, err := r.lookupObject(ref, false); err != nil {
			return false
		}
	}
	return true
}

// isDefinitionCurrent requires the caller to hold stateMu in read or write mode.
func (r *controllerState) isDefinitionCurrent(ref discovery.ObjectRef, selection selectedDefinition) bool {
	if r.definitionVersion != selection.version {
		return false
	}
	current := r.selectedDefinitions[ref.GVK.GroupKind()]
	return current.GVK == selection.GVK && current.Origin == selection.Origin
}

// hasRootOwnership requires the caller to hold stateMu in read or write mode.
func (r *controllerState) hasRootOwnership(ref discovery.ObjectRef) bool {
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

// lookupObject requires stateMu in read or write mode. It makes no API calls.
func (r *objectCache) lookupObject(ref discovery.ObjectRef, requireFullObject bool) (*unstructured.Unstructured, watchKey, error) {
	key := keyOf(ref)
	entry, ok := r.objects[key]
	if !ok && key.Namespace != "" {
		key.Namespace = ""
		entry, ok = r.objects[key]
	}
	sourceKey := entry.source
	if !ok {
		found := false
		for key, source := range r.sources {
			if source.gvk == ref.GVK && (key.Namespace == "" || key.Namespace == ref.Namespace) {
				sourceKey, found = key, true
				break
			}
		}
		if !found {
			return nil, watchKey{}, discovery.ErrPending
		}
	}
	source := r.sources[sourceKey]
	if source.err != nil {
		return nil, sourceKey, source.err
	}
	if !source.synced || (requireFullObject && !source.hasFullObject) {
		return nil, sourceKey, discovery.ErrPending
	}
	if !ok || (ref.UID != "" && entry.object.GetUID() != ref.UID) {
		return nil, sourceKey, apierrors.NewNotFound(sourceKey.Resource.GroupResource(), ref.Name)
	}
	if ref.ResourceVersion != "" && entry.object.GetResourceVersion() != ref.ResourceVersion {
		return nil, sourceKey, discovery.ErrStale
	}
	return entry.object, sourceKey, nil
}
