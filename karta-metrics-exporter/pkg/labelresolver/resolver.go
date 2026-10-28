// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package labelresolver

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelevaluator"
)

// Resolver evaluates label rules for the requested metrics and source objects.
type Resolver interface {
	Resolve(context.Context, Input) ([]ResolvedMetricLabels, error)
}

// Input supplies the root workload and, for Pod metrics, the individual Pod.
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

type Dependencies struct {
	Compiler labelevaluator.Compiler
}
