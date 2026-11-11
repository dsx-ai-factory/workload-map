// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package karta_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestKarta(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Karta Front Door Suite")
}
