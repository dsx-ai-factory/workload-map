// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	k8stesting "k8s.io/client-go/testing"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/definitions"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog/kartas"
)

// complete runs cobra's hidden completion command, returning the candidates
// without the trailing directive line.
func complete(t *testing.T, args ...string) []string {
	t.Helper()

	out, errOut, code := runCmd(t, "__complete", args...)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	return candidates(out)
}

// candidates parses the output of the completion command, dropping the
// trailing directive line.
func candidates(out string) []string {
	var candidates []string
	for line := range strings.Lines(out) {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, ":") {
			candidates = append(candidates, line)
		}
	}
	return candidates
}

func TestCompleteOffersTheTypesDefinitionsCover(t *testing.T) {
	fakeCluster(t)

	got := complete(t, "get", "job")
	if !slices.Contains(got, "jobset") {
		t.Errorf("expected jobset among %v", got)
	}
	for _, candidate := range got {
		if !strings.HasPrefix(candidate, "job") {
			t.Errorf("%q does not match the typed prefix", candidate)
		}
	}
}

// A bare kind two groups define is rejected as ambiguous, so offering it would
// complete to a command that fails.
func TestCompleteQualifiesAKindSeveralGroupsDefine(t *testing.T) {
	fakeCluster(t, jobSet("preprocess", 1))

	fork := kartas.Jobset()
	fork.Name = "jobset-fork"
	fork.Spec.StructureDefinition.RootComponent.Kind.Group = "fork.example.com"
	restore := loadDefinitions
	loadDefinitions = func(context.Context, genericclioptions.RESTClientGetter, bool) (*definitions.Resolver, []definitions.Warning) {
		return definitions.New([]*v1alpha1.Karta{kartas.Jobset(), fork}, nil), nil
	}
	t.Cleanup(func() { loadDefinitions = restore })

	want := []string{"jobset.v1alpha2.fork.example.com", "jobset.v1alpha2.jobset.x-k8s.io"}
	if got := complete(t, "get", "job"); !slices.Equal(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}

	// The qualified token must itself resolve, or completion stops at the type.
	if got := complete(t, "get", "jobset.v1alpha2.jobset.x-k8s.io/"); !slices.Equal(got,
		[]string{"jobset.v1alpha2.jobset.x-k8s.io/preprocess"}) {
		t.Errorf("expected the workload under the qualified type, got %v", got)
	}
}

