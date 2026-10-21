// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package submitterextractor

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

func mustNew(cfg Config) Interface {
	GinkgoHelper()
	extractor, err := New(cfg)
	Expect(err).NotTo(HaveOccurred())
	return extractor
}

var _ = Describe("CEL submitter extractor", func() {
	DescribeTable("Evaluate",
		func(expression string, labels, annotations map[string]string, gvk schema.GroupVersionKind, want string) {
			obj := &metav1.ObjectMeta{Namespace: "team-a", Name: "train", Labels: labels, Annotations: annotations}

			got, err := mustNew(Config{Expression: expression}).Evaluate(context.Background(), obj, gvk)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Identity).To(Equal(want))
		},
		Entry("annotation source", dualSourceExpression,
			nil, map[string]string{"karta.run.ai/submitted-by": "alice"}, jobGVK, "alice"),
		Entry("label source", dualSourceExpression,
			map[string]string{"example.com/submitted-by": "bob"}, nil, jobGVK, "bob"),
		Entry("annotation wins over label", dualSourceExpression,
			map[string]string{"example.com/submitted-by": "bob"},
			map[string]string{"karta.run.ai/submitted-by": "alice"}, jobGVK, "alice"),
		Entry("missing values are unknown", dualSourceExpression, nil, nil, jobGVK, ""),
		Entry("empty configuration is disabled", "",
			map[string]string{"example.com/submitted-by": "bob"}, nil, jobGVK, ""),
		Entry("whitespace configuration is disabled", "  \n", nil, nil, jobGVK, ""),
		Entry("kind selects metadata", `resource.kind == "Job" ? labels["job-owner"] : labels["deploy-owner"]`,
			map[string]string{"job-owner": "carol", "deploy-owner": "dave"}, nil, deploymentGVK, "dave"),
		Entry("resource fields", `resource.namespace + "/" + resource.name + "/" + resource.group + "/" + resource.version`,
			nil, nil, jobGVK, "team-a/train/batch/v1"),
	)

	It("does not mutate the object", func() {
		obj := &metav1.ObjectMeta{
			Labels:      map[string]string{"example.com/submitted-by": "bob"},
			Annotations: map[string]string{"a": "b"},
		}
		before := obj.DeepCopy()

		_, err := mustNew(Config{Expression: dualSourceExpression}).Evaluate(context.Background(), obj, jobGVK)
		Expect(err).NotTo(HaveOccurred())
		Expect(obj).To(Equal(before))
	})

	DescribeTable("New rejects invalid expressions",
		func(expression string, wantErr error) {
			_, err := New(Config{Expression: expression})
			Expect(err).To(HaveOccurred())
			if wantErr != nil {
				Expect(err).To(MatchError(wantErr))
			}
		},
		Entry("syntax error", `labels[`, nil),
		Entry("undeclared variable", `spec.owner`, nil),
		Entry("non-string result", `1 + 1`, ErrNotString),
		Entry("map result", `labels`, ErrNotString),
		Entry("too long", `"`+strings.Repeat("a", DefaultMaxExpressionBytes)+`"`, ErrExpressionTooLong),
	)

	It("returns an error for a missing key", func() {
		_, err := mustNew(Config{Expression: `labels["absent"]`}).Evaluate(context.Background(), &metav1.ObjectMeta{}, jobGVK)
		Expect(err).To(HaveOccurred())
	})

	Context("cancellation", func() {
		It("returns context.Canceled for a cancelled context", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := mustNew(Config{Expression: dualSourceExpression}).Evaluate(ctx, &metav1.ObjectMeta{}, jobGVK)
			Expect(err).To(MatchError(context.Canceled))
		})

		It("returns context.DeadlineExceeded for an expired context", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 0)
			DeferCleanup(cancel)
			_, err := mustNew(Config{Expression: dualSourceExpression}).Evaluate(ctx, &metav1.ObjectMeta{}, jobGVK)
			Expect(err).To(MatchError(context.DeadlineExceeded))
		})

		// Six nested comprehensions run a million iterations: far longer than the deadline.
		const list = "[1,2,3,4,5,6,7,8,9,10]"
		longExpression := fmt.Sprintf(
			"string(size(%[1]s.map(a, %[1]s.map(b, %[1]s.map(c, %[1]s.map(d, %[1]s.map(e, %[1]s.map(f, a+b+c+d+e+f))))))))",
			list)

		DescribeTable("interrupts a long evaluation",
			func(frequency uint) {
				extractor := mustNew(Config{
					Expression:              longExpression,
					CostLimit:               math.MaxUint64,
					InterruptCheckFrequency: frequency,
				})
				// Cancel after Evaluate has started, so only a mid-evaluation check can stop it.
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)
				time.AfterFunc(5*time.Millisecond, cancel)

				start := time.Now()
				_, err := extractor.Evaluate(ctx, &metav1.ObjectMeta{}, jobGVK)
				Expect(err).To(MatchError(context.Canceled))
				Expect(time.Since(start)).To(BeNumerically("<", time.Second))
			},
			Entry("default frequency (0)", uint(0)),
			Entry("frequency 1", uint(1)),
			Entry("frequency 1000", uint(1000)),
		)
	})

	Context("limits", func() {
		obj := &metav1.ObjectMeta{Annotations: map[string]string{"karta.run.ai/submitted-by": "alice"}}

		It("enforces the default cost limit", func() {
			// Nested comprehensions over a large range exceed the cost budget.
			const list = "[1,2,3,4,5,6,7,8,9,10]"
			expression := fmt.Sprintf(
				"string(size(%[1]s.map(a, %[1]s.map(b, %[1]s.map(c, %[1]s.map(d, %[1]s.map(e, a+b+c+d+e)))))))", list)
			_, err := mustNew(Config{Expression: expression}).Evaluate(context.Background(), &metav1.ObjectMeta{}, jobGVK)
			Expect(err).To(HaveOccurred())
		})

		It("enforces the default identity limit", func() {
			big := &metav1.ObjectMeta{Labels: map[string]string{"big": strings.Repeat("a", DefaultMaxIdentityBytes+1)}}
			_, err := mustNew(Config{Expression: `labels["big"]`}).Evaluate(context.Background(), big, jobGVK)
			Expect(err).To(MatchError(ErrIdentityTooLong))
		})

		It("applies a configured cost limit", func() {
			_, err := mustNew(Config{Expression: dualSourceExpression, CostLimit: 1}).Evaluate(context.Background(), obj, jobGVK)
			Expect(err).To(HaveOccurred())
		})

		It("applies a configured identity limit", func() {
			_, err := mustNew(Config{Expression: dualSourceExpression, MaxIdentityBytes: 4}).Evaluate(context.Background(), obj, jobGVK)
			Expect(err).To(MatchError(ErrIdentityTooLong))
		})

		It("applies a configured expression limit", func() {
			_, err := New(Config{Expression: dualSourceExpression, MaxExpressionBytes: 16})
			Expect(err).To(MatchError(ErrExpressionTooLong))
		})

		It("allows a longer identity when the limit is raised", func() {
			long := strings.Repeat("a", DefaultMaxIdentityBytes+1)
			extractor := mustNew(Config{Expression: `labels["big"]`, MaxIdentityBytes: 2 * DefaultMaxIdentityBytes})

			got, err := extractor.Evaluate(context.Background(), &metav1.ObjectMeta{Labels: map[string]string{"big": long}}, jobGVK)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Identity).To(Equal(long))
		})

		DescribeTable("rejects negative limits",
			func(cfg Config) {
				_, err := New(cfg)
				Expect(err).To(MatchError(ErrNegativeLimit))
			},
			Entry("expression bytes", Config{Expression: dualSourceExpression, MaxExpressionBytes: -1}),
			Entry("identity bytes", Config{Expression: dualSourceExpression, MaxIdentityBytes: -1}),
		)
	})
})
