// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package generator

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/workload"
)

// decodeYAML reads yaml through json, so one struct tag set serves both.
func decodeYAML(data []byte, into any) error { return yaml.Unmarshal(data, into) }

// detailView is the worked example from the describe design: a PyTorchJob with
// a ready master and a worker whose last replica cannot be scheduled.
func detailView() *workload.DescribeView {
	return &workload.DescribeView{
		View: workload.View{
			Name:       "llama-finetune",
			Namespace:  "ml-team",
			Kind:       "PyTorchJob",
			CreatedAt:  time.Now().Add(-2 * time.Hour),
			Definition: "kubeflow-org-pytorchjob-v1",
			Origin:     "catalog",
			Phases:     []string{"Running"},
		},
		Resources: workload.Resources{GPUs: 33, CPUMillis: 20000, MemoryBytes: 80 << 30},
		Components: []workload.ComponentView{
			{
				Name:      "master",
				Replicas:  workload.Replicas{Desired: 1, Current: 1, Ready: 1},
				Resources: workload.Resources{GPUs: 1, CPUMillis: 4000, MemoryBytes: 16 << 30},
				Nodes:     []string{"node-01"},
				Pods: []workload.PodView{
					{Name: "master-0", Phase: "Running", Ready: true, Node: ptr.To("node-01")},
				},
			},
			{
				Name:      "worker",
				Replicas:  workload.Replicas{Desired: 4, Current: 4, Ready: 3},
				Resources: workload.Resources{GPUs: 32, CPUMillis: 16000, MemoryBytes: 64 << 30},
				Nodes:     []string{"node-02", "node-03"},
				Pods: []workload.PodView{
					{Name: "worker-0", Phase: "Running", Ready: true, Node: ptr.To("node-02")},
					{Name: "worker-1", Phase: "Running", Ready: true, Node: ptr.To("node-02")},
					{Name: "worker-2", Phase: "Running", Ready: true, Node: ptr.To("node-03")},
					{Name: "worker-3", Phase: "Pending", Reason: "Unschedulable"},
				},
			},
		},
	}
}

// nestedView is a definition-driven shape the catalog does not produce but a
// custom Karta does: a pod-bearing component that also owns a child.
func nestedView() *workload.DescribeView {
	return &workload.DescribeView{
		View:      workload.View{Name: "svc", Namespace: "ml-team", Kind: "Deployment", Phases: []string{"Running"}},
		Resources: workload.Resources{GPUs: 13},
		Components: []workload.ComponentView{{
			Name:      "mid",
			Replicas:  workload.Replicas{Desired: 2, Current: 2, Ready: 2},
			Resources: workload.Resources{GPUs: 13},
			Pods: []workload.PodView{
				{Name: "mid-0", Phase: "Running", Ready: true, Node: ptr.To("node-02")},
				{Name: "mid-1", Phase: "Running", Ready: true, Node: ptr.To("node-02")},
			},
			Children: []workload.ComponentView{{
				Name:      "leaf",
				Replicas:  workload.Replicas{Desired: 3, Current: 3, Ready: 3},
				Resources: workload.Resources{GPUs: 3},
				Pods:      []workload.PodView{{Name: "leaf-0", Phase: "Running", Ready: true, Node: ptr.To("node-03")}},
			}},
		}},
	}
}

func renderWorkload(view *workload.DescribeView, opts DescribeOptions) string {
	GinkgoHelper()
	var out bytes.Buffer
	Expect(RenderWorkload(&out, view, opts)).To(Succeed())
	return out.String()
}

// treeLines returns the rows between the header and the status section.
func treeLines(out string) []string {
	GinkgoHelper()
	_, rest, found := strings.Cut(out, "\n")
	Expect(found).To(BeTrue())
	tree, _, found := strings.Cut(strings.TrimLeft(rest, "\n"), "\nPhase:")
	Expect(found).To(BeTrue())
	return strings.Split(strings.TrimRight(tree, "\n"), "\n")
}

