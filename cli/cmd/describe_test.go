// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

var (
	podGVK = schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	podGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
)

// etlPod builds a pod of the JobSet fixture's "etl" replicated job, owned by
// the JobSet whose uid is given, so the attributor can claim it.
func etlPod(name, owner, node string, ready bool) *unstructured.Unstructured {
	status := map[string]any{
		"phase": "Running",
		"conditions": []any{map[string]any{
			"type": "Ready", "status": "True",
		}},
	}
	spec := map[string]any{
		"nodeName": node,
		"containers": []any{map[string]any{
			"name": "worker",
			"resources": map[string]any{
				"requests": map[string]any{"nvidia.com/gpu": "2"},
			},
		}},
	}
	if !ready {
		status = map[string]any{
			"phase": "Pending",
			"conditions": []any{map[string]any{
				"type": "PodScheduled", "status": "False", "reason": "Unschedulable",
			}},
		}
		delete(spec, "nodeName")
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name": name, "namespace": "ml-team",
			"labels": map[string]any{"jobset.sigs.k8s.io/replicatedjob-name": "etl"},
			"ownerReferences": []any{map[string]any{
				"apiVersion": jobSetGVK.GroupVersion().String(),
				"kind":       jobSetGVK.Kind,
				"name":       strings.TrimPrefix(owner, "ml-team/"),
				"uid":        owner,
				"controller": true,
			}},
		},
		"spec":   spec,
		"status": status,
	}}
}

// describeCluster points the command tree at an in-memory cluster serving
// JobSets and the pods that belong to them.
func describeCluster(t *testing.T, objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	t.Helper()

	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			jobSetGVR: "JobSetList",
			podGVR:    "PodList",
		}, objects...)

	mapper := meta.NewDefaultRESTMapper(nil)
	mapper.AddSpecific(jobSetGVK, jobSetGVR, jobSetGVK.GroupVersion().WithResource("jobset"), meta.RESTScopeNamespace)
	mapper.Add(podGVK, meta.RESTScopeNamespace)

	restore := newDynamicClient
	newDynamicClient = func(genericclioptions.RESTClientGetter) (dynamic.Interface, error) { return client, nil }
	t.Cleanup(func() { newDynamicClient = restore })

	flags := genericclioptions.NewTestConfigFlags().
		WithClientConfig(clientcmd.NewDefaultClientConfig(*clientcmdapi.NewConfig(), nil)).
		WithNamespace("ml-team").
		WithRESTMapper(mapper)

	restoreAccess := clusterAccess
	clusterAccess = func() genericclioptions.RESTClientGetter { return flags }
	t.Cleanup(func() { clusterAccess = restoreAccess })

	return client
}

// runDescribeCmd executes "kli describe" with args, returning stdout, stderr
// and the exit code the binary would produce.
func runDescribeCmd(t *testing.T, args ...string) (string, string, int) {
	t.Helper()

	root := NewRootCommand()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"describe"}, args...))

	code := 0
	if err := root.Execute(); err != nil {
		code = 1
		var coded interface{ ExitCode() int }
		if errors.As(err, &coded) {
			code = coded.ExitCode()
		}
		errOut.WriteString("error: " + err.Error() + "\n")
	}
	return out.String(), errOut.String(), code
}

func TestDescribeRendersEverySection(t *testing.T) {
	describeCluster(t, jobSet("preprocess", 3),
		etlPod("preprocess-etl-0", "ml-team/preprocess", "node-01", true),
		etlPod("preprocess-etl-1", "ml-team/preprocess", "node-02", true))

	out, errOut, code := runDescribeCmd(t, "jobset/preprocess")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	for _, want := range []string{
		"JobSet/preprocess", "namespace: ml-team", "definition: jobset-x-k8s-io-jobset-v1alpha2",
		"age: ", "preprocess-etl-0", "node-01", "Phase:", "Resources:", "TOTAL",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

// A selector says which role a pod plays, never which workload it belongs to,
// so only ownership keeps a neighbour's pods out of this workload's tree.
func TestDescribeShowsOnlyThisWorkloadsPods(t *testing.T) {
	describeCluster(t,
		jobSet("preprocess", 3), jobSet("neighbour", 3),
		etlPod("preprocess-etl-0", "ml-team/preprocess", "node-01", true),
		etlPod("neighbour-etl-0", "ml-team/neighbour", "node-09", true))

	out, _, code := runDescribeCmd(t, "jobset/preprocess")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "preprocess-etl-0") {
		t.Errorf("expected this workload's pod\n%s", out)
	}
	if strings.Contains(out, "neighbour-etl-0") {
		t.Errorf("another workload's pod leaked into the tree\n%s", out)
	}
}

func TestDescribeAcceptsBothArgumentForms(t *testing.T) {
	for _, args := range [][]string{
		{"jobset/preprocess"},
		{"jobset", "preprocess"},
		{"JobSet/preprocess"},
		{"jobsets/preprocess"},
	} {
		describeCluster(t, jobSet("preprocess", 3))

		out, errOut, code := runDescribeCmd(t, args...)
		if code != 0 {
			t.Errorf("%v: expected exit 0, got %d\n%s", args, code, errOut)
			continue
		}
		if !strings.Contains(out, "JobSet/preprocess") {
			t.Errorf("%v: expected the workload header\n%s", args, out)
		}
	}
}

// Phase 1 requires the kind, and the message has to show a reader both forms
// rather than only the one they did not use.
func TestDescribeRequiresAName(t *testing.T) {
	describeCluster(t, jobSet("preprocess", 3))

	_, errOut, code := runDescribeCmd(t, "jobset")
	if code != ExitUsage {
		t.Fatalf("expected exit %d, got %d\n%s", ExitUsage, code, errOut)
	}
	for _, want := range []string{"TYPE/NAME", "TYPE NAME"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("message missing %q\n%s", want, errOut)
		}
	}
}

