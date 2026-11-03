// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package labelevaluator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type celEvaluator struct {
	program        cel.Program
	maxResultBytes int
}

type compiler struct{}

type disabledEvaluator struct{}

const (
	// DefaultMaxExpressionBytes bounds the configured expression before compilation.
	DefaultMaxExpressionBytes = 4096
	// DefaultCostLimit bounds the CEL cost units spent on one evaluation.
	DefaultCostLimit = 10000
	// DefaultMaxResultBytes bounds the value a single evaluation may return.
	DefaultMaxResultBytes = 1024
	// Keep cancellation checks enabled without exposing an engine-specific setting.
	interruptCheckFrequency = 100
)

var (
	ErrMissingObject = errors.New("label input object is nil")
	// ErrValueTooLong is returned when an expression result exceeds Config.Limits.MaxResultBytes.
	ErrValueTooLong = errors.New("label value exceeds the maximum length")
	// ErrNotString is returned when an expression does not produce a string.
	ErrNotString = errors.New("label expression must produce a string")
	// ErrExpressionTooLong is returned when the expression exceeds Config.Limits.MaxExpressionBytes.
	ErrExpressionTooLong = errors.New("label expression exceeds the maximum length")
	// ErrNegativeLimit is returned when a Config limit is negative.
	ErrNegativeLimit = errors.New("label limits must not be negative")
)

// NewCompiler returns a compiler with bounded CEL evaluation.
func NewCompiler() Compiler {
	return compiler{}
}

// Compile compiles the CEL expression once and returns an evaluator for it.
// An empty expression returns an empty value. Dynamic result types are checked
// during evaluation. Invalid expressions and negative limits are rejected.
func (compiler) Compile(cfg Config) (Evaluator, error) {
	limits := cfg.Limits
	if limits.MaxExpressionBytes < 0 || limits.MaxResultBytes < 0 {
		return nil, ErrNegativeLimit
	}
	if limits.CostLimit == 0 {
		limits.CostLimit = DefaultCostLimit
	}
	if limits.MaxExpressionBytes == 0 {
		limits.MaxExpressionBytes = DefaultMaxExpressionBytes
	}
	if limits.MaxResultBytes == 0 {
		limits.MaxResultBytes = DefaultMaxResultBytes
	}

	expression := cfg.Expression
	if strings.TrimSpace(expression) == "" {
		return disabledEvaluator{}, nil
	}
	if len(expression) > limits.MaxExpressionBytes {
		return nil, fmt.Errorf("%w (%d bytes)", ErrExpressionTooLong, limits.MaxExpressionBytes)
	}

	env, err := cel.NewEnv(cel.Variable("object", cel.DynType))
	if err != nil {
		return nil, fmt.Errorf("creating CEL environment: %w", err)
	}

	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("compiling label expression: %w", issues.Err())
	}
	if !ast.OutputType().IsExactType(cel.StringType) && !ast.OutputType().IsExactType(cel.DynType) {
		return nil, fmt.Errorf("%w, got %s", ErrNotString, ast.OutputType())
	}

	program, err := env.Program(ast,
		cel.CostLimit(limits.CostLimit),
		cel.InterruptCheckFrequency(interruptCheckFrequency),
	)
	if err != nil {
		return nil, fmt.Errorf("building label program: %w", err)
	}
	return &celEvaluator{program: program, maxResultBytes: limits.MaxResultBytes}, nil
}

func (disabledEvaluator) Evaluate(ctx context.Context, _ *unstructured.Unstructured) (string, error) {
	return "", ctx.Err()
}

func (e *celEvaluator) Evaluate(ctx context.Context, obj *unstructured.Unstructured) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if obj == nil {
		return "", ErrMissingObject
	}
	out, _, err := e.program.ContextEval(ctx, map[string]any{"object": obj.Object})
	if err != nil {
		// On interruption, ContextEval wraps the context cause, so errors.Is still matches it.
		return "", fmt.Errorf("evaluating label expression: %w", err)
	}

	value, ok := out.Value().(string)
	if !ok {
		return "", ErrNotString
	}
	if len(value) > e.maxResultBytes {
		return "", ErrValueTooLong
	}
	return value, nil
}