var _ = Describe("RenderWorkload", func() {
	Context("the human rendering", func() {
		It("names the workload, its definition and its origin in the header", func() {
			header := strings.SplitN(renderWorkload(detailView(), DescribeOptions{}), "\n", 2)[0]

			Expect(header).To(ContainSubstring("PyTorchJob/llama-finetune"))
			Expect(header).To(ContainSubstring("namespace: ml-team"))
			Expect(header).To(ContainSubstring("definition: kubeflow-org-pytorchjob-v1 (catalog)"))
			Expect(header).To(ContainSubstring("age: "))
		})

		It("renders every pod by default, the way kubectl-tree renders every descendant", func() {
			lines := treeLines(renderWorkload(detailView(), DescribeOptions{}))

			Expect(lines).To(HaveLen(7), "two components and five pods")
			Expect(lines[0]).To(HavePrefix("|-- master"))
			Expect(lines[1]).To(HavePrefix("|   `-- master-0"))
			Expect(lines[2]).To(HavePrefix("`-- worker"))
			Expect(lines[6]).To(HavePrefix("    `-- worker-3"))
		})

		It("reads ready counts against the desired scale, not against the pods that exist", func() {
			Expect(treeLines(renderWorkload(detailView(), DescribeOptions{}))[2]).
				To(ContainSubstring("3/4 ready"))
		})

		It("names why a pod is not running", func() {
			Expect(renderWorkload(detailView(), DescribeOptions{})).
				To(ContainSubstring("Pending (Unschedulable)"))
		})

		It("marks an unscheduled pod rather than leaving the node column blank", func() {
			Expect(treeLines(renderWorkload(detailView(), DescribeOptions{}))[6]).
				To(HaveSuffix("<none>"))
		})

		It("breaks resources down per component with the workload total last", func() {
			_, resources, found := strings.Cut(renderWorkload(detailView(), DescribeOptions{}), "Resources:\n")
			Expect(found).To(BeTrue())

			rows := cells(resources)
			Expect(rows[0]).To(Equal([]string{"COMPONENT", "REPLICAS", "GPU", "CPU", "MEMORY"}))
			Expect(rows[1]).To(Equal([]string{"master", "1", "1", "4", "16Gi"}))
			Expect(rows[2]).To(Equal([]string{"worker", "4", "32", "16", "64Gi"}))
			Expect(rows[3]).To(Equal([]string{"TOTAL", "5", "33", "20", "80Gi"}))
		})

		// A request written as 70M is not a binary multiple, so binary units
		// alone would render it as a raw byte count.
		It("renders a decimal memory request in decimal units", func() {
			view := detailView()
			view.Resources = workload.Resources{MemoryBytes: 70_000_000}
			view.Components = []workload.ComponentView{{
				Name:      "loop",
				Replicas:  workload.Replicas{Desired: 1},
				Resources: workload.Resources{MemoryBytes: 70_000_000},
			}}

			_, resources, found := strings.Cut(renderWorkload(view, DescribeOptions{}), "Resources:\n")
			Expect(found).To(BeTrue())
			Expect(cells(resources)[1]).To(Equal([]string{"loop", "1", "0", "0", "70M"}))
		})

		It("names every matched phase, since status mappings are not exclusive", func() {
			view := detailView()
			view.Phases = []string{"Running", "Degraded"}

			Expect(renderWorkload(view, DescribeOptions{})).To(ContainSubstring("Phase: Running,Degraded"))
		})

		Context("file mode", func() {
			It("says the status is absent rather than rendering an empty one", func() {
				view := detailView()
				view.FileMode = true

				out := renderWorkload(view, DescribeOptions{})
				Expect(out).To(ContainSubstring("(file mode: no live status)"))
				Expect(out).NotTo(ContainSubstring("Phase:"))
				Expect(out).NotTo(ContainSubstring("age:"), "a manifest has no age")
			})

			// "0/4 ready" would read as four pods that failed to start, where
			// the truth is that there are no pods to be ready yet.
			It("reports the desired scale rather than a readiness of zero", func() {
				view := detailView()
				view.FileMode = true

				out := renderWorkload(view, DescribeOptions{})
				Expect(out).To(ContainSubstring("replicas: 4"))
				Expect(out).NotTo(ContainSubstring("ready"))
			})
		})
	})

	Context("--pod-limit", func() {
		It("shows the unhealthy pod first, so truncation cannot hide it", func() {
			lines := treeLines(renderWorkload(detailView(), DescribeOptions{PodLimit: 2}))

			Expect(lines[3]).To(ContainSubstring("worker-3"), "the first worker row is the failing one")
			Expect(lines[5]).To(ContainSubstring("`-- ..."))
			Expect(lines[5]).To(ContainSubstring("and 2 more (1 unhealthy shown)"))
		})

		It("reports what it hid rather than truncating silently", func() {
			Expect(renderWorkload(detailView(), DescribeOptions{PodLimit: 1})).
				To(ContainSubstring("and 3 more (1 unhealthy shown)"))
		})

		// A completed pod is not ready either, so ranking it unhealthy would let
		// it take the row truncation reserves for the failing one.
		It("ranks a failing pod above one that ran to completion", func() {
			view := detailView()
			view.Components[1].Pods = []workload.PodView{
				{Name: "worker-0", Phase: "Succeeded", Reason: "PodCompleted"},
				{Name: "worker-1", Phase: "Succeeded", Reason: "PodCompleted"},
				{Name: "worker-2", Phase: "Pending", Reason: "Unschedulable"},
			}

			lines := treeLines(renderWorkload(view, DescribeOptions{PodLimit: 1}))

			Expect(lines[3]).To(ContainSubstring("worker-2"), "the failing pod keeps the only row")
			Expect(lines[4]).To(ContainSubstring("and 2 more (1 unhealthy shown)"))
		})

		// Sorting the failing pods first only saves them while they fit, so the
		// note names the total rather than implying it covered them all.
		It("says how many unhealthy pods the rows could not reach", func() {
			view := detailView()
			view.Components[1].Pods = []workload.PodView{
				{Name: "worker-0", Phase: "Pending", Reason: "Unschedulable"},
				{Name: "worker-1", Phase: "Pending", Reason: "Unschedulable"},
				{Name: "worker-2", Phase: "Pending", Reason: "Unschedulable"},
			}

			Expect(renderWorkload(view, DescribeOptions{PodLimit: 1})).
				To(ContainSubstring("and 2 more (1 of 3 unhealthy shown)"))
		})

		It("leaves a component alone when its pods fit", func() {
			Expect(renderWorkload(detailView(), DescribeOptions{PodLimit: 4})).
				NotTo(ContainSubstring("and 0 more"))
		})

		// The zero value is what an unset int carries, and no pod rows at all
		// would hide the point of the command.
		It("treats an unset limit as showing every pod", func() {
			Expect(treeLines(renderWorkload(detailView(), DescribeOptions{}))).
				To(Equal(treeLines(renderWorkload(detailView(), DescribeOptions{PodLimit: ShowAllPods}))))
		})

		// A tabwriter ends a column block at a line with fewer cells, so a note
		// without them realigns every row below it. The note must land above
		// another row for that to show.
		It("keeps one column block across the truncation note", func() {
			lines := treeLines(renderWorkload(nestedView(), DescribeOptions{PodLimit: 1}))

			column := strings.Index(lines[0], "2/2 ready")
			Expect(column).To(BeNumerically(">", 0))
			for _, line := range lines {
				if status := strings.Index(line, "Running"); status > 0 {
					Expect(status).To(Equal(column), "status column moved on: "+line)
				}
			}
		})
	})

	Context("a component that owns both pods and children", func() {
		// The component's own request is real, and charging only the leaves
		// drops it from the breakdown while TOTAL still counts it.
		It("breaks the request down into rows that sum to the total", func() {
			_, resources, found := strings.Cut(renderWorkload(nestedView(), DescribeOptions{}), "Resources:\n")
			Expect(found).To(BeTrue())

			Expect(resources).To(ContainSubstring("mid         2          10"))
			Expect(resources).To(ContainSubstring("leaf        3          3"))
			Expect(resources).To(ContainSubstring("TOTAL       5          13"))
		})

		// The children follow the pods at the same depth, so a pod never closes
		// the branch its siblings continue.
		It("leaves the closing glyph to the last row at that depth", func() {
			lines := treeLines(renderWorkload(nestedView(), DescribeOptions{}))

			Expect(lines[2]).To(ContainSubstring("|-- mid-1"))
			Expect(lines[3]).To(ContainSubstring("`-- leaf"))
		})

		// Declaring no resources does not make a component grouping, and its
		// replicas are its own however little it requests.
		It("keeps a component that requests nothing but runs replicas", func() {
			view := nestedView()
			view.Components[0].Resources = workload.Resources{GPUs: 3}
			view.Resources = workload.Resources{GPUs: 3}

			_, resources, found := strings.Cut(renderWorkload(view, DescribeOptions{}), "Resources:\n")
			Expect(found).To(BeTrue())
			Expect(resources).To(ContainSubstring("mid         2          0"))
			Expect(resources).To(ContainSubstring("TOTAL       5          3"))
		})

		// Matching replica counts do not make a pod-bearing component grouping:
		// its pods are work no child row accounts for.
		It("keeps a pod-bearing component whose replicas match its children's", func() {
			view := nestedView()
			view.Components[0].Resources = workload.Resources{GPUs: 3}
			view.Components[0].Children[0].Replicas = workload.Replicas{Desired: 2, Current: 2, Ready: 2}
			view.Resources = workload.Resources{GPUs: 3}

			_, resources, found := strings.Cut(renderWorkload(view, DescribeOptions{}), "Resources:\n")
			Expect(found).To(BeTrue())
			Expect(resources).To(ContainSubstring("mid         2          0"))
			Expect(resources).To(ContainSubstring("TOTAL       4          3"))
		})

		// A grouping component repeats the rows below it, so a row of its own
		// would double the workload in the reader's head.
		It("leaves a grouping component out of the breakdown", func() {
			view := nestedView()
			view.Components[0].Pods = nil
			view.Components[0].Replicas = workload.Replicas{Desired: 3, Current: 3, Ready: 3}
			view.Components[0].Resources = workload.Resources{GPUs: 3}
			view.Resources = workload.Resources{GPUs: 3}

			_, resources, found := strings.Cut(renderWorkload(view, DescribeOptions{}), "Resources:\n")
			Expect(found).To(BeTrue())
			Expect(resources).NotTo(ContainSubstring("mid"))
			Expect(resources).To(ContainSubstring("TOTAL       3          3"))
		})
	})

	Context("machine output", func() {
		// An items/count envelope would say nothing about a single workload, and
		// would cost every consumer an items[0] hop.
		It("emits the view itself, not a list of one", func() {
			var out bytes.Buffer
			Expect(RenderWorkload(&out, detailView(), DescribeOptions{Output: OutputJSON})).To(Succeed())

			var decoded workload.DescribeView
			Expect(json.Unmarshal(out.Bytes(), &decoded)).To(Succeed())
			Expect(decoded.Name).To(Equal("llama-finetune"))
			Expect(decoded.Components).To(HaveLen(2))
		})

		// A consumer that has to re-parse "3/4" breaks silently when the human
		// rendering changes.
		It("carries counts and resources as numbers, never as display strings", func() {
			var out bytes.Buffer
			Expect(RenderWorkload(&out, detailView(), DescribeOptions{Output: OutputJSON})).To(Succeed())

			var decoded map[string]any
			Expect(json.Unmarshal(out.Bytes(), &decoded)).To(Succeed())
			Expect(decoded["resources"]).To(HaveKeyWithValue("gpus", BeNumerically("==", 33)))

			worker := decoded["components"].([]any)[1].(map[string]any)
			Expect(worker["replicas"]).To(HaveKeyWithValue("ready", BeNumerically("==", 3)))
			Expect(worker["replicas"]).To(HaveKeyWithValue("desired", BeNumerically("==", 4)))
		})

		It("reports an unscheduled pod as a null node", func() {
			var out bytes.Buffer
			Expect(RenderWorkload(&out, detailView(), DescribeOptions{Output: OutputJSON})).To(Succeed())

			Expect(out.String()).To(ContainSubstring(`"node": null`))
		})

		It("agrees with json on the same view", func() {
			view := detailView()

			var asJSON, asYAML bytes.Buffer
			Expect(RenderWorkload(&asJSON, view, DescribeOptions{Output: OutputJSON})).To(Succeed())
			Expect(RenderWorkload(&asYAML, view, DescribeOptions{Output: OutputYAML})).To(Succeed())

			var fromJSON, fromYAML workload.DescribeView
			Expect(json.Unmarshal(asJSON.Bytes(), &fromJSON)).To(Succeed())
			Expect(decodeYAML(asYAML.Bytes(), &fromYAML)).To(Succeed())
			Expect(fromYAML).To(Equal(fromJSON))
		})
	})
})
