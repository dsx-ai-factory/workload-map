// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package main validates a Karta definition and, given a real workload object,
// reports what the definition extracted from it. See README.md for usage.
//
// Exit codes: 0 success, 1 load or validation failure, 2 prediction mismatch,
// 3 warnings under --strict.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/resource"
	"github.com/dsx-ai-factory/workload-map/pkg/tree"
)

// observation doubles as the predictions format, so --dump output can seed the
// next run's prediction.
type observation struct {
	Status     []string    `json:"status"`
	Components []component `json:"components"`
}

// component is one extracted component instance. Key is "name",
// "name[instanceId]" when multi-instance, prefixed by the owner path when
// nested ("group/leader"). In a prediction, only the fields set are compared.
type component struct {
	Key         string   `json:"key"`
	Replicas    *int32   `json:"replicas,omitempty"`
	MinReplicas *int32   `json:"minReplicas,omitempty"`
	MaxReplicas *int32   `json:"maxReplicas,omitempty"`
	PodSpec     *bool    `json:"podSpec,omitempty"`
	Containers  []string `json:"containers,omitempty"`
}

func main() {
	var kartaPath, workloadPath, predictPath, dumpPath string
	var strict bool
	flag.StringVar(&kartaPath, "karta", "", "path to the Karta definition (required)")
	flag.StringVar(&workloadPath, "workload", "", "path to a real workload manifest; without it, validation only")
	flag.StringVar(&predictPath, "predict", "", "path to a predictions file to check the extraction against")
	flag.StringVar(&dumpPath, "dump", "", "write the observed extraction to this path, in predictions format")
	flag.BoolVar(&strict, "strict", false, "exit non-zero when the extraction reports warnings")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage:\n"+
			"  go run ./hack/karta-verify --karta <definition.yaml>                           validate only\n"+
			"  go run ./hack/karta-verify --karta <definition.yaml> --workload <real-cr.yaml> validate and extract\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if kartaPath == "" {
		flag.Usage()
		os.Exit(1)
	}

	// Refused rather than ignored, so a dropped --workload cannot exit 0.
	if workloadPath == "" {
		for flagName, set := range map[string]bool{"--predict": predictPath != "", "--dump": dumpPath != "", "--strict": strict} {
			if set {
				fmt.Fprintf(os.Stderr, "error: %s requires --workload\n", flagName)
				os.Exit(1)
			}
		}
	}

	code, err := run(context.Background(), kartaPath, workloadPath, predictPath, dumpPath, strict)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
	}
	os.Exit(code)
}

