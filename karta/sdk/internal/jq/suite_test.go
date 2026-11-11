// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package jq

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestJq(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Jq Suite")
}
