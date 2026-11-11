// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package karta_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog/kartas"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/karta"
)

const jobsetYAML = `
apiVersion: jobset.x-k8s.io/v1alpha2
kind: JobSet
metadata:
  name: training
spec:
  replicatedJobs:
  - name: driver
    replicas: 1
    template:
      spec:
        parallelism: 1
        template:
          metadata:
            labels:
              team: ml
          spec:
            containers:
            - name: main
              image: trainer:v1
  - name: workers
    replicas: 2
    template:
      spec:
        parallelism: 4
        template:
          metadata:
            labels:
              team: ml
          spec:
            containers:
            - name: main
              image: trainer:v1
              env:
              - name: MODE
                value: worker
            - name: sidecar
              image: proxy:v1
`

const nimServiceYAML = `
apiVersion: apps.nvidia.com/v1alpha1
kind: NIMService
metadata:
  name: llm
spec:
  image:
    repository: nvcr.io/nim/llm
  replicas: 1
  labels:
    team: ml
`

const kserveYAML = `
apiVersion: serving.kserve.io/v1beta1
kind: InferenceService
metadata:
  name: model
spec:
  predictor:
    minReplicas: 1
    containers:
    - name: kserve-container
      image: server:v1
  transformer:
    minReplicas: 2
    containers:
    - name: transformer
      image: transformer:v1
`

func load(def *v1alpha1.Karta, manifest string) karta.Workload {
	obj := &unstructured.Unstructured{}
	Expect(yaml.Unmarshal([]byte(manifest), &obj.Object)).To(Succeed())
	w, err := karta.Load(def, obj)
	Expect(err).NotTo(HaveOccurred())
	return w
}

func setScheduler(_ string, pod *corev1.PodTemplateSpec) error {
	pod.Spec.SchedulerName = "kai-scheduler"
	return nil
}

var _ = Describe("Load", func() {
	It("rejects an invalid definition", func() {
		_, err := karta.Compile(&v1alpha1.Karta{})
		Expect(err).To(MatchError(karta.ErrInvalidDefinition))
	})

	It("rejects an object of another kind", func() {
		obj := &unstructured.Unstructured{}
		Expect(yaml.Unmarshal([]byte(nimServiceYAML), &obj.Object)).To(Succeed())
		_, err := karta.Load(kartas.Jobset(), obj)
		Expect(err).To(MatchError(karta.ErrInvalidObject))
	})

	It("never aliases the caller object", func() {
		obj := &unstructured.Unstructured{}
		Expect(yaml.Unmarshal([]byte(jobsetYAML), &obj.Object)).To(Succeed())
		original := obj.DeepCopy()
		w, err := karta.Load(kartas.Jobset(), obj)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.MutatePodTemplates(context.Background(), "replicatedjob", setScheduler)
		Expect(err).NotTo(HaveOccurred())
		Expect(obj).To(Equal(original))
	})

	It("describes writable fields per definition style", func() {
		def, err := karta.Compile(kartas.NIMService())
		Expect(err).NotTo(HaveOccurred())
		Expect(def.Components()[0].PodSource).To(Equal(karta.PodSourceFragments))
		Expect(def.Components()[0].Writable).To(ContainElements("spec.schedulerName", "metadata.labels", "spec.containers.0.resources"))

		def, err = karta.Compile(kartas.Jobset())
		Expect(err).NotTo(HaveOccurred())
		job := def.Components()[1]
		Expect(job.MultiInstance).To(BeTrue())
		Expect(job.Writable).To(Equal([]string{"metadata", "spec"}))
	})
})

var _ = Describe("Read", func() {
	It("projects whole pod templates per instance", func() {
		w := load(kartas.Jobset(), jobsetYAML)
		snapshot, err := w.Read(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.Components[0].Parent).To(BeEmpty())

		job, ok := snapshot.Get("replicatedjob")
		Expect(ok).To(BeTrue())
		Expect(job.Instances).To(HaveLen(2))
		Expect(job.Instances[0].ID).To(Equal("driver"))
		Expect(job.Instances[1].Present).To(BeTrue())
		Expect(job.Instances[1].Pod.Spec.Containers).To(HaveLen(2))
		Expect(*job.Instances[1].Scale.Replicas).To(Equal(int32(8)))
	})

	It("projects fragments into the same pod view", func() {
		w := load(kartas.NIMService(), nimServiceYAML)
		component, err := w.Component(context.Background(), "nimservice")
		Expect(err).NotTo(HaveOccurred())
		Expect(component.Instances[0].Pod.Labels).To(HaveKeyWithValue("team", "ml"))
		Expect(component.Instances[0].Fragments).NotTo(BeNil())
	})

	It("reports an optional component that the object does not hold", func() {
		w := load(kartas.KServe(), `
apiVersion: serving.kserve.io/v1beta1
kind: InferenceService
metadata:
  name: model
spec:
  predictor:
    containers:
    - name: kserve-container
      image: server:v1
`)
		transformer, err := w.Component(context.Background(), "transformer")
		Expect(err).NotTo(HaveOccurred())
		Expect(transformer.Instances[0].Present).To(BeFalse())
		Expect(transformer.Instances[0].Pod).To(BeNil())
	})
})

