// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package store_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

func TestPublicationOwnsInputsAndCaptureRetainsValues(t *testing.T) {
	s := store.New()
	key := store.SourceKey{Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Namespace: "team"}
	root := store.ObjectRef{UID: "root", Name: "job", Namespace: "team", GVK: schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}}
	pod := store.PodResult{Record: store.PodRecord{UID: "pod", Workload: root, Labels: map[string]string{"team": "original"}}, Input: store.ObjectRef{UID: "pod"}, HasValue: true, Sources: []store.SourceKey{key}}
	workload := store.WorkloadResult{Record: store.WorkloadRecord{UID: root.UID, Ref: root, Phases: []string{"Running"}, StatusLabels: map[string]string{"team": "original"}}, HasValue: true, Sources: []store.SourceKey{key}, CreationSources: []store.SourceKey{key}}
	s.UpsertPod(&pod)
	s.UpsertWorkload(&workload)
	s.SetSourceHealth(key, store.SourceHealth{Synced: true})
	s.SetDiscoveryFailure(store.ObjectRef{UID: "input", Name: "original"}, store.ReasonUnresolvedOwner)
	old := s.Capture()
	require.Len(t, old.Pods, 1)
	require.Len(t, old.Workloads, 1)
	require.Len(t, old.Failures, 1)
	assert.Same(t, s.Pod("pod"), old.Pods[0])
	assert.Same(t, s.Workload("root"), old.Workloads[0])

	pod.Record.Labels["team"] = "changed"
	pod.Record.Workload.Name = "changed"
	pod.Input.Name = "changed"
	pod.Sources[0].Namespace = "changed"
	workload.Record.Phases[0] = "Failed"
	workload.Record.StatusLabels["team"] = "changed"
	workload.Sources[0].Namespace = "changed"
	workload.CreationSources[0].Namespace = "changed"
	assert.Equal(t, "original", s.Pod("pod").Record.Labels["team"])
	assert.Equal(t, root, s.Pod("pod").Record.Workload)
	assert.Empty(t, s.Pod("pod").Input.Name)
	assert.Equal(t, []store.SourceKey{key}, s.Pod("pod").Sources)
	assert.Equal(t, []string{"Running"}, s.Workload("root").Record.Phases)
	assert.Equal(t, "original", s.Workload("root").Record.StatusLabels["team"])
	assert.Equal(t, []store.SourceKey{key}, s.Workload("root").Sources)
	assert.Equal(t, []store.SourceKey{key}, s.Workload("root").CreationSources)

	s.UpsertPod(&pod)
	s.UpsertWorkload(&workload)
	s.SetSourceHealth(key, store.SourceHealth{Error: store.ReasonPermissionDenied})
	s.SetDiscoveryFailure(store.ObjectRef{UID: "input", Name: "changed"}, store.ReasonAPIUnavailable)
	assert.NotSame(t, old.Pods[0], s.Pod("pod"))
	assert.NotSame(t, old.Workloads[0], s.Workload("root"))
	assert.Equal(t, "original", old.Pods[0].Record.Labels["team"])
	assert.Equal(t, []string{"Running"}, old.Workloads[0].Record.Phases)
	assert.Equal(t, "original", old.Failures[0].Input.Name)
	valid, reason := old.Valid([]store.SourceKey{key}, "")
	assert.True(t, valid)
	assert.Equal(t, store.ReasonNone, reason)

	s.RemovePod("pod")
	s.RemoveWorkload("root")
	s.RemoveSourceHealth(key)
	s.RemoveDiscoveryFailure("input")
	assert.Len(t, old.Pods, 1)
	assert.Len(t, old.Workloads, 1)
	assert.Len(t, old.Sources, 1)
	assert.Len(t, old.Failures, 1)
	current := s.Capture()
	assert.Empty(t, current.Pods)
	assert.Empty(t, current.Workloads)
	assert.Empty(t, current.Sources)
	assert.Empty(t, current.Failures)
}

