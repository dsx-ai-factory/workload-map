// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package labelresolver

import (
	"context"
	"errors"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelevaluator"
)

// Resolver evaluates label rules for the requested metrics and source objects.
type Resolver interface {
	Resolve(context.Context, Input) ([]ResolvedMetricLabels, error)
}

// resolver is safe for concurrent use.
type resolver struct {
	rules         []compiledRule
	rulesByMetric map[string][]int
	// podMetrics marks the metrics with a rule that reads the Pod.
	podMetrics map[string]bool
}

type compiledRule struct {
	name      string
	sources   []Source
	evaluator labelevaluator.Evaluator
}

// ruleResult is the outcome of one rule, shared by every metric the rule targets.
type ruleResult struct {
	value string
	err   error
}

// Input supplies the root workload and, when a rule for a requested metric
// reads the Pod, the individual Pod.
type Input struct {
	Metrics  []string
	Workload *unstructured.Unstructured
	Pod      *corev1.Pod
}

// ResolvedMetricLabels holds values and evaluation errors keyed by label name.
type ResolvedMetricLabels struct {
	Metric string
	Labels map[string]string
	Errors map[string]error
}

type Config struct {
	Rules  []LabelRule           `json:"labelRules" yaml:"labelRules"`
	Limits labelevaluator.Limits `json:"genericLabelEvaluationLimits" yaml:"genericLabelEvaluationLimits"`
}

type LabelRule struct {
	Name       string   `json:"name" yaml:"name"`
	Metrics    []string `json:"metrics" yaml:"metrics"`
	Sources    []Source `json:"sources" yaml:"sources"`
	Expression string   `json:"expression" yaml:"expression"`
}

type Source string

const (
	SourceWorkload Source = "workload"
	SourcePod      Source = "pod"
)

const (
	// MetricPodWorkloadInfo describes one Pod and may read the Pod or its root workload.
	MetricPodWorkloadInfo = "karta_pod_workload_info"
	// MetricWorkloadStatus describes the root workload and reads only the workload.
	MetricWorkloadStatus = "karta_workload_status"
)

// supportedMetrics maps each metric that accepts label rules to whether its
// rules may read the Pod. A workload metric has no single Pod to choose.
var supportedMetrics = map[string]bool{
	MetricPodWorkloadInfo: true,
	MetricWorkloadStatus:  false,
}

var (
	ErrMissingCompiler   = errors.New("label resolver requires a compiler")
	ErrDuplicateMetric   = errors.New("duplicate metric")
	ErrUnsupportedMetric = errors.New("unsupported metric")
	ErrMissingWorkload   = errors.New("label input workload is nil")
	ErrMissingPod        = errors.New("label input Pod is nil")
)

type Dependencies struct {
	Compiler labelevaluator.Compiler
}

// New validates the rules and compiles each expression once.
func New(cfg Config, deps Dependencies) (Resolver, error) {
	if deps.Compiler == nil {
		return nil, ErrMissingCompiler
	}
	// The compiler checks limits only per rule, so an empty rule list would accept them.
	if cfg.Limits.MaxExpressionBytes < 0 || cfg.Limits.MaxResultBytes < 0 {
		return nil, labelevaluator.ErrNegativeLimit
	}
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	r := &resolver{
		rules:         make([]compiledRule, 0, len(cfg.Rules)),
		rulesByMetric: make(map[string][]int, len(supportedMetrics)),
		podMetrics:    make(map[string]bool),
	}
	for i, rule := range cfg.Rules {
		evaluator, err := deps.Compiler.Compile(labelevaluator.Config{Expression: rule.Expression, Limits: cfg.Limits})
		if err != nil {
			return nil, fmt.Errorf("labelRules[%d] %q: %w", i, rule.Name, err)
		}
		r.rules = append(r.rules, compiledRule{name: rule.Name, sources: slices.Clone(rule.Sources), evaluator: evaluator})
		readsPod := slices.Contains(rule.Sources, SourcePod)
		for _, metric := range rule.Metrics {
			r.rulesByMetric[metric] = append(r.rulesByMetric[metric], i)
			if readsPod {
				r.podMetrics[metric] = true
			}
		}
	}
	return r, nil
}

// Resolve returns one result per requested metric, in request order. The error
// reports invalid input, a Pod conversion failure or cancellation.
func (r *resolver) Resolve(ctx context.Context, in Input) ([]ResolvedMetricLabels, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if in.Workload == nil {
		return nil, ErrMissingWorkload
	}
	requested := make(map[string]bool, len(in.Metrics))
	readsPod := false
	for _, metric := range in.Metrics {
		if _, ok := supportedMetrics[metric]; !ok {
			return nil, fmt.Errorf("%w %q", ErrUnsupportedMetric, metric)
		}
		if requested[metric] {
			return nil, fmt.Errorf("%w %q", ErrDuplicateMetric, metric)
		}
		requested[metric] = true
		readsPod = readsPod || r.podMetrics[metric]
	}

	if readsPod && in.Pod == nil {
		return nil, ErrMissingPod
	}

	// The Pod is added only when a rule reaches it, so a workload value avoids the conversion.
	objects := map[Source]*unstructured.Unstructured{SourceWorkload: in.Workload}

	evaluated := make(map[int]ruleResult)
	results := make([]ResolvedMetricLabels, 0, len(in.Metrics))
	for _, metric := range in.Metrics {
		result := ResolvedMetricLabels{Metric: metric, Labels: map[string]string{}, Errors: map[string]error{}}
		for _, i := range r.rulesByMetric[metric] {
			outcome, ok := evaluated[i]
			if !ok {
				var err error
				if outcome, err = r.rules[i].evaluate(ctx, objects, in.Pod); err != nil {
					return nil, err
				}
				evaluated[i] = outcome
			}
			if outcome.value != "" {
				result.Labels[r.rules[i].name] = outcome.value
			}
			if outcome.err != nil {
				result.Errors[r.rules[i].name] = outcome.err
			}
		}
		results = append(results, result)
	}
	return results, nil
}

// evaluate returns the first nonempty source value and the errors of earlier sources.
// The error result reports cancellation or a Pod conversion failure.
func (rule compiledRule) evaluate(ctx context.Context, objects map[Source]*unstructured.Unstructured, inputPod *corev1.Pod) (ruleResult, error) {
	var errs []error
	for _, source := range rule.sources {
		if source == SourcePod && objects[SourcePod] == nil {
			content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(inputPod)
			if err != nil {
				return ruleResult{}, fmt.Errorf("converting Pod %s/%s: %w", inputPod.Namespace, inputPod.Name, err)
			}
			pod := &unstructured.Unstructured{Object: content}
			// Cached typed Pods have no TypeMeta; CEL must still see apiVersion and kind.
			pod.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Pod"))
			objects[SourcePod] = pod
		}
		value, err := rule.evaluator.Evaluate(ctx, objects[source])
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ruleResult{}, ctxErr
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s source: %w", source, err))
			continue
		}
		if value != "" {
			return ruleResult{value: value, err: errors.Join(errs...)}, nil
		}
	}
	return ruleResult{err: errors.Join(errs...)}, nil
}