func run(ctx context.Context, kartaPath, workloadPath, predictPath, dumpPath string, strict bool) (int, error) {
	kartaYAML, err := os.ReadFile(kartaPath)
	if err != nil {
		return 1, fmt.Errorf("read Karta definition: %w", err)
	}
	karta := &v1alpha1.Karta{}
	if err := yaml.Unmarshal(kartaYAML, karta); err != nil {
		return 1, fmt.Errorf("parse Karta definition: %w", err)
	}

	// Validate first, so a structural error is not reported as an extraction failure.
	if err := v1alpha1.NewKartaValidator(karta).Validate(); err != nil {
		return 1, fmt.Errorf("invalid Karta definition: %w", err)
	}
	fmt.Printf("=== Validation ===\n  %s is structurally valid\n\n", kartaPath)

	if workloadPath == "" {
		fmt.Printf("No --workload given, so the definition was not run against a real object.\n" +
			"It is structurally valid; whether its jq paths resolve is still unproven.\n")
		return 0, nil
	}

	workloadYAML, err := os.ReadFile(workloadPath)
	if err != nil {
		return 1, fmt.Errorf("read workload: %w", err)
	}
	var rawObj map[string]any
	if err := yaml.Unmarshal(workloadYAML, &rawObj); err != nil {
		return 1, fmt.Errorf("parse workload: %w", err)
	}
	obj := &unstructured.Unstructured{Object: rawObj}

	wt, err := tree.Build(ctx, resource.NewComponentFactoryFromObject(karta, obj))
	if err != nil {
		return 1, fmt.Errorf("build workload tree against %s: %w", workloadPath, err)
	}

	observed := observation{Components: []component{}}
	if wt.Status != nil {
		observed.Status = wt.Status.Phases
	}
	// Collected during the walk, not from wt.Children, to catch nested components.
	// A node with no instances contributes no rows, so nothing else reports it.
	var noInstances []string
	var walk func(prefix string, nodes []tree.ComponentNode)
	walk = func(prefix string, nodes []tree.ComponentNode) {
		for _, node := range nodes {
			if len(node.Instances) == 0 {
				noInstances = append(noInstances, prefix+node.Name)
			}
			for _, inst := range node.Instances {
				key := prefix + node.Name
				if inst.InstanceKey != nil {
					key = fmt.Sprintf("%s[%s]", key, *inst.InstanceKey)
				}
				if inst.ReplicaKey != nil {
					key = fmt.Sprintf("%s{%s}", key, *inst.ReplicaKey)
				}
				c := component{Key: key}
				if inst.Scale != nil {
					c.Replicas, c.MinReplicas, c.MaxReplicas = inst.Scale.Replicas, inst.Scale.MinReplicas, inst.Scale.MaxReplicas
				}
				extracted := false
				if e := inst.ExtractedInstance; e != nil {
					switch {
					case e.PodTemplateSpec != nil:
						extracted = true
						for _, ctr := range e.PodTemplateSpec.Spec.Containers {
							c.Containers = append(c.Containers, ctr.Name)
						}
					case e.PodSpec != nil:
						extracted = true
						for _, ctr := range e.PodSpec.Containers {
							c.Containers = append(c.Containers, ctr.Name)
						}
					case e.FragmentedPodSpec != nil:
						extracted = true
						for _, ctr := range e.FragmentedPodSpec.Containers {
							c.Containers = append(c.Containers, ctr.Name)
						}
						if ctr := e.FragmentedPodSpec.Container; ctr != nil {
							c.Containers = append(c.Containers, ctr.Name)
						}
					}
				}
				if node.HasPodDefinition {
					c.PodSpec = &extracted
				}
				observed.Components = append(observed.Components, c)
				walk(key+"/", inst.Children)
			}
		}
	}
	walk("", wt.Children)

	// The resolver reports Undefined explicitly, so unresolved is empty or that value.
	unresolved := len(observed.Status) == 0 ||
		(len(observed.Status) == 1 && observed.Status[0] == string(v1alpha1.UndefinedStatus))

	fmt.Printf("=== Extracted from %s ===\n", workloadPath)
	status := formatList(observed.Status)
	if unresolved {
		status += " (no rule in statusDefinition matched this object)"
	}
	fmt.Printf("  status: %s\n", status)
	for _, c := range observed.Components {
		fmt.Printf("  %-34s replicas=%-8s min=%-6s max=%-6s podSpec=%-9s containers=%s\n",
			c.Key, formatInt(c.Replicas), formatInt(c.MinReplicas), formatInt(c.MaxReplicas),
			formatBool(c.PodSpec), formatList(c.Containers))
	}
	fmt.Println()

	var warnings []string
	if unresolved {
		warnings = append(warnings, "status is unresolved: no rule in statusDefinition matched this object")
	}
	for _, c := range observed.Components {
		if c.PodSpec != nil && !*c.PodSpec {
			warnings = append(warnings, fmt.Sprintf("%s declares a specDefinition but extracted no pod spec: the spec path missed", c.Key))
		}
		if c.PodSpec != nil && *c.PodSpec && len(c.Containers) == 0 {
			warnings = append(warnings, fmt.Sprintf("%s extracted a pod spec with no containers", c.Key))
		}
	}
	for _, name := range noInstances {
		warnings = append(warnings, fmt.Sprintf("%s produced no instances: instanceIdPath matched nothing", name))
	}
	if len(warnings) > 0 {
		fmt.Println("=== Warnings ===")
		for _, w := range warnings {
			fmt.Printf("  %s\n", w)
		}
		fmt.Println()
	}

	if dumpPath != "" {
		out, err := yaml.Marshal(observed)
		if err != nil {
			return 1, fmt.Errorf("marshal observation: %w", err)
		}
		if err := os.WriteFile(dumpPath, out, 0o600); err != nil {
			return 1, fmt.Errorf("write dump: %w", err)
		}
		fmt.Printf("Wrote observed extraction to %s\n\n", dumpPath)
	}

	if predictPath != "" {
		predictYAML, err := os.ReadFile(predictPath)
		if err != nil {
			return 1, fmt.Errorf("read predictions: %w", err)
		}
		predicted := observation{}
		if err := yaml.Unmarshal(predictYAML, &predicted); err != nil {
			return 1, fmt.Errorf("parse predictions: %w", err)
		}
		mismatches := compare(predicted, observed)
		fmt.Println("=== Prediction check ===")
		if len(mismatches) > 0 {
			for _, m := range mismatches {
				fmt.Printf("  MISMATCH %s\n", m)
			}
			fmt.Printf("\n%d mismatch(es). The definition does not do what it was expected to do;\n"+
				"fix the definition, or re-derive the prediction from the manifest, then run again.\n"+
				"Do not copy the extracted values into the prediction to make this pass.\n", len(mismatches))
			return 2, nil
		}
		fmt.Printf("  all %d predicted value(s) matched the extraction\n\n", countPredicted(predicted))
	}

	if strict && len(warnings) > 0 {
		return 3, fmt.Errorf("%d warning(s) under --strict", len(warnings))
	}
	return 0, nil
}

