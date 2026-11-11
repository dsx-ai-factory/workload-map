// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/version"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

var _ = Describe("Grove PodCliqueSet", Ordered, Label("grove"), func() {
	var rec *recorder.Recorder
	var fx recorder.Fixture

	BeforeAll(func(ctx SpecContext) {
		installKarta(ctx, "../../docs/catalog/grove-io-podcliqueset-v1alpha1.yaml", "grove-io-podcliqueset-v1alpha1")
		fx = recorder.Fixture{Operator: "grove", Version: operatorVersion("grove"), KartaName: "grove-io-podcliqueset-v1alpha1", KartaFile: "docs/catalog/grove-io-podcliqueset-v1alpha1.yaml"}
		rec = recorder.New(cfg).
			SetTimeout(4*time.Minute).
			AddState(kartav1alpha1.ProgressingStatus, groveProgressing).
			AddState(kartav1alpha1.PendingStatus, grovePending).
			AddState(kartav1alpha1.RunningStatus, groveRunning)
	})

	It("running", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "running", "testdata/grove/running.yaml").Through(
			recorder.Reaches(kartav1alpha1.PendingStatus),
			recorder.Reaches(kartav1alpha1.RunningStatus),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("updated", func(ctx SpecContext) {
		supported, err := groveSupportsUpdateCondition(fx.Version)
		Expect(err).NotTo(HaveOccurred())
		if !supported {
			Skip("root UpdateInProgress requires Grove v0.1.0-alpha.14 or newer")
		}
		manifest := "testdata/grove/updated.yaml"
		patch, err := groveWorkerUpdatePatch(manifest)
		Expect(err).NotTo(HaveOccurred())
		var originalPods []types.UID
		var originalHash string
		out, err := recorder.NewFlow(rec, "updated", manifest).Through(
			recorder.Reaches(kartav1alpha1.PendingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(func(u *unstructured.Unstructured) bool {
				if !currentGeneration(u) || groveProgressing(u) {
					return false
				}
				originalHash, _, _ = unstructured.NestedString(u.Object, "status", "currentGenerationHash")
				pods := &corev1.PodList{}
				Expect(k8sClient.List(ctx, pods, client.InNamespace(u.GetNamespace()),
					client.MatchingLabels{"app.kubernetes.io/part-of": u.GetName()})).To(Succeed())
				originalPods = groveReadyPodUIDs(pods.Items)
				return originalHash != "" && len(pods.Items) == 2 && len(originalPods) == 2
			}).Do(&recorder.Action{Type: "Update", Patch: patch}),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(func(u *unstructured.Unstructured) bool {
				hash, _, _ := unstructured.NestedString(u.Object, "status", "currentGenerationHash")
				if hash == "" || hash == originalHash || !groveRolloutComplete(u) {
					return false
				}
				pods := &corev1.PodList{}
				Expect(k8sClient.List(ctx, pods, client.InNamespace(u.GetNamespace()),
					client.MatchingLabels{"app.kubernetes.io/part-of": u.GetName()})).To(Succeed())
				return groveReplacementPodsReady(pods.Items, originalPods)
			}),
		).Run(ctx)
		// Grove's generation watermark advances after convergence. The recorder preserves
		// those intermediate frames but deliberately excludes them from journey gates.
		if err == nil && !groveRecordedUpdateProgress(out) {
			err = fmt.Errorf("updated flow did not record native update progress with a lagging observedGeneration")
			out.Result.Succeeded = false
			out.Result.Failures = append(out.Result.Failures, err.Error())
		}
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("pending", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "pending", "testdata/grove/pending.yaml").Through(
			recorder.Reaches(kartav1alpha1.PendingStatus).With(currentGeneration),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})

	It("scaled", func(ctx SpecContext) {
		out, err := recorder.NewFlow(rec, "scaled", "testdata/grove/scaled.yaml").Through(
			recorder.Reaches(kartav1alpha1.PendingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(IntEq(1, "status", "availableReplicas")).Do(ScaleReplicas(2)),
			recorder.Reaches(kartav1alpha1.PendingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(IntEq(2, "status", "availableReplicas")).Do(ScaleReplicas(1)),
			recorder.Reaches(kartav1alpha1.PendingStatus).Optional(),
			recorder.Reaches(kartav1alpha1.RunningStatus).With(IntEq(1, "status", "availableReplicas")),
		).Run(ctx)
		Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
		Expect(err).To(Succeed())
	})
})

func groveSupportsUpdateCondition(installed string) (bool, error) {
	actual, err := version.ParseSemantic(installed)
	if err != nil {
		return false, err
	}
	// Grove spells alpha release candidates alpha.N-rcM. Compare the alpha
	// release first, and exclude candidates of the minimum supported release.
	alpha, _, releaseCandidate := strings.Cut(installed, "-rc")
	if releaseCandidate && strings.Contains(alpha, "-alpha.") {
		actual, err = version.ParseSemantic(alpha)
		if err != nil {
			return false, err
		}
	} else {
		releaseCandidate = false
	}
	minimum := version.MustParseSemantic("v0.1.0-alpha.14")
	return !actual.LessThan(minimum) && (!releaseCandidate || minimum.LessThan(actual)), nil
}

func groveRolloutComplete(u *unstructured.Unstructured) bool {
	ended, _, _ := unstructured.NestedString(u.Object, "status", "updateProgress", "updateEndedAt")
	updated, found, _ := unstructured.NestedInt64(u.Object, "status", "updatedReplicas")
	desired, _, _ := unstructured.NestedInt64(u.Object, "spec", "replicas")
	return currentGeneration(u) && groveRunning(u) && ended != "" && found && updated == desired &&
		operatorConditionReason("UpdateInProgress", "False", "NoActiveUpdate")(u) && !groveProgressing(u)
}

func groveRecordedUpdateProgress(recording *recorder.Recording) bool {
	if recording == nil {
		return false
	}
	updated := false
	for _, event := range recording.Events {
		if event.Kind == recorder.EventAction && event.Action != nil && event.Action.Name == "Update" {
			updated = true
		}
		if !updated || event.Kind != recorder.EventState || !slices.Contains(event.Phases, string(kartav1alpha1.ProgressingStatus)) {
			continue
		}
		u := &unstructured.Unstructured{Object: event.Object}
		observed, found, _ := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
		if found && observed < u.GetGeneration() && CondTrue("UpdateInProgress")(u) && groveProgressing(u) {
			return true
		}
	}
	return false
}

func groveReadyPodUIDs(pods []corev1.Pod) []types.UID {
	var uids []types.UID
	for _, pod := range pods {
		if pod.UID == "" || pod.DeletionTimestamp != nil {
			continue
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				uids = append(uids, pod.UID)
				break
			}
		}
	}
	return uids
}

func groveReplacementPodsReady(pods []corev1.Pod, original []types.UID) bool {
	uids := groveReadyPodUIDs(pods)
	if len(original) != 2 || len(pods) != 2 || len(uids) != 2 {
		return false
	}
	for _, pod := range pods {
		if slices.Contains(original, pod.UID) || pod.Annotations["karta-e2e-update"] != "replacement" {
			return false
		}
	}
	return true
}

// A merge patch replaces the clique array, so preserve every other template field.
func groveWorkerUpdatePatch(manifest string) ([]byte, error) {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := yaml.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	cliques, _, err := unstructured.NestedSlice(object, "spec", "template", "cliques")
	if err != nil {
		return nil, err
	}
	found := false
	for _, value := range cliques {
		clique, ok := value.(map[string]any)
		if !ok || clique["name"] != "worker" {
			continue
		}
		if err := unstructured.SetNestedField(clique, "replacement", "annotations", "karta-e2e-update"); err != nil {
			return nil, err
		}
		found = true
	}
	if !found {
		return nil, fmt.Errorf("worker clique missing from %s", manifest)
	}
	return json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"cliques": cliques}}})
}
