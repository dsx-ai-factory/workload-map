// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package resource

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
)

var _ = Describe("LinksToRoot", func() {
	karta := &v1alpha1.Karta{Spec: v1alpha1.KartaSpec{StructureDefinition: v1alpha1.StructureDefinition{
		RootComponent: v1alpha1.ComponentDefinition{Name: "root"},
		ChildComponents: []v1alpha1.ComponentDefinition{{
			Name: "minio",
			PodSelector: &v1alpha1.PodSelector{
				ComponentTypeSelector: &v1alpha1.ComponentTypeSelector{MatchLabels: map[string]string{"app": "minio"}},
				RootLink:              &v1alpha1.RootLink{LabelKey: "release", ValuePath: `.metadata.name + "-minio"`},
			},
		}},
	}}}
	root := map[string]any{"metadata": map[string]any{"name": "milvus-a"}}
	pod := func(labels map[string]string) *corev1.Pod {
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Labels: labels}}
	}

	DescribeTable("links a pod only when the type and the root-derived label both match",
		func(labels map[string]string, want bool) {
			got, err := LinksToRoot(context.Background(), root, karta, pod(labels))
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("this root's minio", map[string]string{"app": "minio", "release": "milvus-a-minio"}, true),
		Entry("another root's minio", map[string]string{"app": "minio", "release": "milvus-b-minio"}, false),
		Entry("right release, wrong type", map[string]string{"app": "other", "release": "milvus-a-minio"}, false),
		Entry("no link label", map[string]string{"app": "minio"}, false),
	)

	It("rejects a valuePath that does not produce a string", func() {
		bad := karta.DeepCopy()
		bad.Spec.StructureDefinition.ChildComponents[0].PodSelector.RootLink.ValuePath = ".metadata"
		_, err := LinksToRoot(context.Background(), root, bad, pod(map[string]string{"app": "minio", "release": "x"}))
		Expect(err).To(MatchError(ContainSubstring("want string")))
	})
})
