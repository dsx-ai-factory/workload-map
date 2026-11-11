// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

var _ = Describe("Kueue Workload", Ordered, Label("kueue", "workload"), func() {
	It("running", func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/kueue-x-k8s-io-workload-v1beta2.yaml", "kueue-x-k8s-io-workload-v1beta2")
		queue := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "kueue.x-k8s.io/v1beta2", "kind": "LocalQueue",
			"metadata": map[string]any{"name": "karta-e2e", "namespace": testNamespace},
			"spec":     map[string]any{"clusterQueue": "karta-e2e", "stopPolicy": "Hold"},
		}}
		Expect(k8sClient.Create(ctx, queue)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { _ = k8sClient.Delete(ctx, queue) })

		for _, file := range []string{"running-head.yaml", "running-worker.yaml"} {
			pod := &unstructured.Unstructured{}
			Expect(yaml.Unmarshal(readE2E("flows/testdata/workload/"+file), pod)).To(Succeed())
			pod.SetNamespace(testNamespace)
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			DeferCleanup(func(ctx SpecContext) { _ = k8sClient.Delete(ctx, pod) })
		}

		workload := &unstructured.Unstructured{}
		workload.SetAPIVersion("kueue.x-k8s.io/v1beta2")
		workload.SetKind("Workload")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: testNamespace, Name: "karta-e2e-workload-running",
			}, workload)).To(Succeed())
			g.Expect(CondFalse("Admitted")(workload)).To(BeTrue())
		}, time.Minute, time.Second).Should(Succeed())

		// Observe starts from this queued object's resourceVersion, so the
		// watch includes every update after releasing the namespace's queue.
		Expect(k8sClient.Patch(ctx, queue, client.RawPatch(types.MergePatchType,
			[]byte(`{"spec":{"stopPolicy":"None"}}`)))).To(Succeed())
		rec := recorder.New(cfg).SetTimeout(4*time.Minute).
			AddState(kartav1alpha1.InitializingStatus, AllOf(CondFalse("Admitted"), CondNotTrue("Finished"))).
			AddState(kartav1alpha1.RunningStatus, AllOf(CondTrue("Admitted"), CondTrue("PodsReady"),
				CondNotTrue("Finished"), CondNotTrue("Evicted"), CondNotTrue("WaitingForReplacementPods")))
		out, err := recorder.NewFlow(rec, "running", "").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Observe(ctx, workload)
		Expect(rec.Save(recorder.Fixture{
			Operator: "kueue", Version: operatorVersion("kueue"),
			KartaName: "kueue-x-k8s-io-workload-v1beta2",
			KartaFile: "docs/catalog/kueue-x-k8s-io-workload-v1beta2.yaml",
		}, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})

