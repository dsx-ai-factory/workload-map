// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/discovery"
	inventory "github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelevaluator"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
	api "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/instructions"
)

var associationTestGVK = schema.GroupVersionKind{Group: "scheduling.example.org", Version: "v1", Kind: "Workload"}

var _ = Describe("Pod workload association", func() {
	It("keeps physical Pod components while sharing the target metric identity", func() {
		r, podWatch, targetWatch := newAssociationRuntime()
		client := fake.NewSimpleDynamicClient(kruntime.NewScheme())
		r.state.reader.clients.Dynamic = client
		r.state.deps.Labels = associationLabelResolver(`object.metadata.name`)
		target := rootLifetimeObject(associationTestGVK, "ns", "group", "group-uid")
		for _, name := range []string{"first", "second"} {
			pod := associationPod(name, types.UID(name))
			target.SetOwnerReferences(append(target.GetOwnerReferences(), associationOwner(pod)))
			Expect(r.state.applyWatchEvent(podWatch, pod, false)).To(Succeed())
		}
		Expect(unstructured.SetNestedField(target.Object, "Running", "status", "phase")).To(Succeed())
		Expect(r.state.applyWatchEvent(targetWatch, target, false)).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		for _, uid := range []types.UID{"first", "second"} {
			result := r.state.deps.Publications.Pod(uid)
			Expect(result.HasValue).To(BeTrue())
			Expect(result.Error).To(BeEmpty())
			Expect(result.Record.Workload).To(Equal(storedRef(refOf(target))))
			Expect(result.Record.Component).To(Equal("pod"))
			Expect(result.Record.Labels["metric_workload"]).To(Equal("group"))
			Expect(r.state.podRoots[uid]).To(Equal(uid), "physical ownership must remain unchanged")
		}
		workload := r.state.deps.Publications.Workload(target.GetUID())
		Expect(workload.Record.HasStatus).To(BeTrue())
		Expect(workload.Record.Phases).To(Equal([]string{"Running"}))
		Expect(client.Actions()).To(BeEmpty(), "association must read the existing cache without API requests")
	})

	It("updates the workload identity through target and Pod lifecycle changes", func() {
		r, podWatch, targetWatch := newAssociationRuntime()
		pod := associationPod("member", "pod-1")
		target := rootLifetimeObject(associationTestGVK, "ns", "group", "target-1")
		target.SetOwnerReferences([]metav1.OwnerReference{associationOwner(pod)})
		Expect(r.state.applyWatchEvent(podWatch, pod, false)).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		assertIdentity := func(want types.UID) {
			GinkgoHelper()
			result := r.state.deps.Publications.Pod(pod.GetUID())
			Expect(result).NotTo(BeNil())
			Expect(result.HasValue).To(BeTrue())
			if want == pod.GetUID() {
				Expect(result.Error).To(Equal(store.ReasonUnresolvedOwner))
			} else {
				Expect(result.Error).To(BeEmpty())
			}
			Expect(result.Record.Workload.UID).To(Equal(want))
		}
		By("keeping the Pod identity until the named target is created")
		assertIdentity(pod.GetUID())
		Expect(r.state.applyWatchEvent(targetWatch, target, false)).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		assertIdentity(target.GetUID())

		By("falling back when the target removes its Pod owner")
		target = target.DeepCopy()
		target.SetResourceVersion("2")
		target.SetOwnerReferences(nil)
		Expect(r.state.applyWatchEvent(targetWatch, target, false)).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		assertIdentity(pod.GetUID())
		By("restoring the association when the Pod owner returns")
		target = target.DeepCopy()
		target.SetResourceVersion("3")
		target.SetOwnerReferences([]metav1.OwnerReference{associationOwner(pod)})
		Expect(r.state.applyWatchEvent(targetWatch, target, false)).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		assertIdentity(target.GetUID())

		By("replacing the target identity when its UID changes")
		target = target.DeepCopy()
		target.SetUID("target-2")
		target.SetResourceVersion("4")
		Expect(r.state.applyWatchEvent(targetWatch, target, false)).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		assertIdentity(target.GetUID())
		Expect(r.state.deps.Publications.Workload("target-1")).To(BeNil())

		By("requiring the target owner UID to match a replaced Pod")
		pod = pod.DeepCopy()
		pod.SetUID("pod-2")
		pod.SetResourceVersion("2")
		Expect(r.state.applyWatchEvent(podWatch, pod, false)).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		assertIdentity(pod.GetUID())
		target = target.DeepCopy()
		target.SetResourceVersion("5")
		target.SetOwnerReferences([]metav1.OwnerReference{associationOwner(pod)})
		Expect(r.state.applyWatchEvent(targetWatch, target, false)).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		assertIdentity(target.GetUID())

		By("falling back when the association label is removed")
		pod = pod.DeepCopy()
		pod.SetResourceVersion("3")
		pod.SetLabels(nil)
		Expect(r.state.applyWatchEvent(podWatch, pod, false)).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		assertIdentity(pod.GetUID())
		By("following a changed association label when its target is created")
		pod = pod.DeepCopy()
		pod.SetResourceVersion("4")
		pod.SetLabels(map[string]string{"example.org/group": "other-group"})
		Expect(r.state.applyWatchEvent(podWatch, pod, false)).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		assertIdentity(pod.GetUID())
		target = target.DeepCopy()
		target.SetName("other-group")
		target.SetUID("target-3")
		target.SetResourceVersion("6")
		Expect(r.state.applyWatchEvent(targetWatch, target, false)).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		assertIdentity(target.GetUID())
		By("falling back after the target is deleted")
		Expect(r.state.applyWatchEvent(targetWatch, target, true)).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		assertIdentity(pod.GetUID())
	})

	DescribeTable("does not associate an ineligible Pod or target", func(scenario string) {
		r, podWatch, targetWatch := newAssociationRuntime()
		pod := associationPod("member", "pod")
		target := rootLifetimeObject(associationTestGVK, "ns", "group", "target")
		target.SetOwnerReferences([]metav1.OwnerReference{associationOwner(pod)})
		want := pod.GetUID()
		switch scenario {
		case "disabled":
			r.state.config.PodWorkloadAssociation = nil
		case "no target definition":
			delete(r.state.selectedDefinitions, associationTestGVK.GroupKind())
		case "excluded resource":
			r.state.config.ExcludeResourceRules = []ResourceRule{{APIGroups: []string{associationTestGVK.Group}, APIVersions: []string{"v1"}, Resources: []string{"workloads"}}}
		case "target selector":
			var err error
			r.state.objectSelectors, err = compileObjectSelectors([]ObjectSelector{{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"example.org/group": "group"}}}})
			Expect(err).To(Succeed())
		case "Pod selector":
			r.state.associationSelector, _ = selector(&metav1.LabelSelector{MatchLabels: map[string]string{"example.org/managed": "true"}})
		case "wrong namespace":
			target.SetNamespace("other")
		case "wrong Pod name":
			owner := associationOwner(pod)
			owner.Name = "different"
			target.SetOwnerReferences([]metav1.OwnerReference{owner})
		default:
			jobGVK := schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
			job := rootLifetimeObject(jobGVK, "ns", "job", "job")
			jobWatch := rootLifetimeWatch(GinkgoTB(), r, jobGVK, "", true)
			Expect(r.state.applyWatchEvent(jobWatch, job, false)).To(Succeed())
			owner := *metav1.NewControllerRef(job, jobGVK)
			if scenario == "target controller owner" {
				target.SetOwnerReferences(append(target.GetOwnerReferences(), owner))
			} else {
				pod.SetOwnerReferences([]metav1.OwnerReference{owner})
				want = job.GetUID()
				if scenario == "ignored Pod owner" {
					r.state.config.IgnoredOwnerKinds = []schema.GroupVersionKind{jobGVK}
					var err error
					r.state.deps.Discovery, err = discovery.New(r.state.reader, discovery.Config{IgnoredTopOwners: []schema.GroupVersionKind{jobGVK}})
					Expect(err).To(Succeed())
					want = pod.GetUID()
				}
			}
		}
		Expect(r.state.applyWatchEvent(podWatch, pod, false)).To(Succeed())
		Expect(r.state.applyWatchEvent(targetWatch, target, false)).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		Expect(r.state.deps.Publications.Pod(pod.GetUID()).Record.Workload.UID).To(Equal(want))
	},
		Entry("when association is disabled", "disabled"),
		Entry("when the target has no selected definition", "no target definition"),
		Entry("when the target resource is excluded", "excluded resource"),
		Entry("when the target does not match the object selector", "target selector"),
		Entry("when the Pod does not match the association selector", "Pod selector"),
		Entry("when the target is in another namespace", "wrong namespace"),
		Entry("when the target owner names a different Pod", "wrong Pod name"),
		Entry("when the target has a controller owner", "target controller owner"),
		Entry("when the Pod has a controller owner", "Pod controller owner"),
		Entry("when the Pod controller owner is ignored", "ignored Pod owner"),
	)

	It("falls back during source failures and definition removal, then recovers", func(ctx SpecContext) {
		r, podWatch, targetWatch := newAssociationRuntime()
		pod := associationPod("member", "pod")
		target := rootLifetimeObject(associationTestGVK, "ns", "group", "target")
		target.SetOwnerReferences([]metav1.OwnerReference{associationOwner(pod)})
		Expect(r.state.applyWatchEvent(podWatch, pod, false)).To(Succeed())
		Expect(r.state.applyWatchEvent(targetWatch, target, false)).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		r.state.setWatchHealth(targetWatch, apierrors.NewForbidden(targetWatch.key.Resource.GroupResource(), "", errors.New("denied")))
		publications := r.state.deps.Publications.(store.PublicationStore)
		view := publications.Capture()
		valid, reason := view.Valid(publications.Pod(pod.GetUID()).Sources, "")
		Expect(valid).To(BeFalse(), "saved identity must become invalid before a Pod worker runs")
		Expect(reason).To(Equal(store.ReasonPermissionDenied))
		Expect(r.reconcilePod(ctx, objectJobFor(refOf(pod)))).To(Succeed())
		result := publications.Pod(pod.GetUID())
		Expect(result.HasValue).To(BeTrue())
		Expect(result.Record.Workload.UID).To(Equal(pod.GetUID()))
		Expect(result.Error).To(Equal(store.ReasonPermissionDenied))
		r.state.setWatchHealth(targetWatch, nil)
		drainRootLifetimeQueues(GinkgoTB(), r)
		Expect(publications.Pod(pod.GetUID()).Record.Workload.UID).To(Equal(target.GetUID()))
		Expect(publications.Pod(pod.GetUID()).Error).To(BeEmpty())

		targetDefinition := r.state.selectedDefinitions[associationTestGVK.GroupKind()]
		next := map[schema.GroupKind]selectedDefinition{podGVK.GroupKind(): r.state.selectedDefinitions[podGVK.GroupKind()]}
		r.state.stateMu.Lock()
		err := r.state.refreshDefinitions(next, nil)
		r.state.stateMu.Unlock()
		Expect(err).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		Expect(publications.Pod(pod.GetUID()).Record.Workload.UID).To(Equal(pod.GetUID()))
		next = map[schema.GroupKind]selectedDefinition{podGVK.GroupKind(): next[podGVK.GroupKind()], associationTestGVK.GroupKind(): targetDefinition}
		r.state.stateMu.Lock()
		err = r.state.refreshDefinitions(next, nil)
		r.state.stateMu.Unlock()
		Expect(err).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		Expect(publications.Pod(pod.GetUID()).Record.Workload.UID).To(Equal(target.GetUID()))
	})

	It("rejects stale publications without restoring a removed association after label failure", func(ctx SpecContext) {
		r, podWatch, targetWatch := newAssociationRuntime()
		pod := associationPod("member", "pod")
		target := rootLifetimeObject(associationTestGVK, "ns", "group", "target")
		target.SetOwnerReferences([]metav1.OwnerReference{associationOwner(pod)})
		Expect(r.state.applyWatchEvent(podWatch, pod, false)).To(Succeed())
		drainRootLifetimeQueues(GinkgoTB(), r)
		selection, err := r.state.lookupDefinition(podGVK)
		Expect(err).To(Succeed())
		path := []discovery.ObjectRef{refOf(pod)}
		workload := &discovery.GenericWorkload{Root: refOf(pod), Path: path, Resolution: discovery.Resolved}
		_, missing, _, err := r.resolvePodAssociation(ctx, pod, pod, workload)
		Expect(errors.Is(err, ErrInvalidPodAssociation)).To(BeTrue(), "expected ErrInvalidPodAssociation, got %v", err)
		fallback := *r.state.deps.Publications.Pod(pod.GetUID())
		Expect(r.state.applyWatchEvent(targetWatch, target, false)).To(Succeed())
		err = r.state.publishPod(path, refOf(pod), selection, fallback, false, missing)
		Expect(errors.Is(err, discovery.ErrStale)).To(BeTrue(), "expected ErrStale, got %v", err)
		drainRootLifetimeQueues(GinkgoTB(), r)
		_, snapshot, _, err := r.resolvePodAssociation(ctx, pod, pod, workload)
		Expect(err).To(Succeed())
		previous := *r.state.deps.Publications.Pod(pod.GetUID())
		target = target.DeepCopy()
		target.SetResourceVersion("2")
		target.SetOwnerReferences(nil)
		Expect(r.state.applyWatchEvent(targetWatch, target, false)).To(Succeed())
		err = r.state.publishPod(path, refOf(pod), selection, previous, false, snapshot)
		Expect(errors.Is(err, discovery.ErrStale)).To(BeTrue(), "expected ErrStale, got %v", err)
		r.state.deps.Labels = associationLabelResolver(`object.missing`)
		drainRootLifetimeQueues(GinkgoTB(), r)
		result := r.state.deps.Publications.Pod(pod.GetUID())
		Expect(result.HasValue).To(BeTrue())
		Expect(result.Record.Workload.UID).To(Equal(pod.GetUID()), "label failure must not restore a removed association")
		Expect(result.Error).To(Equal(store.ReasonUnresolvedOwner))
	})
})

