// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package store

import (
	"maps"
	"slices"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// Writer publishes independent results. Methods are atomic and safe for concurrent use.
// Inputs must remain stable during upsert; returned results must not be mutated.
type Writer interface {
	UpsertPod(*PodResult)
	Pod(types.UID) *PodResult
	RemovePod(types.UID)
	UpsertWorkload(*WorkloadResult)
	Workload(types.UID) *WorkloadResult
	RemoveWorkload(types.UID)
	SetSourceHealth(SourceKey, SourceHealth)
	RemoveSourceHealth(SourceKey)
	SetDiscoveryFailure(ObjectRef, string)
	RemoveDiscoveryFailure(types.UID)
}

// Capturer returns immutable results and a copy of source health under one lock.
type Capturer interface {
	Capture() CapturedView
}

// PublicationStore combines publication updates with snapshot collection.
type PublicationStore interface {
	Writer
	Capturer
}

type Store struct {
	mu        sync.RWMutex
	pods      map[types.UID]*PodResult
	workloads map[types.UID]*WorkloadResult
	sources   map[SourceKey]SourceHealth
	failures  map[types.UID]DiscoveryFailure
}

type PodResult struct {
	Record   PodRecord
	Input    ObjectRef
	HasValue bool
	Error    string
	Sources  []SourceKey
}

type WorkloadResult struct {
	Record          WorkloadRecord
	HasValue        bool
	Error           string
	StatusError     string
	Sources         []SourceKey
	CreationSources []SourceKey
}

type WorkloadRecord struct {
	UID              types.UID
	Ref              ObjectRef
	Karta            string
	KartaSource      string
	OwnerResolution  OwnerResolution
	HasStatus        bool
	Phases           []string
	CreatedTimestamp int64
	StatusLabels     map[string]string
}

type ObjectRef struct {
	GVK             schema.GroupVersionKind
	Namespace       string
	Name            string
	UID             types.UID
	ResourceVersion string
}

type OwnerResolution string

const (
	Resolved     OwnerResolution = "resolved"
	IgnoredOwner OwnerResolution = "ignored_owner"
	UnknownOwner OwnerResolution = "unknown"
)

type PodRecord struct {
	Workload        ObjectRef
	Karta           string
	KartaSource     string
	Namespace       string
	UID             types.UID
	Name            string
	Component       string
	Instance        string
	Group           string
	OwnerResolution OwnerResolution
	Labels          map[string]string
}

// SourceKey identifies the actual collection, including an all-namespace scope.
type SourceKey struct {
	Resource  schema.GroupVersionResource
	Namespace string
}

type SourceHealth struct {
	GVK    schema.GroupVersionKind
	Synced bool
	Error  string
}

type DiscoveryFailure struct {
	Input ObjectRef
	Error string
}

// CapturedView shares read-only results and owns its source map and pointer slices.
// Later Store writes cannot change this view. Iteration order is unspecified.
type CapturedView struct {
	Pods      []*PodResult
	Workloads []*WorkloadResult
	Sources   map[SourceKey]SourceHealth
	Failures  []DiscoveryFailure
}

const (
	ReasonNone              = "none"
	ReasonPermissionDenied  = "permission_denied"
	ReasonAPIUnavailable    = "api_unavailable"
	ReasonInvalidDefinition = "invalid_definition"
	ReasonUnresolvedOwner   = "unresolved_owner"
	ReasonEvaluationFailed  = "evaluation_failed"
)

func New() PublicationStore {
	return &Store{
		pods:      make(map[types.UID]*PodResult),
		workloads: make(map[types.UID]*WorkloadResult),
		sources:   make(map[SourceKey]SourceHealth),
		failures:  make(map[types.UID]DiscoveryFailure),
	}
}

// UpsertPod copies the result and its nested data before replacing one Pod UID.
func (s *Store) UpsertPod(result *PodResult) {
	next := *result
	next.Record.Labels = maps.Clone(result.Record.Labels)
	next.Sources = slices.Clone(result.Sources)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pods[next.Record.UID] = &next
}

// Pod returns a read-only result, or nil. Clone nested data before modifying it.
func (s *Store) Pod(uid types.UID) *PodResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pods[uid]
}

func (s *Store) RemovePod(uid types.UID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pods, uid)
}

// UpsertWorkload copies the result and its nested data before replacing one root UID.
func (s *Store) UpsertWorkload(result *WorkloadResult) {
	next := *result
	next.Record.StatusLabels = maps.Clone(result.Record.StatusLabels)
	next.Record.Phases = slices.Clone(result.Record.Phases)
	next.Sources = slices.Clone(result.Sources)
	next.CreationSources = slices.Clone(result.CreationSources)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workloads[next.Record.UID] = &next
}

// Workload returns a read-only result, or nil. Clone nested data before modifying it.
func (s *Store) Workload(uid types.UID) *WorkloadResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workloads[uid]
}

func (s *Store) RemoveWorkload(uid types.UID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.workloads, uid)
}

func (s *Store) SetSourceHealth(key SourceKey, health SourceHealth) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sources[key] = health
}

func (s *Store) RemoveSourceHealth(key SourceKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sources, key)
}

func (s *Store) SetDiscoveryFailure(input ObjectRef, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[input.UID] = DiscoveryFailure{Input: input, Error: reason}
}

func (s *Store) RemoveDiscoveryFailure(uid types.UID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failures, uid)
}

func (s *Store) Capture() CapturedView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	view := CapturedView{
		Pods:      make([]*PodResult, 0, len(s.pods)),
		Workloads: make([]*WorkloadResult, 0, len(s.workloads)),
		Sources:   maps.Clone(s.sources),
		Failures:  make([]DiscoveryFailure, 0, len(s.failures)),
	}
	for _, result := range s.pods {
		view.Pods = append(view.Pods, result)
	}
	for _, result := range s.workloads {
		view.Workloads = append(view.Workloads, result)
	}
	for _, failure := range s.failures {
		view.Failures = append(view.Failures, failure)
	}
	return view
}

// Valid checks the saved operation outcome and every collection it depended on.
func (v CapturedView) Valid(sources []SourceKey, reason string) (bool, string) {
	if reason != "" && reason != ReasonNone {
		return false, reason
	}
	reason = ReasonNone
	for _, key := range sources {
		health, exists := v.Sources[key]
		switch {
		case health.Error == ReasonPermissionDenied:
			return false, ReasonPermissionDenied
		case !exists || !health.Synced || (health.Error != "" && health.Error != ReasonNone):
			reason = ReasonAPIUnavailable
		}
	}
	return reason == ReasonNone, reason
}
