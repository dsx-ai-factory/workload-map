// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package replay

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/karta"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

const probeLabel = "karta.dev/mutation-probe"

// Every recorded object of every catalog definition is mutated through the
// front door, one probe field at a time on each pod-bearing component. A
// field the component declares writable must write and read back; any other
// field must fail with ErrFieldNotRoutable and leave the object unchanged.
// The CR status and labels never change.
var _ = Describe("Karta mutates the recorded objects", func() {
	recordings, _ := filepath.Glob(recordedGlob)
	for _, path := range recordings {
		path := path
		It(strings.TrimPrefix(path, "../recorded_data/"), func(ctx SpecContext) {
			r, err := recorder.OpenRecording(path)
			Expect(err).NotTo(HaveOccurred())
			kartaYAML, err := os.ReadFile(filepath.Join(repoRoot, "docs", "catalog", filepath.Base(r.Recording().KartaFile)))
			Expect(err).NotTo(HaveOccurred())
			def := &kartav1alpha1.Karta{}
			Expect(yaml.Unmarshal(kartaYAML, def)).To(Succeed())
			compiled, err := karta.Compile(def)
			Expect(err).NotTo(HaveOccurred())

			for r.Next() {
				state, cr := r.State(), r.Object()
				for _, info := range compiled.Components() {
					if info.PodSource == karta.PodSourceNone {
						continue
					}
					for _, probe := range probes {
						where := fmt.Sprintf("%s: %s %s", state, info.Name, probe.field)
						w, err := compiled.Load(cr)
						Expect(err).NotTo(HaveOccurred(), where)
						statusBefore, err := w.Status(ctx)
						Expect(err).NotTo(HaveOccurred(), where)

						changed, err := w.MutatePodTemplates(ctx, info.Name, func(_ string, pod *corev1.PodTemplateSpec) error {
							probe.set(pod)
							return nil
						}, karta.SkipAbsent())
						if !writable(info, probe.field) {
							// SkipAbsent makes a probe on an absent component a no-op.
							if err != nil {
								Expect(err).To(MatchError(karta.ErrFieldNotRoutable), where)
							}
							Expect(changed).To(BeFalse(), where)
							Expect(w.Modified()).To(BeFalse(), where)
							continue
						}
						Expect(err).NotTo(HaveOccurred(), where)

						component, err := w.Component(ctx, info.Name)
						Expect(err).NotTo(HaveOccurred(), where)
						for _, instance := range component.Instances {
							if instance.Present {
								Expect(changed).To(BeTrue(), where)
								Expect(probe.get(instance.Pod)).To(BeTrue(), "%s/%s", where, instance.ID)
							}
						}

						statusAfter, err := w.Status(ctx)
						Expect(err).NotTo(HaveOccurred(), where)
						Expect(reflect.DeepEqual(statusAfter, statusBefore)).To(BeTrue(), "%s changed the status", where)
						after := w.Object()
						Expect(after.GroupVersionKind()).To(Equal(cr.GroupVersionKind()), where)
						// A root whose pod template is the CR itself (core Pod) owns the CR labels.
						if info.Parent != "" || probe.field != "metadata.labels" {
							Expect(reflect.DeepEqual(after.GetLabels(), cr.GetLabels())).To(BeTrue(), "%s changed the CR labels", where)
						}
						Expect(reflect.DeepEqual(unstructuredStatus(after), unstructuredStatus(cr))).To(BeTrue(), "%s changed the CR status", where)
					}
				}
			}
		})
	}
})

type probe struct {
	field string
	set   func(*corev1.PodTemplateSpec)
	get   func(*corev1.PodTemplateSpec) bool
}

var probes = []probe{
	{
		field: "spec.schedulerName",
		set:   func(pod *corev1.PodTemplateSpec) { pod.Spec.SchedulerName = "probe-scheduler" },
		get:   func(pod *corev1.PodTemplateSpec) bool { return pod.Spec.SchedulerName == "probe-scheduler" },
	},
	{
		field: "metadata.labels",
		set: func(pod *corev1.PodTemplateSpec) {
			if pod.Labels == nil {
				pod.Labels = map[string]string{}
			}
			pod.Labels[probeLabel] = "true"
		},
		get: func(pod *corev1.PodTemplateSpec) bool { return pod.Labels[probeLabel] == "true" },
	},
	{
		field: "spec.tolerations",
		set: func(pod *corev1.PodTemplateSpec) {
			pod.Spec.Tolerations = append(pod.Spec.Tolerations, corev1.Toleration{Key: probeLabel, Operator: corev1.TolerationOpExists})
		},
		get: func(pod *corev1.PodTemplateSpec) bool {
			return slices.ContainsFunc(pod.Spec.Tolerations, func(t corev1.Toleration) bool { return t.Key == probeLabel })
		},
	},
}

// writable reports whether the component declares field, or a parent of it,
// as writable.
func writable(info karta.ComponentInfo, field string) bool {
	return slices.ContainsFunc(info.Writable, func(w string) bool {
		return field == w || strings.HasPrefix(field, w+".")
	})
}

func unstructuredStatus(u *unstructured.Unstructured) any {
	status, _, _ := unstructured.NestedFieldNoCopy(u.Object, "status")
	return status
}