// compare checks only what the prediction declares, so a partial prediction is valid.
func compare(predicted, observed observation) []string {
	byKey := make(map[string]component, len(observed.Components))
	for _, c := range observed.Components {
		byKey[c.Key] = c
	}

	var mismatches []string
	if predicted.Status != nil {
		want, got := append([]string(nil), predicted.Status...), append([]string(nil), observed.Status...)
		sort.Strings(want)
		sort.Strings(got)
		if strings.Join(want, ",") != strings.Join(got, ",") {
			mismatches = append(mismatches, fmt.Sprintf("status: predicted %s, extracted %s", formatList(predicted.Status), formatList(observed.Status)))
		}
	}
	for _, want := range predicted.Components {
		got, ok := byKey[want.Key]
		if !ok {
			keys := make([]string, 0, len(byKey))
			for k := range byKey {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			mismatches = append(mismatches, fmt.Sprintf("%s: predicted but not extracted; extracted keys are %s", want.Key, formatList(keys)))
			continue
		}
		for _, f := range []struct {
			name      string
			want, got *int32
		}{
			{"replicas", want.Replicas, got.Replicas},
			{"minReplicas", want.MinReplicas, got.MinReplicas},
			{"maxReplicas", want.MaxReplicas, got.MaxReplicas},
		} {
			if f.want != nil && (f.got == nil || *f.got != *f.want) {
				mismatches = append(mismatches, fmt.Sprintf("%s %s: predicted %s, extracted %s", want.Key, f.name, formatInt(f.want), formatInt(f.got)))
			}
		}
		if want.PodSpec != nil && (got.PodSpec == nil || *got.PodSpec != *want.PodSpec) {
			mismatches = append(mismatches, fmt.Sprintf("%s podSpec: predicted %s, extracted %s", want.Key, formatBool(want.PodSpec), formatBool(got.PodSpec)))
		}
		if want.Containers != nil {
			wantC, gotC := append([]string(nil), want.Containers...), append([]string(nil), got.Containers...)
			sort.Strings(wantC)
			sort.Strings(gotC)
			if strings.Join(wantC, ",") != strings.Join(gotC, ",") {
				mismatches = append(mismatches, fmt.Sprintf("%s containers: predicted %s, extracted %s", want.Key, formatList(want.Containers), formatList(got.Containers)))
			}
		}
	}
	return mismatches
}

func countPredicted(p observation) int {
	n := 0
	if p.Status != nil {
		n++
	}
	for _, c := range p.Components {
		for _, set := range []bool{c.Replicas != nil, c.MinReplicas != nil, c.MaxReplicas != nil, c.PodSpec != nil, c.Containers != nil} {
			if set {
				n++
			}
		}
	}
	return n
}

func formatInt(v *int32) string {
	if v == nil {
		return "<none>"
	}
	return fmt.Sprintf("%d", *v)
}

func formatBool(v *bool) string {
	if v == nil {
		return "n/a"
	}
	if *v {
		return "yes"
	}
	return "NO"
}

func formatList(v []string) string {
	if len(v) == 0 {
		return "<none>"
	}
	return strings.Join(v, ",")
}
