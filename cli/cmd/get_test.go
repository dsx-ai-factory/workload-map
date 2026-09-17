// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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

	"github.com/dsx-ai-factory/workload-map/cli/pkg/definitions"
	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/catalog"
	"github.com/dsx-ai-factory/workload-map/pkg/catalog/kartas"
)

var (
	jobSetGVK = schema.GroupVersionKind{Group: "jobset.x-k8s.io", Version: "v1alpha2", Kind: "JobSet"}
	jobSetGVR = jobSetGVK.GroupVersion().WithResource("jobsets")
)

// jobSet builds a JobSet with one replicated job of the given parallelism.
func jobSet(name string, parallelism int64) *unstructured.Unstructured {
	return jobSetIn("ml-team", name, parallelism)
}

func jobSetIn(namespace, name string, parallelism int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": jobSetGVK.GroupVersion().String(),
		"kind":       jobSetGVK.Kind,
		"metadata": map[string]any{
			"name": name, "namespace": namespace, "uid": namespace + "/" + name,
		},
		"spec": map[string]any{
			"replicatedJobs": []any{map[string]any{
				"name":     "etl",
				"replicas": int64(1),
				"template": map[string]any{"spec": map[string]any{
					"parallelism": parallelism,
					"template": map[string]any{"spec": map[string]any{
						"containers": []any{map[string]any{"name": "worker"}},
					}},
				}},
			}},
		},
	}}
}

// fakeCluster points the get command at an in-memory cluster serving only the
// JobSet type, and restores the real client factory afterwards.
func fakeCluster(t *testing.T, objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	t.Helper()

	scheme := runtime.NewScheme()
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{jobSetGVR: "JobSetList"}, objects...)

	mapper := meta.NewDefaultRESTMapper(nil)
	mapper.AddSpecific(jobSetGVK, jobSetGVR, jobSetGVK.GroupVersion().WithResource("jobset"), meta.RESTScopeNamespace)

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

// runGetCmd executes "karta get" with args, returning stdout, stderr and the
// exit code the binary would produce.
func runGetCmd(t *testing.T, args ...string) (string, string, int) {
	t.Helper()

	root := NewRootCommand()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"get"}, args...))

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

func TestGetListsWorkloadsOfAType(t *testing.T) {
	fakeCluster(t, jobSet("preprocess", 3))

	out, _, code := runGetCmd(t, "jobset")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, out)
	}
	for _, want := range []string{
		"NAME", "NAMESPACE", "PHASE", "AGE", "preprocess", "ml-team",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
	// KIND is redundant when a single type was requested.
	if strings.Contains(out, "KIND") {
		t.Errorf("KIND column should be omitted for a single type\n%s", out)
	}
}

func TestGetAcceptsEveryArgumentForm(t *testing.T) {
	for _, args := range [][]string{
		{"jobset"},
		{"jobset/preprocess"},
		{"jobset", "preprocess"},
		{"JobSets"}, // lenient: case-insensitive and plural
	} {
		fakeCluster(t, jobSet("preprocess", 3))
		out, errOut, code := runGetCmd(t, args...)
		if code != 0 {
			t.Errorf("%v: expected exit 0, got %d\n%s", args, code, errOut)
			continue
		}
		if !strings.Contains(out, "preprocess") {
			t.Errorf("%v: expected the workload row\n%s", args, out)
		}
	}
}

func TestGetUnknownTypeIsDistinguishable(t *testing.T) {
	fakeCluster(t)

	_, errOut, code := runGetCmd(t, "nosuchtype")
	if code != ExitNotFound {
		t.Fatalf("expected exit %d, got %d\n%s", ExitNotFound, code, errOut)
	}
	if !strings.Contains(errOut, `no Karta definition covers "nosuchtype"`) {
		t.Errorf("unexpected message: %s", errOut)
	}
}

func TestGetEmptyResultExitsZero(t *testing.T) {
	fakeCluster(t)

	out, errOut, code := runGetCmd(t, "jobset")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "No workloads found in namespace ml-team.") {
		t.Errorf("expected the empty-result notice on stderr, got %q", errOut)
	}
	if out != "" {
		t.Errorf("expected no stdout, got %q", out)
	}
}

