// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/discovery"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

type resourceWatch struct {
	key         watchKey
	gvk         schema.GroupVersionKind
	full, ready bool
	err         error
	objects     map[objectKey]struct{}
	cancel      context.CancelFunc
}
type reflectorStore struct {
	controller *controllerState
	watch      *resourceWatch
}

func (r *controllerState) syncWatches(ctx context.Context) error {
	if r.config.NamespaceSelector != nil {
		if _, err := r.ensureWatch(ctx, namespaceGVK, "", false); err != nil {
			return err
		}
	}
	requests, preparationErr := r.prepareWatchRequests()
	if requests == nil {
		return preparationErr
	}
	var errs []error
	if preparationErr != nil {
		errs = append(errs, preparationErr)
	}
	for _, request := range requests {
		if _, err := r.ensureWatch(ctx, request.GVK, request.Namespace, false); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Prepare source changes under the state lock. REST mapping and watch startup run after it is released.
func (r *controllerState) prepareWatchRequests() ([]discovery.WatchRequest, error) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	definitionErr := r.refreshDefinitions()
	if r.config.NamespaceSelector != nil {
		namespaceWatchReady := false
		admittedNamespaces := map[string]bool{}
		for _, w := range r.watches {
			if w.gvk != namespaceGVK || !w.ready || w.err != nil {
				continue
			}
			namespaceWatchReady = true
			for key := range w.objects {
				object := r.objects[key].object
				if r.namespaceSelector.Matches(labelsFor(object)) {
					admittedNamespaces[object.GetName()] = true
				}
			}
		}
		if !namespaceWatchReady {
			return nil, discovery.ErrPending
		}
		r.scopes = admittedNamespaces
	}
	scopes := make([]string, 0, len(r.scopes))
	for namespace := range r.scopes {
		scopes = append(scopes, namespace)
	}
	kinds := map[schema.GroupVersionKind]bool{podGVK: true}
	for _, selection := range r.selectedDefinitions {
		if selection.GVK.Version != "" && selection.GVK.Kind != "" {
			kinds[selection.GVK] = true
		}
	}
	for _, gvk := range r.config.RequiredKinds {
		kinds[gvk] = true
	}
	// Sources encountered during traversal live for the admitted namespace's lifetime.
	for key, w := range r.watches {
		if key.Namespace != "" && !r.scopes[key.Namespace] {
			r.retireWatch(w)
			if replacement := r.replacements[key]; replacement != nil {
				replacement.cancel()
				delete(r.replacements, key)
			}
		}
	}
	for request := range r.watchFailures {
		if request.Namespace != "" && !r.scopes[request.Namespace] {
			delete(r.watchFailures, request)
			r.deps.Publications.RemoveDiscoveryFailure(types.UID("source:" + request.GVK.String() + "/" + request.Namespace))
		}
	}
	requests := make([]discovery.WatchRequest, 0, len(kinds)*len(scopes))
	for gvk := range kinds {
		for _, namespace := range scopes {
			requests = append(requests, discovery.WatchRequest{GVK: gvk, Namespace: namespace})
		}
	}
	return requests, definitionErr
}

func (r *controllerState) ensureWatch(ctx context.Context, gvk schema.GroupVersionKind, namespace string, metadataOnly bool) (watchKey, error) {
	request := discovery.WatchRequest{GVK: gvk, Namespace: namespace}
	failureRef := store.ObjectRef{GVK: gvk, Namespace: namespace, UID: types.UID("source:" + gvk.String() + "/" + namespace)}
	mapping, err := r.reader.restMapping(ctx, gvk)
	if err != nil {
		return watchKey{}, r.recordWatchFailure(request, failureRef, err)
	}
	if mapping.Scope.Name() == meta.RESTScopeNameRoot || r.config.NamespaceSelector == nil {
		namespace = ""
	}
	key := watchKey{Resource: mapping.Resource, Namespace: namespace}
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	delete(r.watchFailures, request)
	r.deps.Publications.RemoveDiscoveryFailure(failureRef.UID)
	if namespace != "" && !r.scopes[namespace] {
		return key, discovery.ErrPending
	}
	if existing := r.watches[key]; existing != nil {
		if metadataOnly || existing.full {
			return key, nil
		}
		if r.replacements[key] != nil {
			return key, nil
		}
	}
	if r.ctx != nil {
		ctx = r.ctx
	}
	if err := ctx.Err(); err != nil {
		return key, err
	}
	watchCtx, cancel := context.WithCancel(ctx)
	w := &resourceWatch{key: key, gvk: gvk, full: !metadataOnly, objects: map[objectKey]struct{}{}, cancel: cancel}
	if r.watches[key] != nil {
		r.replacements[key] = w
	} else {
		r.watches[key] = w
		r.sources[key] = cacheSource{gvk: gvk, hasFullObject: !metadataOnly}
	}
	r.deps.Publications.SetSourceHealth(key, store.SourceHealth{GVK: gvk})
	listWatch := &cache.ListWatch{
		ListWithContextFunc: func(requestCtx context.Context, options metav1.ListOptions) (kruntime.Object, error) {
			return r.listResource(requestCtx, w, options)
		},
		WatchFuncWithContext: func(requestCtx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			return r.watchResource(requestCtx, w, options)
		},
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		cache.NewReflectorWithOptions(listWatch, &unstructured.Unstructured{}, &reflectorStore{controller: r, watch: w}, cache.ReflectorOptions{Name: key.Resource.String() + "/" + namespace}).RunWithContext(watchCtx)
	}()
	return key, nil
}

// Source-state helpers reuse the caller's stateMu lock.
func (r *controllerState) isTrackedWatch(w *resourceWatch) bool {
	return r.watches[w.key] == w || r.replacements[w.key] == w
}
func (r *controllerState) updateWatchHealth(w *resourceWatch, err error) {
	if !r.isTrackedWatch(w) {
		return
	}
	recovered := w.err != nil && err == nil
	w.err = err
	if r.watches[w.key] == w {
		r.sources[w.key] = cacheSource{gvk: w.gvk, hasFullObject: w.full, synced: w.ready, err: err}
	}
	if r.replacements[w.key] == nil || r.replacements[w.key] == w {
		r.deps.Publications.SetSourceHealth(w.key, store.SourceHealth{GVK: w.gvk, Synced: w.ready, Error: failureReason(err)})
	}
	if recovered {
		r.enqueueScopeRefresh(w.key.Namespace)
		for uid := range r.roots {
			r.workloadQueue.Add(workloadJobKey{RootUID: uid})
		}
	}
	if w.gvk == namespaceGVK {
		r.watchSyncQueue.Add("sources")
	}
}
func (r *controllerState) retireWatch(w *resourceWatch) {
	w.cancel()
	delete(r.watches, w.key)
	for key := range w.objects {
		entry := r.objects[key]
		if entry.object != nil && entry.source == w.key {
			delete(r.objects, key)
			r.queueObjectChange(entry.object, nil)
		}
	}
	delete(r.sources, w.key)
	r.deps.Publications.RemoveSourceHealth(w.key)
}

func (r *controllerState) listResource(ctx context.Context, w *resourceWatch, options metav1.ListOptions) (kruntime.Object, error) {
	requestCtx, cancel := context.WithTimeout(ctx, r.reader.options.RequestTimeout)
	defer cancel()
	var result *unstructured.UnstructuredList
	var err error
	if w.full {
		result, err = r.reader.clients.Dynamic.Resource(w.key.Resource).Namespace(w.key.Namespace).List(requestCtx, options)
	} else {
		var list *metav1.PartialObjectMetadataList
		list, err = r.reader.clients.Metadata.Resource(w.key.Resource).Namespace(w.key.Namespace).List(requestCtx, options)
		if err == nil {
			result = &unstructured.UnstructuredList{}
			result.SetResourceVersion(list.ResourceVersion)
			result.SetContinue(list.Continue)
			result.SetRemainingItemCount(list.RemainingItemCount)
			for i := range list.Items {
				var object *unstructured.Unstructured
				object, err = prepareWatchObject(&list.Items[i], w.gvk, false)
				if err != nil {
					break
				}
				result.Items = append(result.Items, *object)
			}
		} else if apierrors.IsNotAcceptable(err) || apierrors.IsUnsupportedMediaType(err) {
			result, err = r.reader.clients.Dynamic.Resource(w.key.Resource).Namespace(w.key.Namespace).List(requestCtx, options)
		}
	}
	if err == nil {
		for i := range result.Items {
			var object *unstructured.Unstructured
			object, err = prepareWatchObject(&result.Items[i], w.gvk, w.full)
			if err != nil {
				break
			}
			result.Items[i] = *object
		}
	}
	if err != nil && ctx.Err() == nil {
		r.setWatchHealth(w, err)
	}
	return result, err
}
func (r *controllerState) watchResource(ctx context.Context, w *resourceWatch, options metav1.ListOptions) (watch.Interface, error) {
	var stream watch.Interface
	var err error
	if w.full {
		stream, err = r.reader.clients.Dynamic.Resource(w.key.Resource).Namespace(w.key.Namespace).Watch(ctx, options)
	} else {
		stream, err = r.reader.clients.Metadata.Resource(w.key.Resource).Namespace(w.key.Namespace).Watch(ctx, options)
		if apierrors.IsNotAcceptable(err) || apierrors.IsUnsupportedMediaType(err) {
			stream, err = r.reader.clients.Dynamic.Resource(w.key.Resource).Namespace(w.key.Namespace).Watch(ctx, options)
		}
	}
	r.recordWatchStart(ctx, w, options, err)
	if err != nil {
		return nil, err
	}
	events := make(chan watch.Event)
	monitored := watch.NewProxyWatcher(events)
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer close(events)
		defer stream.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-monitored.StopChan():
				return
			case event, ok := <-stream.ResultChan():
				if !ok {
					if ctx.Err() == nil && !monitored.Stopping() {
						r.setWatchHealth(w, errors.New("watch closed"))
					}
					return
				}
				if event.Type == watch.Error {
					r.setWatchHealth(w, apierrors.FromObject(event.Object))
				} else {
					object, conversionErr := prepareWatchObject(event.Object, w.gvk, w.full)
					if conversionErr != nil {
						r.setWatchHealth(w, conversionErr)
						return
					}
					event.Object = object
				}
				select {
				case <-ctx.Done():
					return
				case <-monitored.StopChan():
					return
				case events <- event:
				}
			}
		}
	}()
	return monitored, nil
}