func TestDescribeMissingWorkloadIsItsOwnFailure(t *testing.T) {
	describeCluster(t)

	_, errOut, code := runDescribeCmd(t, "jobset/absent")
	if code != ExitWorkloadNotFound {
		t.Fatalf("expected exit %d, got %d\n%s", ExitWorkloadNotFound, code, errOut)
	}
	if !strings.Contains(errOut, "not found") {
		t.Errorf("unexpected message: %s", errOut)
	}
}

// An agent's fallback for a type Karta does not cover - author a definition -
// differs from every other failure, so it must be tellable apart.
func TestDescribeUncoveredTypeIsItsOwnFailure(t *testing.T) {
	describeCluster(t)

	_, errOut, code := runDescribeCmd(t, "flinkdeployment/etl")
	if code != ExitNotFound {
		t.Fatalf("expected exit %d, got %d\n%s", ExitNotFound, code, errOut)
	}
	if !strings.Contains(errOut, "no Karta definition covers") {
		t.Errorf("unexpected message: %s", errOut)
	}
}

// One workload renders no extra columns, so wide has no meaning here and is
// rejected rather than silently treated as the table.
func TestDescribeRejectsWide(t *testing.T) {
	describeCluster(t, jobSet("preprocess", 3))

	_, errOut, code := runDescribeCmd(t, "jobset/preprocess", "-o", "wide")
	if code != ExitUsage {
		t.Fatalf("expected exit %d, got %d\n%s", ExitUsage, code, errOut)
	}
	if !strings.Contains(errOut, "must be one of table, json, yaml") {
		t.Errorf("unexpected message: %s", errOut)
	}
}

func TestDescribeMachineOutputIsTheViewItself(t *testing.T) {
	describeCluster(t, jobSet("preprocess", 3),
		etlPod("preprocess-etl-0", "ml-team/preprocess", "node-01", true))

	out, errOut, code := runDescribeCmd(t, "jobset/preprocess", "-o", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}

	var view struct {
		Name       string `json:"name"`
		Kind       string `json:"kind"`
		Components []struct {
			Replicas struct {
				Desired int `json:"desired"`
				Ready   int `json:"ready"`
			} `json:"replicas"`
		} `json:"components"`
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatalf("decode json: %v\n%s", err, out)
	}
	if view.Name != "preprocess" || view.Kind != "JobSet" {
		t.Errorf("unexpected workload: %+v", view)
	}
	if len(view.Components) == 0 {
		t.Fatalf("expected components\n%s", out)
	}
	if view.Components[0].Replicas.Ready != 1 {
		t.Errorf("expected a typed ready count, got %+v", view.Components[0].Replicas)
	}
	if strings.Contains(out, `"items"`) {
		t.Errorf("a single workload should not be wrapped in a list envelope\n%s", out)
	}
}

func TestDescribePodLimitKeepsTheFailingPod(t *testing.T) {
	describeCluster(t, jobSet("preprocess", 3),
		etlPod("preprocess-etl-0", "ml-team/preprocess", "node-01", true),
		etlPod("preprocess-etl-1", "ml-team/preprocess", "node-02", true),
		etlPod("preprocess-etl-2", "ml-team/preprocess", "", false))

	out, errOut, code := runDescribeCmd(t, "jobset/preprocess", "--pod-limit", "1")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	if !strings.Contains(out, "preprocess-etl-2") {
		t.Errorf("truncation hid the unschedulable pod\n%s", out)
	}
	if !strings.Contains(out, "and 2 more (1 unhealthy shown)") {
		t.Errorf("expected the truncation note\n%s", out)
	}
}

func TestDescribeShowsEveryPodByDefault(t *testing.T) {
	describeCluster(t, jobSet("preprocess", 3),
		etlPod("preprocess-etl-0", "ml-team/preprocess", "node-01", true),
		etlPod("preprocess-etl-1", "ml-team/preprocess", "node-02", true),
		etlPod("preprocess-etl-2", "ml-team/preprocess", "node-03", true))

	out, _, code := runDescribeCmd(t, "jobset/preprocess")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, out)
	}
	for _, name := range []string{"preprocess-etl-0", "preprocess-etl-1", "preprocess-etl-2"} {
		if !strings.Contains(out, name) {
			t.Errorf("output missing %q\n%s", name, out)
		}
	}
	if strings.Contains(out, "more (") {
		t.Errorf("nothing should be truncated by default\n%s", out)
	}
}
