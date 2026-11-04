// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/discovery"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/exportermetrics"
	inventory "github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/instructions"
)

type Runner interface {
	Run(context.Context) error
	Ready() bool
}
type Dependencies struct {
	Discovery    discovery.Discoverer
	Inventory    inventory.Observer
	Labels       labelresolver.Resolver
	Publications store.Writer
}
type Config struct {
	IgnoredOwnerKinds                              []schema.GroupVersionKind
	ResourceRules, ExcludeResourceRules            []ResourceRule
	NamespaceSelector, ObjectSelector, PodSelector *metav1.LabelSelector
	ClusterDefinitions                             bool
	RequiredKinds                                  []schema.GroupVersionKind
	Workers, LeafWorkers, RootWorkers              int
}
type ResourceRule struct {
	APIGroups   []string `json:"apiGroups"`
	APIVersions []string `json:"apiVersions"`
	Resources   []string `json:"resources"`
}

type Runtime struct {
	state   *controllerState
	runOnce sync.Once
	runErr  error
}

type objectJobKey struct {
	Object objectKey
	UID    types.UID
	Rescan bool
}
type workloadJobKey struct {
	RootUID        types.UID
	SelectedObject objectJobKey
}
type watchKey = store.SourceKey

type selectedDefinition struct {
	inventory.Selection
	summary *instructions.StructureSummary
	err     error
	version uint64
}

const defaultAmountOfWorkers = 4

var (
	ErrMissingDependencies    = errors.New("runtime dependencies are required")
	errInventoryChangesClosed = errors.New("definition inventory change channel closed before cancellation")
)

var podGVK = schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
var kartaGVK = schema.GroupVersionKind{Group: "run.ai", Version: "v1alpha1", Kind: "Karta"}
var definitionSource = store.SourceKey{Resource: kartaGVK.GroupVersion().WithResource("kartas")}
var namespaceGVK = schema.GroupVersionKind{Version: "v1", Kind: "Namespace"}

func New(config Config, deps Dependencies, reader *Reader) (*Runtime, error) {
	if deps.Discovery == nil || deps.Inventory == nil || deps.Labels == nil || deps.Publications == nil || reader == nil {
		return nil, ErrMissingDependencies
	}
	if err := ValidateResourceRules(slices.Concat(config.ResourceRules, config.ExcludeResourceRules)); err != nil {
		return nil, err
	}
	if config.Workers == 0 {
		config.Workers = defaultAmountOfWorkers
	}
	if config.LeafWorkers == 0 {
		config.LeafWorkers = config.Workers
	}
	if config.RootWorkers == 0 {
		config.RootWorkers = config.Workers
	}
	if config.Workers < 1 || config.LeafWorkers < 1 || config.RootWorkers < 1 {
		return nil, errors.New("worker limits must be positive")
	}
	if config.NamespaceSelector != nil && len(config.NamespaceSelector.MatchLabels) == 0 && len(config.NamespaceSelector.MatchExpressions) == 0 {
		config.NamespaceSelector = nil
	}
	r := newControllerState(reader.objectCache)
	r.config = config
	r.deps = deps
	r.reader = reader
	r.podQueue = newQueue[objectJobKey]("exporter-pods")
	r.workloadQueue = newQueue[workloadJobKey]("exporter-workloads")
	r.watchSyncQueue = newQueue[string]("exporter-sources")
	if config.NamespaceSelector == nil {
		r.scopes[""] = true
	}
	if config.ClusterDefinitions {
		// The app waits for inventory loading before Run. The source worker then
		// installs the controller's definition snapshot and clears this error.
		r.definitionInventoryErr = inventory.ErrNotReady
		deps.Publications.SetSourceHealth(definitionSource, store.SourceHealth{GVK: kartaGVK})
	}
	var err error
	r.namespaceSelector, err = selector(config.NamespaceSelector)
	if err != nil {
		return nil, err
	}
	r.objectSelector, err = selector(config.ObjectSelector)
	if err != nil {
		return nil, err
	}
	r.podSelector, err = selector(config.PodSelector)
	if err != nil {
		return nil, err
	}
	reader.ensure = r.ensureWatch
	return &Runtime{state: r}, nil
}
func (r *Runtime) Run(ctx context.Context) error {
	r.runOnce.Do(func() {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		r.state.start(ctx)
		for i := 0; i < r.state.config.LeafWorkers; i++ {
			r.state.wg.Go(func() { runWorker(ctx, r.state.podQueue, r.handlePodInput) })
		}
		for i := 0; i < r.state.config.RootWorkers; i++ {
			r.state.wg.Go(func() { runWorker(ctx, r.state.workloadQueue, r.handleWorkloadInput) })
		}
		r.state.wg.Go(func() {
			runWorker(ctx, r.state.watchSyncQueue, func(ctx context.Context, _ string) error { return r.state.syncWatches(ctx) })
		})
		r.state.watchSyncQueue.Add("sources")
		changes := r.state.deps.Inventory.Changes()
	running:
		for {
			select {
			case <-ctx.Done():
				break running
			case _, open := <-changes:
				if !open {
					if ctx.Err() == nil {
						r.runErr = errInventoryChangesClosed
					}
					break running
				}
				r.state.watchSyncQueue.Add("sources")
			}
		}
		cancel()
		r.state.podQueue.ShutDown()
		r.state.workloadQueue.ShutDown()
		r.state.watchSyncQueue.ShutDown()
		r.state.stopWatches()
		r.state.wg.Wait()
	})
	return r.runErr
}
func (r *Runtime) Ready() bool {
	return r.state.ready()
}