func (r *controllerState) recordWatchStart(ctx context.Context, w *resourceWatch, options metav1.ListOptions, err error) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	isWatchListFallback := options.SendInitialEvents != nil && *options.SendInitialEvents && apierrors.IsBadRequest(err)
	if err == nil {
		r.updateWatchHealth(w, nil)
	} else if ctx.Err() == nil && !isWatchListFallback {
		r.updateWatchHealth(w, err)
	}
}

func prepareWatchObject(value any, gvk schema.GroupVersionKind, full bool) (*unstructured.Unstructured, error) {
	if tombstone, ok := value.(cache.DeletedFinalStateUnknown); ok {
		value = tombstone.Obj
	}
	var object *unstructured.Unstructured
	switch value := value.(type) {
	case *unstructured.Unstructured:
		object = value.DeepCopy()
	case *metav1.PartialObjectMetadata:
		data, err := kruntime.DefaultUnstructuredConverter.ToUnstructured(value)
		if err != nil {
			return nil, err
		}
		object = &unstructured.Unstructured{Object: data}
	default:
		return nil, fmt.Errorf("unexpected source object %T", value)
	}
	if !full {
		object.Object = map[string]any{"metadata": object.Object["metadata"]}
	}
	object.SetManagedFields(nil)
	object.SetGroupVersionKind(gvk)
	return object, nil
}
func (s *reflectorStore) Add(object any) error {
	return s.controller.applyWatchEvent(s.watch, object, false)
}
func (s *reflectorStore) Update(object any) error {
	return s.controller.applyWatchEvent(s.watch, object, false)
}
func (s *reflectorStore) Delete(object any) error {
	return s.controller.applyWatchEvent(s.watch, object, true)
}
func (s *reflectorStore) Resync() error { return nil }
func (s *reflectorStore) Replace(values []any, _ string) error {
	// Validate the complete snapshot before making absence authoritative.
	objects := make(map[objectKey]*unstructured.Unstructured, len(values))
	for _, value := range values {
		object, err := prepareWatchObject(value, s.watch.gvk, s.watch.full)
		if err != nil {
			return s.controller.rejectWatchObject(s.watch, err)
		}
		if object.GetUID() == "" || object.GetName() == "" {
			return s.controller.rejectWatchObject(s.watch, errors.New("LIST object has no identity"))
		}
		key := keyOf(refOf(object))
		if _, duplicate := objects[key]; duplicate {
			return s.controller.rejectWatchObject(s.watch, errors.New("LIST contains duplicate object names"))
		}
		objects[key] = object
	}
	r, w := s.controller, s.watch
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if !r.isTrackedWatch(w) {
		return nil
	}
	if previous := r.watches[w.key]; previous != w {
		previous.cancel()
		w.objects = previous.objects
		r.watches[w.key] = w
		delete(r.replacements, w.key)
	}
	for key := range w.objects {
		if objects[key] == nil {
			old := r.objects[key].object
			delete(r.objects, key)
			if old != nil {
				r.queueObjectChange(old, nil)
			}
		}
	}
	w.objects = make(map[objectKey]struct{}, len(objects))
	for key, object := range objects {
		old := r.objects[key].object
		r.objects[key] = cachedObject{object: object, source: w.key}
		w.objects[key] = struct{}{}
		r.queueObjectChange(old, object)
	}
	w.ready = true
	r.updateWatchHealth(w, nil)
	r.enqueueScopeRefresh(w.key.Namespace)
	return nil
}
func (r *controllerState) applyWatchEvent(w *resourceWatch, value any, deleted bool) error {
	object, err := prepareWatchObject(value, w.gvk, w.full)
	if err != nil {
		return r.rejectWatchObject(w, err)
	}
	if object.GetUID() == "" || object.GetName() == "" {
		return r.rejectWatchObject(w, errors.New("watch object has no identity"))
	}
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if r.watches[w.key] != w {
		return nil
	}
	key := keyOf(refOf(object))
	old := r.objects[key].object
	if deleted {
		if old == nil || old.GetUID() != object.GetUID() {
			return nil
		}
		delete(r.objects, key)
		delete(w.objects, key)
		r.queueObjectChange(old, nil)
	} else {
		r.objects[key] = cachedObject{object: object, source: w.key}
		w.objects[key] = struct{}{}
		r.queueObjectChange(old, object)
	}
	return nil
}

