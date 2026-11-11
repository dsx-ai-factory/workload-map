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

	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/karta"
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
	definition := &v1alpha1.Karta{}
	if err := yaml.Unmarshal(kartaYAML, definition); err != nil {
		return fmt.Errorf("parse Karta: %w", err)
	}

	// Parse the workload into an unstructured object.
	var rawObj map[string]any
	if err := yaml.Unmarshal(ex.workloadYAML, &rawObj); err != nil {
		return fmt.Errorf("parse workload: %w", err)
	}
	obj := &unstructured.Unstructured{Object: rawObj}

	// karta.Load is the single entry point for all Karta operations. It binds
	// a deep copy of the object to the definition.
	w, err := karta.Load(definition, obj)
	if err != nil {
		return fmt.Errorf("load workload: %w", err)
	}

	// Read a snapshot for uniform inspection of status, scale, and pods.
	snapshot, err := w.Read(ctx)
	if err != nil {
		return fmt.Errorf("read workload: %w", err)
	}

	// ── Step 1: Read unified status ──────────────────────────────────────────
	fmt.Println("=== Workload status ===")
	status := "<none>"
	if snapshot.Status != nil {
		phases := make([]string, len(snapshot.Status.MatchedStatuses))
		for i, phase := range snapshot.Status.MatchedStatuses {
			phases[i] = string(phase)
		}
		status = strings.Join(phases, ", ")
	}
	fmt.Printf("  Karta workload status: %s\n\n", status)

	// ── Step 2: Inspect replica counts ───────────────────────────────────────
	// Scale values are read from wherever the CRD stores them — a JQ formula
	// for LWS, per replicatedJob entry for JobSet — with no per-type branching.
	fmt.Println("=== Component replica counts ===")
	for _, comp := range snapshot.Components {
		for _, inst := range comp.Instances {
			label := instanceLabel(comp.Name, inst.ID)
			if comp.PodSource == karta.PodSourceNone {
				label += " (virtual)"
			}
			if inst.Scale != nil && inst.Scale.Replicas != nil {
				fmt.Printf("  %-28s replicas=%d\n", label, *inst.Scale.Replicas)
			}
		}
	}
	fmt.Println()

	// ── Step 3: Resource requests per component ─────────────────────────────
	// Instance.Pod is the pod template however the definition locates it, so
	// container resources are directly accessible — no per-CRD path knowledge
	// required.
	fmt.Println("=== Resource requests per component ===")
	for _, comp := range snapshot.Components {
		for _, inst := range comp.Instances {
			if inst.Pod == nil {
				continue
			}
			for _, c := range inst.Pod.Spec.Containers {
				req := c.Resources.Requests
				lim := c.Resources.Limits
				// Extended resources (e.g. GPUs) are often set only in limits;
				// the API server normalises requests=limits, but offline YAML won't.
				gpu := req[corev1.ResourceName("nvidia.com/gpu")]
				if gpu.IsZero() {
					gpu = lim[corev1.ResourceName("nvidia.com/gpu")]
				}
				fmt.Printf("  %-28s container=%-12s cpu=%-8s memory=%-10s gpu=%s\n",
					instanceLabel(comp.Name, inst.ID), c.Name,
					formatQuantity(req.Cpu()),
					formatQuantity(req.Memory()),
					formatQuantity(&gpu),
				)
			}
		}
	}
	fmt.Println()

	// ── Step 4: Inject scheduler + label into all pod-bearing components ─────
	// MutatePodTemplates hands each instance's pod template to plain Go code; any pod
	// field can change. Karta writes back only what changed, at the right
	// path in the underlying CRD, without any per-type branching.
	fmt.Printf("=== Injecting scheduler %q + label ===\n", o.scheduler)
	for _, comp := range w.Definition().Components() {
		if comp.PodSource == karta.PodSourceNone {
			continue
		}
		instances := 0
		_, err := w.MutatePodTemplates(ctx, comp.Name, func(_ string, pod *corev1.PodTemplateSpec) error {
			pod.Spec.SchedulerName = o.scheduler
			if pod.Labels == nil {
				pod.Labels = map[string]string{}
			}
			pod.Labels["app.kubernetes.io/managed-by"] = "karta"
			instances++
			return nil
		}, karta.SkipAbsent())
		if err != nil {
			return fmt.Errorf("mutate pods of %s: %w", comp.Name, err)
		}
		noun := "instances"
		if instances == 1 {
			noun = "instance"
		}
		fmt.Printf("  Injected into %q (%d %s)\n", comp.Name, instances, noun)
	}
	fmt.Println()

	// ── Step 5: Verify via Karta read-back ───────────────────────────────────
	// MutatePodTemplates already verifies every write; reading back shows both
	// mutations landed regardless of where in the CRD the template lives.
	fmt.Println("=== Verification ===")
	snapshot, err = w.Read(ctx)
	if err != nil {
		return fmt.Errorf("read back workload: %w", err)
	}
	for _, comp := range snapshot.Components {
		for _, inst := range comp.Instances {
			if inst.Pod == nil {
				continue
			}
			fmt.Printf("  %-28s schedulerName=%-20q managed-by=%q\n",
				instanceLabel(comp.Name, inst.ID), inst.Pod.Spec.SchedulerName, inst.Pod.Labels["app.kubernetes.io/managed-by"])
		}
	}

	// ── Step 6: Retrieve the fully mutated object ─────────────────────────────
	// Object returns a deep copy of the mutated object, ready for
	//   k8sClient.Update(ctx, updated)
	updated := w.Object()
	fmt.Printf("\n  → In a real controller: if w.Modified() { k8sClient.Update(ctx, updated) }\n")

	// ── Optional: print full mutated CRD YAML ────────────────────────────────
	if o.printMutated {
		mutatedYAML, err := yaml.Marshal(updated.Object)
		if err != nil {
			return fmt.Errorf("marshal mutated %s: %w", ex.name, err)
		}
		fmt.Printf("\n=== Mutated %s YAML ===\n%s", ex.name, string(mutatedYAML))
	}

	return nil
}

func instanceLabel(component, id string) string {
	if id == "" {
		return component
	}
	return fmt.Sprintf("%s[%s]", component, id)
}
