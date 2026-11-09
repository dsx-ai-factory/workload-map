// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/discovery"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

// handleWorkloadInput dispatches watched-object discovery or root reconciliation.
// Callers must not hold stateMu; state operations acquire their own locks.
func (r *Runtime) handleWorkloadInput(ctx context.Context, key workloadJobKey) error {
	if key.SelectedObject.UID != "" {
		return r.resolveSelectedObjectRoot(ctx, key.SelectedObject)
	}
	return r.reconcileWorkload(ctx, key.RootUID)
}

// resolveSelectedObjectRoot discovers and registers a watched object's root.
// Callers must not hold stateMu; discovery runs unlocked and state operations lock internally.
func (r *Runtime) resolveSelectedObjectRoot(ctx context.Context, key objectJobKey) error {
	if !r.state.namespaceAdmitted(key.Object.Namespace) {
		r.state.unregisterSelectedObject(key)
		return nil
	}

	r.state.trackSelectedObject(key)
	selection, selectionErr := r.state.lookupDefinition(key.Object.GVK)
	if selection.Definition == nil && selectionErr != nil {
		r.state.deps.Publications.SetDiscoveryFailure(storedRef(key.ref()), failureReason(selectionErr))
		return selectionErr
	}
	if selection.Definition != nil && selection.GVK != key.Object.GVK {
		selectedRef := key.ref()
		selectedRef.GVK = selection.GVK
		if _, err := r.state.ensureWatch(ctx, selectedRef.GVK, selectedRef.Namespace, false); err != nil {
			return err
		}
		selectedObject, _, err := r.state.reader.read(selectedRef, true)
		isUnexpectedReadError := err != nil && !apierrors.IsNotFound(err)
		if isUnexpectedReadError {
			return err
		}
		return r.state.replaceSelectedObjectTracking(key, selectedObject, selection)
	}
	selectedObject, _, err := r.state.reader.read(key.ref(), false)
	if apierrors.IsNotFound(err) {
		r.state.unregisterSelectedObject(key)
		return nil
	}
	if err != nil {
		r.state.failSelectedObjectDiscovery(key, err)
		return err
	}
	discoveryResult, err := r.state.deps.Discovery.Get(ctx, refOf(selectedObject))
	r.state.setOwnerWait(ownerWaiter{key: key, selectedObject: true}, err)
	isOwnerUnresolved := err == nil && discoveryResult.Workload == nil
	if isOwnerUnresolved {
		err = discovery.ErrUnresolvedOwner
	}
	if err != nil {
		r.state.failSelectedObjectDiscovery(key, err)
		return err
	}
	workload := discoveryResult.Workload
	rootSelection, rootSelectionErr := r.state.lookupDefinition(workload.Root.GVK)
	selectedGVK := workload.Root.GVK
	if rootSelection.Definition != nil {
		selectedGVK = rootSelection.GVK
	}
	isResourceSelected, scopeErr := r.state.isResourceSelected(ctx, selectedGVK)
	if scopeErr != nil {
		r.state.failSelectedObjectDiscovery(key, scopeErr)
		return scopeErr
	}
	if !isResourceSelected {
		if err := errors.Join(selectionErr, rootSelectionErr); err != nil {
			r.state.failSelectedObjectDiscovery(key, err)
			return err
		}
		return r.state.excludeSelectedObject(key, workload, selection, rootSelection)
	}
	return r.state.registerSelectedObjectRoot(key, workload, selection, rootSelection)
}

// Only canonical UID keys publish workload results. Selected-object keys register
// or unregister root membership, including watched children that have no Pods.
func (r *Runtime) reconcileWorkload(ctx context.Context, uid types.UID) error {
	ref, exists := r.state.snapshotRoot(uid)
	if !exists {
		return nil
	}
	selection, selectionErr := r.state.lookupDefinition(ref.GVK)
	if selection.Definition != nil {
		ref.GVK = selection.GVK
	}
	ref.ResourceVersion = ""
	if _, err := r.state.ensureWatch(ctx, ref.GVK, ref.Namespace, false); err != nil {
		return r.state.failWorkload(uid, ref, err)
	}
	workloadObject, _, err := r.state.reader.read(ref, true)
	if apierrors.IsNotFound(err) {
		r.state.withdrawWorkload(uid)
		return nil
	}
	if err != nil {
		return r.state.failWorkload(uid, ref, err)
	}
	ref = refOf(workloadObject)
	owner, isOwnerValid := discovery.ControllerOwner(workloadObject)
	if !isOwnerValid {
		return r.state.failWorkload(uid, ref, discovery.ErrUnresolvedOwner)
	}
	hasNonIgnoredOwner := owner.UID != "" && !slices.Contains(r.state.config.IgnoredOwnerKinds, owner.GVK)
	if hasNonIgnoredOwner {
		r.state.withdrawWorkload(uid)
		return nil
	}
	if !r.state.isRoot(ref) {
		return r.state.failWorkload(uid, ref, discovery.ErrUnresolvedOwner)
	}
	isRootSelected, err := r.state.isRootSelected(ctx, workloadObject)
	if err != nil {
		return r.state.failWorkload(uid, ref, err)
	}
	if !isRootSelected {
		r.state.deps.Publications.RemoveWorkload(uid)
		return nil
	}
	record := store.WorkloadRecord{
		UID:              uid,
		Ref:              storedRef(ref),
		OwnerResolution:  store.Resolved,
		CreatedTimestamp: workloadObject.GetCreationTimestamp().Unix(),
	}
	if owner.UID != "" {
		record.OwnerResolution = store.IgnoredOwner
	}
	statusErr := selectionErr
	if statusErr == nil {
		if selection.Definition == nil {
			// A healthy lookup with no Karta publishes every phase as zero.
			record.HasStatus = true
		} else {
			record.Karta = selection.Origin.Name
			record.KartaSource = string(selection.Origin.Source)
			factory := resource.NewComponentFactoryFromObject(selection.Definition, workloadObject)
			statusErr = evaluateWorkloadStatus(ctx, factory, &record)
		}
		statusErr = errors.Join(statusErr, r.evaluateWorkloadStatusLabels(ctx, workloadObject, &record))
	}
	sources := r.state.sourcesFor([]discovery.ObjectRef{ref})
	creationSources := slices.DeleteFunc(slices.Clone(sources), func(key store.SourceKey) bool {
		return key.Resource.Group == kartaGVK.Group && key.Resource.Resource == "kartas"
	})
	workloadResult := store.WorkloadResult{
		Record:          record,
		HasValue:        true,
		StatusError:     failureReason(statusErr),
		Sources:         sources,
		CreationSources: creationSources,
	}
	return r.state.publishWorkload(uid, ref, selection, workloadResult)
}

