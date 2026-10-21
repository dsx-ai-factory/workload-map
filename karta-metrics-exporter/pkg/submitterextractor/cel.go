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
	// DefaultMaxExpressionBytes bounds the configured expression before compilation.
	DefaultMaxExpressionBytes = 4096
	// DefaultCostLimit bounds the CEL cost units spent on one evaluation.
	DefaultCostLimit = 10000
	// DefaultMaxIdentityBytes bounds the identity a single evaluation may return.
	DefaultMaxIdentityBytes = 1024
	// DefaultInterruptCheckFrequency is how many comprehension iterations pass
	// between context cancellation checks.
	DefaultInterruptCheckFrequency = 100
)

var (
	// ErrIdentityTooLong is returned when an expression result exceeds Config.MaxIdentityBytes.
	ErrIdentityTooLong = errors.New("submitter identity exceeds the maximum length")
	// ErrNotString is returned when an expression does not produce a string.
	ErrNotString = errors.New("submitter expression must produce a string")
	// ErrExpressionTooLong is returned when the expression exceeds Config.MaxExpressionBytes.
	ErrExpressionTooLong = errors.New("submitter expression exceeds the maximum length")
	// ErrNegativeLimit is returned when a Config limit is negative.
	ErrNegativeLimit = errors.New("submitter limits must not be negative")
)

// Config configures the extractor. A zero limit selects its default.
type Config struct {
	// Expression is the CEL expression. Empty disables extraction.
	Expression string
	// CostLimit bounds the CEL cost units spent on one evaluation.
	CostLimit uint64
	// MaxExpressionBytes bounds the expression length.
	MaxExpressionBytes int
	// MaxIdentityBytes bounds the length of the returned identity.
	MaxIdentityBytes int
	// InterruptCheckFrequency is how many comprehension iterations pass between
	// context cancellation checks. Zero selects the default rather than
	// disabling the checks.
	InterruptCheckFrequency uint
}

type celExtractor struct {
	program          cel.Program
	maxIdentityBytes int
}

type disabledExtractor struct{}

// New compiles the CEL expression once and returns an extractor for it.
// An empty expression disables extraction: every evaluation yields an unknown
// identity. An invalid expression, one whose result is not a string, or a
// negative limit is an error.
func New(cfg Config) (Interface, error) {
	if cfg.MaxExpressionBytes < 0 || cfg.MaxIdentityBytes < 0 {
		return nil, ErrNegativeLimit
	}
	if cfg.CostLimit == 0 {
		cfg.CostLimit = DefaultCostLimit
	}
	if cfg.MaxExpressionBytes == 0 {
		cfg.MaxExpressionBytes = DefaultMaxExpressionBytes
	}
	if cfg.MaxIdentityBytes == 0 {
		cfg.MaxIdentityBytes = DefaultMaxIdentityBytes
	}
	// cel-go disables cancellation checks during evaluation at zero.
	if cfg.InterruptCheckFrequency == 0 {
		cfg.InterruptCheckFrequency = DefaultInterruptCheckFrequency
	}

	expression := cfg.Expression
	if strings.TrimSpace(expression) == "" {
		return disabledExtractor{}, nil
	}
	if len(expression) > cfg.MaxExpressionBytes {
		return nil, fmt.Errorf("%w (%d bytes)", ErrExpressionTooLong, cfg.MaxExpressionBytes)
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
		cel.CostLimit(cfg.CostLimit),
		cel.InterruptCheckFrequency(cfg.InterruptCheckFrequency),
	)
	if err != nil {
		return nil, fmt.Errorf("building submitter program: %w", err)
	}
	return &celExtractor{program: program, maxIdentityBytes: cfg.MaxIdentityBytes}, nil
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
	if len(identity) > e.maxIdentityBytes {
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
