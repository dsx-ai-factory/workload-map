// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package config

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Kinds JSON", func() {
	It("reads core and grouped kinds and trims surrounding whitespace", func() {
		var kinds Kinds
		Expect(json.Unmarshal([]byte(`["v1/Pod","apps/v1/Deployment","\t example.io/v1beta1/Widget \n"]`), &kinds)).To(Succeed())
		Expect(kinds).To(Equal(Kinds{
			{Version: "v1", Kind: "Pod"},
			{Group: "apps", Version: "v1", Kind: "Deployment"},
			{Group: "example.io", Version: "v1beta1", Kind: "Widget"},
		}))
	})

	DescribeTable("rejects invalid kind strings",
		func(value, message string) {
			data, err := json.Marshal([]string{value})
			Expect(err).NotTo(HaveOccurred())
			var kinds Kinds
			Expect(json.Unmarshal(data, &kinds)).To(MatchError(ContainSubstring(message)))
		},
		Entry("empty string", "", "expected group/version/Kind or version/Kind"),
		Entry("missing API version", "Pod", "expected group/version/Kind or version/Kind"),
		Entry("dot syntax", "Deployment.v1.apps", "expected group/version/Kind or version/Kind"),
		Entry("extra segment", "apps/v1/Deployment/extra", "expected group/version/Kind or version/Kind"),
		Entry("empty core version", "/Pod", "every segment must be nonempty and exact"),
		Entry("empty group", "/v1/Pod", "every segment must be nonempty and exact"),
		Entry("empty grouped version", "apps//Deployment", "every segment must be nonempty and exact"),
		Entry("empty kind", "apps/v1/", "every segment must be nonempty and exact"),
		Entry("space", "ap ps/v1/Deployment", "every segment must be nonempty and exact"),
		Entry("tab", "apps/v\t1/Deployment", "every segment must be nonempty and exact"),
		Entry("newline", "apps/v1/Deploy\nment", "every segment must be nonempty and exact"),
		Entry("carriage return", "apps/v1/Deploy\rment", "every segment must be nonempty and exact"),
		Entry("wildcard group", "*/v1/Deployment", "every segment must be nonempty and exact"),
		Entry("wildcard version", "apps/*/Deployment", "every segment must be nonempty and exact"),
		Entry("wildcard kind", "apps/v1/*", "every segment must be nonempty and exact"),
	)

	DescribeTable("rejects JSON values that are not string arrays",
		func(data string) {
			var kinds Kinds
			Expect(json.Unmarshal([]byte(data), &kinds)).To(MatchError(ContainSubstring("kinds must be group/version/Kind or version/Kind strings")))
		},
		Entry("string", `"v1/Pod"`),
		Entry("object", `{}`),
		Entry("boolean", `true`),
		Entry("number in array", `[1]`),
		Entry("object in array", `[{}]`),
	)

	It("preserves first occurrence order and round-trips the slash syntax", func() {
		var kinds Kinds
		Expect(json.Unmarshal([]byte(`["apps/v1/Deployment","v1/Pod","apps/v1/Deployment"," v1/Pod "]`), &kinds)).To(Succeed())
		Expect(kinds).To(Equal(Kinds{
			{Group: "apps", Version: "v1", Kind: "Deployment"},
			{Version: "v1", Kind: "Pod"},
		}))
		data, err := json.Marshal(kinds)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal(`["apps/v1/Deployment","v1/Pod"]`))
		var decoded Kinds
		Expect(json.Unmarshal(data, &decoded)).To(Succeed())
		Expect(decoded).To(Equal(kinds))
	})

	DescribeTable("normalizes empty input to an empty array",
		func(data string) {
			kinds := Kinds{{Version: "v1", Kind: "Pod"}}
			Expect(json.Unmarshal([]byte(data), &kinds)).To(Succeed())
			Expect(kinds).To(Equal(Kinds{}))
			encoded, err := json.Marshal(kinds)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(encoded)).To(Equal(`[]`))
		},
		Entry("empty array", `[]`),
		Entry("null", `null`),
	)

	It("encodes a nil slice as an empty array", func() {
		var kinds Kinds
		data, err := json.Marshal(kinds)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal(`[]`))
	})

	DescribeTable("leaves the receiver unchanged on failure",
		func(data string) {
			kinds := Kinds{{Group: "apps", Version: "v1", Kind: "Deployment"}}
			Expect(json.Unmarshal([]byte(data), &kinds)).To(HaveOccurred())
			Expect(kinds).To(Equal(Kinds{{Group: "apps", Version: "v1", Kind: "Deployment"}}))
		},
		Entry("invalid kind after a valid one", `["v1/Pod","bad"]`),
		Entry("invalid JSON type after a valid kind", `["v1/Pod",1]`),
		Entry("null array element", `[null]`),
		Entry("malformed JSON", `["v1/Pod"`),
	)
})
