// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package labelresolver

import (
	"context"
	"errors"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelevaluator"
)

const (
	teamExpression = `has(object.metadata.labels) && "example.com/team" in object.metadata.labels
  ? object.metadata.labels["example.com/team"] : ""`
	schedulerExpression = `has(object.spec.schedulerName) ? object.spec.schedulerName : ""`
)

var _ = Describe("New", func() {
	It("compiles every rule once with the configured limits", func() {
		limits := labelevaluator.Limits{CostLimit: 7, MaxExpressionBytes: 64, MaxResultBytes: 8}
		compiler := labelevaluator.NewMockLabelCompiler(GinkgoT())
		compiler.EXPECT().Compile(labelevaluator.Config{Expression: teamExpression, Limits: limits}).
			Return(labelevaluator.NewMockLabelEvaluator(GinkgoT()), nil).Once()
		compiler.EXPECT().Compile(labelevaluator.Config{Expression: schedulerExpression, Limits: limits}).
			Return(labelevaluator.NewMockLabelEvaluator(GinkgoT()), nil).Once()
		_, err := New(Config{Rules: []LabelRule{teamRule(MetricWorkloadStatus), schedulerRule()}, Limits: limits},
			Dependencies{Compiler: compiler})
		Expect(err).To(Succeed())
	})

	It("rejects an invalid expression", func() {
		rule := teamRule(MetricWorkloadStatus)
		rule.Expression = "object["
		_, err := New(Config{Rules: []LabelRule{rule}}, realCompiler())
		Expect(err).To(MatchError(ContainSubstring(`labelRules[0] "team"`)))
	})

	It("rejects invalid rules before compiling", func() {
		rule := teamRule(MetricWorkloadStatus)
		rule.Sources = []Source{SourceWorkload, SourcePod}
		_, err := New(Config{Rules: []LabelRule{rule}}, Dependencies{Compiler: labelevaluator.NewMockLabelCompiler(GinkgoT())})
		Expect(err).To(MatchError(ContainSubstring("cannot use a Pod source")))
	})

	for _, tc := range []struct {
		name   string
		limits labelevaluator.Limits
	}{
		{"negative expression limit", labelevaluator.Limits{MaxExpressionBytes: -1}},
		{"negative result limit", labelevaluator.Limits{MaxResultBytes: -1}},
	} {
		It("rejects a "+tc.name+" without rules", func() {
			_, err := New(Config{Limits: tc.limits}, realCompiler())
			Expect(err).To(MatchError(labelevaluator.ErrNegativeLimit))
		})
	}

	It("is not affected by changes to the configuration after construction", func(ctx SpecContext) {
		rule := teamRule(MetricPodWorkloadInfo)
		rule.Sources = []Source{SourceWorkload, SourcePod}
		rules := []LabelRule{rule}
		r, err := New(Config{Rules: rules}, realCompiler())
		Expect(err).To(Succeed())
		rules[0].Sources[1] = SourceWorkload
		rules[0].Metrics[0] = MetricWorkloadStatus
		rules[0].Name = "changed"
		results, err := r.Resolve(ctx, Input{
			Metrics:  []string{MetricPodWorkloadInfo},
			Workload: workload(nil),
			Pod:      pod("", map[string]string{"example.com/team": "from-pod"}),
		})
		Expect(err).To(Succeed())
		Expect(results[0].Labels).To(Equal(map[string]string{"team": "from-pod"}))
	})

	It("requires a compiler", func() {
		_, err := New(Config{}, Dependencies{})
		Expect(err).To(MatchError(ErrMissingCompiler))
	})
})