func TestReplacementAndRemovalRespectUIDAndDomain(t *testing.T) {
	s := store.New()
	first := store.ObjectRef{UID: "first", Name: "same", Namespace: "team", GVK: schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}}
	second := store.ObjectRef{UID: "second", Name: "same", Namespace: "other", GVK: schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}}
	for _, ref := range []store.ObjectRef{first, second} {
		s.UpsertPod(&store.PodResult{Record: store.PodRecord{UID: ref.UID, Name: "same", Workload: first, Labels: map[string]string{"stale": "value"}}, HasValue: true})
		s.UpsertWorkload(&store.WorkloadResult{Record: store.WorkloadRecord{UID: ref.UID, Ref: ref, Phases: []string{"Running"}, StatusLabels: map[string]string{"stale": "value"}}, HasValue: true})
	}
	before := s.Capture()
	s.UpsertPod(&store.PodResult{Record: store.PodRecord{UID: first.UID, Name: "same", Workload: second}, HasValue: true})
	s.UpsertWorkload(&store.WorkloadResult{Record: store.WorkloadRecord{UID: first.UID, Ref: first}, StatusError: store.ReasonEvaluationFailed})
	assert.Nil(t, s.Pod(first.UID).Record.Labels)
	assert.Nil(t, s.Workload(first.UID).Record.Phases)
	assert.Nil(t, s.Workload(first.UID).Record.StatusLabels)
	assert.False(t, s.Workload(first.UID).HasValue)
	assert.Equal(t, second, s.Pod(first.UID).Record.Workload)
	assert.Len(t, s.Capture().Pods, 2)
	for _, p := range before.Pods {
		assert.Equal(t, first, p.Record.Workload)
	}

	s.SetDiscoveryFailure(first, store.ReasonUnresolvedOwner)
	s.SetDiscoveryFailure(second, store.ReasonAPIUnavailable)
	s.SetDiscoveryFailure(store.ObjectRef{UID: first.UID, Name: "replacement"}, store.ReasonPermissionDenied)
	assert.ElementsMatch(t, []store.DiscoveryFailure{
		{Input: store.ObjectRef{UID: first.UID, Name: "replacement"}, Error: store.ReasonPermissionDenied},
		{Input: second, Error: store.ReasonAPIUnavailable},
	}, s.Capture().Failures)
	assert.Equal(t, store.ReasonEvaluationFailed, s.Workload(first.UID).StatusError)
	s.RemoveWorkload(first.UID)
	assert.NotNil(t, s.Pod(first.UID))
	assert.NotNil(t, s.Pod(second.UID))
	assert.NotNil(t, s.Workload(second.UID))
	assert.Len(t, s.Capture().Failures, 2)
	s.RemovePod(second.UID)
	assert.NotNil(t, s.Workload(second.UID))
	s.RemovePod("unknown")
	s.RemoveWorkload("unknown")
	s.RemoveDiscoveryFailure("unknown")
	s.RemoveSourceHealth(store.SourceKey{})
	assert.Len(t, s.Capture().Pods, 1)
	assert.Len(t, s.Capture().Workloads, 1)
	assert.Len(t, before.Pods, 2)
	assert.Len(t, before.Workloads, 2)
	assert.Nil(t, s.Pod("unknown"))
	assert.Nil(t, s.Workload("unknown"))
}

func TestValidityPrecedence(t *testing.T) {
	key := store.SourceKey{Resource: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, Namespace: "team"}
	denied := store.SourceKey{Namespace: "denied"}
	missing := store.SourceKey{Namespace: "missing"}
	tests := []struct {
		name    string
		health  store.SourceHealth
		sources []store.SourceKey
		saved   string
		want    string
	}{
		{name: "empty dependencies", want: store.ReasonNone},
		{name: "unsynced", sources: []store.SourceKey{key}, want: store.ReasonAPIUnavailable},
		{name: "healthy", health: store.SourceHealth{Synced: true}, sources: []store.SourceKey{key}, want: store.ReasonNone},
		{name: "explicit none", health: store.SourceHealth{Synced: true, Error: store.ReasonNone}, sources: []store.SourceKey{key}, saved: store.ReasonNone, want: store.ReasonNone},
		{name: "source error", health: store.SourceHealth{Synced: true, Error: "transport failure"}, sources: []store.SourceKey{key}, want: store.ReasonAPIUnavailable},
		{name: "denied after missing", sources: []store.SourceKey{missing, denied}, want: store.ReasonPermissionDenied},
		{name: "denied before missing", sources: []store.SourceKey{denied, missing}, want: store.ReasonPermissionDenied},
		{name: "saved error wins", sources: []store.SourceKey{denied, missing}, saved: store.ReasonEvaluationFailed, want: store.ReasonEvaluationFailed},
		{name: "saved error without sources", saved: store.ReasonInvalidDefinition, want: store.ReasonInvalidDefinition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := store.CapturedView{Sources: map[store.SourceKey]store.SourceHealth{key: tt.health, denied: {Error: store.ReasonPermissionDenied}}}
			valid, reason := view.Valid(tt.sources, tt.saved)
			assert.Equal(t, tt.want == store.ReasonNone, valid)
			assert.Equal(t, tt.want, reason)
		})
	}
	var zero store.CapturedView
	valid, reason := zero.Valid([]store.SourceKey{key}, "")
	assert.False(t, valid)
	assert.Equal(t, store.ReasonAPIUnavailable, reason)
}