var _ = Describe("MutatePodTemplates", func() {
	var ctx context.Context
	BeforeEach(func() { ctx = context.Background() })

	field := func(w karta.Workload, path ...string) any {
		value, _, err := unstructured.NestedFieldNoCopy(w.Object().Object, path...)
		Expect(err).NotTo(HaveOccurred())
		return value
	}
	replicatedJob := func(w karta.Workload, i int) map[string]any {
		jobs := field(w, "spec", "replicatedJobs").([]any)
		return jobs[i].(map[string]any)
	}
	podSpec := func(job map[string]any) map[string]any {
		spec, _, _ := unstructured.NestedMap(job, "template", "spec", "template", "spec")
		return spec
	}

	Context("whole pod template", func() {
		It("writes every instance by default", func() {
			w := load(kartas.Jobset(), jobsetYAML)
			changed, err := w.MutatePodTemplates(ctx, "replicatedjob", setScheduler)
			Expect(err).NotTo(HaveOccurred())
			Expect(changed).To(BeTrue())
			Expect(w.Modified()).To(BeTrue())
			Expect(podSpec(replicatedJob(w, 0))).To(HaveKeyWithValue("schedulerName", "kai-scheduler"))
			Expect(podSpec(replicatedJob(w, 1))).To(HaveKeyWithValue("schedulerName", "kai-scheduler"))
		})

		It("touches only the targeted instances", func() {
			w := load(kartas.Jobset(), jobsetYAML)
			driverBefore := replicatedJob(w, 0)
			_, err := w.MutatePodTemplates(ctx, "replicatedjob", func(_ string, pod *corev1.PodTemplateSpec) error {
				pod.Spec.Containers[0].Image = "trainer:v2"
				pod.Spec.Tolerations = append(pod.Spec.Tolerations, corev1.Toleration{Key: "gpu", Operator: corev1.TolerationOpExists})
				return nil
			}, karta.OnInstances("workers"))
			Expect(err).NotTo(HaveOccurred())
			Expect(replicatedJob(w, 0)).To(Equal(driverBefore))
			workers := podSpec(replicatedJob(w, 1))
			containers := workers["containers"].([]any)
			Expect(containers[0]).To(HaveKeyWithValue("image", "trainer:v2"))
			Expect(containers[0]).To(HaveKey("env"))
			Expect(containers[1]).To(HaveKeyWithValue("image", "proxy:v1"))
			Expect(workers["tolerations"]).To(HaveLen(1))
		})

		It("deletes what the callback removes", func() {
			w := load(kartas.Jobset(), jobsetYAML)
			_, err := w.MutatePodTemplates(ctx, "replicatedjob", func(_ string, pod *corev1.PodTemplateSpec) error {
				delete(pod.Labels, "team")
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
			labels, _, _ := unstructured.NestedMap(replicatedJob(w, 0), "template", "spec", "template", "metadata", "labels")
			Expect(labels).NotTo(HaveKey("team"))
		})

		It("reports an untouched object as unchanged", func() {
			w := load(kartas.Jobset(), jobsetYAML)
			changed, err := w.MutatePodTemplates(ctx, "replicatedjob", func(string, *corev1.PodTemplateSpec) error { return nil })
			Expect(err).NotTo(HaveOccurred())
			Expect(changed).To(BeFalse())
			Expect(w.Modified()).To(BeFalse())
		})

		It("fails on an unknown instance before writing", func() {
			w := load(kartas.Jobset(), jobsetYAML)
			before := w.Object()
			_, err := w.MutatePodTemplates(ctx, "replicatedjob", setScheduler, karta.OnInstances("typo"))
			Expect(err).To(MatchError(karta.ErrInstanceNotFound))
			Expect(w.Object()).To(Equal(before))
		})

		It("keeps the object unchanged when the callback fails on a later instance", func() {
			w := load(kartas.Jobset(), jobsetYAML)
			before := w.Object()
			boom := errors.New("boom")
			_, err := w.MutatePodTemplates(ctx, "replicatedjob", func(id string, pod *corev1.PodTemplateSpec) error {
				if id == "workers" {
					return boom
				}
				pod.Spec.SchedulerName = "kai-scheduler"
				return nil
			})
			Expect(err).To(MatchError(boom))
			Expect(w.Object()).To(Equal(before))
			Expect(w.Modified()).To(BeFalse())
		})

		It("rejects an unknown component and a component without pods", func() {
			w := load(kartas.Jobset(), jobsetYAML)
			_, err := w.MutatePodTemplates(ctx, "nope", setScheduler)
			Expect(err).To(MatchError(karta.ErrComponentNotFound))
			_, err = w.MutatePodTemplates(ctx, "jobset", setScheduler)
			Expect(err).To(MatchError(karta.ErrNoPodDefinition))
		})
	})

	Context("fragmented pod definition", func() {
		It("routes each field to its own path", func() {
			w := load(kartas.NIMService(), nimServiceYAML)
			_, err := w.MutatePodTemplates(ctx, "nimservice", func(_ string, pod *corev1.PodTemplateSpec) error {
				pod.Spec.SchedulerName = "kai-scheduler"
				pod.Labels["queue"] = "a"
				pod.Spec.Affinity = &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
						MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "gpu", Operator: corev1.NodeSelectorOpExists}},
					}}},
				}}
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(field(w, "spec", "schedulerName")).To(Equal("kai-scheduler"))
			Expect(field(w, "spec", "labels")).To(Equal(map[string]any{"team": "ml", "queue": "a"}))
			Expect(field(w, "spec", "affinity", "nodeAffinity")).NotTo(BeNil())
			Expect(field(w, "spec", "image")).To(Equal(map[string]any{"repository": "nvcr.io/nim/llm"}))
		})

		It("bridges the sole container resources to the fragment path", func() {
			w := load(kartas.NIMService(), nimServiceYAML)
			_, err := w.MutatePodTemplates(ctx, "nimservice", func(_ string, pod *corev1.PodTemplateSpec) error {
				pod.Spec.Containers = []corev1.Container{{Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{"nvidia.com/gpu": apiresource.MustParse("1")},
				}}}
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(field(w, "spec", "resources", "limits", "nvidia.com/gpu")).To(Equal("1"))
		})

		It("refuses a field the definition cannot route and writes nothing", func() {
			w := load(kartas.NIMService(), nimServiceYAML)
			before := w.Object()
			_, err := w.MutatePodTemplates(ctx, "nimservice", func(_ string, pod *corev1.PodTemplateSpec) error {
				pod.Spec.SchedulerName = "kai-scheduler"
				pod.Spec.Tolerations = []corev1.Toleration{{Key: "gpu", Operator: corev1.TolerationOpExists}}
				return nil
			})
			Expect(err).To(MatchError(karta.ErrFieldNotRoutable))
			var fieldErr *karta.FieldError
			Expect(errors.As(err, &fieldErr)).To(BeTrue())
			Expect(fieldErr.Field).To(Equal("spec.tolerations"))
			Expect(w.Object()).To(Equal(before))
		})
	})

	Context("pod spec with metadata at the same path", func() {
		It("keeps CR fields that are not pod fields", func() {
			w := load(kartas.KServe(), kserveYAML)
			_, err := w.MutatePodTemplates(ctx, "transformer", func(_ string, pod *corev1.PodTemplateSpec) error {
				pod.Spec.SchedulerName = "kai-scheduler"
				pod.Labels = map[string]string{"queue": "a"}
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(field(w, "spec", "transformer", "minReplicas")).To(BeEquivalentTo(2))
			Expect(field(w, "spec", "transformer", "schedulerName")).To(Equal("kai-scheduler"))
			Expect(field(w, "spec", "transformer", "labels")).To(Equal(map[string]any{"queue": "a"}))
		})

		It("does not create an absent component", func() {
			w := load(kartas.KServe(), `
apiVersion: serving.kserve.io/v1beta1
kind: InferenceService
metadata:
  name: model
spec:
  predictor:
    containers:
    - name: kserve-container
      image: server:v1
`)
			_, err := w.MutatePodTemplates(ctx, "transformer", setScheduler)
			Expect(err).To(MatchError(karta.ErrComponentAbsent))
			changed, err := w.MutatePodTemplates(ctx, "transformer", setScheduler, karta.SkipAbsent())
			Expect(err).NotTo(HaveOccurred())
			Expect(changed).To(BeFalse())
			Expect(field(w, "spec", "transformer")).To(BeNil())
		})
	})
})

var _ = Describe("Suspend and Resume", func() {
	It("applies the definition actions", func() {
		w := load(kartas.Jobset(), jobsetYAML)
		Expect(w.CanSuspend()).To(BeTrue())
		Expect(w.Suspend(context.Background())).To(Succeed())
		suspended, _, _ := unstructured.NestedBool(w.Object().Object, "spec", "suspend")
		Expect(suspended).To(BeTrue())
		Expect(w.Resume(context.Background())).To(Succeed())
		suspended, _, _ = unstructured.NestedBool(w.Object().Object, "spec", "suspend")
		Expect(suspended).To(BeFalse())
	})

	It("fails when the definition cannot suspend", func() {
		w := load(kartas.NIMService(), nimServiceYAML)
		Expect(w.CanSuspend()).To(BeFalse())
		Expect(w.Suspend(context.Background())).To(MatchError(karta.ErrSuspendNotSupported))
	})
})