var _ = Describe("Kueue Workload lifecycle", Ordered, Label("kueue"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/kueue-x-k8s-io-workload-v1beta2.yaml", "kueue-x-k8s-io-workload-v1beta2")
		queues := &unstructured.UnstructuredList{}
		Expect(yaml.Unmarshal(readE2E("flows/testdata/kueue/queues.yaml"), queues)).To(Succeed())
		for _, item := range queues.Items {
			switch item.GetKind() {
			case "LocalQueue":
				item.SetNamespace(testNamespace)
			case "ClusterQueue":
				Expect(unstructured.SetNestedStringMap(item.Object, map[string]string{
					"kubernetes.io/metadata.name": testNamespace,
				}, "spec", "namespaceSelector", "matchLabels")).To(Succeed())
			}
			Expect(k8sClient.Create(ctx, &item)).To(Succeed())
			DeferCleanup(func(ctx SpecContext) {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &item))).To(Succeed())
			})
		}
		fx = recorder.Fixture{
			Operator: "kueue", Version: operatorVersion("kueue"),
			KartaName: "kueue-x-k8s-io-workload-v1beta2",
			KartaFile: "docs/catalog/kueue-x-k8s-io-workload-v1beta2.yaml",
		}
		active := func(u *unstructured.Unstructured) bool {
			value, found, _ := unstructured.NestedBool(u.Object, "spec", "active")
			return !found || value
		}
		inactive := func(u *unstructured.Unstructured) bool { return !active(u) }
		rec = recorder.New(cfg).SetTimeout(4*time.Minute).
			AddState(kartav1alpha1.InitializingStatus, AllOf(active, CondFalse("Admitted"), CondNotTrue("Finished"))).
			AddState(kartav1alpha1.RunningStatus, AllOf(active, CondTrue("Admitted"), CondTrue("PodsReady"),
				CondNotTrue("Finished"), CondNotTrue("Evicted"), CondNotTrue("WaitingForReplacementPods"))).
			AddState(kartav1alpha1.SuspendedStatus, AllOf(inactive, CondNotTrue("QuotaReserved"), CondNotTrue("Finished"))).
			AddState(kartav1alpha1.FailedStatus, func(u *unstructured.Unstructured) bool {
				return CondReason("Finished", "Failed")(u) || CondReason("Finished", "FailedToStart")(u)
			}).
			AddState(kartav1alpha1.CompletedStatus, CondReason("Finished", "Succeeded"))
	})

	It("pending", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "pending", "testdata/kueue/pending.yaml").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).With(CondFalse("QuotaReserved")),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("keeps admission without Pods Undefined", func(ctx SpecContext) {
		workload := &unstructured.Unstructured{}
		Expect(yaml.Unmarshal(readE2E("flows/testdata/kueue/admitted.yaml"), workload)).To(Succeed())
		workload.SetNamespace(testNamespace)
		Expect(k8sClient.Create(ctx, workload)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, workload))).To(Succeed())
		})
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(workload), workload)).To(Succeed())
			g.Expect(CondTrue("Admitted")(workload)).To(BeTrue())
		}, time.Minute, time.Second).Should(Succeed())
		Expect(CondTrue("PodsReady")(workload)).To(BeFalse())
		expectKueueStatus(ctx, workload, kartav1alpha1.UndefinedStatus)
	})

	It("inactive", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "inactive", "testdata/kueue/inactive.yaml").Through(
			recorder.Reaches(kartav1alpha1.SuspendedStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("suspends and resumes a running Job", func(ctx SpecContext) {
		job, workload := createKueueJob(ctx, "job.yaml")
		waitForKueueJobExecution(ctx, job, workload)
		out, err := recorder.NewFlow(rec, "job-suspended", "").Through(
			recorder.Reaches(kartav1alpha1.RunningStatus).Do(kueueLifecycleAction(ctx, workload, recorder.ActionSuspend)),
			recorder.Reaches(kartav1alpha1.SuspendedStatus),
		).Observe(ctx, workload)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(job), job)).To(Succeed())
			g.Expect(BoolTrue("spec", "suspend")(job)).To(BeTrue())
			active, _, err := unstructured.NestedInt64(job.Object, "status", "active")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(active).To(BeZero())
			pods := &corev1.PodList{}
			g.Expect(k8sClient.List(ctx, pods, client.InNamespace(testNamespace), client.MatchingLabels{
				"batch.kubernetes.io/controller-uid": string(job.GetUID()),
			})).To(Succeed())
			g.Expect(pods.Items).To(BeEmpty())
		}, time.Minute, time.Second).Should(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(workload), workload)).To(Succeed())
		expectKueueStatus(ctx, workload, kartav1alpha1.SuspendedStatus)
		out, err = recorder.NewFlow(rec, "job-resumed", "").Through(
			recorder.Reaches(kartav1alpha1.SuspendedStatus).Do(kueueLifecycleAction(ctx, workload, recorder.ActionResume)),
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Observe(ctx, workload)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
		waitForKueueJobExecution(ctx, job, workload)
	})

	It("failed Job", func(ctx SpecContext) {
		job, workload := createKueueJob(ctx, "job_failed.yaml")
		out, err := recorder.NewFlow(rec, "job-failed", "").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).Optional(),
			recorder.Reaches(kartav1alpha1.FailedStatus),
		).Observe(ctx, workload)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(job), job)).To(Succeed())
		Expect(CondTrue("Failed")(job)).To(BeTrue())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(workload), workload)).To(Succeed())
		Expect(CondReason("Finished", "Failed")(workload)).To(BeTrue())
		expectKueueStatus(ctx, workload, kartav1alpha1.FailedStatus)
	})

	It("completed Job", func(ctx SpecContext) {
		job, workload := createKueueJob(ctx, "job_completed.yaml")
		out, err := recorder.NewFlow(rec, "job-completed", "").Through(
			recorder.Reaches(kartav1alpha1.InitializingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).Optional(),
			recorder.Reaches(kartav1alpha1.CompletedStatus),
		).Observe(ctx, workload)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(job), job)).To(Succeed())
		Expect(CondTrue("Complete")(job)).To(BeTrue())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(workload), workload)).To(Succeed())
		Expect(CondReason("Finished", "Succeeded")(workload)).To(BeTrue())
		expectKueueStatus(ctx, workload, kartav1alpha1.CompletedStatus)
	})
})

