// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package exporter_test

import (
	"context"
	"io"
	"maps"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/app"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/bootstrap"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/config"
)

type consistencySample struct {
	Labels map[string]string
	Value  float64
}

var _ = Describe("Metric consistency", func() {
	It("matches API ownership through reparenting, broken owners and UID replacement", SpecTimeout(90*time.Second), func(ctx SpecContext) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "exporter-consistency-"}}
		Expect(api.Create(ctx, ns)).To(Succeed())
		DeferCleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			Expect(api.Delete(cleanupCtx, ns)).To(Succeed())
		})
		rootA := deployment(ctx, ns.Name, "root-a")
		rootB := deployment(ctx, ns.Name, "root-b")
		rs := replicaSet(ctx, rootA, "middle")
		first, second := pod(ctx, rs, "first"), pod(ctx, rs, "second")
		standalone := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "standalone", Namespace: ns.Name},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "example.test/image:v1"}}},
		}
		Expect(api.Create(ctx, standalone)).To(Succeed())

		cfg := config.DefaultConfig()
		cfg.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": ns.Name}}
		cfg.KartaDefinitionSource.Catalog.Enabled = false
		cfg.KartaDefinitionSource.Cluster.Enabled = false
		cfg.RequiredKinds = []schema.GroupVersionKind{deployGVK}
		cfg.RequestTimeout = "1s"
		components, err := bootstrap.New(cfg, kubeConfig)
		Expect(err).NotTo(HaveOccurred())
		application, err := app.New(components)
		Expect(err).NotTo(HaveOccurred())
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		runCtx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- application.ServeListener(runCtx, listener) }()
		DeferCleanup(func() {
			cancel()
			Eventually(done, 10*time.Second).Should(Receive(Succeed()))
		})
		httpClient := &http.Client{Timeout: time.Second}
		DeferCleanup(httpClient.CloseIdleConnections)
		lastRoots := map[types.UID]client.Object{}
		phases := []string{"Initializing", "Running", "Completed", "Failed", "Degraded", "Suspended", "Undefined"}

		checkMetrics := func() {
			GinkgoHelper()
			// No controller writes these fixtures. Read the expected graph before polling HTTP.
			deployments, replicaSets, pods := &appsv1.DeploymentList{}, &appsv1.ReplicaSetList{}, &corev1.PodList{}
			Expect(api.List(ctx, deployments, client.InNamespace(ns.Name))).To(Succeed())
			Expect(api.List(ctx, replicaSets, client.InNamespace(ns.Name))).To(Succeed())
			Expect(api.List(ctx, pods, client.InNamespace(ns.Name))).To(Succeed())
			roots := map[types.UID]client.Object{}
			for i := range deployments.Items {
				root := &deployments.Items[i]
				roots[root.UID] = root
			}
			sets := map[types.UID]*appsv1.ReplicaSet{}
			for i := range replicaSets.Items {
				set := &replicaSets.Items[i]
				sets[set.UID] = set
			}
			expected := map[string][]consistencySample{
				"dsx_workload_pod_info": {}, "dsx_workload_status": {},
				"dsx_workload_created_timestamp_seconds": {}, "dsx_exporter_observation_valid": {},
			}
			failedRoots := map[types.UID]client.Object{}
			for i := range pods.Items {
				p := &pods.Items[i]
				var root client.Object = p
				valid := true
				if owner := metav1.GetControllerOf(p); owner != nil {
					Expect(owner.Kind).To(Equal("ReplicaSet"))
					if set := sets[owner.UID]; set != nil {
						Expect(set.Name).To(Equal(owner.Name))
						parent := metav1.GetControllerOf(set)
						Expect(parent).NotTo(BeNil())
						Expect(parent.Kind).To(Equal("Deployment"))
						root = roots[parent.UID]
						Expect(root).NotTo(BeNil())
						Expect(root.GetName()).To(Equal(parent.Name))
					} else {
						// An unresolved owner retains the previously confirmed attribution.
						root, valid = lastRoots[p.UID], false
						Expect(root).NotTo(BeNil(), "broken ownership requires a confirmed baseline")
						failedRoots[root.GetUID()] = root
					}
				} else {
					roots[p.UID] = p
				}
				if valid {
					lastRoots[p.UID] = root
				}
				labels := consistencyIdentity(root)
				labels["pod_name"], labels["pod_uid"] = p.Name, string(p.UID)
				labels["component"], labels["component_instance"], labels["replica_group_id"] = "<unknown>", "<unknown>", ""
				labels["owner_resolution"], labels["observation_valid"] = "resolved", strconv.FormatBool(valid)
				expected["dsx_workload_pod_info"] = append(expected["dsx_workload_pod_info"], consistencySample{Labels: labels, Value: 1})
			}
			for _, root := range roots {
				labels := consistencyIdentity(root)
				labels["owner_resolution"], labels["observation_valid"] = "resolved", "true"
				labels["karta"], labels["karta_source"] = "", ""
				for _, phase := range phases {
					phaseLabels := maps.Clone(labels)
					phaseLabels["phase"] = phase
					expected["dsx_workload_status"] = append(expected["dsx_workload_status"], consistencySample{Labels: phaseLabels})
				}
				expected["dsx_workload_created_timestamp_seconds"] = append(expected["dsx_workload_created_timestamp_seconds"], consistencySample{
					Labels: map[string]string{"namespace": ns.Name, "workload_name": root.GetName(), "workload_uid": string(root.GetUID())},
					Value:  float64(root.GetCreationTimestamp().Unix()),
				})
			}
			for _, kind := range []string{"Namespace", "Pod", "Deployment", "ReplicaSet"} {
				namespace, group := ns.Name, ""
				if kind == "Namespace" {
					namespace = ""
				}
				if kind == "Deployment" || kind == "ReplicaSet" {
					group = "apps"
				}
				expected["dsx_exporter_observation_valid"] = append(expected["dsx_exporter_observation_valid"], consistencySample{
					Labels: map[string]string{"scope": "resource_type", "namespace": namespace, "resource_group": group, "resource_version": "v1", "resource_kind": kind, "workload_name": "", "workload_uid": "", "reason": "none"}, Value: 1,
				})
			}
			for _, root := range failedRoots {
				expected["dsx_exporter_observation_valid"] = append(expected["dsx_exporter_observation_valid"], consistencySample{
					Labels: map[string]string{"scope": "workload", "namespace": ns.Name, "resource_group": "apps", "resource_version": "v1", "resource_kind": "Deployment", "workload_name": root.GetName(), "workload_uid": string(root.GetUID()), "reason": "unresolved_owner"},
				})
			}
			assertMetrics := func(g Gomega) {
				request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String()+"/metrics", nil)
				g.Expect(err).NotTo(HaveOccurred())
				response, err := httpClient.Do(request)
				g.Expect(err).NotTo(HaveOccurred())
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(response.StatusCode).To(Equal(http.StatusOK), string(body))
				parser := expfmt.NewTextParser(model.UTF8Validation)
				families, err := parser.TextToMetricFamilies(strings.NewReader(string(body)))
				g.Expect(err).NotTo(HaveOccurred(), string(body))
				for name, want := range expected {
					got := []consistencySample{}
					for _, metric := range families[name].GetMetric() {
						labels := map[string]string{}
						for _, label := range metric.GetLabel() {
							g.Expect(labels).NotTo(HaveKey(label.GetName()), name)
							labels[label.GetName()] = label.GetValue()
						}
						g.Expect(metric.Gauge).NotTo(BeNil(), name)
						got = append(got, consistencySample{Labels: labels, Value: metric.GetGauge().GetValue()})
					}
					// Exact multisets reject duplicate identities, missing rows and stale UIDs.
					g.Expect(got).To(ConsistOf(want), name)
				}
			}
			Eventually(assertMetrics).Should(Succeed())
			Consistently(assertMetrics, 300*time.Millisecond).Should(Succeed())
		}

		checkPodsUnchanged := func() {
			GinkgoHelper()
			for _, original := range []*corev1.Pod{first, second} {
				current := &corev1.Pod{}
				Expect(api.Get(ctx, client.ObjectKeyFromObject(original), current)).To(Succeed())
				Expect(current.UID).To(Equal(original.UID))
				Expect(current.ResourceVersion).To(Equal(original.ResourceVersion))
				Expect(current.OwnerReferences).To(Equal(original.OwnerReferences))
			}
		}

		By("publishing both roots, two owned Pods and a standalone Pod")
		checkMetrics()
		By("reparenting only the ReplicaSet from root A to root B")
		rs.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(rootB, deployGVK)}
		Expect(api.Update(ctx, rs)).To(Succeed())
		checkMetrics()
		checkPodsUnchanged()

		By("retaining confirmed Pod rows as invalid when their ReplicaSet disappears")
		oldSetUID := rs.UID
		deleteObjects(ctx, rs)
		checkMetrics()
		checkPodsUnchanged()
		By("rejecting a same-name ReplicaSet with a different UID")
		rs = replicaSet(ctx, rootB, rs.Name)
		Expect(rs.UID).NotTo(Equal(oldSetUID))
		checkMetrics()
		checkPodsUnchanged()
		By("recovering after both Pod owner references point to the new UID")
		for _, p := range []*corev1.Pod{first, second} {
			p.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(rs, appsv1.SchemeGroupVersion.WithKind("ReplicaSet"))}
			Expect(api.Update(ctx, p)).To(Succeed())
		}
		checkMetrics()

		By("removing a deleted Pod UID before recreating its name")
		oldPodUID := first.UID
		deleteObjects(ctx, first)
		checkMetrics()
		first = pod(ctx, rs, first.Name)
		Expect(first.UID).NotTo(Equal(oldPodUID))
		checkMetrics()
		By("removing workload metrics after all fixture objects are deleted")
		deleteObjects(ctx, first, second, standalone, rs, rootA, rootB)
		checkMetrics()
	})
})

func consistencyIdentity(root client.Object) map[string]string {
	group, kind := "apps", "Deployment"
	if _, isPod := root.(*corev1.Pod); isPod {
		group, kind = "", "Pod"
	}
	return map[string]string{
		"namespace": root.GetNamespace(), "workload_name": root.GetName(), "workload_uid": string(root.GetUID()),
		"workload_group": group, "workload_version": "v1", "workload_kind": kind,
	}
}
