// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package generator

import (
	"bytes"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/workload"
)

var views = []workload.View{{
	Name:      "preprocess",
	Namespace: "ml-team",
	Kind:      "JobSet",
	Phases:    []string{"Completed"},
	Origin:    "catalog",
}}

// cells splits a rendered table into its header and row fields. The tab writer
// pads with spaces and no cell contains one, so splitting on whitespace is
// enough, and comparing the fields pins the column set and its order. A
// substring check could not: "NAME" is a substring of "NAMESPACE".
func cells(out string) [][]string {
	var rows [][]string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		rows = append(rows, strings.Fields(line))
	}
	return rows
}

// failingWriter stands in for a closed or full stderr.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

var _ = Describe("RenderWorkloads", func() {
	DescribeTable("renders the table a format asks for",
		func(opts Options, headers, row []string) {
			var out, errOut bytes.Buffer
			Expect(RenderWorkloads(&out, &errOut, views, opts)).To(Succeed())

			Expect(cells(out.String())).To(Equal([][]string{headers, row}))
			Expect(errOut.Len()).To(BeZero())
		},
		Entry("the default columns",
			Options{Output: OutputTable},
			[]string{"NAME", "NAMESPACE", "PHASE", "AGE"},
			[]string{"preprocess", "ml-team", "Completed", "<unknown>"}),
		// Options{} has to stay usable, since table is the flag's default too.
		Entry("the zero value, as the default table",
			Options{},
			[]string{"NAME", "NAMESPACE", "PHASE", "AGE"},
			[]string{"preprocess", "ml-team", "Completed", "<unknown>"}),
		Entry("wide, which adds ORIGIN",
			Options{Output: OutputWide},
			[]string{"NAME", "NAMESPACE", "PHASE", "AGE", "ORIGIN"},
			[]string{"preprocess", "ml-team", "Completed", "<unknown>", "catalog"}),
	)

	// A view resolved outside a live read carries no timestamp, which must not
	// saturate to the age of the zero time.
	It("reports an unset timestamp as unknown", func() {
		var out, errOut bytes.Buffer
		Expect(RenderWorkloads(&out, &errOut,
			[]workload.View{{Name: "offline"}}, Options{})).To(Succeed())

		Expect(out.String()).To(ContainSubstring("<unknown>"))
	})

	// The flag layer rejects an unknown format, so reaching the renderer with one
	// means a programmatic caller passed it; a table would misrepresent the ask.
	It("rejects a format that is set but unrecognized", func() {
		var out, errOut bytes.Buffer
		err := RenderWorkloads(&out, &errOut, views, Options{Output: "bogus"})

		Expect(errors.Is(err, ErrUnsupportedOutput)).To(BeTrue())
		Expect(out.Len()).To(BeZero())
	})

	Describe("the empty result", func() {
		// The notice must not reach stdout, where it would corrupt a pipe.
		It("reports the namespace on stderr and exits cleanly", func() {
			var out, errOut bytes.Buffer
			Expect(RenderWorkloads(&out, &errOut, nil,
				Options{Output: OutputTable, Namespace: "ml-team"})).To(Succeed())

			Expect(out.Len()).To(BeZero())
			Expect(errOut.String()).To(ContainSubstring("No workloads found in namespace ml-team."))
		})

		It("drops the namespace when every namespace was searched", func() {
			var out, errOut bytes.Buffer
			Expect(RenderWorkloads(&out, &errOut, nil,
				Options{Output: OutputTable, AllNamespaces: true})).To(Succeed())

			Expect(errOut.String()).To(ContainSubstring("No workloads found in any namespace."))
		})

		// A caller has to be told when the notice never reached the terminal.
		It("reports a failure to write the notice", func() {
			var out bytes.Buffer
			err := RenderWorkloads(&out, failingWriter{}, nil, Options{Namespace: "ml-team"})

			Expect(err).To(MatchError(ContainSubstring("write empty-result notice")))
		})

		// Machine output carries the result alone, so a consumer parsing stdout
		// never has to strip a human message.
		It("stays out of the machine formats", func() {
			var out, errOut bytes.Buffer
			Expect(RenderWorkloads(&out, &errOut, nil,
				Options{Output: OutputJSON, Namespace: "ml-team"})).To(Succeed())

			Expect(out.String()).To(ContainSubstring(`"items": []`))
			Expect(errOut.Len()).To(BeZero())
		})
	})

	Describe("a request that named one workload", func() {
		It("emits the view itself, with no envelope around it", func() {
			var out, errOut bytes.Buffer
			Expect(RenderWorkloads(&out, &errOut, views,
				Options{Output: OutputJSON, Namespace: "ml-team", ByName: true})).To(Succeed())

			Expect(out.String()).NotTo(ContainSubstring(`"items"`))
			Expect(out.String()).To(ContainSubstring(`"name": "preprocess"`))
		})

		It("renders the same table as a list does", func() {
			var named, listed bytes.Buffer
			Expect(RenderWorkloads(&named, &bytes.Buffer{}, views,
				Options{Namespace: "ml-team", ByName: true})).To(Succeed())
			Expect(RenderWorkloads(&listed, &bytes.Buffer{}, views,
				Options{Namespace: "ml-team"})).To(Succeed())

			Expect(named.String()).To(Equal(listed.String()))
		})

		It("falls back to the envelope when nothing resolved", func() {
			var out, errOut bytes.Buffer
			Expect(RenderWorkloads(&out, &errOut, nil,
				Options{Output: OutputJSON, Namespace: "ml-team", ByName: true})).To(Succeed())

			Expect(out.String()).To(ContainSubstring(`"items": []`))
		})
	})
})
