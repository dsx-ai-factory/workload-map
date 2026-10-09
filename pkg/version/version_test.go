// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package version

import (
	"runtime/debug"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Version resolution", func() {
	DescribeTable("resolves the highest-priority available version",
		func(explicit string, info *debug.BuildInfo, hasBuildInfo bool, want string, wantRead bool) {
			read := false
			got := resolve(explicit, func() (*debug.BuildInfo, bool) {
				read = true
				return info, hasBuildInfo
			})
			Expect(got).To(Equal(want))
			Expect(read).To(Equal(wantRead))
		},
		Entry("explicit linker version wins without reading build info",
			"1.2.3-explicit", buildInfo("v9.9.9"), true, "1.2.3-explicit", false),
		Entry("tagged module fallback",
			"", buildInfo("v1.2.3"), true, "1.2.3", true),
		Entry("development module fallback",
			"", buildInfo("(devel)"), true, unknownVersion, true),
		Entry("missing build info fallback",
			"", nil, false, unknownVersion, true),
		Entry("nil successful build info fallback",
			"", nil, true, unknownVersion, true),
		Entry("leading v normalization",
			"", buildInfo("v2.3.4"), true, "2.3.4", true),
		Entry("main development version",
			"0.0.0-main-deadbee", buildInfo("v9.9.9"), true, "0.0.0-main-deadbee", false),
		Entry("empty main module version",
			"", buildInfo(""), true, unknownVersion, true),
	)
})

func buildInfo(version string) *debug.BuildInfo {
	return &debug.BuildInfo{Main: debug.Module{Version: version}}
}