func newAssociationRuntime() (*Runtime, *resourceWatch, *resourceWatch) {
	GinkgoHelper()
	r := newRootLifetimeRuntime(GinkgoTB(), Config{PodWorkloadAssociation: &PodWorkloadAssociation{
		Target: AssociationTarget{APIVersion: associationTestGVK.GroupVersion().String(), Kind: associationTestGVK.Kind, NameFromLabel: "example.org/group"},
	}})
	podDefinition, err := catalog.Get(podGVK)
	Expect(err).To(Succeed())
	targetDefinition := podDefinition.DeepCopy()
	targetDefinition.Name = "group-workload"
	targetDefinition.Spec.StructureDefinition.RootComponent.Name = "group"
	targetDefinition.Spec.StructureDefinition.RootComponent.Kind = &api.GroupVersionKind{Group: associationTestGVK.Group, Version: associationTestGVK.Version, Kind: associationTestGVK.Kind}
	targetDefinition.Spec.StructureDefinition.RootComponent.SpecDefinition = nil
	for gvk, definition := range map[schema.GroupVersionKind]*api.Karta{podGVK: podDefinition, associationTestGVK: targetDefinition} {
		summary, err := instructions.NewStructureSummary(definition)
		Expect(err).To(Succeed())
		r.state.selectedDefinitions[gvk.GroupKind()] = selectedDefinition{Selection: inventory.Selection{
			Definition: definition, GVK: gvk, Origin: inventory.Origin{Name: definition.Name, Source: inventory.SourceCatalog},
		}, summary: summary}
	}
	return r, rootLifetimeWatch(GinkgoTB(), r, podGVK, "", true), rootLifetimeWatch(GinkgoTB(), r, associationTestGVK, "", true)
}

func associationPod(name string, uid types.UID) *unstructured.Unstructured {
	pod := rootLifetimeObject(podGVK, "ns", name, uid)
	pod.SetLabels(map[string]string{"example.org/group": "group"})
	return pod
}

func associationOwner(pod *unstructured.Unstructured) metav1.OwnerReference {
	return metav1.OwnerReference{APIVersion: "v1", Kind: "Pod", Name: pod.GetName(), UID: pod.GetUID()}
}

func associationLabelResolver(expression string) labelresolver.Resolver {
	GinkgoHelper()
	resolver, err := labelresolver.New(labelresolver.Config{Rules: []labelresolver.LabelRule{{
		Name: "metric_workload", Metrics: []string{labelresolver.MetricPodWorkloadInfo}, Sources: []labelresolver.Source{labelresolver.SourceWorkload}, Expression: expression,
	}}}, labelresolver.Dependencies{Compiler: labelevaluator.NewCompiler()})
	Expect(err).To(Succeed())
	return resolver
}
