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

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/discovery"
	inventory "github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
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
	// Watched workload objects can discover themselves or an ancestor as root.
	// A zero outcome keeps the object tracked until discovery succeeds.
	selectedObjectRoots map[objectJobKey]selectedObjectOutcome

	selectedDefinitions    map[schema.GroupKind]selectedDefinition
	definitionInventoryErr error
	definitionVersion      uint64
	namespaceSelector      labels.Selector
	objectSelector         labels.Selector
	podSelector            labels.Selector

	ctx context.Context
	wg  sync.WaitGroup
}

type selectedObjectOutcome struct {
	rootUID   types.UID
	exclusion *selectedObjectExclusion
}

type selectedObjectExclusion struct {
	path          []discovery.ObjectRef
	root          discovery.ObjectRef
	selection     selectedDefinition
	rootSelection selectedDefinition
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
	pods            map[types.UID]objectJobKey
	selectedObjects map[objectJobKey]struct{}
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
		selectedObjectRoots: map[objectJobKey]selectedObjectOutcome{},
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
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.isNamespaceInScope(namespace)
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
	if !r.isNamespaceInScope(key.Object.Namespace) || !r.isNamespaceInScope(workload.Root.Namespace) || !r.isPathCurrent(workload.Path) {
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
			isDefinitionRemoved := selection.Definition == nil && previous.Record.Karta != ""
			if !isDefinitionRemoved {
				next.Record = previous.Record
				next.Sources = appendSources(previous.Sources, next.Sources)
			}
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
	// Include previous memberships so removed inputs can clean up their tracking.
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
	outcome := r.selectedObjectRoots[key]
	outcome.exclusion = nil
	r.selectedObjectRoots[key] = outcome
}

func (r *controllerState) replaceSelectedObjectTracking(key objectJobKey, object *unstructured.Unstructured, selection selectedDefinition) error {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if !r.isDefinitionCurrent(key.ref(), selection) {
		return discovery.ErrStale
	}
	if object != nil {
		replacement := objectJobFor(refOf(object))
		r.workloadQueue.Add(workloadJobKey{SelectedObject: replacement})
		outcome := r.selectedObjectRoots[replacement]
		if outcome.rootUID == "" {
			if outcome.exclusion == nil {
				return discovery.ErrPending
			}
			exclusion := outcome.exclusion
			// A queued reevaluation does not make the previous exclusion current.
			if !r.isPathCurrent(exclusion.path) || !r.isDefinitionCurrent(replacement.ref(), exclusion.selection) || !r.isDefinitionCurrent(exclusion.root, exclusion.rootSelection) {
				return discovery.ErrStale
			}
		}
	}
	r.removeSelectedObjectMembership(key)
	return nil
}

func (r *controllerState) registerSelectedObjectRoot(key objectJobKey, workload *discovery.GenericWorkload, selection, rootSelection selectedDefinition) error {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if !r.isNamespaceInScope(key.Object.Namespace) || !r.isNamespaceInScope(workload.Root.Namespace) {
		return discovery.ErrStale
	}
	if !r.isPathCurrent(workload.Path) || !r.isDefinitionCurrent(key.ref(), selection) || !r.isDefinitionCurrent(workload.Root, rootSelection) {
		return discovery.ErrStale
	}
	previous := r.selectedObjectRoots[key].rootUID
	root := r.ensureRoot(workload)
	if previous != root.ref.UID {
		if old := r.roots[previous]; old != nil {
			delete(old.selectedObjects, key)
			r.workloadQueue.Add(workloadJobKey{RootUID: previous})
		}
	}
	r.selectedObjectRoots[key] = selectedObjectOutcome{rootUID: root.ref.UID}
	root.selectedObjects[key] = struct{}{}
	r.workloadQueue.Add(workloadJobKey{RootUID: root.ref.UID})
	r.deps.Publications.RemoveDiscoveryFailure(key.UID)
	return nil
}

func (r *controllerState) excludeSelectedObject(key objectJobKey, workload *discovery.GenericWorkload, selection, rootSelection selectedDefinition) error {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if !r.isPathCurrent(workload.Path) || !r.isDefinitionCurrent(key.ref(), selection) || !r.isDefinitionCurrent(workload.Root, rootSelection) {
		return discovery.ErrStale
	}
	r.removeSelectedObjectMembership(key)
	r.selectedObjectRoots[key] = selectedObjectOutcome{exclusion: &selectedObjectExclusion{
		path:          slices.Clone(workload.Path),
		root:          workload.Root,
		selection:     selection,
		rootSelection: rootSelection,
	}}
	return nil
}

// unregisterSelectedObject removes exporter tracking, not the Kubernetes object.
func (r *controllerState) unregisterSelectedObject(key objectJobKey) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.removeSelectedObjectMembership(key)
}

// snapshotRoot is called only by the root-UID job, which owns its queue key
// through publication. Root lifetime does not depend on its current members.
func (r *controllerState) snapshotRoot(uid types.UID) (discovery.ObjectRef, bool) {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	root := r.roots[uid]
	if root == nil {
		return discovery.ObjectRef{}, false
	}
	return root.ref, true
}

func (r *controllerState) withdrawWorkload(uid types.UID) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.removeRoot(uid)
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
	if root == nil || !r.isNamespaceInScope(ref.Namespace) {
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
			isDefinitionRemoved := selection.Definition == nil && next.Record.HasStatus && previous.Record.Karta != ""
			if !isDefinitionRemoved {
				next.Record = previous.Record
				next.Sources = appendSources(previous.Sources, next.Sources)
			}
		}
	}
	root.ref = ref
	r.deps.Publications.UpsertWorkload(&next)
	return nil
}

func (r *controllerState) start(ctx context.Context) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.ctx = ctx
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
func (r *controllerState) isNamespaceInScope(namespace string) bool {
	return r.config.NamespaceSelector == nil || namespace == "" || r.scopes[namespace]
}

func (r *controllerState) removeRoot(uid types.UID) {
	if root := r.roots[uid]; root != nil {
		r.enqueueMembers(root)
		for podUID := range root.pods {
			delete(r.podRoots, podUID)
		}
		for key := range root.selectedObjects {
			r.selectedObjectRoots[key] = selectedObjectOutcome{}
		}
	}
	delete(r.roots, uid)
	r.deps.Publications.RemoveWorkload(uid)
}

func (r *controllerState) enqueueScopeRefresh(namespace string) {
	r.podQueue.Add(objectJobKey{Object: objectKey{Namespace: namespace}, Rescan: true})
}

func (r *controllerState) enqueueObject(object *unstructured.Unstructured) {
	r.enqueuePod(object)
	if r.shouldDiscoverObject(object.GroupVersionKind()) {
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

func (r *controllerState) shouldDiscoverObject(gvk schema.GroupVersionKind) bool {
	return gvk != namespaceGVK && gvk != kartaGVK && (gvk != podGVK || r.hasSelectedDefinition(gvk))
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
			selectedObjects: map[objectJobKey]struct{}{},
		}
		r.roots[workload.Root.UID] = root
	}
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
	if root := r.roots[r.selectedObjectRoots[key].rootUID]; root != nil {
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
	for sourceKey, source := range r.sources {
		if !source.synced || source.err != nil || source.gvk.GroupKind() != ref.GVK.GroupKind() {
			continue
		}
		entry := r.objects[objectKey{GVK: source.gvk, Namespace: ref.Namespace, Name: ref.Name}]
		if entry.object == nil || entry.source != sourceKey || entry.object.GetUID() != ref.UID {
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
