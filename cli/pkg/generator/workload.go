// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package generator

import (
	"fmt"
	"io"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/cli-runtime/pkg/printers"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/workload"
)

// Options controls how a set of workload views is rendered.
type Options struct {
	// Output selects the format. The zero value renders the default table, so
	// Options{} is usable as-is.
	Output Output
	// Namespace is reported in the empty-result message.
	Namespace string
	// AllNamespaces drops the namespace from the empty-result message.
	AllNamespaces bool
}

// RenderWorkloads writes views to out. The machine formats go through Render, so
// every command emits the same shapes; only the table is specific to a view. The
// empty-result notice goes to errOut so it cannot corrupt piped output.
func RenderWorkloads(out, errOut io.Writer, views []workload.View, opts Options) error {
	format := opts.Output
	if format == "" {
		format = OutputTable
	}

	return Render(out, format, views, func(w io.Writer) error {
		if len(views) == 0 {
			notice := fmt.Sprintf("No workloads found in namespace %s.", opts.Namespace)
			if opts.AllNamespaces {
				notice = "No workloads found in any namespace."
			}
			// On an empty result the notice is the whole output; no flush rechecks it.
			if _, err := fmt.Fprintln(errOut, notice); err != nil {
				return fmt.Errorf("write empty-result notice: %w", err)
			}
			return nil
		}
		return renderWorkloadTable(w, views, format)
	})
}

func renderWorkloadTable(out io.Writer, views []workload.View, format Output) error {
	writer := printers.GetNewTabWriter(out)

	headers := []string{"NAME", "NAMESPACE", "PHASE", "AGE"}
	if format == OutputWide {
		headers = append(headers, "ORIGIN")
	}
	fmt.Fprintln(writer, strings.Join(headers, "\t"))

	now := time.Now()
	for _, view := range views {
		cells := []string{
			view.Name,
			view.Namespace,
			strings.Join(view.Phases, ","),
			age(now, view.CreatedAt),
		}
		if format == OutputWide {
			cells = append(cells, view.Origin)
		}
		fmt.Fprintln(writer, strings.Join(cells, "\t"))
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("write workloads table: %w", err)
	}
	return nil
}

// age formats a timestamp, unset for a workload resolved outside a live read.
func age(now, createdAt time.Time) string {
	if createdAt.IsZero() {
		return "<unknown>"
	}
	return duration.HumanDuration(now.Sub(createdAt))
}