var _ = Describe("Resolve", func() {
	It("returns a root-only label for both metrics in request order", func(ctx SpecContext) {
		r := newResolver(teamRule(MetricWorkloadStatus, MetricPodWorkloadInfo))
		results, err := r.Resolve(ctx, Input{
			Metrics:  []string{MetricWorkloadStatus, MetricPodWorkloadInfo},
			Workload: workload(map[string]any{"example.com/team": "research"}),
			Pod:      pod("", map[string]string{"example.com/team": "other"}),
		})
		Expect(err).To(Succeed())
		Expect(results).To(Equal([]ResolvedMetricLabels{
			{Metric: MetricWorkloadStatus, Labels: map[string]string{"team": "research"}, Errors: map[string]error{}},
			{Metric: MetricPodWorkloadInfo, Labels: map[string]string{"team": "research"}, Errors: map[string]error{}},
		}))
	})

	It("reads Pod rules only for the Pod metric", func(ctx SpecContext) {
		r := newResolver(teamRule(MetricWorkloadStatus), schedulerRule())
		results, err := r.Resolve(ctx, Input{
			Metrics:  []string{MetricPodWorkloadInfo, MetricWorkloadStatus},
			Workload: workload(map[string]any{"example.com/team": "research"}),
			Pod:      pod("example-scheduler", nil),
		})
		Expect(err).To(Succeed())
		Expect(results).To(HaveLen(2))
		Expect(results[0].Labels).To(Equal(map[string]string{"scheduler_name": "example-scheduler"}))
		Expect(results[1].Labels).To(Equal(map[string]string{"team": "research"}))
	})

	It("does not need a Pod for workload metrics only", func(ctx SpecContext) {
		r := newResolver(teamRule(MetricWorkloadStatus))
		results, err := r.Resolve(ctx, Input{Metrics: []string{MetricWorkloadStatus}, Workload: workload(nil)})
		Expect(err).To(Succeed())
		Expect(results).To(Equal([]ResolvedMetricLabels{{Metric: MetricWorkloadStatus, Labels: map[string]string{}, Errors: map[string]error{}}}))
	})

	It("does not need a Pod when no rule for the requested metrics reads it", func(ctx SpecContext) {
		r := newResolver(teamRule(MetricWorkloadStatus, MetricPodWorkloadInfo))
		results, err := r.Resolve(ctx, Input{
			Metrics:  []string{MetricPodWorkloadInfo},
			Workload: workload(map[string]any{"example.com/team": "research"}),
		})
		Expect(err).To(Succeed())
		Expect(results[0].Labels).To(Equal(map[string]string{"team": "research"}))
	})

	It("exposes the Pod apiVersion and kind when the typed Pod has no TypeMeta", func(ctx SpecContext) {
		rule := LabelRule{
			Name: "kind", Metrics: []string{MetricPodWorkloadInfo}, Sources: []Source{SourcePod},
			Expression: `object.apiVersion + "/" + object.kind`,
		}
		p := pod("", nil)
		Expect(p.TypeMeta).To(BeZero())
		results, err := newResolver(rule).Resolve(ctx, Input{Metrics: []string{MetricPodWorkloadInfo}, Workload: workload(nil), Pod: p})
		Expect(err).To(Succeed())
		Expect(results[0].Labels).To(Equal(map[string]string{"kind": "v1/Pod"}))
		Expect(results[0].Errors).To(BeEmpty())
	})

	It("falls back to the Pod after an empty root value", func(ctx SpecContext) {
		rule := teamRule(MetricPodWorkloadInfo)
		rule.Sources = []Source{SourceWorkload, SourcePod}
		results, err := newResolver(rule).Resolve(ctx, Input{
			Metrics:  []string{MetricPodWorkloadInfo},
			Workload: workload(nil),
			Pod:      pod("", map[string]string{"example.com/team": "from-pod"}),
		})
		Expect(err).To(Succeed())
		Expect(results[0].Labels).To(Equal(map[string]string{"team": "from-pod"}))
		Expect(results[0].Errors).To(BeEmpty())
	})

	It("keeps the root error when the Pod fallback succeeds", func(ctx SpecContext) {
		rule := LabelRule{
			Name: "team", Metrics: []string{MetricPodWorkloadInfo}, Sources: []Source{SourceWorkload, SourcePod},
			Expression: `object.metadata.labels["example.com/team"]`,
		}
		results, err := newResolver(rule).Resolve(ctx, Input{
			Metrics:  []string{MetricPodWorkloadInfo},
			Workload: workload(map[string]any{}),
			Pod:      pod("", map[string]string{"example.com/team": "from-pod"}),
		})
		Expect(err).To(Succeed())
		Expect(results[0].Labels).To(Equal(map[string]string{"team": "from-pod"}))
		Expect(results[0].Errors).To(HaveKeyWithValue("team", MatchError(ContainSubstring("workload source"))))
	})

	It("retains the workload limit error after a successful Pod fallback", func(ctx SpecContext) {
		r, err := New(Config{
			Limits: labelevaluator.Limits{MaxResultBytes: 3},
			Rules: []LabelRule{{
				Name:       "team",
				Metrics:    []string{MetricPodWorkloadInfo},
				Sources:    []Source{SourceWorkload, SourcePod},
				Expression: `object.kind == "Pod" ? "pod" : "too-long"`,
			}},
		}, realCompiler())
		Expect(err).To(Succeed())
		results, err := r.Resolve(ctx, Input{Metrics: []string{MetricPodWorkloadInfo}, Workload: workload(nil), Pod: pod("", nil)})
		Expect(err).To(Succeed())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Labels).To(Equal(map[string]string{"team": "pod"}))
		Expect(results[0].Errors["team"]).To(MatchError(labelevaluator.ErrValueTooLong))
	})

	It("reads the Pod first when it is listed first", func(ctx SpecContext) {
		rule := teamRule(MetricPodWorkloadInfo)
		rule.Sources = []Source{SourcePod, SourceWorkload}
		r := newResolver(rule)
		root := workload(map[string]any{"example.com/team": "research"})
		results, err := r.Resolve(ctx, Input{
			Metrics: []string{MetricPodWorkloadInfo}, Workload: root, Pod: pod("", map[string]string{"example.com/team": "from-pod"}),
		})
		Expect(err).To(Succeed())
		Expect(results[0].Labels).To(Equal(map[string]string{"team": "from-pod"}))
		results, err = r.Resolve(ctx, Input{Metrics: []string{MetricPodWorkloadInfo}, Workload: root, Pod: pod("", nil)})
		Expect(err).To(Succeed())
		Expect(results[0].Labels).To(Equal(map[string]string{"team": "research"}))
	})

	It("does not need a Pod for workload status when another rule reads the Pod", func(ctx SpecContext) {
		r := newResolver(teamRule(MetricWorkloadStatus), schedulerRule())
		results, err := r.Resolve(ctx, Input{
			Metrics: []string{MetricWorkloadStatus}, Workload: workload(map[string]any{"example.com/team": "research"}),
		})
		Expect(err).To(Succeed())
		Expect(results[0].Labels).To(Equal(map[string]string{"team": "research"}))
	})

	It("returns independent maps for each metric and call", func(ctx SpecContext) {
		r := newResolver(teamRule(MetricWorkloadStatus, MetricPodWorkloadInfo))
		in := Input{
			Metrics:  []string{MetricWorkloadStatus, MetricPodWorkloadInfo},
			Workload: workload(map[string]any{"example.com/team": "research"}),
			Pod:      pod("", nil),
		}
		first, err := r.Resolve(ctx, in)
		Expect(err).To(Succeed())
		first[0].Labels["team"] = "changed"
		first[0].Errors["team"] = errors.New("changed")
		Expect(first[1].Labels).To(Equal(map[string]string{"team": "research"}))
		Expect(first[1].Errors).To(BeEmpty())
		second, err := r.Resolve(ctx, in)
		Expect(err).To(Succeed())
		Expect(second[0].Labels).To(Equal(map[string]string{"team": "research"}))
		Expect(second[0].Errors).To(BeEmpty())
	})

	It("uses the last nonempty value when several rules set the same label", func(ctx SpecContext) {
		job := LabelRule{
			Name: "team", Metrics: []string{MetricWorkloadStatus}, Sources: []Source{SourceWorkload},
			Expression: `object.kind == "Job" ? "jobs" : ""`,
		}
		jobSet := job
		jobSet.Expression = `object.kind == "JobSet" ? "jobsets" : ""`
		catchAll := job
		catchAll.Expression = `"any"`
		in := Input{Metrics: []string{MetricWorkloadStatus}, Workload: workload(nil)}

		results, err := newResolver(jobSet, job).Resolve(ctx, in)
		Expect(err).To(Succeed())
		Expect(results[0].Labels).To(Equal(map[string]string{"team": "jobsets"}))

		results, err = newResolver(jobSet, catchAll).Resolve(ctx, in)
		Expect(err).To(Succeed())
		Expect(results[0].Labels).To(Equal(map[string]string{"team": "any"}))
	})

	It("keeps the errors of every rule for the same label", func(ctx SpecContext) {
		first := LabelRule{
			Name: "team", Metrics: []string{MetricWorkloadStatus}, Sources: []Source{SourceWorkload},
			Expression: `object.metadata.labels["first"]`,
		}
		second := first
		second.Expression = `object.metadata.labels["second"]`
		results, err := newResolver(first, second).Resolve(ctx, Input{
			Metrics: []string{MetricWorkloadStatus}, Workload: workload(map[string]any{}),
		})
		Expect(err).To(Succeed())
		Expect(results[0].Labels).To(BeEmpty())
		Expect(results[0].Errors["team"]).To(MatchError(ContainSubstring("first")))
		Expect(results[0].Errors["team"]).To(MatchError(ContainSubstring("second")))
	})

	It("stops at the first nonempty source", func(ctx SpecContext) {
		rule := teamRule(MetricPodWorkloadInfo)
		rule.Sources = []Source{SourceWorkload, SourcePod}
		results, err := newResolver(rule).Resolve(ctx, Input{
			Metrics:  []string{MetricPodWorkloadInfo},
			Workload: workload(map[string]any{"example.com/team": "research"}),
			Pod:      pod("", map[string]string{"example.com/team": "from-pod"}),
		})
		Expect(err).To(Succeed())
		Expect(results[0].Labels).To(Equal(map[string]string{"team": "research"}))
	})

	It("omits a label when every source is empty", func(ctx SpecContext) {
		rule := teamRule(MetricPodWorkloadInfo)
		rule.Sources = []Source{SourcePod, SourceWorkload}
		results, err := newResolver(rule).Resolve(ctx, Input{
			Metrics: []string{MetricPodWorkloadInfo}, Workload: workload(nil), Pod: pod("", nil),
		})
		Expect(err).To(Succeed())
		Expect(results[0].Labels).To(BeEmpty())
		Expect(results[0].Errors).To(BeEmpty())
	})

	It("keeps successful labels when another label fails on every source", func(ctx SpecContext) {
		failing := LabelRule{
			Name: "priority", Metrics: []string{MetricPodWorkloadInfo}, Sources: []Source{SourceWorkload, SourcePod},
			Expression: `object.spec.priority`,
		}
		results, err := newResolver(failing, schedulerRule()).Resolve(ctx, Input{
			Metrics: []string{MetricPodWorkloadInfo}, Workload: workload(nil), Pod: pod("example-scheduler", nil),
		})
		Expect(err).To(Succeed())
		Expect(results[0].Labels).To(Equal(map[string]string{"scheduler_name": "example-scheduler"}))
		Expect(results[0].Errors).To(HaveLen(1))
		Expect(results[0].Errors["priority"]).To(MatchError(ContainSubstring("workload source")))
		Expect(results[0].Errors["priority"]).To(MatchError(ContainSubstring("pod source")))
	})

	It("evaluates a rule once for several metrics", func(ctx SpecContext) {
		evaluator := labelevaluator.NewMockLabelEvaluator(GinkgoT())
		evaluator.EXPECT().Evaluate(mock.Anything, mock.Anything).Return("research", nil).Once()
		compiler := labelevaluator.NewMockLabelCompiler(GinkgoT())
		compiler.EXPECT().Compile(mock.Anything).Return(evaluator, nil)
		r, err := New(Config{Rules: []LabelRule{teamRule(MetricWorkloadStatus, MetricPodWorkloadInfo)}}, Dependencies{Compiler: compiler})
		Expect(err).To(Succeed())
		results, err := r.Resolve(ctx, Input{
			Metrics: []string{MetricWorkloadStatus, MetricPodWorkloadInfo}, Workload: workload(nil), Pod: pod("", nil),
		})
		Expect(err).To(Succeed())
		Expect(results).To(HaveLen(2))
	})

	It("does not mutate its input", func(ctx SpecContext) {
		rule := teamRule(MetricPodWorkloadInfo)
		rule.Sources = []Source{SourceWorkload, SourcePod}
		root := workload(nil)
		p := pod("example-scheduler", map[string]string{"example.com/team": "from-pod"})
		rootBefore, podBefore := root.DeepCopy(), p.DeepCopy()
		_, err := newResolver(rule, schedulerRule()).Resolve(ctx, Input{Metrics: []string{MetricPodWorkloadInfo}, Workload: root, Pod: p})
		Expect(err).To(Succeed())
		Expect(root).To(Equal(rootBefore))
		Expect(p).To(Equal(podBefore))
	})

	It("is safe for concurrent use", func(ctx SpecContext) {
		rule := teamRule(MetricPodWorkloadInfo)
		rule.Sources = []Source{SourceWorkload, SourcePod}
		r := newResolver(rule, schedulerRule(), teamRule(MetricWorkloadStatus))
		root := workload(nil)
		p := pod("example-scheduler", map[string]string{"example.com/team": "from-pod"})
		want := []ResolvedMetricLabels{
			{Metric: MetricPodWorkloadInfo, Labels: map[string]string{"team": "from-pod", "scheduler_name": "example-scheduler"}, Errors: map[string]error{}},
			{Metric: MetricWorkloadStatus, Labels: map[string]string{}, Errors: map[string]error{}},
		}
		var wg sync.WaitGroup
		results := make([][]ResolvedMetricLabels, 16)
		errs := make([]error, len(results))
		for i := range results {
			wg.Go(func() {
				results[i], errs[i] = r.Resolve(ctx, Input{Metrics: []string{MetricPodWorkloadInfo, MetricWorkloadStatus}, Workload: root, Pod: p})
			})
		}
		wg.Wait()
		for i := range results {
			Expect(errs[i]).To(Succeed())
			Expect(results[i]).To(Equal(want))
		}
	})

	for _, tc := range []struct {
		name string
		in   Input
		want error
	}{
		{"missing workload", Input{Metrics: []string{MetricWorkloadStatus}}, ErrMissingWorkload},
		{"missing Pod for a Pod rule", Input{Metrics: []string{MetricPodWorkloadInfo}, Workload: workload(nil)}, ErrMissingPod},
		{"duplicate metric", Input{Metrics: []string{MetricWorkloadStatus, MetricWorkloadStatus}, Workload: workload(nil)}, ErrDuplicateMetric},
		{"unsupported metric", Input{Metrics: []string{"other"}, Workload: workload(nil)}, ErrUnsupportedMetric},
	} {
		It("rejects "+tc.name, func(ctx SpecContext) {
			results, err := newResolver(teamRule(MetricWorkloadStatus), schedulerRule()).Resolve(ctx, tc.in)
			Expect(err).To(MatchError(tc.want))
			Expect(results).To(BeNil())
		})
	}

	It("returns cancellation before evaluating", func(ctx SpecContext) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		_, err := newResolver(teamRule(MetricWorkloadStatus)).Resolve(cancelled, Input{Metrics: []string{MetricWorkloadStatus}, Workload: workload(nil)})
		Expect(err).To(MatchError(context.Canceled))
	})

	It("returns cancellation during evaluation instead of a label error", func(ctx SpecContext) {
		cancelled, cancel := context.WithCancel(ctx)
		evaluator := labelevaluator.NewMockLabelEvaluator(GinkgoT())
		evaluator.EXPECT().Evaluate(mock.Anything, mock.Anything).RunAndReturn(
			func(context.Context, *unstructured.Unstructured) (string, error) {
				cancel()
				return "", errors.New("interrupted")
			}).Once()
		compiler := labelevaluator.NewMockLabelCompiler(GinkgoT())
		compiler.EXPECT().Compile(mock.Anything).Return(evaluator, nil)
		rule := teamRule(MetricPodWorkloadInfo)
		rule.Sources = []Source{SourceWorkload, SourcePod}
		r, err := New(Config{Rules: []LabelRule{rule}}, Dependencies{Compiler: compiler})
		Expect(err).To(Succeed())
		results, err := r.Resolve(cancelled, Input{Metrics: []string{MetricPodWorkloadInfo}, Workload: workload(nil), Pod: pod("", nil)})
		Expect(err).To(MatchError(context.Canceled))
		Expect(results).To(BeNil())
	})
})