func TestGetEmptyResultAsJSONIsAnArray(t *testing.T) {
	fakeCluster(t)

	out, _, code := runGetCmd(t, "jobset", "-o", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	// A consumer piping into jq must not receive empty input.
	items, count := decodeEnvelope(t, out)
	if len(items) != 0 || count != 0 {
		t.Errorf("expected an empty envelope on stdout, got %q", out)
	}
}

// A named workload that does not exist is a distinct failure from an empty
// list, and from a cluster that could not be reached at all.
func TestGetNamedWorkloadNotFound(t *testing.T) {
	fakeCluster(t)

	_, errOut, code := runGetCmd(t, "jobset/absent")
	if code != ExitWorkloadNotFound {
		t.Fatalf("expected exit %d, got %d\n%s", ExitWorkloadNotFound, code, errOut)
	}
	if !strings.Contains(errOut, "not found") {
		t.Errorf("unexpected message: %s", errOut)
	}
}

func TestGetPhaseFilterRejectsUnknownValues(t *testing.T) {
	fakeCluster(t)

	_, errOut, code := runGetCmd(t, "jobset", "--phase", "Bogus")
	if code != ExitUsage {
		t.Fatalf("expected exit %d, got %d\n%s", ExitUsage, code, errOut)
	}
	if !strings.Contains(errOut, "must be one of") {
		t.Errorf("unexpected message: %s", errOut)
	}
}

func TestGetPhaseFilterNarrowsResults(t *testing.T) {
	fakeCluster(t, jobSet("preprocess", 3))

	// The fixture has no status, so it resolves as Undefined and Running
	// excludes it while Undefined keeps it.
	out, _, _ := runGetCmd(t, "jobset", "--phase", "Running")
	if strings.Contains(out, "preprocess") {
		t.Errorf("Running should not match an undefined workload\n%s", out)
	}

	fakeCluster(t, jobSet("preprocess", 3))
	out, _, _ = runGetCmd(t, "jobset", "--phase", "Undefined")
	if !strings.Contains(out, "preprocess") {
		t.Errorf("Undefined should match\n%s", out)
	}
}

// A denial says nothing about whether workloads exist, so it must not read as
// an empty result.
func TestGetForbiddenTypeFailsHard(t *testing.T) {
	client := fakeCluster(t)
	client.PrependReactor("list", "jobsets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(jobSetGVR.GroupResource(), "", errors.New("nope"))
	})

	_, errOut, code := runGetCmd(t, "jobset")
	if code == 0 {
		t.Fatalf("expected a non-zero exit, got 0\n%s", errOut)
	}
	if strings.Contains(errOut, "No workloads found") {
		t.Errorf("a denial must not report an empty result: %s", errOut)
	}
	if !strings.Contains(errOut, "not allowed to list JobSet") {
		t.Errorf("unexpected message: %s", errOut)
	}
}

// An absent CRD does say that no such workload exists, so it stays an empty
// result rather than a failure.
func TestGetUninstalledTypeIsAnEmptyResult(t *testing.T) {
	fakeCluster(t) // serves JobSet only

	_, errOut, code := runGetCmd(t, "pytorchjob")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "PyTorchJob is not installed in this cluster") {
		t.Errorf("expected the not-installed warning, got %q", errOut)
	}
}

// Naming a workload of an absent type is not an empty result: the one workload
// asked for is missing, which is the same fallback as a plain miss.
func TestGetNamedWorkloadOfUninstalledType(t *testing.T) {
	fakeCluster(t) // serves JobSet only

	_, errOut, code := runGetCmd(t, "pytorchjob/absent")
	if code != ExitWorkloadNotFound {
		t.Fatalf("expected exit %d, got %d\n%s", ExitWorkloadNotFound, code, errOut)
	}
	if !strings.Contains(errOut, "PyTorchJob is not installed in this cluster") {
		t.Errorf("unexpected message: %s", errOut)
	}
}

