// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartadefinitioninventory

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog"
)

// Reader looks up the selected Karta definition for a workload type.
type Reader interface {
	Lookup(schema.GroupVersionKind) (Selection, error)
}

// Selection pairs a definition with the source it came from.
type Selection struct {
	Definition *v1alpha1.Karta
	GVK        schema.GroupVersionKind
	Origin     Origin
}

// Origin identifies the definition. Catalog entries have no UID or resource version.
type Origin struct {
	Source          Source
	Name            string
	UID             types.UID
	ResourceVersion string
}

type Source string

const (
	SourceCatalog Source = "catalog"
	SourceCluster Source = "cluster"
)

// Config selects definitions before they enter the lookup caches. A source
// selects a definition when its name is in Include; an empty list allows all,
// and Exclude wins.
type Config struct {
	Catalog CatalogConfig
	Cluster ClusterConfig
}

type CatalogConfig struct {
	Enabled bool
	Include []string
	Exclude []string
}

type ClusterConfig struct {
	Enabled bool
	Include []string
	Exclude []string
}

var (
	ErrInvalidConfig     = errors.New("invalid definition source config")
	ErrNotReady          = errors.New("definition sources not ready")
	ErrUnavailable       = errors.New("definition source unavailable")
	ErrPermissionDenied  = errors.New("definition source permission denied")
	ErrInvalidDefinition = errors.New("invalid Karta definition")
)

// Inventory serves definitions from the catalog and the cluster. Reads never
// call the API server.
type Inventory struct {
	client        dynamic.Interface
	catalog       map[schema.GroupKind]definitionEntry
	clusterFilter sourceFilter
	log           *slog.Logger
	loaded        chan struct{}
	done          chan struct{}

	mu       sync.RWMutex
	cluster  map[string]definitionEntry
	started  bool
	synced   bool
	stopped  bool
	failure  error
	failures uint64
	// listedFrom is the failure count when the latest list began.
	listedFrom        uint64
	unkeyable         []string
	unmatchedIncludes []string
}

// Entries own their definitions and validation errors. A nil definition means
// decoding failed. Updates replace whole entries, so readers can release mu.
type definitionEntry struct {
	definition *v1alpha1.Karta
	err        error
}

type sourceFilter struct {
	enabled bool
	include sets.Set[string]
	exclude sets.Set[string]
}

type reflectorStore struct {
	inventory *Inventory
}

var (
	errStopped = errors.New("inventory stopped")
	errRelist  = errors.New("watch not resumed after a failure; relisting to confirm fresh data")
	// client-go relists after a watch that ends within a second without events.
	errShortWatch = errors.New("watch closed within a second without events; relisting")

	_ Reader               = (*Inventory)(nil)
	_ cache.ReflectorStore = reflectorStore{}
)

// New needs a client only when the cluster source is enabled.
func New(client dynamic.Interface, config Config) (*Inventory, error) {
	catalogDefinitions := catalog.List()
	known := sets.New[string]()
	for _, k := range catalogDefinitions {
		known.Insert(k.Name)
	}
	catalogFilter, catalogErrs := compileSource(SourceCatalog, config.Catalog.Enabled, config.Catalog.Include, config.Catalog.Exclude, known)
	clusterFilter, clusterErrs := compileSource(SourceCluster, config.Cluster.Enabled, config.Cluster.Include, config.Cluster.Exclude, nil)
	if err := errors.Join(append(catalogErrs, clusterErrs...)...); err != nil {
		return nil, err
	}
	if clusterFilter.enabled && client == nil {
		return nil, errors.New("the cluster definition source requires a client")
	}
	i := &Inventory{
		client:        client,
		catalog:       make(map[schema.GroupKind]definitionEntry),
		clusterFilter: clusterFilter,
		log:           slog.Default(),
		loaded:        make(chan struct{}),
		done:          make(chan struct{}),
		cluster:       make(map[string]definitionEntry),
	}
	for _, k := range catalogDefinitions {
		if !catalogFilter.matches(k) {
			continue
		}
		key := catalog.RootKey(k)
		other := i.catalog[key.GroupKind()].definition
		if other == nil || cmp.Or(
			version.CompareKubeAwareVersionStrings(catalog.RootKey(other).Version, key.Version),
			strings.Compare(k.Name, other.Name)) < 0 {
			i.catalog[key.GroupKind()] = definitionEntry{definition: k}
		}
	}
	for gk, entry := range i.catalog {
		i.catalog[gk] = validatedEntry(entry.definition)
	}
	if !catalogFilter.enabled && !clusterFilter.enabled {
		i.log.Warn("both Karta definition sources are disabled; no workload has a definition")
	}
	if !clusterFilter.enabled {
		i.synced = true
		close(i.loaded)
	}
	return i, nil
}

