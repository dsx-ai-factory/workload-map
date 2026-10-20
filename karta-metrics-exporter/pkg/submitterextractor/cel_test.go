// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package submitterextractor

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const dualSourceExpression = `("karta.run.ai/submitted-by" in annotations)
  ? annotations["karta.run.ai/submitted-by"]
  : ("example.com/submitted-by" in labels
      ? labels["example.com/submitted-by"] : "")`

var (
	jobGVK        = schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
	deploymentGVK = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
)

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		labels     map[string]string
		annotation map[string]string
		gvk        schema.GroupVersionKind
		want       string
	}{
		{
			name:       "annotation source",
			expression: dualSourceExpression,
			annotation: map[string]string{"karta.run.ai/submitted-by": "alice"},
			gvk:        jobGVK,
			want:       "alice",
		},
		{
			name:       "label source",
			expression: dualSourceExpression,
			labels:     map[string]string{"example.com/submitted-by": "bob"},
			gvk:        jobGVK,
			want:       "bob",
		},
		{
			name:       "annotation wins over label",
			expression: dualSourceExpression,
			labels:     map[string]string{"example.com/submitted-by": "bob"},
			annotation: map[string]string{"karta.run.ai/submitted-by": "alice"},
			gvk:        jobGVK,
			want:       "alice",
		},
		{
			name:       "missing values are unknown",
			expression: dualSourceExpression,
			gvk:        jobGVK,
			want:       "",
		},
		{
			name:       "empty configuration is disabled",
			expression: "",
			labels:     map[string]string{"example.com/submitted-by": "bob"},
			gvk:        jobGVK,
			want:       "",
		},
		{
			name:       "whitespace configuration is disabled",
			expression: "  \n",
			gvk:        jobGVK,
			want:       "",
		},
		{
			name:       "kind selects metadata",
			expression: `resource.kind == "Job" ? labels["job-owner"] : labels["deploy-owner"]`,
			labels:     map[string]string{"job-owner": "carol", "deploy-owner": "dave"},
			gvk:        deploymentGVK,
			want:       "dave",
		},
		{
			name:       "resource fields",
			expression: `resource.namespace + "/" + resource.name + "/" + resource.group + "/" + resource.version`,
			gvk:        jobGVK,
			want:       "team-a/train/batch/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extractor, err := New(tt.expression)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			obj := &metav1.ObjectMeta{
				Namespace:   "team-a",
				Name:        "train",
				Labels:      tt.labels,
				Annotations: tt.annotation,
			}

			got, err := extractor.Evaluate(context.Background(), obj, tt.gvk)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if got.Identity != tt.want {
				t.Errorf("Identity = %q, want %q", got.Identity, tt.want)
			}
		})
	}
}

func TestEvaluateDoesNotMutateObject(t *testing.T) {
	extractor, err := New(dualSourceExpression)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	obj := &metav1.ObjectMeta{
		Labels:      map[string]string{"example.com/submitted-by": "bob"},
		Annotations: map[string]string{"a": "b"},
	}

	if _, err := extractor.Evaluate(context.Background(), obj, jobGVK); err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(obj.Labels) != 1 || len(obj.Annotations) != 1 || obj.Name != "" {
		t.Errorf("object was mutated: %+v", obj)
	}
}

func TestNewInvalid(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		wantErr    error
	}{
		{name: "syntax error", expression: `labels[`},
		{name: "undeclared variable", expression: `spec.owner`},
		{name: "non-string result", expression: `1 + 1`, wantErr: ErrNotString},
		{name: "map result", expression: `labels`, wantErr: ErrNotString},
		{name: "too long", expression: `"` + strings.Repeat("a", maxExpressionBytes) + `"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.expression)
			if err == nil {
				t.Fatal("New() error = nil, want error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("New() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestEvaluateMissingKeyIsError(t *testing.T) {
	extractor, err := New(`labels["absent"]`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := extractor.Evaluate(context.Background(), &metav1.ObjectMeta{}, jobGVK); err == nil {
		t.Error("Evaluate() error = nil, want error")
	}
}

func TestEvaluateCancellation(t *testing.T) {
	extractor, err := New(dualSourceExpression)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := extractor.Evaluate(ctx, &metav1.ObjectMeta{}, jobGVK); !errors.Is(err, context.Canceled) {
		t.Errorf("Evaluate() error = %v, want context.Canceled", err)
	}

	ctx, cancel = context.WithTimeout(context.Background(), 0)
	defer cancel()
	if _, err := extractor.Evaluate(ctx, &metav1.ObjectMeta{}, jobGVK); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Evaluate() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestEvaluateCostLimit(t *testing.T) {
	// Nested comprehensions over a large range exceed the cost budget.
	extractor, err := New(`string(size([1,2,3,4,5,6,7,8,9,10].map(a, [1,2,3,4,5,6,7,8,9,10].map(b, [1,2,3,4,5,6,7,8,9,10].map(c, [1,2,3,4,5,6,7,8,9,10].map(d, [1,2,3,4,5,6,7,8,9,10].map(e, a+b+c+d+e)))))))`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := extractor.Evaluate(context.Background(), &metav1.ObjectMeta{}, jobGVK); err == nil {
		t.Error("Evaluate() error = nil, want cost limit error")
	}
}

func TestEvaluateOutputLimit(t *testing.T) {
	extractor, err := New(`labels["big"]`)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	obj := &metav1.ObjectMeta{Labels: map[string]string{"big": strings.Repeat("a", maxIdentityBytes+1)}}

	if _, err := extractor.Evaluate(context.Background(), obj, jobGVK); !errors.Is(err, ErrIdentityTooLong) {
		t.Errorf("Evaluate() error = %v, want ErrIdentityTooLong", err)
	}
}
