// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
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
	spec := map[string]any{
		"containers": []any{map[string]any{
			"name": "worker",
			"resources": map[string]any{
				"requests": map[string]any{"nvidia.com/gpu": "2"},
			},
		}},
	}

	// An unready pod here is one the scheduler could not place, so it names no
	// node and node is ignored.
	status := map[string]any{
		"phase": "Pending",
		"conditions": []any{map[string]any{
			"type": "PodScheduled", "status": "False", "reason": "Unschedulable",
		}},
	}
	if ready {
		spec["nodeName"] = node
		status = map[string]any{
			"phase": "Running",
			"conditions": []any{map[string]any{
				"type": "Ready", "status": "True",
			}},
		}
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

func runDescribeCmd(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return runCmd(t, "describe", args...)
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

// Zero is what an unset int carries, so it means no limit rather than no rows.
func TestDescribeZeroPodLimitShowsEveryPod(t *testing.T) {
	describeCluster(t, jobSet("preprocess", 3),
		etlPod("preprocess-etl-0", "ml-team/preprocess", "node-01", true),
		etlPod("preprocess-etl-1", "ml-team/preprocess", "node-02", true))

	out, errOut, code := runDescribeCmd(t, "jobset/preprocess", "--pod-limit", "0")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	for _, name := range []string{"preprocess-etl-0", "preprocess-etl-1"} {
		if !strings.Contains(out, name) {
			t.Errorf("output missing %q\n%s", name, out)
		}
	}
	if strings.Contains(out, "more (") {
		t.Errorf("a zero limit truncated the pods\n%s", out)
	}
}

// A definition can cover a type whose CRD was never installed, which is a
// different miss from a workload that is merely absent.
func TestDescribeUninstalledTypeIsItsOwnFailure(t *testing.T) {
	describeCluster(t)

	_, errOut, code := runDescribeCmd(t, "deployment/web")
	if code != ExitWorkloadNotFound {
		t.Fatalf("expected exit %d, got %d\n%s", ExitWorkloadNotFound, code, errOut)
	}
	if !strings.Contains(errOut, "is not installed in this cluster") {
		t.Errorf("unexpected message: %s", errOut)
	}
}

func TestDescribeForbiddenReadReportsTheDenial(t *testing.T) {
	client := describeCluster(t, jobSet("preprocess", 3))
	client.PrependReactor("get", "jobsets",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewForbidden(
				jobSetGVR.GroupResource(), "preprocess", errors.New("rbac denied"))
		})

	_, errOut, code := runDescribeCmd(t, "jobset/preprocess")
	if code != ExitError {
		t.Fatalf("expected exit %d, got %d\n%s", ExitError, code, errOut)
	}
	if !strings.Contains(errOut, "not allowed to read JobSet") {
		t.Errorf("unexpected message: %s", errOut)
	}
}

// A negative limit would otherwise pass through as "show every pod".
func TestDescribeRejectsANegativePodLimit(t *testing.T) {
	describeCluster(t, jobSet("preprocess", 3))

	_, errOut, code := runDescribeCmd(t, "jobset/preprocess", "--pod-limit", "-10")
	if code != ExitUsage {
		t.Fatalf("expected exit %d, got %d\n%s", ExitUsage, code, errOut)
	}
	if !strings.Contains(errOut, "--pod-limit must not be negative") {
		t.Errorf("message missing the rejected flag\n%s", errOut)
	}
}

// noCluster points the command tree at a kubeconfig-less environment, which is
// what file mode is for and what keeps these tests off any real cluster.
func noCluster(t *testing.T) {
	t.Helper()

	restore := clusterAccess
	clusterAccess = noClusterGetter
	t.Cleanup(func() { clusterAccess = restore })
}

// writeManifest puts a manifest in a temp file and returns its path.
func writeManifest(t *testing.T, manifest string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "workload.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

const jobSetManifest = `apiVersion: jobset.x-k8s.io/v1alpha2
kind: JobSet
metadata:
  name: preprocess
  namespace: ml-team
spec:
  replicatedJobs:
    - name: etl
      replicas: 1
      template:
        spec:
          parallelism: 3
          template:
            spec:
              containers:
                - name: worker
                  resources:
                    requests:
                      nvidia.com/gpu: "2"
`

// File mode is for a manifest that has not been submitted, so it must not need
// a cluster to answer.
func TestDescribeFileModeNeedsNoCluster(t *testing.T) {
	noCluster(t)

	out, errOut, code := runDescribeCmd(t, "-f", writeManifest(t, jobSetManifest))
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	for _, want := range []string{
		"JobSet/preprocess", "(file mode: no live status)", "replicatedjob", "Resources:", "TOTAL",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "Phase:") {
		t.Errorf("a manifest that never reached the cluster has no phase\n%s", out)
	}
}

func TestDescribeFileModeReadsStdin(t *testing.T) {
	noCluster(t)

	root := NewRootCommand()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(jobSetManifest))
	root.SetArgs([]string{"describe", "-f", "-"})

	if err := root.Execute(); err != nil {
		t.Fatalf("expected success, got %v\n%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "JobSet/preprocess") {
		t.Errorf("expected the workload header\n%s", out.String())
	}
}

// Live mode and file mode answer the same question, so they must answer it in
// the same shape; only the live fields differ.
func TestDescribeFileModeSharesTheLiveShape(t *testing.T) {
	describeCluster(t, jobSet("preprocess", 3))
	live, _, code := runDescribeCmd(t, "jobset/preprocess", "-o", "json")
	if code != 0 {
		t.Fatalf("live: expected exit 0, got %d\n%s", code, live)
	}

	fromFile, _, code := runDescribeCmd(t, "-f", writeManifest(t, jobSetManifest), "-o", "json")
	if code != 0 {
		t.Fatalf("file: expected exit 0, got %d\n%s", code, fromFile)
	}

	var liveKeys, fileKeys map[string]any
	if err := json.Unmarshal([]byte(live), &liveKeys); err != nil {
		t.Fatalf("decode live: %v", err)
	}
	if err := json.Unmarshal([]byte(fromFile), &fileKeys); err != nil {
		t.Fatalf("decode file: %v", err)
	}
	for key := range liveKeys {
		if _, ok := fileKeys[key]; !ok {
			t.Errorf("file mode dropped the %q field instead of leaving it empty", key)
		}
	}
	if fileKeys["fileMode"] != true {
		t.Errorf("expected fileMode to be marked, got %v", fileKeys["fileMode"])
	}
}

// The no-definition case is the one an agent handles differently from every
// other failure, so with a machine format it is emitted as a parseable object.
func TestDescribeFileModeStructuredNoDefinitionError(t *testing.T) {
	noCluster(t)

	path := writeManifest(t, `apiVersion: flink.apache.org/v1beta1
kind: FlinkDeployment
metadata:
  name: etl
`)

	out, errOut, code := runDescribeCmd(t, "-f", path, "-o", "json")
	if code != ExitNotFound {
		t.Fatalf("expected exit %d, got %d\n%s", ExitNotFound, code, errOut)
	}

	var reported struct {
		Error   string `json:"error"`
		GVK     string `json:"gvk"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
	}
	if err := json.Unmarshal([]byte(out), &reported); err != nil {
		t.Fatalf("decode json: %v\n%s", err, out)
	}
	if reported.Error != "no_definition_for_type" {
		t.Errorf("unexpected reason %q", reported.Error)
	}
	if !strings.Contains(reported.GVK, "FlinkDeployment") {
		t.Errorf("the payload must name the type it could not cover, got %q", reported.GVK)
	}
	if reported.Message == "" || reported.Hint == "" {
		t.Errorf("expected a message and a hint, got %+v", reported)
	}
	// A human watching stderr must be told too.
	if !strings.Contains(errOut, "no Karta definition covers") {
		t.Errorf("expected the human message on stderr\n%s", errOut)
	}
}

// The table is for a reader, who is served by the plain message.
func TestDescribeNoDefinitionStaysPlainForTheTable(t *testing.T) {
	noCluster(t)

	path := writeManifest(t, `apiVersion: flink.apache.org/v1beta1
kind: FlinkDeployment
metadata:
  name: etl
`)

	out, errOut, code := runDescribeCmd(t, "-f", path)
	if code != ExitNotFound {
		t.Fatalf("expected exit %d, got %d\n%s", ExitNotFound, code, errOut)
	}
	if out != "" {
		t.Errorf("nothing should reach stdout\n%s", out)
	}
	if !strings.Contains(errOut, "no Karta definition covers") {
		t.Errorf("unexpected message: %s", errOut)
	}
}

func TestDescribeFileModeRejectsAPositionalArgument(t *testing.T) {
	noCluster(t)

	_, errOut, code := runDescribeCmd(t, "-f", writeManifest(t, jobSetManifest), "jobset/preprocess")
	if code != ExitUsage {
		t.Fatalf("expected exit %d, got %d\n%s", ExitUsage, code, errOut)
	}
	if !strings.Contains(errOut, "cannot be combined") {
		t.Errorf("unexpected message: %s", errOut)
	}
}

func TestDescribeFileModeRejectsAManifestWithNoKind(t *testing.T) {
	noCluster(t)

	_, errOut, code := runDescribeCmd(t, "-f", writeManifest(t, "metadata:\n  name: etl\n"))
	if code != ExitUsage {
		t.Fatalf("expected exit %d, got %d\n%s", ExitUsage, code, errOut)
	}
	if !strings.Contains(errOut, "apiVersion and kind") {
		t.Errorf("unexpected message: %s", errOut)
	}
}

func TestDescribeFileModeReportsAnUnreadableFile(t *testing.T) {
	noCluster(t)

	_, errOut, code := runDescribeCmd(t, "-f", filepath.Join(t.TempDir(), "absent.yaml"))
	if code != ExitError {
		t.Fatalf("expected exit %d, got %d\n%s", ExitError, code, errOut)
	}
	if !strings.Contains(errOut, "read ") {
		t.Errorf("unexpected message: %s", errOut)
	}
}