func TestCompleteOffersNamesAfterTheType(t *testing.T) {
	fakeCluster(t, jobSet("preprocess", 1), jobSet("postprocess", 1), jobSetIn("other", "pretrain", 1))

	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"TYPE NAME", []string{"get", "jobset", ""}, []string{"postprocess", "preprocess"}},
		{"TYPE NAME with a prefix", []string{"describe", "jobset", "pre"}, []string{"preprocess"}},
		{"TYPE/NAME", []string{"describe", "jobset/pre"}, []string{"jobset/preprocess"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := complete(t, tc.args...); !slices.Equal(got, tc.want) {
				t.Errorf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestCompleteOffersNothingItCannotUse(t *testing.T) {
	fakeCluster(t, jobSet("preprocess", 1))

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"a type no definition covers", []string{"get", "flinkdeployment", ""}},
		{"a third argument", []string{"get", "jobset", "preprocess", ""}},
		{"a name already given as TYPE/NAME", []string{"get", "jobset/preprocess", ""}},
		{"describe -f, which takes no argument", []string{"describe", "-f", "job.yaml", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := complete(t, tc.args...); len(got) != 0 {
				t.Errorf("expected no candidates, got %v", got)
			}
		})
	}
}

func TestCompleteOffersNamespacesForTheNamespaceFlag(t *testing.T) {
	fakeCluster(t, namespace("ml-team"), namespace("ml-infra"), namespace("default"))

	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"-n on get", []string{"get", "-n", ""}, []string{"default", "ml-infra", "ml-team"}},
		{"--namespace on describe with a prefix", []string{"describe", "--namespace", "ml-"}, []string{"ml-infra", "ml-team"}},
		{"-n on definitions", []string{"definitions", "-n", "d"}, []string{"default"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := complete(t, tc.args...); !slices.Equal(got, tc.want) {
				t.Errorf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

// A user who may not list namespaces gets nothing to complete, not an error.
func TestCompleteOffersNoNamespacesWhenTheListFails(t *testing.T) {
	client := fakeCluster(t, namespace("ml-team"))
	client.PrependReactor("list", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(namespaceGVR.GroupResource(), "", errors.New("rbac denied"))
	})

	if got := complete(t, "get", "-n", ""); len(got) != 0 {
		t.Errorf("expected no candidates, got %v", got)
	}
}

// namespace builds a Namespace object for the fake cluster.
func namespace(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata":   map[string]any{"name": name},
	}}
}

// A cluster that never answers must not freeze the shell on TAB.
func TestCompleteGivesUpOnAClusterThatDoesNotAnswer(t *testing.T) {
	fakeCluster(t)

	restoreTimeout := completionTimeout
	completionTimeout = 50 * time.Millisecond
	t.Cleanup(func() { completionTimeout = restoreTimeout })

	restore := loadDefinitions
	loadDefinitions = func(ctx context.Context, _ genericclioptions.RESTClientGetter, _ bool) (*definitions.Resolver, []definitions.Warning) {
		<-ctx.Done()
		return definitions.New(nil, nil), nil
	}
	t.Cleanup(func() { loadDefinitions = restore })

	// The goroutine only runs the command: t.Fatal must be called from the test
	// goroutine, so the checks happen after the select.
	type result struct {
		out, errOut string
		code        int
	}
	done := make(chan result, 1)
	go func() {
		out, errOut, code := runCmd(t, "__complete", "get", "")
		done <- result{out, errOut, code}
	}()
	select {
	case r := <-done:
		if r.code != 0 {
			t.Fatalf("expected exit 0, got %d\n%s", r.code, r.errOut)
		}
		if got := candidates(r.out); len(got) != 0 {
			t.Errorf("expected no candidates, got %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("completion did not give up on a cluster that never answers")
	}
}

// Discovery takes no context, so only the request timeout stops a server that
// serves the definitions but never answers discovery.
func TestCompleteGivesUpOnDiscoveryThatDoesNotAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "absent"))

	restoreTimeout := completionTimeout
	completionTimeout = 50 * time.Millisecond
	t.Cleanup(func() { completionTimeout = restoreTimeout })

	restore := loadDefinitions
	loadDefinitions = func(context.Context, genericclioptions.RESTClientGetter, bool) (*definitions.Resolver, []definitions.Warning) {
		return definitions.New([]*v1alpha1.Karta{kartas.Jobset()}, nil), nil
	}
	t.Cleanup(func() { loadDefinitions = restore })

	type result struct {
		out, errOut string
		code        int
	}
	done := make(chan result, 1)
	go func() {
		out, errOut, code := runCmd(t, "__complete",
			"--server", server.URL, "--cache-dir", t.TempDir(), "get", "jobset", "")
		done <- result{out, errOut, code}
	}()
	select {
	case r := <-done:
		if r.code != 0 {
			t.Fatalf("expected exit 0, got %d\n%s", r.code, r.errOut)
		}
		if got := candidates(r.out); len(got) != 0 {
			t.Errorf("expected no candidates, got %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("completion did not give up on discovery that never answers")
	}
}

func TestCompleteOffersDefinitionNames(t *testing.T) {
	fakeCluster(t)

	deployment := kartas.Deployment()
	restore := loadDefinitions
	loadDefinitions = func(context.Context, genericclioptions.RESTClientGetter, bool) (*definitions.Resolver, []definitions.Warning) {
		return definitions.New([]*v1alpha1.Karta{kartas.Jobset(), deployment}, nil), nil
	}
	t.Cleanup(func() { loadDefinitions = restore })

	all := []string{deployment.Name, kartas.Jobset().Name}
	slices.Sort(all)

	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"every name, sorted", []string{"definitions", ""}, all},
		{"a prefix", []string{"definitions", deployment.Name[:4]}, []string{deployment.Name}},
		{"a prefix nothing matches", []string{"definitions", "zz-no-such"}, nil},
		{"after one name", []string{"definitions", deployment.Name, ""}, nil},
		{"with --group", []string{"definitions", "--group", "apps", ""}, nil},
		{"with --group ''", []string{"definitions", "--group", "", ""}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := complete(t, tc.args...); !slices.Equal(got, tc.want) {
				t.Errorf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

// Without a cluster the names still come from the built-in catalog.
func TestCompleteOffersCatalogDefinitionNamesWithoutACluster(t *testing.T) {
	fakeCluster(t)

	got := complete(t, "definitions", kartas.Jobset().Name)
	if !slices.Contains(got, kartas.Jobset().Name) {
		t.Errorf("expected %q among %v", kartas.Jobset().Name, got)
	}
}

// A configured cluster that refuses the definitions request still completes
// from the catalog, quietly: stderr has only cobra's directive line.
func TestCompleteOffersCatalogDefinitionNamesWhenTheClusterRefuses(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","code":403}`,
			http.StatusForbidden)
	}))
	t.Cleanup(server.Close)

	restore := clusterAccess
	clusterAccess = func() genericclioptions.RESTClientGetter { return clusterGetter(server) }
	t.Cleanup(func() { clusterAccess = restore })

	name := kartas.Jobset().Name
	out, errOut, code := runCmd(t, "__complete", "definitions", name[:len(name)-1])
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	got := candidates(out)
	if !slices.Contains(got, name) {
		t.Errorf("expected %q among %v", name, got)
	}
	for _, candidate := range got {
		if !strings.HasPrefix(candidate, name[:len(name)-1]) {
			t.Errorf("candidate %q does not match the prefix %q", candidate, name[:len(name)-1])
		}
	}
	if requests.Load() == 0 {
		t.Error("expected completion to ask the cluster for its definitions")
	}
	if want := "Completion ended with directive: ShellCompDirectiveNoFileComp\n"; errOut != want {
		t.Errorf("expected stderr %q, got %q", want, errOut)
	}
}