func TestValidityMatchesCompleteCollectionKey(t *testing.T) {
	key := store.SourceKey{Resource: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, Namespace: "team"}
	s := store.New()
	s.SetSourceHealth(key, store.SourceHealth{Synced: true})
	for _, different := range []store.SourceKey{
		{Resource: key.Resource},
		{Resource: key.Resource, Namespace: "other"},
		{Resource: schema.GroupVersionResource{Group: "other", Version: "v1", Resource: "jobs"}, Namespace: key.Namespace},
		{Resource: schema.GroupVersionResource{Group: "batch", Version: "v2", Resource: "jobs"}, Namespace: key.Namespace},
		{Resource: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}, Namespace: key.Namespace},
	} {
		valid, reason := s.Capture().Valid([]store.SourceKey{different}, "")
		assert.False(t, valid, "%+v", different)
		assert.Equal(t, store.ReasonAPIUnavailable, reason)
	}
	allNamespaces := store.SourceKey{Resource: key.Resource}
	s.RemoveSourceHealth(key)
	s.SetSourceHealth(allNamespaces, store.SourceHealth{Synced: true})
	valid, _ := s.Capture().Valid([]store.SourceKey{allNamespaces}, "")
	assert.True(t, valid)
	valid, _ = s.Capture().Valid([]store.SourceKey{key}, "")
	assert.False(t, valid)
}

func TestFailuresAndRecoveryKeepIndependentCompletedValues(t *testing.T) {
	s := store.New()
	root := store.SourceKey{Namespace: "root"}
	definition := store.SourceKey{Namespace: "definition"}
	s.SetSourceHealth(root, store.SourceHealth{Synced: true})
	s.SetSourceHealth(definition, store.SourceHealth{Synced: true})
	s.UpsertPod(&store.PodResult{Record: store.PodRecord{UID: "pod"}, Error: store.ReasonEvaluationFailed})
	assert.False(t, s.Pod("pod").HasValue)
	assert.Empty(t, s.Pod("pod").Record.Component)
	s.UpsertPod(&store.PodResult{Record: store.PodRecord{UID: "pod", Component: "worker"}, HasValue: true, Error: store.ReasonEvaluationFailed})
	assert.True(t, s.Pod("pod").HasValue)
	assert.Equal(t, "worker", s.Pod("pod").Record.Component)
	s.UpsertWorkload(&store.WorkloadResult{Record: store.WorkloadRecord{UID: "workload"}, Error: store.ReasonEvaluationFailed})
	assert.False(t, s.Workload("workload").HasValue)
	assert.Empty(t, s.Workload("workload").Record.Phases)
	assert.Zero(t, s.Workload("workload").Record.CreatedTimestamp)
	w := store.WorkloadResult{Record: store.WorkloadRecord{UID: "workload", CreatedTimestamp: 123, Phases: []string{"Running"}}, HasValue: true, StatusError: store.ReasonEvaluationFailed, Sources: []store.SourceKey{root, definition}, CreationSources: []store.SourceKey{root}}
	s.UpsertWorkload(&w)
	view := s.Capture()
	valid, reason := view.Valid(w.Sources, w.StatusError)
	assert.False(t, valid)
	assert.Equal(t, store.ReasonEvaluationFailed, reason)
	valid, _ = view.Valid(w.CreationSources, w.Error)
	assert.True(t, valid)
	assert.EqualValues(t, 123, s.Workload("workload").Record.CreatedTimestamp)

	w.StatusError = ""
	s.UpsertWorkload(&w)
	s.SetSourceHealth(definition, store.SourceHealth{Error: store.ReasonPermissionDenied})
	failed := s.Capture()
	valid, _ = failed.Valid(w.Sources, w.StatusError)
	assert.False(t, valid)
	valid, _ = failed.Valid(w.CreationSources, w.Error)
	assert.True(t, valid)
	s.SetSourceHealth(definition, store.SourceHealth{Synced: true})
	valid, _ = s.Capture().Valid(w.Sources, "")
	assert.True(t, valid)
	valid, _ = failed.Valid(w.Sources, "")
	assert.False(t, valid)
	valid, reason = s.Capture().Valid(s.Pod("pod").Sources, s.Pod("pod").Error)
	assert.False(t, valid)
	assert.Equal(t, store.ReasonEvaluationFailed, reason)

	w.Error = store.ReasonUnresolvedOwner
	s.UpsertWorkload(&w)
	valid, reason = s.Capture().Valid(w.CreationSources, w.Error)
	assert.False(t, valid)
	assert.Equal(t, store.ReasonUnresolvedOwner, reason)
	assert.True(t, s.Workload("workload").HasValue)
	assert.Equal(t, []string{"Running"}, s.Workload("workload").Record.Phases)
	assert.EqualValues(t, 123, s.Workload("workload").Record.CreatedTimestamp)
}

