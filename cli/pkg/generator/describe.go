// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package generator

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/cli-runtime/pkg/printers"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/workload"
)

// ShowAllPods is the --pod-limit default: a hidden pod is the one a reader most
// needs to see. Zero means the same, so no limit is the only reading of 0.
const ShowAllPods = -1

// fileModeNote marks output built from a manifest that never reached a cluster,
// so an empty status section reads as "not applicable" and not as "healthy".
const fileModeNote = "(file mode: no live status)"

// DescribeOptions controls how one workload is rendered.
type DescribeOptions struct {
	// Output selects the format. The zero value renders the default table, so
	// DescribeOptions{} is usable as-is.
	Output Output
	// PodLimit caps the pod rows per component. Zero and negative both show
	// every pod, so DescribeOptions{} and an explicit --pod-limit 0 agree.
	PodLimit int
}

// RenderWorkload writes one workload to out. The machine formats emit the view
// itself, so what a human reads and what an agent parses cannot drift.
func RenderWorkload(out io.Writer, view *workload.DescribeView, opts DescribeOptions) error {
	format := opts.Output
	if format == "" {
		format = OutputTable
	}

	limit := opts.PodLimit
	if limit == 0 {
		limit = ShowAllPods
	}

	return RenderOne(out, format, view, func(w io.Writer) error {
		return workloadText(w, view, limit)
	})
}

func workloadText(out io.Writer, view *workload.DescribeView, limit int) error {
	if err := writeHeader(out, view); err != nil {
		return err
	}
	if err := writeTree(out, view, limit); err != nil {
		return err
	}
	if err := writeStatus(out, view); err != nil {
		return err
	}
	return writeResources(out, view)
}

func writeHeader(out io.Writer, view *workload.DescribeView) error {
	fields := []string{
		fmt.Sprintf("%s/%s", view.Kind, view.Name),
		"namespace: " + orNone(view.Namespace),
		fmt.Sprintf("definition: %s (%s)", view.Definition, view.Origin),
	}
	if !view.FileMode {
		fields = append(fields, "age: "+age(time.Now(), view.CreatedAt))
	}

	if _, err := fmt.Fprintln(out, strings.Join(fields, "   ")); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if view.FileMode {
		if _, err := fmt.Fprintln(out, fileModeNote); err != nil {
			return fmt.Errorf("write header: %w", err)
		}
	}
	return nil
}

// writeTree renders the component hierarchy, one row per component and one per
// pod, through a tab writer so every column lines up across both row kinds.
func writeTree(out io.Writer, view *workload.DescribeView, limit int) error {
	if len(view.Components) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return fmt.Errorf("write tree: %w", err)
	}

	// Every row keeps all its cells so the columns stay aligned down the tree.
	var tree bytes.Buffer
	writer := printers.GetNewTabWriter(&tree)
	writeComponents(writer, view.Components, "", limit, view.FileMode)
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("write tree: %w", err)
	}
	for line := range strings.Lines(tree.String()) {
		if _, err := fmt.Fprintln(out, strings.TrimRight(line, " \n")); err != nil {
			return fmt.Errorf("write tree: %w", err)
		}
	}
	return nil
}

func writeComponents(out io.Writer, components []workload.ComponentView, prefix string, limit int, fileMode bool) {
	for i, component := range components {
		last := i == len(components)-1
		fmt.Fprintln(out, strings.Join([]string{
			prefix + branch(last) + component.Name,
			scale(component.Replicas, fileMode),
			resourceCell(component.Resources),
			strings.Join(component.Nodes, ","),
		}, "\t"))

		childPrefix := prefix + indent(last)
		writePods(out, component.Pods, childPrefix, limit, len(component.Children) > 0)
		writeComponents(out, component.Children, childPrefix, limit, fileMode)
	}
}

// writePods draws a component's pods. childComponents says whether component
// rows follow at this same depth, which decides who owns the closing glyph.
func writePods(out io.Writer, pods []workload.PodView, prefix string, limit int, childComponents bool) {
	shown, hidden, shownUnhealthy, allUnhealthy := limitPods(pods, limit)

	for i, pod := range shown {
		last := i == len(shown)-1 && hidden == 0 && !childComponents
		fmt.Fprintln(out, strings.Join([]string{
			prefix + branch(last) + pod.Name,
			podStatus(pod),
			resourceCell(pod.Resources),
			orNone(deref(pod.Node)),
		}, "\t"))
	}

	if hidden > 0 {
		// Naming the unhealthy total keeps the note from implying every failing
		// pod survived the cut, which holds only while they fit.
		coverage := fmt.Sprintf("%d unhealthy shown", shownUnhealthy)
		if shownUnhealthy < allUnhealthy {
			coverage = fmt.Sprintf("%d of %d unhealthy shown", shownUnhealthy, allUnhealthy)
		}
		// The prose sits in the status cell so it cannot widen the name column.
		fmt.Fprintln(out, strings.Join([]string{
			prefix + branch(!childComponents) + "...",
			fmt.Sprintf("and %d more (%s)", hidden, coverage),
			"", "",
		}, "\t"))
	}
}