func TestCompleteOffersDefinitionFilterValues(t *testing.T) {
	fakeCluster(t)

	// A kind at a second version of a group sorts after the group's v1 kinds in
	// the resolver's own order, so the candidates must be sorted, not passed on.
	bundle := kartas.Deployment()
	bundle.Name = "apps-appbundle-v2"
	bundle.Spec.StructureDefinition.RootComponent.Kind.Version = "v2"
	bundle.Spec.StructureDefinition.RootComponent.Kind.Kind = "AppBundle"
	restore := loadDefinitions
	loadDefinitions = func(context.Context, genericclioptions.RESTClientGetter, bool) (*definitions.Resolver, []definitions.Warning) {
		return definitions.New([]*v1alpha1.Karta{
			kartas.Pod(), kartas.Deployment(), kartas.StatefulSet(), bundle, kartas.Jobset(), kartas.Dynamo(), kartas.DynamoV1beta1(),
		}, nil), nil
	}
	t.Cleanup(func() { loadDefinitions = restore })

	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"every group once, sorted, without the core group", []string{"definitions", "--group", ""}, []string{"apps", "jobset.x-k8s.io", "nvidia.com"}},
		{"a group prefix", []string{"definitions", "--group", "job"}, []string{"jobset.x-k8s.io"}},
		{"a group prefix nothing matches", []string{"definitions", "--group", "zz"}, nil},
		{"the kinds of the group, sorted", []string{"definitions", "--group", "apps", "--kind", ""}, []string{"AppBundle", "Deployment", "StatefulSet"}},
		{"a kind prefix", []string{"definitions", "--group", "apps", "--kind", "S"}, []string{"StatefulSet"}},
		{"the kinds of the core group", []string{"definitions", "--group", "", "--kind", ""}, []string{"Pod"}},
		{"the kinds of a group nothing covers", []string{"definitions", "--group", "example.com", "--kind", ""}, nil},
		{"no kind without a group", []string{"definitions", "--kind", ""}, nil},
		{"the versions of the kind, once each", []string{"definitions", "--group", "nvidia.com", "--kind", "DynamoGraphDeployment", "--version", ""}, []string{"v1alpha1", "v1beta1"}},
		{"a version prefix", []string{"definitions", "--group", "nvidia.com", "--kind", "DynamoGraphDeployment", "--version", "v1b"}, []string{"v1beta1"}},
		{"the kind matches case-insensitively", []string{"definitions", "--group", "nvidia.com", "--kind", "dynamographdeployment", "--version", ""}, []string{"v1alpha1", "v1beta1"}},
		{"the version of the core group", []string{"definitions", "--group", "", "--kind", "Pod", "--version", ""}, []string{"v1"}},
		{"no version without a kind", []string{"definitions", "--group", "nvidia.com", "--version", ""}, nil},
		{"no version without a group", []string{"definitions", "--kind", "Pod", "--version", ""}, nil},
		{"nothing after a NAME", []string{"definitions", kartas.Jobset().Name, "--group", ""}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := complete(t, tc.args...); !slices.Equal(got, tc.want) {
				t.Errorf("expected %v, got %v", tc.want, got)
			}
		})
	}

	// complete skips blank lines, so check the raw output: the core group is the
	// empty string, and a blank candidate would reach the shell as an empty line.
	out, _, _ := runCmd(t, "__complete", "definitions", "--group", "")
	for line := range strings.Lines(out) {
		if strings.TrimSpace(line) == "" {
			t.Errorf("expected no blank candidate, got %q", out)
		}
	}
}

// A configured cluster that refuses the definitions request still completes
// the filters from the catalog, quietly: stderr has only cobra's directive line.
func TestCompleteOffersCatalogDefinitionFiltersWhenTheClusterRefuses(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","code":403}`,
			http.StatusForbidden)
	}))
	t.Cleanup(server.Close)

	restore := clusterAccess
	clusterAccess = func() genericclioptions.RESTClientGetter { return clusterGetter(server) }
	t.Cleanup(func() { clusterAccess = restore })

	out, errOut, code := runCmd(t, "__complete", "definitions", "--group", "jobset.x-k8s.io", "--kind", "Job")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	if got, want := candidates(out), []string{"JobSet"}; !slices.Equal(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
	if requests.Load() == 0 {
		t.Error("expected completion to ask the cluster for its definitions")
	}
	if want := "Completion ended with directive: ShellCompDirectiveNoFileComp\n"; errOut != want {
		t.Errorf("expected stderr %q, got %q", want, errOut)
	}
}
