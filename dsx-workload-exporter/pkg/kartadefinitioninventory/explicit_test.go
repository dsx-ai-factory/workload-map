// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartadefinitioninventory

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Explicit workload selection", func() {
	DescribeTable("preserves include intent through source precedence",
		func(config Config, expected bool) {
			inventory := loadedInventory(config, newKarta("cluster-job", jobGVK))
			selection, err := inventory.Lookup(jobGVK)
			Expect(err).NotTo(HaveOccurred())
			Expect(selection.Explicit).To(Equal(expected))
			results, err := inventory.List()
			Expect(err).NotTo(HaveOccurred())
			found := false
			for _, result := range results {
				if result.GVK == jobGVK {
					Expect(result.Explicit).To(Equal(expected))
					found = true
				}
			}
			Expect(found).To(BeTrue())
		},
		Entry("default catalog", Config{Catalog: SourceConfig{Enabled: true}}, false),
		Entry("included catalog", Config{Catalog: SourceConfig{Enabled: true, Include: []string{"batch-job-v1"}}}, true),
		Entry("default cluster", clusterOnly, false),
		Entry("included cluster", Config{Cluster: SourceConfig{Enabled: true, Include: []string{"cluster-job"}}}, true),
		Entry("cluster override of included catalog", Config{Catalog: SourceConfig{Enabled: true, Include: []string{"batch-job-v1"}}, Cluster: SourceConfig{Enabled: true}}, true),
		Entry("excluded catalog include", Config{Catalog: SourceConfig{Enabled: true, Include: []string{"batch-job-v1"}, Exclude: []string{"batch-job-v1"}}, Cluster: SourceConfig{Enabled: true}}, false),
		Entry("disabled catalog include", Config{Catalog: SourceConfig{Include: []string{"batch-job-v1"}}, Cluster: SourceConfig{Enabled: true}}, false),
	)
})
