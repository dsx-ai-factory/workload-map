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

// NewPodAttributor returns an attributor reading pod owners through client.
func NewPodAttributor(client metadata.Interface, mapper meta.RESTMapper) *PodAttributor {
	return &PodAttributor{owners: NewOwnerWalker(client, mapper)}
}

// Filter returns the pods whose owner-reference chain reaches rootUID, decoding
// only the matches. pods may span namespaces, as in a cluster-wide search. A pod
// whose chain cannot be walked is left out, and warnings say so once per cause,
// since an unrelated pod can hit the same unreadable owner kind.
func (a *PodAttributor) Filter(
	ctx context.Context, pods []unstructured.Unstructured, rootUID types.UID,
) ([]corev1.Pod, []string, error) {
	var (
		matched []corev1.Pod
		causes  []string
	)
	unresolved := map[string]int{}
	for i := range pods {
		owned, err := a.owners.climb(ctx, pods[i].GetOwnerReferences(), pods[i].GetNamespace(),
			func(refs []metav1.OwnerReference) bool {
				return slices.ContainsFunc(refs, func(ref metav1.OwnerReference) bool { return ref.UID == rootUID })
			})
		if err != nil {
			if unresolved[err.Error()] == 0 {
				causes = append(causes, err.Error())
			}
			unresolved[err.Error()]++
			continue
		}
		if !owned {
			continue
		}

		var pod corev1.Pod
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(pods[i].Object, &pod); err != nil {
			return nil, nil, fmt.Errorf("decode pod %s: %w", pods[i].GetName(), err)
		}
		matched = append(matched, pod)
	}

	warnings := make([]string, 0, len(causes))
	for _, cause := range causes {
		warnings = append(warnings, fmt.Sprintf(
			"could not tell whether %d pod(s) belong to this workload, so pod counts may be low: %s",
			unresolved[cause], cause))
	}
	return matched, warnings, nil
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
