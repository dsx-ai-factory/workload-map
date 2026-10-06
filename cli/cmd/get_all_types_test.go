// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	metadatafake "k8s.io/client-go/metadata/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/utils/ptr"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/definitions"
	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/catalog/kartas"
)

var (
	appsDeploymentGVK   = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	appsReplicaSetGVK   = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"}
	batchJobGVK         = schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
	inferenceServiceGVK = schema.GroupVersionKind{Group: "serving.kserve.io", Version: "v1beta1", Kind: "InferenceService"}
)

// object builds a namespaced object of gvk, controlled by owner when one is
// given.
func object(gvk schema.GroupVersionKind, name string, owner *metav1.OwnerReference) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": gvk.GroupVersion().String(),
		"kind":       gvk.Kind,
		"metadata": map[string]any{
			"name": name, "namespace": "ml-team", "uid": "ml-team/" + name,
		},
	}}
	if owner != nil {
		obj.SetOwnerReferences([]metav1.OwnerReference{*owner})
	}
	return obj
}

func controlledBy(gvk schema.GroupVersionKind, name string) *metav1.OwnerReference {
	return &metav1.OwnerReference{
		APIVersion: gvk.GroupVersion().String(),
		Kind:       gvk.Kind,
		Name:       name,
		UID:        types.UID("ml-team/" + name),
		Controller: ptr.To(true),
	}
}

// fakeMixedCluster serves JobSet, Deployment, ReplicaSet and Pod, so a listing
// across types meets nested objects. Every other covered type is absent.
func fakeMixedCluster(t *testing.T, objects ...runtime.Object) (*dynamicfake.FakeDynamicClient, *metadatafake.FakeMetadataClient) {
	t.Helper()

	served := map[schema.GroupVersionKind]string{
		jobSetGVK:         "jobsets",
		appsDeploymentGVK: "deployments",
		appsReplicaSetGVK: "replicasets",
		podGVK:            "pods",
	}
	listKinds := map[schema.GroupVersionResource]string{}
	mapper := meta.NewDefaultRESTMapper(nil)
	for gvk, resource := range served {
		gvr := gvk.GroupVersion().WithResource(resource)
		listKinds[gvr] = gvk.Kind + "List"
		mapper.AddSpecific(gvk, gvr, gvk.GroupVersion().WithResource(strings.ToLower(gvk.Kind)), meta.RESTScopeNamespace)
	}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, objects...)

	restore := newDynamicClient
	newDynamicClient = func(genericclioptions.RESTClientGetter) (dynamic.Interface, error) { return client, nil }
	t.Cleanup(func() { newDynamicClient = restore })
	metadataClient := fakeMetadata(t, objects...)

	flags := genericclioptions.NewTestConfigFlags().
		WithClientConfig(clientcmd.NewDefaultClientConfig(*clientcmdapi.NewConfig(), nil)).
		WithNamespace("ml-team").
		WithRESTMapper(mapper)
	restoreAccess := clusterAccess
	clusterAccess = func() genericclioptions.RESTClientGetter { return flags }
	t.Cleanup(func() { clusterAccess = restoreAccess })

	return client, metadataClient
}

// mixedNamespace holds top-level workloads of three types, plus objects nested
// under covered owners directly and through an uncovered ReplicaSet.
func mixedNamespace() []runtime.Object {
	return []runtime.Object{
		jobSet("preprocess", 1),
		object(appsDeploymentGVK, "web", nil),
		object(appsReplicaSetGVK, "web-abc", controlledBy(appsDeploymentGVK, "web")),
		object(podGVK, "web-abc-1", controlledBy(appsReplicaSetGVK, "web-abc")),
		object(podGVK, "debug-shell", nil),
		object(podGVK, "etl-0", controlledBy(batchJobGVK, "etl")),
		object(appsDeploymentGVK, "llama-predictor", controlledBy(inferenceServiceGVK, "llama")),
	}
}

// rowNames reads the NAME column of a rendered table, header excluded.
func rowNames(out string) []string {
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n")[1:] {
		names = append(names, strings.Fields(line)[0])
	}
	return names
}