// The command must follow continuation tokens rather than printing page one.
func TestGetFollowsContinuationTokens(t *testing.T) {
	client := fakeCluster(t)

	page := 0
	client.PrependReactor("list", "jobsets", func(k8stesting.Action) (bool, runtime.Object, error) {
		page++
		list := &unstructured.UnstructuredList{Object: map[string]any{
			"apiVersion": jobSetGVK.GroupVersion().String(),
			"kind":       "JobSetList",
		}}
		switch page {
		case 1:
			list.Items = []unstructured.Unstructured{*jobSet("first", 1)}
			list.SetContinue("more")
		default:
			list.Items = []unstructured.Unstructured{*jobSet("second", 1)}
		}
		return true, list, nil
	})

	out, errOut, code := runGetCmd(t, "jobset", "--chunk-size", "1")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	for _, want := range []string{"first", "second"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q from a later page\n%s", want, out)
		}
	}
}

func TestGetWideAddsOrigin(t *testing.T) {
	fakeCluster(t, jobSet("preprocess", 3))

	out, _, code := runGetCmd(t, "jobset", "-o", "wide")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(out, "ORIGIN") || !strings.Contains(out, "catalog") {
		t.Errorf("expected the ORIGIN column\n%s", out)
	}
}

// Guard the one lenient-match result that differs from kubectl: Karta ships no
// definition for core Services, so "service" resolves to the Knative one.
func TestGetServiceResolvesToKnative(t *testing.T) {
	if _, err := catalog.Get(schema.GroupVersionKind{
		Group: "serving.knative.dev", Version: "v1", Kind: "Service",
	}); err != nil {
		t.Fatalf("expected the catalog to cover the Knative Service: %v", err)
	}
}

func TestGetJSONIsTypedAndShapedByTheRequest(t *testing.T) {
	fakeCluster(t, jobSet("preprocess", 3))

	named, _, code := runGetCmd(t, "jobset/preprocess", "-o", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if strings.Contains(named, `"items"`) {
		t.Errorf("expected a bare object for a named workload\n%s", named)
	}
	var view map[string]any
	if err := json.Unmarshal([]byte(named), &view); err != nil {
		t.Fatalf("decode %q: %v", named, err)
	}
	if view["name"] != "preprocess" {
		t.Errorf("expected the workload name\n%s", named)
	}
	// Typed values, not display strings: phases is a list, not a joined cell.
	if !strings.Contains(named, `"phases": [`) {
		t.Errorf("expected phases as a list\n%s", named)
	}

	listed, _, code := runGetCmd(t, "jobset", "-o", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	items, count := decodeEnvelope(t, listed)
	if len(items) != 1 || count != 1 {
		t.Errorf("expected one wrapped item, got %q", listed)
	}
}

// Lenient matching leans on discovery: the RESTMapper resolves plurals and short
// names, while the offline fallback is an exact Kind match. A type whose CRD the
// cluster does not serve must still resolve by its exact Kind.
func TestGetResolvesUnservedTypeByExactKind(t *testing.T) {
	fakeCluster(t) // serves JobSet only

	// PyTorchJob has no RESTMapping here, so this can only come from the
	// definition-side fallback. It must not be reported as an unknown type.
	_, errOut, code := runGetCmd(t, "pytorchjob")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	if strings.Contains(errOut, "no Karta definition covers") {
		t.Errorf("an unserved type should still resolve by exact Kind: %s", errOut)
	}
	if !strings.Contains(errOut, "PyTorchJob is not installed in this cluster") {
		t.Errorf("expected the not-installed warning, got %q", errOut)
	}
}

// The group-qualified form is what the ambiguity error tells users to retry
// with, and ParseResourceArg gives it two readings.
func TestGetResolvesQualifiedTypeToken(t *testing.T) {
	fakeCluster(t, jobSet("preprocess", 3))

	for _, token := range []string{"jobsets.jobset.x-k8s.io", "jobsets.v1alpha2.jobset.x-k8s.io"} {
		out, errOut, code := runGetCmd(t, token)
		if code != 0 {
			t.Errorf("%s: expected exit 0, got %d\n%s", token, code, errOut)
			continue
		}
		if !strings.Contains(out, "preprocess") {
			t.Errorf("%s: expected the workload row\n%s", token, out)
		}
	}
}

// A selector that is never applied would let a scripted filter pass silently.
func TestGetRejectsSelectorWithAName(t *testing.T) {
	fakeCluster(t)

	_, errOut, code := runGetCmd(t, "jobset/preprocess", "-l", "team=nlp")
	if code != ExitUsage {
		t.Fatalf("expected exit %d, got %d\n%s", ExitUsage, code, errOut)
	}
	if !strings.Contains(errOut, "cannot be combined with a NAME") {
		t.Errorf("unexpected message: %s", errOut)
	}
}

var dynamoGVK = schema.GroupVersionKind{Group: "nvidia.com", Version: "v1alpha1", Kind: "DynamoGraphDeployment"}

// dynamoGraph builds a DynamoGraphDeployment in the v1alpha1 spec shape.
func dynamoGraph() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": dynamoGVK.GroupVersion().String(),
		"kind":       dynamoGVK.Kind,
		"metadata": map[string]any{
			"name": "my-pipeline", "namespace": "ml-team", "uid": "dynamo-uid",
		},
		"spec": map[string]any{"services": map[string]any{
			"Frontend": map[string]any{
				"replicas":  int64(2),
				"resources": map[string]any{"requests": map[string]any{"nvidia.com/gpu": "1"}},
			},
		}},
	}}
}