func createKueueJob(ctx SpecContext, manifest string) (*unstructured.Unstructured, *unstructured.Unstructured) {
	job := &unstructured.Unstructured{}
	Expect(yaml.Unmarshal(readE2E("flows/testdata/kueue/"+manifest), job)).To(Succeed())
	job.SetNamespace(testNamespace)
	Expect(k8sClient.Create(ctx, job)).To(Succeed())
	var workload *unstructured.Unstructured
	DeferCleanup(func(ctx SpecContext) {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, job,
			client.PropagationPolicy(metav1.DeletePropagationForeground)))).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(job), job))).To(BeTrue())
			if workload != nil {
				g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(workload), workload))).To(BeTrue(),
					"the Job's dependent Workload must also be deleted")
			}
		}, time.Minute, time.Second).Should(Succeed())
	})
	Eventually(func(g Gomega) {
		workloads := &unstructured.UnstructuredList{}
		workloads.SetAPIVersion("kueue.x-k8s.io/v1beta2")
		workloads.SetKind("WorkloadList")
		g.Expect(k8sClient.List(ctx, workloads, client.InNamespace(testNamespace))).To(Succeed())
		for _, item := range workloads.Items {
			if owner := metav1.GetControllerOf(&item); owner != nil && owner.UID == job.GetUID() {
				workload = item.DeepCopy()
				break
			}
		}
		g.Expect(workload).NotTo(BeNil(), "Kueue must create a Workload owned by the Job")
	}, time.Minute, time.Second).Should(Succeed())
	return job, workload
}

func kueueLifecycleAction(ctx SpecContext, workload *unstructured.Unstructured, actionType recorder.ActionType) *recorder.Action {
	definition, err := catalog.Get(workload.GroupVersionKind())
	Expect(err).NotTo(HaveOccurred())
	factory := resource.NewComponentFactoryFromObject(definition, workload.DeepCopy())
	root, err := factory.GetRootComponent()
	Expect(err).NotTo(HaveOccurred())
	Expect(root.HasSuspendDefinition()).To(BeTrue())
	switch actionType {
	case recorder.ActionSuspend:
		Expect(root.Suspend(ctx)).To(Succeed())
	case recorder.ActionResume:
		Expect(root.Resume(ctx)).To(Succeed())
	default:
		Fail("unsupported Kueue lifecycle action: " + string(actionType))
	}
	updated, err := factory.GetResource()
	Expect(err).NotTo(HaveOccurred())
	patch, err := client.MergeFrom(workload).Data(updated)
	Expect(err).NotTo(HaveOccurred())
	return &recorder.Action{Type: actionType, Patch: patch}
}

func expectKueueStatus(ctx SpecContext, workload *unstructured.Unstructured, want kartav1alpha1.ResourceStatus) {
	definition, err := catalog.Get(schema.GroupVersionKind{Group: "kueue.x-k8s.io", Version: "v1beta2", Kind: "Workload"})
	Expect(err).NotTo(HaveOccurred())
	root, err := resource.NewComponentFactoryFromObject(definition, workload).GetRootComponent()
	Expect(err).NotTo(HaveOccurred())
	status, err := root.GetStatus(ctx)
	Expect(err).NotTo(HaveOccurred())
	Expect(status.MatchedStatuses).To(ConsistOf(want))
}

func waitForKueueJobExecution(ctx SpecContext, job, workload *unstructured.Unstructured) {
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(workload), workload)).To(Succeed())
		g.Expect(CondTrue("Admitted")(workload)).To(BeTrue())
		g.Expect(CondTrue("PodsReady")(workload)).To(BeTrue())
		g.Expect(CondTrue("Evicted")(workload)).To(BeFalse())
		pods := &corev1.PodList{}
		g.Expect(k8sClient.List(ctx, pods, client.InNamespace(testNamespace), client.MatchingLabels{
			"batch.kubernetes.io/controller-uid": string(job.GetUID()),
		})).To(Succeed())
		g.Expect(pods.Items).To(HaveLen(1))
		g.Expect(pods.Items[0].DeletionTimestamp).To(BeNil())
		g.Expect(pods.Items[0].Status.Phase).To(Equal(corev1.PodRunning))
		g.Expect(metav1.GetControllerOf(&pods.Items[0])).NotTo(BeNil())
		g.Expect(metav1.GetControllerOf(&pods.Items[0]).UID).To(Equal(job.GetUID()))
	}, 3*time.Minute, time.Second).Should(Succeed())
	expectKueueStatus(ctx, workload, kartav1alpha1.RunningStatus)
}
