// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package generator_test

import (
	"bytes"
	"errors"
	"io"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sigs.k8s.io/yaml"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/generator"
)

type item struct {
	Name string `json:"name"`
}

var items = []item{{Name: "alpha"}, {Name: "beta"}}

// unusedTable fails the spec if a machine format reaches the table callback.
func unusedTable(io.Writer) error {
	Fail("the table callback must not run for a machine format")
	return nil
}

var _ = Describe("Render", func() {
	DescribeTable("hands the human formats to the table callback",
		func(format generator.Output) {
			var out bytes.Buffer
			called := false
			Expect(generator.Render(&out, format, items, func(w io.Writer) error {
				called = true
				_, err := io.WriteString(w, "TABLE")
				return err
			})).To(Succeed())

			Expect(called).To(BeTrue())
			Expect(out.String()).To(Equal("TABLE"))
		},
		Entry("table", generator.OutputTable),
		// wide reaches the callback so a command that renders extra columns can.
		// One that cannot must reject wide before calling Render.
		Entry("wide", generator.OutputWide),
	)

	It("returns what the table callback returns", func() {
		boom := errors.New("boom")
		err := generator.Render(io.Discard, generator.OutputTable, items,
			func(io.Writer) error { return boom })
		Expect(err).To(MatchError(boom))
	})

	It("encodes json through the json tags", func() {
		var out bytes.Buffer
		Expect(generator.Render(&out, generator.OutputJSON, items, unusedTable)).To(Succeed())
		Expect(out.String()).To(ContainSubstring(`"name": "alpha"`))
	})

	It("emits an empty item list for no items, not null", func() {
		var out bytes.Buffer
		Expect(generator.Render[item](&out, generator.OutputJSON, nil, unusedTable)).To(Succeed())

		items, count := decodeEnvelope(out.String())
		Expect(items).To(BeEmpty())
		Expect(count).To(BeZero())
	})

	It("wraps yaml in the same envelope as json", func() {
		var yamlOut, jsonOut bytes.Buffer
		Expect(generator.Render(&yamlOut, generator.OutputYAML, items, unusedTable)).To(Succeed())
		Expect(generator.Render(&jsonOut, generator.OutputJSON, items, unusedTable)).To(Succeed())

		// One document, not a stream, so the two formats decode alike.
		Expect(yamlOut.String()).NotTo(ContainSubstring("\n---\n"))

		fromYAML, yamlCount := decodeEnvelope(yamlOut.String())
		fromJSON, jsonCount := decodeEnvelope(jsonOut.String())
		Expect(fromYAML).To(Equal(fromJSON))
		Expect(yamlCount).To(Equal(jsonCount))
	})

	It("names the format it cannot render", func() {
		var out bytes.Buffer
		err := generator.Render(&out, generator.Output("toml"), items, unusedTable)
		Expect(err).To(MatchError(generator.ErrUnsupportedOutput))
		Expect(err.Error()).To(ContainSubstring(`"toml"`))
		Expect(out.String()).To(BeEmpty())
	})
})

// decodeEnvelope reads the items and count the machine formats wrap a result in.
// yaml decodes through json, so one decoder serves both.
func decodeEnvelope(out string) ([]item, int) {
	GinkgoHelper()
	var envelope struct {
		Items []item `json:"items"`
		Count int    `json:"count"`
	}
	Expect(yaml.Unmarshal([]byte(out), &envelope)).To(Succeed())
	return envelope.Items, envelope.Count
}
