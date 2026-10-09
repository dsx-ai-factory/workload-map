// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package workload

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	metadatafake "k8s.io/client-go/metadata/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
)

const namespace = "ml-team"

var (
	deploymentGVK   = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	replicaSetGVK   = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"}
	podGVK          = schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	clusterOwnerGVK = schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "ClusterOwner"}
)

// owned builds an unstructured object of gvk owned by the named controller,
// which is what the walk climbs.
func owned(gvk schema.GroupVersionKind, name string, uid types.UID, owner *metav1.OwnerReference) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": gvk.GroupVersion().String(),
		"kind":       gvk.Kind,
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"uid":       string(uid),
		},
	}}
	if owner != nil {
		obj.SetOwnerReferences([]metav1.OwnerReference{*owner})
	}
	return obj
}

// clusterScoped builds an owner that lives outside any namespace.
func clusterScoped(name string, uid types.UID, owner *metav1.OwnerReference) *unstructured.Unstructured {
	obj := owned(clusterOwnerGVK, name, uid, owner)
	unstructured.RemoveNestedField(obj.Object, "metadata", "namespace")
	return obj
}

func controllerOf(gvk schema.GroupVersionKind, name string, uid types.UID) *metav1.OwnerReference {
	return &metav1.OwnerReference{
		APIVersion: gvk.GroupVersion().String(),
		Kind:       gvk.Kind,
		Name:       name,
		UID:        uid,
		Controller: ptr.To(true),
	}
}

func pod(name string, owner *metav1.OwnerReference) unstructured.Unstructured {
	return *owned(podGVK, name, "", owner)
}

// filter runs the attributor and fails the spec if a matched pod cannot decode.
func filter(a *PodAttributor, pods []unstructured.Unstructured, rootUID types.UID) []corev1.Pod {
	GinkgoHelper()
	matched, err := a.Filter(context.Background(), pods, rootUID)
	Expect(err).NotTo(HaveOccurred())
	return matched
}

// fakeCluster serves the metadata of objects, the only part an owner walk
// reads, through a mapper that knows the kinds an owner chain climbs through.
func fakeCluster(objects ...*unstructured.Unstructured) (*metadatafake.FakeMetadataClient, meta.RESTMapper) {
	mapper := meta.NewDefaultRESTMapper(nil)
	for _, gvk := range []schema.GroupVersionKind{deploymentGVK, replicaSetGVK, podGVK} {
		mapper.Add(gvk, meta.RESTScopeNamespace)
	}
	mapper.Add(clusterOwnerGVK, meta.RESTScopeRoot)

	scheme := runtime.NewScheme()
	metav1.AddMetaToScheme(scheme)
	partials := make([]runtime.Object, 0, len(objects))
	for _, obj := range objects {
		var partial metav1.PartialObjectMetadata
		Expect(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &partial)).To(Succeed())
		partials = append(partials, &partial)
	}
	return metadatafake.NewSimpleMetadataClient(scheme, partials...), mapper
}

// countReads counts the owner reads of each verb, which is what listing an
// owner kind once saves.
func countReads(client *metadatafake.FakeMetadataClient) map[string]int {
	reads := map[string]int{}
	client.PrependReactor("*", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		reads[action.GetVerb()]++
		return false, nil, nil
	})
	return reads
}

