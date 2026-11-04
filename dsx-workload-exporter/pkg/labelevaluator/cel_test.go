// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package labelevaluator

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const list = "[1,2,3,4,5,6,7,8,9,10]"

var expensiveExpression = fmt.Sprintf("string(size(%[1]s.map(a, %[1]s.map(b, %[1]s.map(c, %[1]s.map(d, %[1]s.map(e, %[1]s.map(f, a+b+c+d+e+f))))))))", list)

var _ = Describe("evaluate object", func() {
	for _, tc := range []struct{ name, expression, want string }{
		{"annotation", `object.metadata.annotations["example.com/owner"]`, "alice"},
		{"label", `object.metadata.labels["example.com/team"]`, "research"},
		{"spec", `object.spec.schedulerName`, "example-scheduler"},
		{"status", `object.status.phase`, "Running"},
		{"identity", `object.apiVersion + "/" + object.kind + "/" + object.metadata.namespace + "/" + object.metadata.name`, "batch/v1/Job/team-a/train"},
		{"kind selection", `object.kind == "Job" ? object.metadata.annotations["example.com/owner"] : ""`, "alice"},
		{"fallback", `"absent" in object.metadata.annotations ? object.metadata.annotations["absent"] : object.metadata.labels["example.com/team"]`, "research"},
		{"empty result", `""`, ""},
		{"disabled", "", ""},
		{"whitespace", " \n", ""},
	} {
		It(tc.name, func(specCtx SpecContext) {
			evaluator, err := NewCompiler().Compile(Config{Expression: tc.expression})
			Expect(err).To(Succeed())
			obj := labelObject()
			before := obj.DeepCopy()
			value, err := evaluator.Evaluate(specCtx, obj)
			Expect(err).To(Succeed())
			Expect(value).To(Equal(tc.want))
			Expect(obj).To(Equal(before))
		})
	}
})

var _ = Describe("Evaluation failures", func() {
	for _, tc := range []struct {
		name, expression string
		obj              *unstructured.Unstructured
		want             error
	}{
		{"dynamic number", `object.spec.parallelism`, labelObject(), ErrNotString},
		{"dynamic map", `object.metadata`, labelObject(), ErrNotString},
		{"missing object", `object.kind`, nil, ErrMissingObject},
		{"missing key", `object.metadata.labels["absent"]`, labelObject(), nil},
	} {
		It(tc.name, func(specCtx SpecContext) {
			evaluator, err := NewCompiler().Compile(Config{Expression: tc.expression})
			Expect(err).To(Succeed())
			value, err := evaluator.Evaluate(specCtx, tc.obj)
			Expect(value).To(BeEmpty())
			Expect(err).To(HaveOccurred())
			if tc.want != nil {
				Expect(err).To(MatchError(tc.want))
			}
		})
	}
	It("allows guards for missing optional fields", func(specCtx SpecContext) {
		evaluator, err := NewCompiler().Compile(Config{Expression: `has(object.metadata.labels) && "team" in object.metadata.labels ? object.metadata.labels["team"] : ""`})
		Expect(err).To(Succeed())
		value, err := evaluator.Evaluate(specCtx, &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{}}})
		Expect(err).To(Succeed())
		Expect(value).To(BeEmpty())
	})
})

var _ = Describe("compile failures", func() {
	for _, tc := range []struct {
		name string
		cfg  Config
		want error
	}{
		{"syntax", Config{Expression: `object[`}, nil},
		{"undeclared variable", Config{Expression: `unknownField`}, nil},
		{"static number", Config{Expression: `1 + 1`}, ErrNotString},
		{"static map", Config{Expression: `{"team": "a"}`}, ErrNotString},
		{"expression default bound", Config{Expression: `"` + strings.Repeat("a", DefaultMaxExpressionBytes) + `"`}, ErrExpressionTooLong},
		{"configured expression bound", Config{Expression: `"hello"`, Limits: Limits{MaxExpressionBytes: 4}}, ErrExpressionTooLong},
		{"negative expression bound", Config{Limits: Limits{MaxExpressionBytes: -1}}, ErrNegativeLimit},
		{"negative value bound", Config{Limits: Limits{MaxResultBytes: -1}}, ErrNegativeLimit},
	} {
		It(tc.name, func() {
			evaluator, err := NewCompiler().Compile(tc.cfg)
			Expect(err).To(HaveOccurred())
			Expect(evaluator).To(BeNil())
			if tc.want != nil {
				Expect(err).To(MatchError(tc.want))
			}
		})
	}
})