var _ = Describe("execution order", func() {
	statusRule := func(expression string) LabelRule {
		return LabelRule{Name: "team", Metrics: []string{MetricWorkloadStatus}, Sources: []Source{SourceWorkload}, Expression: expression}
	}

	It("reads sources in order and stops at the first nonempty successful value", func(ctx SpecContext) {
		rule := LabelRule{
			Name: "team", Metrics: []string{MetricPodWorkloadInfo}, Sources: []Source{SourceWorkload, SourcePod},
			Expression: `object.kind == "Pod" ? "pod" : object.metadata.labels["team"]`,
		}
		in := Input{Metrics: []string{MetricPodWorkloadInfo}, Workload: workload(map[string]any{}), Pod: pod("", nil)}
		results, err := newResolver(rule).Resolve(ctx, in)
		Expect(err).To(Succeed())
		Expect(results[0].Labels).To(Equal(map[string]string{"team": "pod"}))
		Expect(results[0].Errors["team"]).To(MatchError(ContainSubstring("workload source")))

		rule.Sources = []Source{SourcePod, SourceWorkload}
		results, err = newResolver(rule).Resolve(ctx, in)
		Expect(err).To(Succeed())
		Expect(results[0].Labels).To(Equal(map[string]string{"team": "pod"}))
		Expect(results[0].Errors).To(BeEmpty(), "the workload source must not be read after the Pod value")
	})

	It("keeps the last nonempty value across rules and reports later errors", func(ctx SpecContext) {
		r := newResolver(
			statusRule(`"research"`),
			statusRule(`"platform"`),
			statusRule(`""`),
			statusRule(`object.metadata.labels["missing"]`),
		)
		results, err := r.Resolve(ctx, Input{Metrics: []string{MetricWorkloadStatus}, Workload: workload(map[string]any{})})
		Expect(err).To(Succeed())
		Expect(results[0].Labels).To(Equal(map[string]string{"team": "platform"}))
		Expect(results[0].Errors["team"]).To(MatchError(ContainSubstring("missing")))
	})

	It("keeps the built-in label when every rule is empty or fails", func(ctx SpecContext) {
		r := newResolver(statusRule(`""`), statusRule(`object.metadata.labels["missing"]`))
		results, err := r.Resolve(ctx, Input{Metrics: []string{MetricWorkloadStatus}, Workload: workload(map[string]any{})})
		Expect(err).To(Succeed())
		Expect(results[0].Errors).To(HaveKey("team"))
		Expect(results[0].Apply(map[string]string{"team": "built-in"})).To(Equal(map[string]string{"team": "built-in"}))
	})
})