var _ = Describe("PodAttributor", func() {
	const rootUID = types.UID("root-uid")

	var (
		deployment *unstructured.Unstructured
		replicaSet *unstructured.Unstructured
	)

	BeforeEach(func() {
		deployment = owned(deploymentGVK, "web", rootUID, nil)
		replicaSet = owned(replicaSetGVK, "web-abc", "rs-uid", controllerOf(deploymentGVK, "web", rootUID))
	})

	It("claims a pod whose chain reaches the root through an intermediate owner", func() {
		dyn, mapper := fakeCluster(deployment, replicaSet)

		mine := pod("web-abc-1", controllerOf(replicaSetGVK, "web-abc", "rs-uid"))
		matched := filter(NewPodAttributor(dyn, mapper), []unstructured.Unstructured{mine}, rootUID)

		Expect(matched).To(HaveLen(1))
		Expect(matched[0].Name).To(Equal("web-abc-1"))
	})

	It("claims a pod the root owns directly, with no walk at all", func() {
		dyn, mapper := fakeCluster()

		mine := pod("standalone", controllerOf(deploymentGVK, "web", rootUID))
		Expect(filter(NewPodAttributor(dyn, mapper), []unstructured.Unstructured{mine}, rootUID)).To(HaveLen(1))
	})

	It("leaves a pod belonging to another workload of the same type", func() {
		other := owned(replicaSetGVK, "api-xyz", "other-rs",
			controllerOf(deploymentGVK, "api", "other-root"))
		dyn, mapper := fakeCluster(deployment, replicaSet, other,
			owned(deploymentGVK, "api", "other-root", nil))

		theirs := pod("api-xyz-1", controllerOf(replicaSetGVK, "api-xyz", "other-rs"))
		Expect(filter(NewPodAttributor(dyn, mapper), []unstructured.Unstructured{theirs}, rootUID)).To(BeEmpty())
	})

	It("leaves an unowned pod", func() {
		dyn, mapper := fakeCluster(deployment, replicaSet)

		Expect(filter(NewPodAttributor(dyn, mapper), []unstructured.Unstructured{pod("bare", nil)}, rootUID)).To(BeEmpty())
	})

	// A missing intermediate is a chain that cannot be walked, not a match.
	It("leaves a pod whose intermediate owner is gone", func() {
		dyn, mapper := fakeCluster(deployment)

		orphan := pod("web-abc-1", controllerOf(replicaSetGVK, "web-abc", "rs-uid"))
		Expect(filter(NewPodAttributor(dyn, mapper), []unstructured.Unstructured{orphan}, rootUID)).To(BeEmpty())
	})

	// Sibling pods share an intermediate, and a broken chain must not cost a
	// read per pod either.
	It("lists an owner kind once across pods, hit or miss", func() {
		dyn, mapper := fakeCluster(deployment, replicaSet)
		reads := countReads(dyn)

		pods := []unstructured.Unstructured{
			pod("web-abc-1", controllerOf(replicaSetGVK, "web-abc", "rs-uid")),
			pod("web-abc-2", controllerOf(replicaSetGVK, "web-abc", "rs-uid")),
			pod("gone-1", controllerOf(replicaSetGVK, "gone", "gone-uid")),
			pod("gone-2", controllerOf(replicaSetGVK, "gone", "gone-uid")),
		}
		attributor := NewPodAttributor(dyn, mapper)

		Expect(filter(attributor, pods, rootUID)).To(HaveLen(2))
		Expect(reads).To(Equal(map[string]int{"list": 1}),
			"one ReplicaSet list answers the shared owner and the missing one")
	})

	// Without the scope check the pod is silently dropped from its workload.
	It("climbs through a cluster-scoped owner", func() {
		gateway := clusterScoped("gateway", "gateway-uid", controllerOf(deploymentGVK, "web", rootUID))
		dyn, mapper := fakeCluster(deployment, gateway)

		mine := pod("web-1", controllerOf(clusterOwnerGVK, "gateway", "gateway-uid"))
		Expect(filter(NewPodAttributor(dyn, mapper), []unstructured.Unstructured{mine}, rootUID)).To(HaveLen(1))
	})

	// Malformed data can point an owner chain at itself; the walk must end.
	It("gives up on a cycle rather than recursing forever", func() {
		loop := owned(replicaSetGVK, "loop", "loop-uid", controllerOf(replicaSetGVK, "loop", "loop-uid"))
		dyn, mapper := fakeCluster(loop)

		caught := pod("loop-1", controllerOf(replicaSetGVK, "loop", "loop-uid"))
		Expect(filter(NewPodAttributor(dyn, mapper), []unstructured.Unstructured{caught}, rootUID)).To(BeEmpty())
	})
})

var _ = Describe("ListPods", func() {
	// Undecoded, but whole: Filter decodes the matches, so anything the view
	// reads later has to survive the round trip.
	It("returns every pod in the namespace, decodable in full", func() {
		running := owned(podGVK, "web-abc-1", "pod-uid", nil)
		Expect(unstructured.SetNestedField(running.Object, "node-01", "spec", "nodeName")).To(Succeed())
		dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
			map[schema.GroupVersionResource]string{podsGVR: "PodList"}, running)

		pods, err := ListPods(context.Background(), dyn, namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(pods).To(HaveLen(1))

		var pod corev1.Pod
		Expect(runtime.DefaultUnstructuredConverter.FromUnstructured(pods[0].Object, &pod)).To(Succeed())
		Expect(pod.Name).To(Equal("web-abc-1"))
		Expect(pod.Spec.NodeName).To(Equal("node-01"))
	})
})
