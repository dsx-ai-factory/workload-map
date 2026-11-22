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

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/discovery"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/instructions"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

var ErrMissingComponentInstance = errors.New("required component instance is missing")

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

	rootRef := workload.Root
	if selection.Definition != nil {
		rootRef.GVK = selection.GVK
	}
	isResourceSelected, scopeErr := r.state.isResourceSelected(ctx, rootRef.GVK)
	if scopeErr != nil {
		return r.state.failPod(key, workload.Path, scopeErr, rootRef)
	}
	if !isResourceSelected {
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
	hasWatchFailure := r.state.watchFailed(rootRef)
	shouldRefreshRootWatch := err != nil || hasWatchFailure
	if shouldRefreshRootWatch {
		if _, err = r.state.ensureWatch(ctx, rootRef.GVK, rootRef.Namespace, false); err != nil {
			return r.state.failPod(key, workload.Path, err, workload.Root)
		}
		rootObject, rootSource, err = r.state.reader.read(rootRef, true)
	}
	if err != nil {
		return r.state.failPod(key, workload.Path, err, workload.Root)
	}
	isRootSelected, err := r.state.isRootSelected(ctx, rootObject)
	if err != nil {
		return r.state.failPod(key, workload.Path, err, workload.Root)
	}
	if !isRootSelected {
		r.state.unregisterPod(key)
		return nil
	}
	rootRef = refOf(rootObject)
	metricWorkload, association, associationSource, associationErr := r.resolvePodAssociation(ctx, podObject, rootObject, workload)
	associationChanged := false
	if previous := r.state.deps.Publications.Pod(key.UID); previous != nil {
		associationChanged = !r.state.associationTarget.Empty() && previous.Record.Workload.GVK == r.state.associationTarget && previous.Record.Workload.UID != metricWorkload.GetUID()
	}
	if selectionErr != nil && !association.eligible && !associationChanged {
		return r.state.failPodEvaluation(key, workload.Path, selectionErr, rootRef, selection)
	}
	record := store.PodRecord{
		UID:             key.UID,
		Name:            ref.Name,
		Namespace:       ref.Namespace,
		Component:       "<unknown>",
		Instance:        "<unknown>",
		Workload:        storedRef(refOf(metricWorkload)),
		OwnerResolution: store.OwnerResolution(workload.Resolution),
	}
	hasLabelErrors := false
	err = selectionErr
	if err == nil {
		hasLabelErrors, err = r.evaluatePodRecord(ctx, selection, podObject, rootObject, metricWorkload, &record)
	}
	if err != nil {
		if !association.eligible && !associationChanged {
			return r.state.failPodEvaluation(key, workload.Path, fmt.Errorf("%w: %w", discovery.ErrEvaluation, err), rootRef, selection)
		}
		hasLabelErrors = true
	}
	sources := r.state.sourcesFor(workload.Path)
	if !slices.Contains(sources, rootSource) {
		sources = append(sources, rootSource)
	}
	if associationSource.Resource.Resource != "" && !slices.Contains(sources, associationSource) {
		sources = append(sources, associationSource)
	}
	podResult := store.PodResult{Record: record, Input: storedRef(ref), HasValue: true, Sources: sources, Error: failureReason(errors.Join(associationErr, selectionErr))}
	return r.state.publishPod(workload.Path, rootRef, selection, podResult, hasLabelErrors, association)
}

func (r *Runtime) evaluatePodRecord(ctx context.Context, selection selectedDefinition, podObject, rootObject, metricWorkload *unstructured.Unstructured, record *store.PodRecord) (hasLabelErrors bool, err error) {
	if selection.Definition != nil {
		pod := &corev1.Pod{}
		if err := kruntime.DefaultUnstructuredConverter.FromUnstructured(podObject.Object, pod); err != nil {
			return false, err
		}
		record.Karta = selection.Origin.Name
		record.KartaSource = string(selection.Origin.Source)
		factory := resource.NewComponentFactoryFromObject(selection.Definition, rootObject)
		record.Component, record.Instance, record.Group, err = evaluatePodComponent(ctx, factory, selection.summary, pod)
		if err != nil {
			return false, err
		}
	}
	labelResults, err := r.state.deps.Labels.Resolve(ctx, labelresolver.Input{Metrics: []string{labelresolver.MetricPodWorkloadInfo}, Workload: metricWorkload, Pod: podObject})
	if err != nil {
		return false, err
	}
	labelResult := labelResults[0]
	record.Labels = labelResult.Labels
	return len(labelResult.Errors) > 0, nil
}

func evaluatePodComponent(ctx context.Context, factory *resource.ComponentFactory, summary *instructions.StructureSummary, pod *corev1.Pod) (string, string, string, error) {
	query := resource.NewPodQuerier(pod)
	componentName, err := instructions.InferPodComponent(ctx, query, summary)
	if err != nil {
		return "", "", "", err
	}
	component, err := factory.GetComponent(componentName)
	if err != nil {
		return "", "", "", err
	}
	instanceID, err := instructions.InferPodComponentInstance(ctx, query, componentName, factory)
	if err != nil {
		return "", "", "", err
	}
	instance := ""
	if instanceID != nil {
		instance = *instanceID
	}
	isRequiredInstanceMissing := component.Definition().InstanceIdPath != nil && instance == ""
	if isRequiredInstanceMissing {
		return "", "", "", ErrMissingComponentInstance
	}
	group := ""
	// A component can inherit the replica selector from an owner component.
	for {
		selector := component.GetPodSelector()
		hasReplicaKeyPath := selector != nil && selector.ReplicaSelector != nil && selector.ReplicaSelector.KeyPath != ""
		if hasReplicaKeyPath {
			group, _, err = query.ExtractReplicaKey(ctx, selector.ReplicaSelector)
			if err != nil {
				return "", "", "", err
			}
			break
		}
		ownerComponentName := component.Definition().OwnerRef
		if ownerComponentName == nil {
			break
		}
		component, err = factory.GetComponent(*ownerComponentName)
		if err != nil {
			return "", "", "", err
		}
	}
	return componentName, instance, group, nil
}

func labelsFor(object *unstructured.Unstructured) labels.Set { return labels.Set(object.GetLabels()) }