var _ = Describe("Apply", func() {
	It("replaces built-in values and keeps built-in labels without a resolved value", func() {
		builtIn := map[string]string{"workload_name": "train", "component": "workers", "workload_uid": "uid-1"}
		resolved := ResolvedMetricLabels{Labels: map[string]string{"component": "trainer", "team": "research"}}
		Expect(resolved.Apply(builtIn)).To(Equal(map[string]string{
			"workload_name": "train", "component": "trainer", "workload_uid": "uid-1", "team": "research",
		}))
		Expect(builtIn).To(Equal(map[string]string{"workload_name": "train", "component": "workers", "workload_uid": "uid-1"}))
	})

	It("keeps the built-in value when the rule resolves an empty value", func(ctx SpecContext) {
		rule := teamRule(MetricWorkloadStatus)
		rule.Name = "workload_name"
		results, err := newResolver(rule).Resolve(ctx, Input{Metrics: []string{MetricWorkloadStatus}, Workload: workload(nil)})
		Expect(err).To(Succeed())
		Expect(results[0].Apply(map[string]string{"workload_name": "train"})).To(Equal(map[string]string{"workload_name": "train"}))
	})

	It("accepts nil built-in labels", func() {
		Expect(ResolvedMetricLabels{Labels: map[string]string{"team": "research"}}.Apply(nil)).To(Equal(map[string]string{"team": "research"}))
	})
})

func realCompiler() Dependencies {
	return Dependencies{Compiler: labelevaluator.NewCompiler()}
}

func newResolver(rules ...LabelRule) Resolver {
	r, err := New(Config{Rules: rules}, realCompiler())
	Expect(err).To(Succeed())
	return r
}

func teamRule(metrics ...string) LabelRule {
	return LabelRule{Name: "team", Metrics: metrics, Sources: []Source{SourceWorkload}, Expression: teamExpression}
}

func schedulerRule() LabelRule {
	return LabelRule{Name: "scheduler_name", Metrics: []string{MetricPodWorkloadInfo}, Sources: []Source{SourcePod}, Expression: schedulerExpression}
}

func workload(labels map[string]any) *unstructured.Unstructured {
	metadata := map[string]any{"name": "train", "namespace": "team-a"}
	if labels != nil {
		metadata["labels"] = labels
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "jobset.x-k8s.io/v1alpha2", "kind": "JobSet", "metadata": metadata, "spec": map[string]any{},
	}}
}

func pod(scheduler string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "train-0", Namespace: "team-a", Labels: labels},
		Spec:       corev1.PodSpec{SchedulerName: scheduler},
	}
}
