// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package instructions

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog/kartas"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

func TestInferMilvusDependencyPodComponent(t *testing.T) {
	summary, err := NewStructureSummary(kartas.Milvus())
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		labels    map[string]string
		component string
	}{
		{name: "etcd", labels: map[string]string{"app.kubernetes.io/name": "etcd"}, component: "etcd"},
		{name: "minio", labels: map[string]string{"app": "minio"}, component: "minio"},
		{name: "milvus-proxy", labels: map[string]string{"app.kubernetes.io/component": "proxy"}, component: "proxy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: tt.name, Labels: tt.labels}}
			got, err := InferPodComponent(context.Background(), resource.NewPodQuerier(pod), summary)
			if err != nil {
				t.Fatalf("infer component: %v", err)
			}
			if got != tt.component {
				t.Errorf("component = %q, want %q", got, tt.component)
			}
		})
	}

	t.Run("unrelated dependency", func(t *testing.T) {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Labels: map[string]string{"app": "unrelated"}}}
		if got, err := InferPodComponent(context.Background(), resource.NewPodQuerier(pod), summary); err == nil {
			t.Errorf("component = %q, want an error for an unrelated pod", got)
		}
	})
}
