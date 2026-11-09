// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/discovery"
)

// PodWorkloadAssociation changes metric identity for bare Pods whose named target
// includes their current UID in its owner references. It does not change ownership.
type PodWorkloadAssociation struct {
	PodSelector *metav1.LabelSelector `json:"podSelector" yaml:"podSelector"`
	Target      AssociationTarget     `json:"target" yaml:"target"`
}

// AssociationTarget names the watched kind and the Pod label containing its name.
type AssociationTarget struct {
	APIVersion    string `json:"apiVersion" yaml:"apiVersion"`
	Kind          string `json:"kind" yaml:"kind"`
	NameFromLabel string `json:"nameFromLabel" yaml:"nameFromLabel"`
}

var ErrInvalidPodAssociation = errors.New("pod workload association cannot be verified")

type podAssociationSnapshot struct {
	eligible   bool
	target     discovery.ObjectRef
	object     *unstructured.Unstructured
	readReason string
}

func (r *Runtime) resolvePodAssociation(ctx context.Context, pod, root *unstructured.Unstructured, workload *discovery.GenericWorkload) (*unstructured.Unstructured, podAssociationSnapshot, watchKey, error) {
	config := r.state.config.PodWorkloadAssociation
	var snapshot podAssociationSnapshot
	if config == nil || workload.Root.GVK != podGVK || workload.Root.UID != pod.GetUID() || workload.Resolution != discovery.Resolved || !r.state.associationSelector.Matches(labelsFor(pod)) {
		return root, snapshot, watchKey{}, nil
	}
	owner, valid := discovery.ControllerOwner(pod)
	name := pod.GetLabels()[config.Target.NameFromLabel]
	if !valid || owner.UID != "" {
		return root, snapshot, watchKey{}, nil
	}
	snapshot.eligible = true
	if name == "" {
		return root, snapshot, watchKey{}, ErrInvalidPodAssociation
	}
	selection, err := r.state.lookupDefinition(r.state.associationTarget)
	if err != nil {
		return root, snapshot, watchKey{}, err
	}
	// Association uses an already selected full-object collection. It never
	// introduces a watch or gives a metadata-only collection workload semantics.
	if selection.Definition == nil || selection.GVK != r.state.associationTarget {
		return root, snapshot, watchKey{}, ErrInvalidPodAssociation
	}
	selected, err := r.state.isResourceSelected(ctx, r.state.associationTarget)
	if err != nil {
		return root, snapshot, watchKey{}, err
	}
	if !selected {
		return root, snapshot, watchKey{}, ErrInvalidPodAssociation
	}
	snapshot.target = discovery.ObjectRef{GVK: r.state.associationTarget, Namespace: pod.GetNamespace(), Name: name}
	object, source, err := r.state.reader.read(snapshot.target, true)
	snapshot.object, snapshot.readReason = object, failureReason(err)
	if apierrors.IsNotFound(err) {
		return root, snapshot, source, ErrInvalidPodAssociation
	}
	if err != nil {
		return root, snapshot, source, err
	}
	owner, valid = discovery.ControllerOwner(object)
	if !valid || owner.UID != "" || object.GetNamespace() != pod.GetNamespace() || !r.state.matchesObjectSelectors(object) {
		return root, snapshot, source, ErrInvalidPodAssociation
	}
	for _, ref := range object.GetOwnerReferences() {
		if ref.APIVersion == "v1" && ref.Kind == "Pod" && ref.Name == pod.GetName() && ref.UID == pod.GetUID() {
			return object, snapshot, source, nil
		}
	}
	return root, snapshot, source, ErrInvalidPodAssociation
}

func ValidatePodWorkloadAssociation(config *PodWorkloadAssociation) error {
	if config == nil {
		return nil
	}
	if _, err := parseAssociationTarget(config.Target.APIVersion + "/" + config.Target.Kind); err != nil {
		return err
	}
	if errors := validation.IsQualifiedName(config.Target.NameFromLabel); len(errors) > 0 {
		return fmt.Errorf("podWorkloadAssociation.target.nameFromLabel: %s", strings.Join(errors, ", "))
	}
	if _, err := selector(config.PodSelector); err != nil {
		return fmt.Errorf("podWorkloadAssociation.podSelector: %w", err)
	}
	return nil
}

func parseAssociationTarget(value string) (schema.GroupVersionKind, error) {
	separator := strings.LastIndexByte(value, '/')
	if separator > 0 && separator < len(value)-1 && !strings.ContainsAny(value, " \t\r\n*") && value[0] != '/' {
		gv, err := schema.ParseGroupVersion(value[:separator])
		if err == nil && gv.Version != "" {
			gvk := gv.WithKind(value[separator+1:])
			if gvk != podGVK && gvk != namespaceGVK && gvk != kartaGVK {
				return gvk, nil
			}
		}
	}
	return schema.GroupVersionKind{}, fmt.Errorf("podWorkloadAssociation.target must name a workload as group/version/Kind or version/Kind: %q", value)
}
