// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package main demonstrates how to use Karta to uniformly read and mutate
// distributed training workloads without writing per-CRD integration code.
//
// The same operations run over two completely different CRD types — JobSet and
// LeaderWorkerSet — without any per-type branching.
//
// Usage (from the docs/examples/quickstart directory):
//
//	go run . [flags]
//
// Flags:
//
//	--scheduler name to inject (default: kai-scheduler)
//	--print-mutated  Print the full mutated CRD YAML after injection
//
// Examples:
//
//	go run .
//	go run . --scheduler volcano
//	go run . --scheduler kai-scheduler --print-mutated
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/resource"
	"github.com/dsx-ai-factory/workload-map/pkg/tree"
)

// Sample workload objects embedded at compile time.
// In a real controller these come from the Kubernetes API (Reconcile request).

//go:embed jobset.yaml
var jobsetWorkloadYAML []byte

//go:embed lws.yaml
var lwsWorkloadYAML []byte

// workloadExample pairs a sample workload with its Karta definition path.
// Karta definitions live in docs/catalog/ and are read at runtime so they
// always reflect the latest version in the repository.
type workloadExample struct {
	name         string
	kartaPath    string
	workloadYAML []byte
}

// opts carries the parsed CLI flags shared across all workload samples.
type opts struct {
	scheduler    string
	printMutated bool
}

func formatQuantity(q *apiresource.Quantity) string {
	if q == nil || q.IsZero() {
		return "<none>"
	}
	return q.String()
}

// eachComponent visits every (ComponentNode, InstanceNode) pair depth-first,
// including nested children (e.g. LWS leader/worker inside each group instance).
func eachComponent(nodes []tree.ComponentNode, fn func(tree.ComponentNode, tree.InstanceNode)) {
	for _, comp := range nodes {
		for _, inst := range comp.Instances {
			fn(comp, inst)
			eachComponent(inst.Children, fn)
		}
	}
}

func main() {
	o := opts{}
	flag.StringVar(&o.scheduler, "scheduler", "kai-scheduler",
		"scheduler name to inject into all pod-bearing components")
	flag.BoolVar(&o.printMutated, "print-mutated", false,
		"print the full mutated CRD YAML after injection")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go run ./docs/examples/quickstart [flags]\n\nFlags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  go run ./docs/examples/quickstart\n")
		fmt.Fprintf(os.Stderr, "  go run ./docs/examples/quickstart --scheduler volcano\n")
		fmt.Fprintf(os.Stderr, "  go run ./docs/examples/quickstart --scheduler kai-scheduler --print-mutated\n")
	}
	flag.Parse()

	ctx := context.Background()

	examples := []workloadExample{
		{
			name:         "JobSet",
			kartaPath:    "../../catalog/jobset-x-k8s-io-jobset-v1alpha2.yaml",
			workloadYAML: jobsetWorkloadYAML,
		},
		{
			name:         "LeaderWorkerSet",
			kartaPath:    "../../catalog/leaderworkerset-x-k8s-io-leaderworkerset-v1.yaml",
			workloadYAML: lwsWorkloadYAML,
		},
	}

	for _, ex := range examples {
		fmt.Printf("══════════════════════════════════════════\n")
		fmt.Printf("  %s  (scheduler: %s)\n", ex.name, o.scheduler)
		fmt.Printf("══════════════════════════════════════════\n\n")
		if err := run(ctx, ex, o); err != nil {
			log.Fatalf("%s: %v", ex.name, err)
		}
		fmt.Println()
	}
}