func (r *controllerState) ready() bool {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	isRunning := r.ctx != nil && r.ctx.Err() == nil
	if !isRunning || r.definitionInventoryErr != nil || len(r.watches) == 0 {
		return false
	}
	for request := range r.watchFailures {
		if r.requiredWatch(request.GVK) {
			return false
		}
	}
	hasNamespaceWatch := false
	for _, w := range r.watches {
		isUnavailable := !w.ready || w.err != nil || r.replacements[w.key] != nil
		if r.requiredWatch(w.gvk) && isUnavailable {
			return false
		}
		if w.gvk == namespaceGVK && r.config.NamespaceSelector != nil {
			hasNamespaceWatch = true
			for key := range w.objects {
				object := r.objects[key].object
				if r.namespaceSelector.Matches(labelsFor(object)) && !r.hasRequiredWatches(object.GetName()) {
					return false
				}
			}
		}
	}
	if r.config.NamespaceSelector != nil {
		return hasNamespaceWatch
	}
	return r.hasRequiredWatches("")
}

// hasRequiredWatches requires the caller to hold stateMu in read or write mode.
func (r *controllerState) hasRequiredWatches(namespace string) bool {
	for _, gvk := range slices.Concat([]schema.GroupVersionKind{podGVK}, r.config.RequiredKinds) {
		found := false
		for key, w := range r.watches {
			if w.gvk == gvk && (key.Namespace == "" || key.Namespace == namespace) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func newQueue[T comparable](name string) workqueue.TypedRateLimitingInterface[T] {
	return workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.NewTypedItemExponentialFailureRateLimiter[T](10*time.Millisecond, 10*time.Second), workqueue.TypedRateLimitingQueueConfig[T]{Name: name, MetricsProvider: exportermetrics.Workqueue})
}
func selector(value *metav1.LabelSelector) (labels.Selector, error) {
	if value == nil {
		return labels.Everything(), nil
	}
	return metav1.LabelSelectorAsSelector(value)
}
func runWorker[T comparable](ctx context.Context, queue workqueue.TypedRateLimitingInterface[T], reconcile func(context.Context, T) error) {
	for {
		key, shutdown := queue.Get()
		if shutdown {
			return
		}
		if err := reconcile(ctx, key); err != nil && ctx.Err() == nil {
			queue.AddRateLimited(key)
		} else {
			queue.Forget(key)
		}
		queue.Done(key)
	}
}
func (r *controllerState) requiredWatch(gvk schema.GroupVersionKind) bool {
	return gvk == podGVK || (gvk == namespaceGVK && r.config.NamespaceSelector != nil) || slices.Contains(r.config.RequiredKinds, gvk)
}

// prepareDefinitions reads and builds the next snapshot without holding stateMu.
// The single source worker serializes definition refreshes.
func (r *controllerState) prepareDefinitions() (map[schema.GroupKind]selectedDefinition, error) {
	results, err := r.deps.Inventory.List()
	if err != nil {
		return nil, err
	}
	next := make(map[schema.GroupKind]selectedDefinition, len(results))
	for _, result := range results {
		selected := selectedDefinition{Selection: result.Selection, err: result.Err}
		if selected.err == nil && selected.Definition != nil {
			selected.summary, err = instructions.NewStructureSummary(selected.Definition)
			if err != nil {
				selected.err = fmt.Errorf("%w: %w", discovery.ErrInvalidDefinition, err)
			}
		}
		next[result.GVK.GroupKind()] = selected
	}
	return next, nil
}

// refreshDefinitions commits the prepared snapshot and requeues affected work.
// The caller must hold stateMu in write mode.
func (r *controllerState) refreshDefinitions(next map[schema.GroupKind]selectedDefinition, sourceErr error) error {
	if sourceErr != nil {
		next = r.selectedDefinitions
	}
	healthChanged := failureReason(r.definitionInventoryErr) != failureReason(sourceErr)
	changedKinds := map[schema.GroupKind]bool{}
	for kind, selected := range next {
		previous, exists := r.selectedDefinitions[kind]
		if exists && isSameDefinition(previous, selected) {
			// Keep the evaluation snapshot stable across metadata-only updates.
			next[kind] = previous
			continue
		}
		changedKinds[kind] = true
	}
	for kind := range r.selectedDefinitions {
		if _, exists := next[kind]; !exists {
			changedKinds[kind] = true
		}
	}
	r.definitionInventoryErr = sourceErr
	r.selectedDefinitions = next
	if r.config.ClusterDefinitions {
		r.deps.Publications.SetSourceHealth(definitionSource, store.SourceHealth{GVK: kartaGVK, Synced: sourceErr == nil, Error: failureReason(sourceErr)})
	}
	if !healthChanged && len(changedKinds) == 0 {
		return sourceErr
	}
	r.definitionVersion++
	if healthChanged {
		r.enqueueScopeRefresh("")
	} else {
		for _, entry := range r.objects {
			ref := refOf(entry.object)
			if changedKinds[ref.GVK.GroupKind()] {
				r.enqueueObject(entry.object)
			}
			if ref.GVK != podGVK {
				continue
			}
			root := r.roots[r.podRoots[ref.UID]]
			if root == nil || changedKinds[root.ref.GVK.GroupKind()] {
				r.enqueuePod(entry.object)
			}
		}
	}
	for uid, root := range r.roots {
		if healthChanged || changedKinds[root.ref.GVK.GroupKind()] {
			r.workloadQueue.Add(workloadJobKey{RootUID: uid})
			r.enqueueMembers(root)
		}
	}
	for selectedObject := range r.selectedObjectRoots {
		if changedKinds[selectedObject.Object.GVK.GroupKind()] {
			r.workloadQueue.Add(workloadJobKey{SelectedObject: selectedObject})
		}
	}
	return sourceErr
}

// isSameDefinition ignores metadata that does not affect workload evaluation.
func isSameDefinition(previous, next selectedDefinition) bool {
	hasSameWinner := previous.Origin.Source == next.Origin.Source && previous.Origin.Name == next.Origin.Name && previous.Origin.UID == next.Origin.UID
	if previous.GVK != next.GVK || !hasSameWinner {
		return false
	}
	if failureReason(previous.err) != failureReason(next.err) {
		return false
	}
	if previous.Definition == nil || next.Definition == nil {
		return previous.Definition == nil && next.Definition == nil
	}
	return reflect.DeepEqual(previous.Definition.Spec, next.Definition.Spec)
}

func objectJobFor(ref discovery.ObjectRef) objectJobKey {
	return objectJobKey{Object: keyOf(ref), UID: ref.UID}
}
func (key objectJobKey) ref() discovery.ObjectRef {
	return discovery.ObjectRef{GVK: key.Object.GVK, Namespace: key.Object.Namespace, Name: key.Object.Name, UID: key.UID}
}

// sourcesFor takes its own read lock. Callers must not hold stateMu.
func (r *controllerState) sourcesFor(path []discovery.ObjectRef) []store.SourceKey {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	var sources []store.SourceKey
	for _, ref := range path {
		if entry, ok := r.objects[keyOf(ref)]; ok && !slices.Contains(sources, entry.source) {
			sources = append(sources, entry.source)
		}
	}
	for key, source := range r.sources {
		if source.gvk == namespaceGVK && r.config.NamespaceSelector != nil {
			if !slices.Contains(sources, key) {
				sources = append(sources, key)
			}
		}
	}
	if r.config.ClusterDefinitions {
		sources = append(sources, definitionSource)
	}
	return sources
}
func appendSources(sources, additional []store.SourceKey) []store.SourceKey {
	result := slices.Clone(sources)
	for _, key := range additional {
		if !slices.Contains(result, key) {
			result = append(result, key)
		}
	}
	return result
}