var _ = Describe("value limits", func() {
	for _, tc := range []struct {
		name        string
		size, limit int
		failed      bool
	}{
		{"default", DefaultMaxResultBytes + 1, 0, true},
		{"configured", 5, 4, true},
		{"raised", DefaultMaxResultBytes + 1, 2 * DefaultMaxResultBytes, false},
		{"boundary", 4, 4, false},
	} {
		It(tc.name, func(specCtx SpecContext) {
			evaluator, err := NewCompiler().Compile(Config{Expression: `object.value`, Limits: Limits{MaxResultBytes: tc.limit}})
			Expect(err).To(Succeed())
			obj := &unstructured.Unstructured{Object: map[string]any{"value": strings.Repeat("a", tc.size)}}
			value, err := evaluator.Evaluate(specCtx, obj)
			if tc.failed {
				Expect(err).To(MatchError(ErrValueTooLong))
				Expect(value).To(BeEmpty())
			} else {
				Expect(err).To(Succeed())
				Expect(value).To(HaveLen(tc.size))
			}
		})
	}
})

var _ = Describe("cost limits", func() {
	It("stops evaluation at the configured cost limit", func(specCtx SpecContext) {
		for _, limit := range []uint64{0, 1} {
			evaluator, err := NewCompiler().Compile(Config{Expression: expensiveExpression, Limits: Limits{CostLimit: limit}})
			Expect(err).To(Succeed())
			_, err = evaluator.Evaluate(specCtx, labelObject())
			Expect(err).To(MatchError(ContainSubstring("cost limit")))
		}
	})
})

var _ = Describe("cancellation", func() {
	It("honors canceled contexts and deadlines", func(specCtx SpecContext) {
		for _, expression := range []string{"", `object.kind`} {
			evaluator, err := NewCompiler().Compile(Config{Expression: expression})
			Expect(err).To(Succeed())
			ctx, cancel := context.WithCancel(specCtx)
			cancel()
			_, err = evaluator.Evaluate(ctx, labelObject())
			Expect(err).To(MatchError(context.Canceled))
			ctx, cancel = context.WithDeadline(specCtx, time.Now().Add(-time.Second))
			defer cancel()
			_, err = evaluator.Evaluate(ctx, labelObject())
			Expect(err).To(MatchError(context.DeadlineExceeded))
		}
	})
	It("interrupts evaluation with the internal cancellation interval", func(specCtx SpecContext) {
		evaluator, err := NewCompiler().Compile(Config{Expression: expensiveExpression, Limits: Limits{CostLimit: math.MaxUint64}})
		Expect(err).To(Succeed())
		ctx, cancel := context.WithCancel(specCtx)
		defer cancel()
		timer := time.AfterFunc(5*time.Millisecond, cancel)
		defer timer.Stop()
		start := time.Now()
		_, err = evaluator.Evaluate(ctx, labelObject())
		Expect(err).To(MatchError(context.Canceled))
		Expect(time.Since(start)).To(BeNumerically("<", time.Second))
	})
})

var _ = Describe("Concurrent evaluation", func() {
	It("shares one compiled evaluator across callers", func(ctx SpecContext) {
		evaluator, err := NewCompiler().Compile(Config{Expression: `object.metadata.name`})
		Expect(err).To(Succeed())
		var workers sync.WaitGroup
		for i := range 20 {
			workers.Go(func() {
				defer GinkgoRecover()
				obj := labelObject()
				obj.SetName(fmt.Sprint(i))
				value, err := evaluator.Evaluate(ctx, obj)
				Expect(err).To(Succeed())
				Expect(value).To(Equal(fmt.Sprint(i)))
			})
		}
		workers.Wait()
	})
})

func labelObject() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": map[string]any{"name": "train", "namespace": "team-a",
			"labels":      map[string]any{"example.com/team": "research"},
			"annotations": map[string]any{"example.com/owner": "alice"}},
		"spec":   map[string]any{"schedulerName": "example-scheduler", "parallelism": int64(2)},
		"status": map[string]any{"phase": "Running"},
	}}
}