func TestGetAllTypesListsTopLevelWorkloadsOnly(t *testing.T) {
	fakeMixedCluster(t, mixedNamespace()...)

	out, errOut, code := runGetCmd(t, "--all-types")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	if got := strings.Fields(strings.SplitN(out, "\n", 2)[0]); !slices.Equal(got,
		[]string{"NAME", "NAMESPACE", "TYPE", "PHASE", "AGE"}) {
		t.Errorf("unexpected header %v", got)
	}
	// Equal ages fall back to kind, then name.
	want := []string{"web", "preprocess", "debug-shell"}
	if got := rowNames(out); !slices.Equal(got, want) {
		t.Errorf("expected rows %v, got %v\n%s", want, got, out)
	}
	for _, typ := range []string{"Deployment", "JobSet", "Pod"} {
		if !strings.Contains(out, typ) {
			t.Errorf("expected a %s row\n%s", typ, out)
		}
	}
	// Under --all-types an absent type is the norm, not news.
	if strings.Contains(errOut, "not installed") {
		t.Errorf("absent types should be skipped silently: %s", errOut)
	}
}

func TestGetAllTypesComposesWithFilters(t *testing.T) {
	objects := mixedNamespace()
	objects[1].(*unstructured.Unstructured).SetLabels(map[string]string{"team": "web"})
	fakeMixedCluster(t, objects...)

	out, errOut, code := runGetCmd(t, "--all-types", "-l", "team=web")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	if got := rowNames(out); !slices.Equal(got, []string{"web"}) {
		t.Errorf("expected the labelled Deployment alone, got %v\n%s", got, out)
	}

	// The fixtures carry no status, so every one resolves as Undefined.
	fakeMixedCluster(t, mixedNamespace()...)
	_, errOut, code = runGetCmd(t, "--all-types", "--phase", "Running")
	if code != 0 || !strings.Contains(errOut, "No workloads found in namespace ml-team.") {
		t.Errorf("expected an empty result, got exit %d\n%s", code, errOut)
	}
}

func TestGetAllTypesRejectsATypeToken(t *testing.T) {
	fakeMixedCluster(t)

	_, errOut, code := runGetCmd(t, "jobset", "--all-types")
	if code != ExitUsage {
		t.Fatalf("expected exit %d, got %d\n%s", ExitUsage, code, errOut)
	}
	if !strings.Contains(errOut, "--all-types cannot be combined with a TYPE") {
		t.Errorf("unexpected message: %s", errOut)
	}
}

// --all-types takes no TYPE, so completing one would lead to a usage error.
func TestCompleteOffersNoTypeAfterAllTypes(t *testing.T) {
	fakeMixedCluster(t)

	if got := complete(t, "get", "--all-types", ""); len(got) != 0 {
		t.Errorf("expected no candidates, got %v", got)
	}
}

func TestGetRequiresATypeOrAllTypes(t *testing.T) {
	fakeMixedCluster(t)

	_, errOut, code := runGetCmd(t)
	if code != ExitUsage {
		t.Fatalf("expected exit %d, got %d\n%s", ExitUsage, code, errOut)
	}
	if !strings.Contains(errOut, "a TYPE is required, or --all-types") {
		t.Errorf("unexpected message: %s", errOut)
	}
}

// Being denied one type degrades the result, and the warning says which part
// is missing.
func TestGetAllTypesSkipsATypeItMayNotList(t *testing.T) {
	client, _ := fakeMixedCluster(t, mixedNamespace()...)
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("nope"))
	})

	out, errOut, code := runGetCmd(t, "--all-types")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	if got := rowNames(out); !slices.Equal(got, []string{"web", "preprocess"}) {
		t.Errorf("expected the listable types, got %v\n%s", got, out)
	}
	if !strings.Contains(errOut, "warning: not allowed to list Pod") {
		t.Errorf("expected a warning naming the type, got %q", errOut)
	}
}

