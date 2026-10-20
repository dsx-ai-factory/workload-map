// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package submitterextractor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// maxExpressionBytes bounds the configured expression before compilation.
	maxExpressionBytes = 4096
	// maxEvaluationCost bounds the CEL cost units spent on one evaluation.
	maxEvaluationCost = 10000
	// maxIdentityBytes bounds the identity a single evaluation may return.
	maxIdentityBytes = 1024
	// interruptCheckFrequency is how many comprehension iterations pass
	// between context cancellation checks.
	interruptCheckFrequency = 100
)

var (
	// ErrIdentityTooLong is returned when an expression result exceeds maxIdentityBytes.
	ErrIdentityTooLong = errors.New("submitter identity exceeds the maximum length")
	// ErrNotString is returned when an expression does not produce a string.
	ErrNotString = errors.New("submitter expression must produce a string")
)

type celExtractor struct {
	program cel.Program
}

type disabledExtractor struct{}

// New compiles the CEL expression once and returns an extractor for it.
// An empty expression disables extraction: every evaluation yields an unknown
// identity. An invalid expression, or one whose result is not a string, is an error.
func New(expression string) (Interface, error) {
	if strings.TrimSpace(expression) == "" {
		return disabledExtractor{}, nil
	}
	if len(expression) > maxExpressionBytes {
		return nil, fmt.Errorf("submitter expression exceeds %d bytes", maxExpressionBytes)
	}

	env, err := cel.NewEnv(
		cel.Variable("labels", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("annotations", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("resource", cel.MapType(cel.StringType, cel.StringType)),
	)
	if err != nil {
		return nil, fmt.Errorf("creating CEL environment: %w", err)
	}

	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("compiling submitter expression: %w", issues.Err())
	}
	if !ast.OutputType().IsExactType(cel.StringType) {
		return nil, fmt.Errorf("%w, got %s", ErrNotString, ast.OutputType())
	}

	program, err := env.Program(ast,
		cel.CostLimit(maxEvaluationCost),
		cel.InterruptCheckFrequency(interruptCheckFrequency),
	)
	if err != nil {
		return nil, fmt.Errorf("building submitter program: %w", err)
	}
	return &celExtractor{program: program}, nil
}

func (disabledExtractor) Evaluate(context.Context, metav1.Object, schema.GroupVersionKind) (Submitter, error) {
	return Submitter{}, nil
}

func (e *celExtractor) Evaluate(ctx context.Context, obj metav1.Object, gvk schema.GroupVersionKind) (Submitter, error) {
	if err := ctx.Err(); err != nil {
		return Submitter{}, err
	}

	out, _, err := e.program.ContextEval(ctx, map[string]any{
		"labels":      stringMap(obj.GetLabels()),
		"annotations": stringMap(obj.GetAnnotations()),
		"resource": map[string]string{
			"namespace": obj.GetNamespace(),
			"name":      obj.GetName(),
			"group":     gvk.Group,
			"version":   gvk.Version,
			"kind":      gvk.Kind,
		},
	})
	if err != nil {
		// CEL reports interruption with its own error; surface the context error instead.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Submitter{}, ctxErr
		}
		return Submitter{}, fmt.Errorf("evaluating submitter expression: %w", err)
	}

	identity, ok := out.Value().(string)
	if !ok {
		return Submitter{}, ErrNotString
	}
	if len(identity) > maxIdentityBytes {
		return Submitter{}, ErrIdentityTooLong
	}
	return Submitter{Identity: identity}, nil
}

// stringMap returns a non-nil map so CEL always sees a map value.
func stringMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
