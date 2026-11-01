// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	kruntime "k8s.io/apimachinery/pkg/runtime"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/discovery"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

func (r *Runtime) handlePodInput(ctx context.Context, key objectJobKey) error {
	if !key.Rescan {
		return r.reconcilePod(ctx, key)
	}
	return r.state.requeueScope(key.Object.Namespace)
}

func (r *Runtime) reconcilePod(ctx context.Context, key objectJobKey) error {
	ref := key.ref()
	if !r.state.namespaceAdmitted(ref.Namespace) {
		r.state.unregisterPod(key)
		return nil
	}
	podObject, _, err := r.state.reader.read(ref, true)
	if apierrors.IsNotFound(err) {
		r.state.unregisterPod(key)
		return nil
	}
	if err != nil {
		return r.state.failPod(key, nil, err)
	}
	if !r.state.podSelector.Matches(labelsFor(podObject)) {
		r.state.unregisterPod(key)
		return nil
	}
	ref = refOf(podObject)
	discoveryResult, err := r.state.deps.Discovery.Get(ctx, ref)
	if err != nil {
		return r.state.failPod(key, []discovery.ObjectRef{ref}, err)
	}
	if discoveryResult.Workload == nil {
		return r.state.failPod(key, []discovery.ObjectRef{ref}, discovery.ErrUnresolvedOwner)
	}
	workload := discoveryResult.Workload
	selection, selectionErr := r.state.lookupDefinition(workload.Root.GVK)
	if workload.Root.GVK == podGVK && selectionErr != nil {
		return r.state.failPod(key, workload.Path, selectionErr)
	}
	if workload.Root.GVK == podGVK && selection.Definition == nil {
		r.state.unregisterPod(key)
		return nil
	}

	rootRef := workload.Root
	if selection.Definition != nil {
		rootRef.GVK = selection.GVK
	}
	allowed, scopeErr := r.state.isResourceSelected(ctx, rootRef.GVK)
	if scopeErr != nil {
		return r.state.failPod(key, workload.Path, scopeErr, rootRef)
	}
	if !allowed {
		r.state.unregisterPod(key)
		return nil
	}
	if err := r.state.registerPodRoot(key, workload); err != nil {
		if errors.Is(err, discovery.ErrStale) {
			return err
		}
		return r.state.failPod(key, workload.Path, err, workload.Root)
	}
	rootRef.ResourceVersion = ""
	rootObject, rootSource, err := r.state.reader.read(rootRef, true)
	watchFailed := r.state.watchFailed(rootRef)
	if err != nil || watchFailed {
		if _, err = r.state.ensureWatch(ctx, rootRef.GVK, rootRef.Namespace, false); err != nil {
			return r.state.failPod(key, workload.Path, err, workload.Root)
		}
		rootObject, rootSource, err = r.state.reader.read(rootRef, true)
	}
	if err != nil {
		return r.state.failPod(key, workload.Path, err, workload.Root)
	}
	selected, err := r.state.isRootSelected(ctx, rootObject)
	if err != nil {
		return r.state.failPod(key, workload.Path, err, workload.Root)
	}
	if !selected {
		r.state.unregisterPod(key)
		return nil
	}
	rootRef = refOf(rootObject)
	if selectionErr != nil {
		return r.state.failPodEvaluation(key, workload.Path, selectionErr, rootRef, selection)
	}
	record := store.PodRecord{
		UID:             key.UID,
		Name:            ref.Name,
		Namespace:       ref.Namespace,
		Component:       "<unknown>",
		Instance:        "<unknown>",
		Workload:        storedRef(rootRef),
		OwnerResolution: store.OwnerResolution(workload.Resolution),
	}
	if selection.Definition != nil {
		pod := &corev1.Pod{}
		if err := kruntime.DefaultUnstructuredConverter.FromUnstructured(podObject.Object, pod); err != nil {
			return r.state.failPodEvaluation(key, workload.Path, fmt.Errorf("%w: %w", discovery.ErrEvaluation, err), rootRef, selection)
		}
		record.Karta = selection.Origin.Name
		record.KartaSource = string(selection.Origin.Source)
		factory := resource.NewComponentFactoryFromObject(selection.Definition, rootObject)
		record.Component, record.Instance, record.Group, err = evaluatePodComponent(ctx, factory, selection.summary, pod)
		if err != nil {
			return r.state.failPodEvaluation(key, workload.Path, fmt.Errorf("%w: %w", discovery.ErrEvaluation, err), rootRef, selection)
		}
	}
	labelResults, err := r.state.deps.Labels.Resolve(ctx, labelresolver.Input{Metrics: []string{labelresolver.MetricPodWorkloadInfo}, Workload: rootObject, PodObject: podObject})
	if err != nil {
		return r.state.failPodEvaluation(key, workload.Path, fmt.Errorf("%w: %w", discovery.ErrEvaluation, err), rootRef, selection)
	}
	labelResult := labelResults[0]
	record.Labels = labelResult.Labels
	sources := r.state.sourcesFor(workload.Path)
	if !slices.Contains(sources, rootSource) {
		sources = append(sources, rootSource)
	}
	podResult := store.PodResult{Record: record, Input: storedRef(ref), HasValue: true, Sources: sources}
	return r.state.publishPod(workload.Path, rootRef, selection, podResult, len(labelResult.Errors) > 0)
}

func labelsFor(object *unstructured.Unstructured) labels.Set { return labels.Set(object.GetLabels()) }