// An owner that cannot be read leaves the question open; hiding the object
// would drop a real workload with no signal.
func TestGetAllTypesKeepsAnObjectWhoseOwnerItMayNotRead(t *testing.T) {
	_, metadataClient := fakeMixedCluster(t, mixedNamespace()...)
	metadataClient.PrependReactor("list", "replicasets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Group: "apps", Resource: "replicasets"}, "", errors.New("nope"))
	})

	out, errOut, code := runGetCmd(t, "--all-types")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	if !slices.Contains(rowNames(out), "web-abc-1") {
		t.Errorf("expected the pod to be listed\n%s", out)
	}
	if !strings.Contains(errOut, "warning: listing Pod/web-abc-1, though it may belong to another workload") {
		t.Errorf("expected a warning for the unreadable owner, got %q", errOut)
	}
}

// The server returns the same objects at every version it serves, so a Kind
// covered at two versions must be listed once.
func TestGetAllTypesListsAKindCoveredAtTwoVersionsOnce(t *testing.T) {
	client := setupDynamoCluster(t)

	_, errOut, code := runGetCmd(t, "--all-types")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	var versions []string
	for _, action := range client.Actions() {
		if action.GetVerb() == "list" && action.GetResource().Resource == "dynamographdeployments" {
			versions = append(versions, action.GetResource().Version)
		}
	}
	if !slices.Equal(versions, []string{"v1beta1"}) {
		t.Errorf("expected one list at the most stable version, got %v", versions)
	}
}

// Two covered kinds sharing a name must stay apart in the TYPE column.
func TestGetAllTypesQualifiesASharedKind(t *testing.T) {
	fakeMixedCluster(t, jobSet("preprocess", 1))

	other := kartas.Jobset()
	other.Name = "example-jobset"
	other.Spec.StructureDefinition.RootComponent.Kind.Group = "example.com"
	restore := loadDefinitions
	loadDefinitions = func(context.Context, genericclioptions.RESTClientGetter) (*definitions.Resolver, []definitions.Warning) {
		return definitions.New([]*v1alpha1.Karta{kartas.Jobset()}, []*v1alpha1.Karta{other}), nil
	}
	t.Cleanup(func() { loadDefinitions = restore })

	out, errOut, code := runGetCmd(t, "--all-types")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	if !strings.Contains(out, "JobSet.jobset.x-k8s.io") {
		t.Errorf("expected the qualified type\n%s", out)
	}
}

// The machine formats keep the per-type contract: kind and apiVersion already
// identify each item's type.
func TestGetAllTypesJSONCarriesEachItemsType(t *testing.T) {
	fakeMixedCluster(t, mixedNamespace()...)

	out, errOut, code := runGetCmd(t, "--all-types", "-o", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	items, count := decodeEnvelope(t, out)
	if count != 3 || len(items) != 3 {
		t.Fatalf("expected three items, got %d\n%s", count, out)
	}
	for _, item := range items {
		if item["kind"] == "" || item["apiVersion"] == "" {
			t.Errorf("expected kind and apiVersion on %v", item)
		}
	}
}

// Owners are read as metadata, one list per owner kind, so a namespace of
// Deployments costs one ReplicaSet read however many pods it runs.
func TestGetAllTypesReadsOwnersAsOneMetadataListPerKind(t *testing.T) {
	objects := append(mixedNamespace(),
		object(podGVK, "web-abc-2", controlledBy(appsReplicaSetGVK, "web-abc")),
		object(appsReplicaSetGVK, "web-def", controlledBy(appsDeploymentGVK, "web")),
		object(podGVK, "web-def-1", controlledBy(appsReplicaSetGVK, "web-def")),
	)
	dynamicClient, metadataClient := fakeMixedCluster(t, objects...)

	out, errOut, code := runGetCmd(t, "--all-types")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	if got := rowNames(out); !slices.Equal(got, []string{"web", "preprocess", "debug-shell"}) {
		t.Errorf("expected the top-level rows alone, got %v\n%s", got, out)
	}

	var reads []string
	for _, action := range metadataClient.Actions() {
		reads = append(reads, action.GetVerb()+" "+action.GetResource().Resource)
	}
	if !slices.Equal(reads, []string{"list replicasets"}) {
		t.Errorf("expected one ReplicaSet metadata list, got %v", reads)
	}
	for _, action := range dynamicClient.Actions() {
		if action.GetVerb() == "get" {
			t.Errorf("owners must not be read as full objects: %v", action)
		}
	}
}
