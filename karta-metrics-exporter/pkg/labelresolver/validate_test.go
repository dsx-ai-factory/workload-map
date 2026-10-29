// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package labelresolver

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ValidateConfig", func() {
	var base LabelRule
	BeforeEach(func() {
		base = LabelRule{
			Name:       "team",
			Metrics:    []string{"karta_pod_workload_info"},
			Sources:    []Source{SourceWorkload, SourcePod},
			Expression: `object.metadata.labels["team"]`,
		}
	})
	It("accepts empty rules and the same label on different metrics", func() {
		Expect(ValidateConfig(Config{})).To(Succeed())
		Expect(ValidateConfig(Config{Rules: []LabelRule{base}})).To(Succeed())
		status := base
		status.Metrics = []string{"karta_workload_status"}
		status.Sources = []Source{SourceWorkload}
		Expect(ValidateConfig(Config{Rules: []LabelRule{base, status}})).To(Succeed())
	})
	for _, tc := range []struct {
		name   string
		change func(*LabelRule)
	}{
		{"empty name", func(r *LabelRule) { r.Name = "" }},
		{"invalid name", func(r *LabelRule) { r.Name = "my-team" }},
		{"internal name", func(r *LabelRule) { r.Name = "__name__" }},
		{"no metrics", func(r *LabelRule) { r.Metrics = nil }},
		{"unknown metric", func(r *LabelRule) { r.Metrics = []string{"other"} }},
		{"duplicate metric", func(r *LabelRule) { r.Metrics = []string{"karta_pod_workload_info", "karta_pod_workload_info"} }},
		{"no sources", func(r *LabelRule) { r.Sources = nil }},
		{"unknown source", func(r *LabelRule) { r.Sources = []Source{"other"} }},
		{"duplicate source", func(r *LabelRule) { r.Sources = []Source{SourcePod, SourcePod} }},
		{"pod for status", func(r *LabelRule) { r.Metrics = []string{"karta_workload_status"} }},
		{"empty expression", func(r *LabelRule) { r.Expression = " " }},
	} {
		It("rejects "+tc.name, func() {
			rule := base
			tc.change(&rule)
			Expect(ValidateConfig(Config{Rules: []LabelRule{rule}})).To(HaveOccurred())
		})
	}
	It("accepts built-in names", func() {
		for _, name := range []string{"workload_name", "workload_uid", "pod_name", "phase", "observation_valid", "instance"} {
			rule := base
			rule.Name = name
			Expect(ValidateConfig(Config{Rules: []LabelRule{rule}})).To(Succeed(), name)
		}
	})
	It("rejects duplicate labels on the same metric", func() {
		Expect(ValidateConfig(Config{Rules: []LabelRule{base, base}})).To(HaveOccurred())
	})
})