// run executes the Karta operations for a single workload type.
// Notice there is no switch on CRD kind anywhere in this function — the Karta
// definition absorbs all structural differences between workload types.
func run(ctx context.Context, ex workloadExample, o opts) error {
	// Load the Karta definition from docs/catalog/.
	kartaYAML, err := os.ReadFile(ex.kartaPath)
	if err != nil {
		return fmt.Errorf("read Karta definition %s: %w", ex.kartaPath, err)
	}
	karta := &v1alpha1.Karta{}
	if err := yaml.Unmarshal(kartaYAML, karta); err != nil {
		return fmt.Errorf("parse Karta: %w", err)
	}

	// Parse the workload into an unstructured object.
	var rawObj map[string]any
	if err := yaml.Unmarshal(ex.workloadYAML, &rawObj); err != nil {
		return fmt.Errorf("parse workload: %w", err)
	}
	obj := &unstructured.Unstructured{Object: rawObj}

	// Create a ComponentFactory — the single entry point for all Karta operations.
	factory := resource.NewComponentFactoryFromObject(karta, obj)

	// Build a WorkloadTree for uniform inspection of status, scale, and spec resources.
	wt, err := tree.Build(ctx, factory)
	if err != nil {
		return fmt.Errorf("build workload tree: %w", err)
	}

	// ── Step 1: Read unified status ──────────────────────────────────────────
	fmt.Println("=== Workload status ===")
	status := "<none>"
	if wt.Status != nil {
		status = strings.Join(wt.Status.Phases, ", ")
	}
	fmt.Printf("  Karta workload status: %s\n\n", status)

	// ── Step 2: Inspect replica counts ───────────────────────────────────────
	// Scale values are read from wherever the CRD stores them — a JQ formula
	// for LWS, per replicatedJob entry for JobSet — with no per-type branching.
	fmt.Println("=== Component replica counts ===")
	eachComponent(wt.Children, func(comp tree.ComponentNode, inst tree.InstanceNode) {
		label := comp.Name
		if inst.InstanceKey != nil {
			label = fmt.Sprintf("%s[%s]", comp.Name, *inst.InstanceKey)
		}
		if !comp.HasPodDefinition {
			label += " (virtual)"
		}
		if inst.Scale != nil && inst.Scale.Replicas != nil {
			fmt.Printf("  %-28s replicas=%d\n", label, *inst.Scale.Replicas)
		}
	})
	fmt.Println()

	// ── Step 3: Resource requests per component ─────────────────────────────
	// PodTemplateSpec is extracted from the workload spec via Karta, so container
	// resources are directly accessible — no per-CRD path knowledge required.
	fmt.Println("=== Resource requests per component ===")
	eachComponent(wt.Children, func(comp tree.ComponentNode, inst tree.InstanceNode) {
		if !comp.HasPodDefinition || inst.ExtractedInstance == nil || inst.ExtractedInstance.PodTemplateSpec == nil {
			return
		}
		compLabel := comp.Name
		if inst.InstanceKey != nil {
			compLabel = fmt.Sprintf("%s[%s]", comp.Name, *inst.InstanceKey)
		}
		for _, c := range inst.ExtractedInstance.PodTemplateSpec.Spec.Containers {
			req := c.Resources.Requests
			lim := c.Resources.Limits
			cpu := req.Cpu()
			mem := req.Memory()
			// Extended resources (e.g. GPUs) are often set only in limits;
			// the API server normalises requests=limits, but offline YAML won't.
			gpu := req[corev1.ResourceName("nvidia.com/gpu")]
			if gpu.IsZero() {
				gpu = lim[corev1.ResourceName("nvidia.com/gpu")]
			}
			fmt.Printf("  %-28s container=%-12s cpu=%-8s memory=%-10s gpu=%s\n",
				compLabel, c.Name,
				formatQuantity(cpu),
				formatQuantity(mem),
				formatQuantity(&gpu),
			)
		}
	})
	fmt.Println()

	// ── Step 4: Inject scheduler + label into all pod-bearing components ─────
	// A single UpdatePodTemplateSpec call covers any field on the pod template —
	// not just schedulerName. Karta routes each mutation to the right path in
	// the underlying CRD without any per-type branching.
	children, err := factory.GetChildComponents()
	if err != nil {
		return fmt.Errorf("get child components: %w", err)
	}
	fmt.Printf("=== Injecting scheduler %q + label ===\n", o.scheduler)
	for _, comp := range children {
		if !comp.HasPodDefinition() {
			continue
		}
		podTemplateSpecs, err := comp.GetPodTemplateSpec(ctx)
		if err != nil {
			return fmt.Errorf("get pod template spec for %s: %w", comp.Name(), err)
		}
		updates := make(map[string]corev1.PodTemplateSpec, len(podTemplateSpecs))
		for id, pts := range podTemplateSpecs {
			pts.Spec.SchedulerName = o.scheduler
			if pts.Labels == nil {
				pts.Labels = make(map[string]string)
			}
			pts.Labels["app.kubernetes.io/managed-by"] = "karta"
			updates[id] = pts
		}
		if err := comp.UpdatePodTemplateSpec(ctx, updates); err != nil {
			return fmt.Errorf("update pod template spec for %s: %w", comp.Name(), err)
		}
		noun := "instances"
		if len(updates) == 1 {
			noun = "instance"
		}
		fmt.Printf("  Injected into %q (%d %s)\n", comp.Name(), len(updates), noun)
	}
	fmt.Println()

	// ── Step 5: Verify via Karta read-back ───────────────────────────────────
	// Reading back through Karta confirms both mutations landed at the right
	// paths regardless of where in the CRD structure the template lives.
	fmt.Println("=== Verification ===")
	for _, comp := range children {
		if !comp.HasPodDefinition() {
			continue
		}
		podTemplateSpecs, err := comp.GetPodTemplateSpec(ctx)
		if err != nil {
			return fmt.Errorf("read back pod template spec for %s: %w", comp.Name(), err)
		}
		for id, pts := range podTemplateSpecs {
			compLabel := comp.Name()
			if id != "" {
				compLabel = fmt.Sprintf("%s[%s]", comp.Name(), id)
			}
			fmt.Printf("  %-28s schedulerName=%-20q managed-by=%q\n",
				compLabel, pts.Spec.SchedulerName, pts.Labels["app.kubernetes.io/managed-by"])
		}
	}

	// ── Step 5: Retrieve the fully mutated object ─────────────────────────────
	// GetResource returns the modified unstructured object ready for
	//   k8sClient.Update(ctx, updated)
	updated, err := factory.GetResource()
	if err != nil {
		return fmt.Errorf("get updated resource: %w", err)
	}
	fmt.Printf("\n  → In a real controller: k8sClient.Update(ctx, updated)\n")

	// ── Optional: print full mutated CRD YAML ────────────────────────────────
	if o.printMutated {
		mutatedYAML, err := yaml.Marshal(updated.(*unstructured.Unstructured).Object)
		if err != nil {
			return fmt.Errorf("marshal mutated %s: %w", ex.name, err)
		}
		fmt.Printf("\n=== Mutated %s YAML ===\n%s", ex.name, string(mutatedYAML))
	}

	return nil
}
