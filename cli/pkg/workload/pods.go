// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package workload

import (
	"context"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/metadata"
)

// podsGVR is the fixed core/v1 Pod resource, which discovery does not need to map.
var podsGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

// PodAttributor narrows a namespace of pods to the one workload that owns them.
// A Karta PodSelector names a component type, never a workload.
type PodAttributor struct {
	owners *OwnerWalker
}

func NewPodAttributor(client metadata.Interface, mapper meta.RESTMapper) *PodAttributor {
	return &PodAttributor{owners: NewOwnerWalker(client, mapper)}
}

// Filter returns the pods whose owner-reference chain reaches rootUID, decoding
// only the matches. pods may span namespaces, as in a cluster-wide search.
func (a *PodAttributor) Filter(
	ctx context.Context, pods []unstructured.Unstructured, rootUID types.UID,
) ([]corev1.Pod, error) {
	var matched []corev1.Pod
	for i := range pods {
		// An unreadable owner chain is not a match.
		owned, err := a.owners.climb(ctx, pods[i].GetOwnerReferences(), pods[i].GetNamespace(),
			func(refs []metav1.OwnerReference) bool {
				return slices.ContainsFunc(refs, func(ref metav1.OwnerReference) bool { return ref.UID == rootUID })
			})
		if err != nil || !owned {
			continue
		}

		var pod corev1.Pod
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(pods[i].Object, &pod); err != nil {
			return nil, fmt.Errorf("decode pod %s: %w", pods[i].GetName(), err)
		}
		matched = append(matched, pod)
	}
	return matched, nil
}

// ListPods reads every pod in namespace once, left undecoded for Filter to
// narrow. An empty namespace lists cluster-wide.
func ListPods(ctx context.Context, dyn dynamic.Interface, namespace string) ([]unstructured.Unstructured, error) {
	// ResourceVersion 0 serves the list from the apiserver's watch cache rather
	// than etcd, which a describe read can afford to have a moment stale.
	list, err := dyn.Resource(podsGVR).Namespace(namespace).
		List(ctx, metav1.ListOptions{ResourceVersion: "0"})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}