// limitPods applies --pod-limit. Unhealthy pods sort first, so the rows that
// survive truncation go to the failing pods before the healthy ones.
func limitPods(pods []workload.PodView, limit int) (shown []workload.PodView, hidden, shownUnhealthy, allUnhealthy int) {
	for _, pod := range pods {
		if podUnhealthy(pod) {
			allUnhealthy++
		}
	}
	if limit < 0 || len(pods) <= limit {
		return pods, 0, allUnhealthy, allUnhealthy
	}

	ordered := slices.Clone(pods)
	slices.SortStableFunc(ordered, func(a, b workload.PodView) int {
		switch {
		case podUnhealthy(a) == podUnhealthy(b):
			return 0
		case podUnhealthy(a):
			return -1
		default:
			return 1
		}
	})

	shown = ordered[:limit]
	for _, pod := range shown {
		if podUnhealthy(pod) {
			shownUnhealthy++
		}
	}
	return shown, len(ordered) - limit, shownUnhealthy, allUnhealthy
}

// podUnhealthy separates a pod needing attention from one that finished: a
// completed pod is not ready either, and would take a failing pod's row.
func podUnhealthy(pod workload.PodView) bool {
	return !pod.Ready && pod.Phase != string(corev1.PodSucceeded)
}

func writeStatus(out io.Writer, view *workload.DescribeView) error {
	if view.FileMode {
		return nil
	}
	// Several status mappings can match at once, and hiding one would misreport
	// the workload, so every matched phase is named.
	if _, err := fmt.Fprintf(out, "\nPhase: %s\n", strings.Join(view.Phases, ",")); err != nil {
		return fmt.Errorf("write status: %w", err)
	}
	return nil
}

// writeResources breaks the request down per component, with the workload total
// last, so a reader can see which component accounts for the bill.
func writeResources(out io.Writer, view *workload.DescribeView) error {
	if _, err := fmt.Fprintln(out, "\nResources:"); err != nil {
		return fmt.Errorf("write resources: %w", err)
	}

	writer := printers.GetNewTabWriter(out)
	fmt.Fprintln(writer, "COMPONENT\tREPLICAS\tGPU\tCPU\tMEMORY")

	var replicas int32
	for _, row := range resourceRows(view.Components) {
		replicas += row.replicas
		writeResourceRow(writer, row.name, row.replicas, row.request)
	}
	writeResourceRow(writer, "TOTAL", replicas, view.Resources)

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("write resources: %w", err)
	}
	return nil
}

func writeResourceRow(out io.Writer, name string, replicas int32, request workload.Resources) {
	fmt.Fprintf(out, "%s\t%d\t%d\t%s\t%s\n",
		name, replicas, request.GPUs, cpu(request.CPUMillis), memory(request.MemoryBytes))
}

type resourceRow struct {
	name     string
	replicas int32
	request  workload.Resources
}

// resourceRows charges a component only what it requests beyond its children,
// whose totals already roll up into it, so the rows sum to TOTAL.
func resourceRows(components []workload.ComponentView) []resourceRow {
	var rows []resourceRow
	for _, component := range components {
		if !isGrouping(component) {
			rows = append(rows, resourceRow{component.Name, component.Replicas.Desired, ownRequest(component)})
		}
		rows = append(rows, resourceRows(component.Children)...)
	}
	return rows
}

// isGrouping reports a component that only repeats its children: it runs no pods
// of its own, requests nothing beyond theirs, and its replicas are their sum.
func isGrouping(component workload.ComponentView) bool {
	if len(component.Children) == 0 || len(component.Pods) > 0 {
		return false
	}
	var replicas int32
	for _, child := range component.Children {
		replicas += child.Replicas.Desired
	}
	return ownRequest(component) == (workload.Resources{}) && component.Replicas.Desired == replicas
}

// ownRequest subtracts the children a component already rolled up. Replicas
// need no such correction: only a grouping component counts its children's.
func ownRequest(component workload.ComponentView) workload.Resources {
	request := component.Resources
	for _, child := range component.Children {
		request.GPUs -= child.Resources.GPUs
		request.CPUMillis -= child.Resources.CPUMillis
		request.MemoryBytes -= child.Resources.MemoryBytes
	}
	return request
}

func branch(last bool) string {
	if last {
		return "`-- "
	}
	return "|-- "
}

func indent(last bool) string {
	if last {
		return "    "
	}
	return "|   "
}

// scale reports readiness against the desired count. File mode has no pods, so
// it reports the count alone; "0/9 ready" would read as nine that failed.
func scale(replicas workload.Replicas, fileMode bool) string {
	if fileMode {
		return fmt.Sprintf("replicas: %d", replicas.Desired)
	}
	return fmt.Sprintf("%d/%d ready", replicas.Ready, replicas.Desired)
}

func podStatus(pod workload.PodView) string {
	if pod.Reason == "" {
		return pod.Phase
	}
	return fmt.Sprintf("%s (%s)", pod.Phase, pod.Reason)
}

func resourceCell(request workload.Resources) string {
	if request.GPUs == 0 {
		return ""
	}
	return fmt.Sprintf("gpu: %d", request.GPUs)
}

// cpu renders millicores the way a request is written, so 20000m reads as 20.
func cpu(millis int64) string {
	return resource.NewMilliQuantity(millis, resource.DecimalSI).String()
}

// memory prefers binary units and falls back to decimal, so a request written
// as 70M renders as 70M rather than as a raw byte count.
func memory(bytes int64) string {
	if binary := resource.NewQuantity(bytes, resource.BinarySI).String(); strings.HasSuffix(binary, "i") {
		return binary
	}
	return resource.NewQuantity(bytes, resource.DecimalSI).String()
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func orNone(value string) string {
	if value == "" {
		return "<none>"
	}
	return value
}