// Run blocks until ctx ends. Afterwards cluster lookups fail; a catalog-only
// inventory keeps serving. Run may be called only once.
func (i *Inventory) Run(ctx context.Context) error {
	i.mu.Lock()
	if i.started {
		i.mu.Unlock()
		return errors.New("definition inventory already started")
	}
	i.started = true
	i.mu.Unlock()
	defer func() {
		i.mu.Lock()
		defer i.mu.Unlock()
		i.stopped = true
		close(i.done)
	}()
	if !i.clusterFilter.enabled {
		<-ctx.Done()
		return nil
	}

	resource := i.client.Resource(v1alpha1.GroupVersion.WithResource("kartas"))
	reflector := cache.NewReflectorWithOptions(
		cache.ToListWatcherWithWatchListSemantics(i.listWatch(resource), i.client),
		&unstructured.Unstructured{}, reflectorStore{inventory: i},
		cache.ReflectorOptions{Name: "karta-definitions", TypeDescription: "Karta definitions"})
	reflector.RunWithContext(ctx)
	return nil
}

// WaitLoaded waits for the first complete cluster list to be applied. It does
// not start Run or report later source failures; Lookup checks current health.
func (i *Inventory) WaitLoaded(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-i.loaded:
		return nil
	case <-i.done:
		i.mu.RLock()
		defer i.mu.RUnlock()
		if i.synced {
			return nil
		}
		return fmt.Errorf("%w: %w", ErrUnavailable, errStopped)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// listWatch records every failure; the reflector retries some without reporting them.
func (i *Inventory) listWatch(resource dynamic.ResourceInterface) *cache.ListWatch {
	return &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			// Later pages continue the same list.
			if options.Continue == "" {
				i.mu.Lock()
				i.listedFrom = i.failures
				i.mu.Unlock()
			}
			list, err := resource.List(ctx, options)
			if err != nil {
				i.mu.Lock()
				i.fail(err)
				i.mu.Unlock()
			}
			return list, err
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			started := time.Now()
			streaming := options.SendInitialEvents != nil && *options.SendInitialEvents
			i.mu.Lock()
			failed := i.failure != nil
			if streaming {
				i.listedFrom = i.failures
			}
			i.mu.Unlock()
			// A resumed watch never marks the end of its replay; only a new list is fresh.
			if failed && !streaming {
				return nil, errRelist
			}
			w, err := resource.Watch(ctx, options)
			switch {
			// A server without streaming lists answers invalid; LIST follows.
			case err != nil && streaming && apierrors.IsInvalid(err):
				return nil, err
			case err != nil:
				i.mu.Lock()
				i.fail(err)
				i.mu.Unlock()
				return nil, err
			}
			events := make(chan watch.Event)
			proxy := watch.NewProxyWatcher(events)
			go func() {
				defer close(events)
				defer w.Stop()
				received := false
				for {
					select {
					case event, ok := <-w.ResultChan():
						if !ok {
							if !received && time.Since(started) < time.Second {
								i.failWatch(ctx, proxy, errShortWatch)
							}
							return
						}
						received = true
						// Recorded before the reflector sees it. A stopped watch is abandoned.
						if event.Type == watch.Error {
							i.failWatch(ctx, proxy, apierrors.FromObject(event.Object))
						}
						select {
						case events <- event:
						case <-proxy.StopChan():
							return
						case <-ctx.Done():
							return
						}
					case <-proxy.StopChan():
						return
					case <-ctx.Done():
						return
					}
				}
			}()
			return proxy, nil
		},
	}
}