// setupDynamoCluster serves a Kind the catalog covers at two versions, which is
// what makes the bare type token ambiguous.
func setupDynamoCluster(t *testing.T) {
	alpha := dynamoGVK.GroupVersion().WithResource("dynamographdeployments")
	beta := schema.GroupVersion{Group: "nvidia.com", Version: "v1beta1"}.WithResource("dynamographdeployments")

	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			alpha: "DynamoGraphDeploymentList",
			beta:  "DynamoGraphDeploymentList",
		}, dynamoGraph())

	mapper := meta.NewDefaultRESTMapper(nil)
	mapper.AddSpecific(dynamoGVK, alpha, alpha, meta.RESTScopeNamespace)
	mapper.AddSpecific(dynamoGVK.GroupKind().WithVersion("v1beta1"), beta, beta, meta.RESTScopeNamespace)

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

}

// The ambiguity error names a token, which must itself resolve: a user who
// follows the suggestion has to end up with a working command.
func TestGetAmbiguousTypeSuggestsAResolvableToken(t *testing.T) {
	setupDynamoCluster(t)

	_, errOut, code := runGetCmd(t, "dynamographdeployment")
	if code != ExitUsage {
		t.Fatalf("expected the bare token to be ambiguous, got exit %d\n%s", code, errOut)
	}
	suggestion := "dynamographdeployment.v1alpha1.nvidia.com"
	if !strings.Contains(errOut, suggestion) {
		t.Fatalf("expected a qualified suggestion, got %q", errOut)
	}

	setupDynamoCluster(t)
	out, errOut, code := runGetCmd(t, suggestion)
	if code != 0 {
		t.Fatalf("the suggested token must resolve, got exit %d\n%s", code, errOut)
	}
	if !strings.Contains(out, "my-pipeline") {
		t.Errorf("expected the workload row\n%s", out)
	}
}

// A denial on a named fetch must not read as "no workloads found".
func TestGetNamedFetchFailsHardOnForbidden(t *testing.T) {
	client := fakeCluster(t)
	client.PrependReactor("get", "jobsets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(jobSetGVR.GroupResource(), "preprocess", errors.New("nope"))
	})

	_, errOut, code := runGetCmd(t, "jobset/preprocess")
	if code == 0 {
		t.Fatalf("expected a non-zero exit, got 0\n%s", errOut)
	}
	if strings.Contains(errOut, "No workloads found") {
		t.Errorf("a denial must not report an empty result: %s", errOut)
	}
}