// queueObjectChange runs inside cache acceptance under stateMu. It only queues work.
func (r *controllerState) queueObjectChange(old, current *unstructured.Unstructured) {
	// Keep the old match so an update that leaves the selector can clean up.
	r.enqueuePod(old)
	r.enqueuePod(current)
	object := current
	if object == nil {
		object = old
	}
	gvk := object.GroupVersionKind()
	if gvk == namespaceGVK && r.config.NamespaceSelector != nil {
		r.watchSyncQueue.Add("sources")
	}
	ancestor := gvk != podGVK || r.ancestorPods[object.GetUID()]
	if old != nil && gvk == podGVK {
		ancestor = ancestor || r.ancestorPods[old.GetUID()]
	}
	if old != nil && (current == nil || old.GetUID() != current.GetUID()) {
		delete(r.ancestorPods, old.GetUID())
		oldKey := objectJobFor(refOf(old))
		if _, known := r.selectedObjectRoots[oldKey]; known {
			r.workloadQueue.Add(workloadJobKey{SelectedObject: oldKey})
		}
		r.enqueueRootAndMembers(old.GetUID())
	}
	ownershipChanged := old == nil || current == nil || old.GetUID() != current.GetUID()
	if !ownershipChanged {
		before, beforeOK := discovery.ControllerOwner(old)
		after, afterOK := discovery.ControllerOwner(current)
		ownershipChanged = beforeOK != afterOK || before != after
	}
	if current != nil {
		key := objectJobFor(refOf(current))
		_, tracked := r.selectedObjectRoots[key]
		if (!tracked || ownershipChanged) && r.hasSelectedDefinition(gvk) {
			r.workloadQueue.Add(workloadJobKey{SelectedObject: key})
		}
		if root := r.roots[current.GetUID()]; root != nil {
			r.workloadQueue.Add(workloadJobKey{RootUID: current.GetUID()})
			for _, pod := range root.pods {
				r.podQueue.Add(pod)
			}
		}
	}
	selectorChanged := !r.objectSelector.Empty() && old != nil && current != nil && r.objectSelector.Matches(labelsFor(old)) != r.objectSelector.Matches(labelsFor(current))
	if (ownershipChanged && ancestor) || selectorChanged {
		r.enqueueScopeRefresh(object.GetNamespace())
	}
}

func (r *controllerState) rejectWatchObject(w *resourceWatch, err error) error {
	r.setWatchHealth(w, err)
	return err
}

func (r *controllerState) setWatchHealth(w *resourceWatch, err error) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.updateWatchHealth(w, err)
}

func (r *controllerState) recordWatchFailure(request discovery.WatchRequest, ref store.ObjectRef, err error) error {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if r.config.NamespaceSelector != nil && request.Namespace != "" && !r.scopes[request.Namespace] {
		return discovery.ErrPending
	}
	r.watchFailures[request] = err
	r.deps.Publications.SetDiscoveryFailure(ref, failureReason(err))
	return err
}