// failWatch records err unless the watch was stopped on purpose.
func (i *Inventory) failWatch(ctx context.Context, proxy *watch.ProxyWatcher, err error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !proxy.Stopping() && ctx.Err() == nil {
		i.fail(err)
	}
}

// fail records err; callers hold i.mu.
func (i *Inventory) fail(err error) {
	i.failure = err
	i.failures++
}

func (s reflectorStore) Add(obj any) error {
	name, entry, err := decodeKarta(obj)
	if err != nil {
		return err
	}
	i := s.inventory
	i.mu.Lock()
	defer i.mu.Unlock()
	i.cluster[name] = entry
	i.report()
	return nil
}

func (s reflectorStore) Update(obj any) error { return s.Add(obj) }

func (s reflectorStore) Delete(obj any) error {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("unexpected Karta object %T", obj)
	}
	i := s.inventory
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.cluster, u.GetName())
	i.report()
	return nil
}

func (s reflectorStore) Replace(list []any, _ string) error {
	cluster := make(map[string]definitionEntry, len(list))
	for _, obj := range list {
		name, entry, err := decodeKarta(obj)
		if err != nil {
			return err
		}
		cluster[name] = entry
	}
	i := s.inventory
	i.mu.Lock()
	defer i.mu.Unlock()
	i.cluster = cluster
	// A failure after this list began is newer than the list.
	if i.failures == i.listedFrom {
		i.failure = nil
	}
	i.report()
	if !i.synced {
		i.synced = true
		close(i.loaded)
		i.log.Info("Karta definition inventory synced", "listed", len(list), "error", i.ready())
	}
	return nil
}

func (s reflectorStore) Resync() error { return nil }

func decodeKarta(obj any) (string, definitionEntry, error) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return "", definitionEntry{}, fmt.Errorf("unexpected Karta object %T", obj)
	}
	var k v1alpha1.Karta
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.DeepCopy().Object, &k); err != nil {
		return u.GetName(), definitionEntry{err: fmt.Errorf("%w: decode Karta %q: %w", ErrUnavailable, u.GetName(), err)}, nil
	}
	return u.GetName(), validatedEntry(&k), nil
}

// report logs diagnostics only when they change; callers hold i.mu.
func (i *Inventory) report() {
	var unkeyable, unmatched []string
	for name, entry := range i.cluster {
		if k := entry.definition; k != nil && i.clusterFilter.matches(k) {
			gvk := catalog.RootKey(k)
			if gvk.Version == "" || gvk.Kind == "" {
				unkeyable = append(unkeyable, name)
			}
		}
	}
	for name := range i.clusterFilter.include {
		if _, ok := i.cluster[name]; !ok {
			unmatched = append(unmatched, name)
		}
	}
	slices.Sort(unkeyable)
	slices.Sort(unmatched)
	if !slices.Equal(unkeyable, i.unkeyable) {
		i.unkeyable = unkeyable
		if len(unkeyable) > 0 {
			i.log.Warn("ignoring cluster Karta definitions whose root has no version or kind", "names", unkeyable)
		}
	}
	if !slices.Equal(unmatched, i.unmatchedIncludes) {
		i.unmatchedIncludes = unmatched
		if len(unmatched) > 0 {
			i.log.Warn("cluster include names match no Karta definition", "names", unmatched)
		}
	}
}