// "jobset/" in a script with an unset variable must not list the namespace.
func TestGetRejectsEmptyName(t *testing.T) {
	fakeCluster(t)

	_, errOut, code := runGetCmd(t, "jobset/")
	if code != ExitUsage {
		t.Fatalf("expected exit %d, got %d\n%s", ExitUsage, code, errOut)
	}
	if !strings.Contains(errOut, "NAME is empty") {
		t.Errorf("unexpected message: %s", errOut)
	}
}

// A named fetch of a type whose CRD is absent must not read as an empty result.
func TestGetNamedFetchFailsHardWhenTypeNotInstalled(t *testing.T) {
	fakeCluster(t) // serves JobSet only

	_, errOut, code := runGetCmd(t, "pytorchjob/llama")
	if code == 0 {
		t.Fatalf("expected a non-zero exit, got 0\n%s", errOut)
	}
	if strings.Contains(errOut, "No workloads found") {
		t.Errorf("an absent CRD must not report an empty result: %s", errOut)
	}
}

// "karta get jobset $NAME" with NAME unset must not list the namespace.
func TestGetRejectsEmptyNameInTwoTokenForm(t *testing.T) {
	fakeCluster(t, jobSet("preprocess", 3))

	out, errOut, code := runGetCmd(t, "jobset", "")
	if code != ExitUsage {
		t.Fatalf("expected exit %d, got %d\n%s", ExitUsage, code, errOut)
	}
	if strings.Contains(out, "preprocess") {
		t.Errorf("an empty NAME must not degrade to a list\n%s", out)
	}
}

// Two cluster definitions claiming one root GVK cannot be told apart by a
// qualified token, so the error must name the definitions instead.
func TestGetReportsCollidingDefinitionsByName(t *testing.T) {
	fakeCluster(t)

	collide := func(name string) *v1alpha1.Karta {
		k := kartas.Jobset()
		k.Name = name
		return k
	}
	restore := loadDefinitions
	loadDefinitions = func(context.Context, genericclioptions.RESTClientGetter) (*definitions.Resolver, []definitions.Warning) {
		return definitions.New(nil, []*v1alpha1.Karta{collide("first"), collide("second")}), nil
	}
	t.Cleanup(func() { loadDefinitions = restore })

	// The plural resolves through discovery, so the collision surfaces from the
	// resolver rather than from Kind matching.
	for _, token := range []string{"jobset", "jobsets"} {
		_, errOut, code := runGetCmd(t, token)
		if code != ExitUsage {
			t.Errorf("%s: expected exit %d, got %d\n%s", token, ExitUsage, code, errOut)
			continue
		}
		if strings.Contains(errOut, "no Karta definition covers") {
			t.Errorf("%s: two definitions cover it, so this is false: %s", token, errOut)
		}
	}
}

// A NAME containing a separator must be rejected in both argument forms.
func TestGetRejectsSlashInNameInBothForms(t *testing.T) {
	for _, args := range [][]string{{"jobset/a/b"}, {"jobset", "a/b"}} {
		fakeCluster(t)
		_, errOut, code := runGetCmd(t, args...)
		if code != ExitUsage {
			t.Errorf("%v: expected exit %d, got %d\n%s", args, ExitUsage, code, errOut)
			continue
		}
		if !strings.Contains(errOut, "NAME must not contain") {
			t.Errorf("%v: unexpected message: %s", args, errOut)
		}
	}
}

// An unrecognised command is a usage error, not a cluster failure.
func TestRootRejectsUnknownCommand(t *testing.T) {
	root := NewRootCommand()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"wrkload"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error for an unknown command")
	}
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != ExitUsage {
		t.Errorf("expected exit %d, got %v", ExitUsage, err)
	}
}

// decodeEnvelope reads the items and count the machine formats wrap a result in.
func decodeEnvelope(t *testing.T, out string) ([]map[string]any, int) {
	t.Helper()
	var envelope struct {
		Items []map[string]any `json:"items"`
		Count int              `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return envelope.Items, envelope.Count
}
