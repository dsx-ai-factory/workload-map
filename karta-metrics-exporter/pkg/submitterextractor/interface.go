// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package submitterextractor

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type Interface interface {
	Evaluate(context.Context, metav1.Object, schema.GroupVersionKind) (Submitter, error)
}

type Submitter struct {
	Identity string
}