// ready checks source state; callers hold i.mu.
func (i *Inventory) ready() error {
	if !i.clusterFilter.enabled {
		return nil
	}
	switch {
	case i.stopped:
		return fmt.Errorf("%w: %w", ErrUnavailable, errStopped)
	case i.failure != nil && (apierrors.IsForbidden(i.failure) || apierrors.IsUnauthorized(i.failure)):
		return fmt.Errorf("%w: %w", ErrPermissionDenied, i.failure)
	case i.failure != nil:
		return fmt.Errorf("%w: %w", ErrUnavailable, i.failure)
	case !i.synced:
		return ErrNotReady
	}
	for _, entry := range i.cluster {
		if entry.definition == nil {
			return entry.err
		}
	}
	return nil
}

// Lookup ignores the requested version and never falls back while a source fails.
// A nil Definition with no error means neither source has a matching definition.
func (i *Inventory) Lookup(gvk schema.GroupVersionKind) (Selection, error) {
	i.mu.RLock()
	if err := i.ready(); err != nil {
		i.mu.RUnlock()
		return Selection{}, err
	}
	var winner definitionEntry
	for _, entry := range i.cluster {
		k := entry.definition
		key := catalog.RootKey(k)
		if key.GroupKind() != gvk.GroupKind() || key.Version == "" || key.Kind == "" || !i.clusterFilter.matches(k) {
			continue
		}
		if winner.definition == nil || cmp.Or(
			k.CreationTimestamp.Compare(winner.definition.CreationTimestamp.Time),
			strings.Compare(k.Name, winner.definition.Name)) < 0 {
			winner = entry
		}
	}
	source := SourceCluster
	if winner.definition == nil {
		winner, source = i.catalog[gvk.GroupKind()], SourceCatalog
	}
	i.mu.RUnlock()
	if winner.err != nil {
		return Selection{}, winner.err
	}
	k := winner.definition
	if k == nil {
		return Selection{}, nil
	}
	origin := Origin{Source: source, Name: k.Name}
	if source == SourceCluster {
		origin.UID, origin.ResourceVersion = k.UID, k.ResourceVersion
	}
	return Selection{Definition: k.DeepCopy(), GVK: catalog.RootKey(k), Origin: origin}, nil
}

func validatedEntry(k *v1alpha1.Karta) definitionEntry {
	copy := k.DeepCopy()
	// The SDK requires a root status definition; the exporter does not.
	if copy.Spec.StructureDefinition.RootComponent.StatusDefinition == nil {
		copy.Spec.StructureDefinition.RootComponent.StatusDefinition = &v1alpha1.StatusDefinition{}
	}
	err := v1alpha1.NewKartaValidator(copy).Validate()
	if err != nil {
		err = fmt.Errorf("%w %q: %w", ErrInvalidDefinition, k.Name, err)
	}
	return definitionEntry{definition: k, err: err}
}

func (f sourceFilter) matches(k *v1alpha1.Karta) bool {
	return f.enabled && (f.include.Len() == 0 || f.include.Has(k.Name)) && !f.exclude.Has(k.Name)
}

// compileSource skips the name check when known is nil.
func compileSource(source Source, enabled bool, include, exclude []string, known sets.Set[string]) (sourceFilter, []error) {
	var errs []error
	compileNames := func(field string, names []string) sets.Set[string] {
		set := sets.New[string]()
		for _, name := range names {
			var problem string
			switch {
			case name == "":
				problem = "empty definition name"
			case set.Has(name):
				problem = fmt.Sprintf("duplicate definition name %q", name)
			case known != nil && !known.Has(name):
				problem = fmt.Sprintf("unknown definition %q; known: %s", name, strings.Join(sets.List(known), ", "))
			}
			if problem != "" {
				errs = append(errs, fmt.Errorf("%w: %s.%s: %s", ErrInvalidConfig, source, field, problem))
			}
			set.Insert(name)
		}
		return set
	}

	f := sourceFilter{enabled: enabled, include: compileNames("include", include), exclude: compileNames("exclude", exclude)}
	return f, errs
}
