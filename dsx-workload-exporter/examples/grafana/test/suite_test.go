// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package grafana_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGrafana(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Exporter Grafana Suite")
}