func TestCaptureContainersAreIndependent(t *testing.T) {
	s := store.New()
	key := store.SourceKey{}
	s.SetSourceHealth(key, store.SourceHealth{Synced: true})
	s.UpsertPod(&store.PodResult{Record: store.PodRecord{UID: "pod"}})
	s.UpsertWorkload(&store.WorkloadResult{Record: store.WorkloadRecord{UID: "root"}})
	s.SetDiscoveryFailure(store.ObjectRef{UID: "input"}, store.ReasonUnresolvedOwner)
	view := s.Capture()
	view.Sources[key] = store.SourceHealth{Error: store.ReasonPermissionDenied}
	view.Pods[0] = nil
	view.Workloads[0] = nil
	view.Failures[0].Error = "changed"
	current := s.Capture()
	assert.True(t, current.Sources[key].Synced)
	assert.NotNil(t, current.Pods[0])
	assert.NotNil(t, current.Workloads[0])
	assert.Equal(t, store.ReasonUnresolvedOwner, current.Failures[0].Error)
}

func TestConcurrentPublicationRemovalLookupAndCapture(t *testing.T) {
	s := store.New()
	var wg sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for revision := 0; revision < 150; revision++ {
				uid := types.UID(fmt.Sprint(revision % 4))
				value := fmt.Sprintf("%d/%d", worker, revision)
				key := store.SourceKey{Namespace: string(uid)}
				pod := store.PodResult{Record: store.PodRecord{UID: uid, Name: value, Labels: map[string]string{"revision": value}}, Sources: []store.SourceKey{key}}
				workload := store.WorkloadResult{Record: store.WorkloadRecord{UID: uid, Ref: store.ObjectRef{Name: value}, Phases: []string{value}, StatusLabels: map[string]string{"revision": value}}, Sources: []store.SourceKey{key}}
				s.UpsertPod(&pod)
				s.UpsertWorkload(&workload)
				pod.Record.Labels["revision"] = "reused"
				workload.Record.Phases[0] = "reused"
				s.SetSourceHealth(key, store.SourceHealth{Synced: true})
				s.SetDiscoveryFailure(store.ObjectRef{UID: uid}, store.ReasonUnresolvedOwner)
				if p := s.Pod(uid); p != nil {
					assert.Equal(t, p.Record.Name, p.Record.Labels["revision"])
				}
				if w := s.Workload(uid); w != nil {
					assert.Equal(t, []string{w.Record.Ref.Name}, w.Record.Phases)
					assert.Equal(t, w.Record.Ref.Name, w.Record.StatusLabels["revision"])
				}
				view := s.Capture()
				s.RemovePod(uid)
				s.RemoveWorkload(uid)
				s.RemoveSourceHealth(key)
				s.RemoveDiscoveryFailure(uid)
				for _, p := range view.Pods {
					assert.Equal(t, p.Record.Name, p.Record.Labels["revision"])
					view.Valid(p.Sources, p.Error)
				}
				for _, w := range view.Workloads {
					assert.Equal(t, []string{w.Record.Ref.Name}, w.Record.Phases)
					assert.Equal(t, w.Record.Ref.Name, w.Record.StatusLabels["revision"])
				}
			}
		}()
	}
	wg.Wait()
	for id := 0; id < 4; id++ {
		uid := types.UID(fmt.Sprint(id))
		s.RemovePod(uid)
		s.RemoveWorkload(uid)
		s.RemoveSourceHealth(store.SourceKey{Namespace: string(uid)})
		s.RemoveDiscoveryFailure(uid)
	}
	view := s.Capture()
	assert.Empty(t, view.Pods)
	assert.Empty(t, view.Workloads)
	assert.Empty(t, view.Sources)
	assert.Empty(t, view.Failures)
}
