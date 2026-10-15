// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package resource

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// KubernetesObject is the structural interface that karta's resource helpers
// accept and return. Its method set is identical to
// sigs.k8s.io/controller-runtime/pkg/client.Object, so any client.Object value
// satisfies it transparently — karta itself stays free of the controller-runtime
// dependency. Same pattern used by knative/pkg's kmeta.Accessor and
// crossplane-runtime's resource.Object.
type KubernetesObject interface {
	metav1.Object
	runtime.Object
}
