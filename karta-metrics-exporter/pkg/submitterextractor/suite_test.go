// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package submitterextractor

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSubmitterExtractor(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Submitter Extractor Suite")
}
