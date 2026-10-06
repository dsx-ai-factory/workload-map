// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package instructions

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestInstructions(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Instructions Suite")
}