func evaluateWorkloadStatus(ctx context.Context, factory *resource.ComponentFactory, record *store.WorkloadRecord) error {
	component, statusErr := factory.GetRootComponent()
	if statusErr == nil {
		status, evaluationErr := component.GetStatus(ctx)
		statusErr = evaluationErr
		if status != nil {
			record.HasStatus = true
			for _, phase := range status.MatchedStatuses {
				record.Phases = append(record.Phases, string(phase))
			}
		}
	}
	shouldWrapEvaluationError := statusErr != nil && !errors.Is(statusErr, discovery.ErrInvalidDefinition)
	if shouldWrapEvaluationError {
		return fmt.Errorf("%w: %w", discovery.ErrEvaluation, statusErr)
	}
	return statusErr
}

func (r *Runtime) evaluateWorkloadStatusLabels(ctx context.Context, object *unstructured.Unstructured, record *store.WorkloadRecord) error {
	results, err := r.state.deps.Labels.Resolve(ctx, labelresolver.Input{
		Metrics:  []string{labelresolver.MetricWorkloadStatus},
		Workload: object,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", discovery.ErrEvaluation, err)
	}
	statusLabels := results[0]
	if len(statusLabels.Errors) > 0 {
		return discovery.ErrEvaluation
	}
	record.StatusLabels = statusLabels.Labels
	return nil
}

func ValidateResourceRules(rules []ResourceRule) error {
	for index, rule := range rules {
		hasMissingFields := len(rule.APIGroups) == 0 || len(rule.APIVersions) == 0 || len(rule.Resources) == 0
		if hasMissingFields {
			return fmt.Errorf("resource rule %d requires apiGroups, apiVersions and resources", index)
		}
		for _, version := range rule.APIVersions {
			isInvalidVersion := version == "" || strings.ContainsAny(version, "/ \t\n")
			if isInvalidVersion {
				return errors.New("resource rule API versions must name a version or *")
			}
		}
		for _, resource := range rule.Resources {
			isInvalidResource := resource == "" || strings.ContainsAny(resource, "/ \t\n")
			if isInvalidResource {
				return errors.New("resource rules must name whole resources or *")
			}
		}
	}
	return nil
}

func (r *controllerState) isRootSelected(ctx context.Context, object *unstructured.Unstructured) (bool, error) {
	if !r.objectSelector.Matches(labelsFor(object)) {
		return false, nil
	}
	return r.isResourceSelected(ctx, object.GroupVersionKind())
}

func (r *controllerState) isResourceSelected(ctx context.Context, gvk schema.GroupVersionKind) (bool, error) {
	hasResourceFilters := len(r.config.ResourceRules) > 0 || len(r.config.ExcludeResourceRules) > 0
	if !hasResourceFilters {
		return true, nil
	}
	mapping, err := r.reader.restMapping(ctx, gvk)
	if err != nil {
		return false, err
	}
	for _, rule := range r.config.ExcludeResourceRules {
		if matchesResourceRule(rule, mapping.Resource) {
			return false, nil
		}
	}
	if len(r.config.ResourceRules) == 0 {
		return true, nil
	}
	for _, rule := range r.config.ResourceRules {
		if matchesResourceRule(rule, mapping.Resource) {
			return true, nil
		}
	}
	return false, nil
}

func matchesResourceRule(rule ResourceRule, gvr schema.GroupVersionResource) bool {
	hasMatchingGroup := slices.Contains(rule.APIGroups, "*") || slices.Contains(rule.APIGroups, gvr.Group)
	if !hasMatchingGroup {
		return false
	}
	hasMatchingVersion := slices.Contains(rule.APIVersions, "*") || slices.Contains(rule.APIVersions, gvr.Version)
	if !hasMatchingVersion {
		return false
	}
	hasMatchingResource := slices.Contains(rule.Resources, "*") || slices.Contains(rule.Resources, gvr.Resource)
	return hasMatchingResource
}
