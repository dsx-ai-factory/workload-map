// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package replay

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestReplay(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Karta Replay Suite")
}
