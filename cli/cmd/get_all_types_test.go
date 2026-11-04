// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
	"k8s.io/client-go/discovery"
	discoveryfake "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	metadatafake "k8s.io/client-go/metadata/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/utils/ptr"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/definitions"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog/kartas"
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

// fakeDiscovery is a discovery cache that reads as healthy, while server
// answers live reads.
type fakeDiscovery struct {
	discoveryfake.FakeDiscovery
	server *httptest.Server
}

func (d *fakeDiscovery) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	return nil, nil, nil
}

func (d *fakeDiscovery) RESTClient() rest.Interface {
	return discovery.NewDiscoveryClientForConfigOrDie(&rest.Config{Host: d.server.URL}).RESTClient()
}

func (d *fakeDiscovery) Fresh() bool { return true }

func (d *fakeDiscovery) Invalidate() {}

// discoveryServer serves discovery for groups, and 503 for each group version
// in failed.
func discoveryServer(t *testing.T, groups []schema.GroupVersion, failed ...schema.GroupVersion) *httptest.Server {
	t.Helper()

	list := metav1.APIGroupList{TypeMeta: metav1.TypeMeta{Kind: "APIGroupList", APIVersion: "v1"}}
	for _, gv := range append(groups, failed...) {
		if gv.Group == "" {
			continue
		}
		version := metav1.GroupVersionForDiscovery{GroupVersion: gv.String(), Version: gv.Version}
		list.Groups = append(list.Groups, metav1.APIGroup{
			Name: gv.Group, Versions: []metav1.GroupVersionForDiscovery{version}, PreferredVersion: version,
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body any
		switch path := strings.TrimPrefix(r.URL.Path, "/apis/"); {
		case r.URL.Path == "/api":
			body = metav1.APIVersions{TypeMeta: metav1.TypeMeta{Kind: "APIVersions"}, Versions: []string{"v1"}}
		case r.URL.Path == "/apis":
			body = list
		case slices.ContainsFunc(failed, func(gv schema.GroupVersion) bool { return gv.String() == path }):
			http.Error(w, "the server is currently unable to handle the request", http.StatusServiceUnavailable)
			return
		default:
			gv := strings.TrimPrefix(path, "/api/")
			body = metav1.APIResourceList{TypeMeta: metav1.TypeMeta{Kind: "APIResourceList", APIVersion: "v1"}, GroupVersion: gv}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(server.Close)
	return server
}

// fakeMixedCluster serves JobSet, Deployment, ReplicaSet, Pod, Job and
// InferenceService. Every other covered type is absent.
func fakeMixedCluster(t *testing.T, objects ...runtime.Object) (*dynamicfake.FakeDynamicClient, *metadatafake.FakeMetadataClient) {
	t.Helper()

	served := map[schema.GroupVersionKind]string{
		jobSetGVK:           "jobsets",
		appsDeploymentGVK:   "deployments",
		appsReplicaSetGVK:   "replicasets",
		podGVK:              "pods",
		batchJobGVK:         "jobs",
		inferenceServiceGVK: "inferenceservices",
	}
	listKinds := map[schema.GroupVersionResource]string{}
	// Default versions let an unversioned lookup find a kind, as discovery does.
	var groupVersions []schema.GroupVersion
	for gvk := range served {
		groupVersions = append(groupVersions, gvk.GroupVersion())
	}
	mapper := meta.NewDefaultRESTMapper(groupVersions)
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
		WithRESTMapper(mapper).
		WithDiscoveryClient(&fakeDiscovery{server: discoveryServer(t, groupVersions)})
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

// denyReplicaSets makes each verb forbidden on the ReplicaSet metadata. A denied
// get names its object, as the apiserver does.
func denyReplicaSets(client *metadatafake.FakeMetadataClient, verbs ...string) {
	for _, verb := range verbs {
		client.PrependReactor(verb, "replicasets", func(action k8stesting.Action) (bool, runtime.Object, error) {
			var name string
			if get, ok := action.(k8stesting.GetAction); ok {
				name = get.GetName()
			}
			return true, nil, apierrors.NewForbidden(
				schema.GroupResource{Group: "apps", Resource: "replicasets"}, name, errors.New("nope"))
		})
	}
}

// A role may grant get on an owner kind without list, which still answers
// whether the object is nested.
func TestGetAllTypesReadsAnOwnerByNameWhenItMayNotList(t *testing.T) {
	_, metadataClient := fakeMixedCluster(t, mixedNamespace()...)
	denyReplicaSets(metadataClient, "list")

	out, errOut, code := runGetCmd(t, "--all-types")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	if got := rowNames(out); !slices.Equal(got, []string{"web", "preprocess", "debug-shell"}) {
		t.Errorf("expected the top-level rows alone, got %v\n%s", got, out)
	}
	if errOut != "" {
		t.Errorf("expected no warning, got %q", errOut)
	}
}

// A kind left out of the listing is not covered either, or what it owns is
// hidden behind an owner that never becomes a row.
func TestGetAllTypesListsTheChildrenOfAKindItMayNotList(t *testing.T) {
	client, _ := fakeMixedCluster(t, append(mixedNamespace(),
		object(batchJobGVK, "preprocess-etl", controlledBy(jobSetGVK, "preprocess")))...)
	client.PrependReactor("list", "jobsets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "jobset.x-k8s.io", Resource: "jobsets"}, "", errors.New("nope"))
	})

	out, errOut, code := runGetCmd(t, "--all-types")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	if !slices.Contains(rowNames(out), "preprocess-etl") {
		t.Errorf("expected the JobSet's Job to be listed\n%s", out)
	}
	if !strings.Contains(errOut, "warning: not allowed to list JobSet") {
		t.Errorf("expected a warning naming the type, got %q", errOut)
	}
}

// A cached discovery can outlive an uninstalled CRD, whose list is then a 404.
// That one type must not fail the whole listing.
func TestGetAllTypesSkipsAKindTheServerNoLongerServes(t *testing.T) {
	client, _ := fakeMixedCluster(t, mixedNamespace()...)
	client.PrependReactor("list", "jobsets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Group: "jobset.x-k8s.io", Resource: "jobsets"}, "")
	})

	out, errOut, code := runGetCmd(t, "--all-types")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	if got := rowNames(out); !slices.Equal(got, []string{"web", "debug-shell"}) {
		t.Errorf("expected the other types, got %v\n%s", got, out)
	}
	if errOut != "" {
		t.Errorf("expected no warning, got %q", errOut)
	}
}

// One unreadable owner kind fails the walk of every object under it, which
// must cost one warning, not one per object.
func TestGetAllTypesWarnsOncePerCause(t *testing.T) {
	_, metadataClient := fakeMixedCluster(t, append(mixedNamespace(),
		object(podGVK, "web-abc-2", controlledBy(appsReplicaSetGVK, "web-abc")),
		object(appsReplicaSetGVK, "web-def", controlledBy(appsDeploymentGVK, "web")),
		object(podGVK, "web-def-1", controlledBy(appsReplicaSetGVK, "web-def")),
	)...)
	denyReplicaSets(metadataClient, "list", "get")

	_, errOut, code := runGetCmd(t, "--all-types")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	if n := strings.Count(errOut, "warning:"); n != 1 {
		t.Errorf("expected one warning, got %d\n%s", n, errOut)
	}
	if !strings.Contains(errOut, "and 2 more, though they may belong to another workload") {
		t.Errorf("expected the three pods in one warning, got %q", errOut)
	}
}

// An owner that cannot be read leaves the question open; hiding the object
// would drop a real workload with no signal.
func TestGetAllTypesKeepsAnObjectWhoseOwnerItMayNotRead(t *testing.T) {
	_, metadataClient := fakeMixedCluster(t, mixedNamespace()...)
	denyReplicaSets(metadataClient, "list", "get")

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

// A kind discovery missed looks absent, and must not hide its children.
func TestGetAllTypesShowsAChildOfAKindDiscoveryMissed(t *testing.T) {
	rayClusterGVK := schema.GroupVersionKind{Group: "ray.io", Version: "v1", Kind: "RayCluster"}
	fakeMixedCluster(t, append(mixedNamespace(),
		object(podGVK, "ray-head", controlledBy(rayClusterGVK, "ray")))...)

	out, errOut, code := runGetCmd(t, "--all-types")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	if !slices.Contains(rowNames(out), "ray-head") {
		t.Errorf("expected the pod to be listed\n%s", out)
	}
	if !strings.Contains(errOut, "warning: listing Pod/ray-head, though it may belong to another workload: discover RayCluster") {
		t.Errorf("expected a warning for the unmapped owner, got %q", errOut)
	}
}

// A covered kind whose group fails live discovery warns itself, as a childless
// workload leaves no owner reference to warn on. Other groups stay quiet.
func TestGetAllTypesWarnsOnACoveredKindWhoseGroupFailedDiscovery(t *testing.T) {
	fakeMixedCluster(t, mixedNamespace()...)
	clusterAccess().(*genericclioptions.TestConfigFlags).WithDiscoveryClient(&fakeDiscovery{
		server: discoveryServer(t, nil,
			schema.GroupVersion{Group: "ray.io", Version: "v1"},
			schema.GroupVersion{Group: "metrics.k8s.io", Version: "v1beta1"}),
	})

	out, errOut, code := runGetCmd(t, "--all-types")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	if got := rowNames(out); !slices.Equal(got, []string{"web", "preprocess", "debug-shell"}) {
		t.Errorf("expected the readable types, got %v\n%s", got, out)
	}
	for _, kind := range []string{"RayCluster", "RayJob", "RayService"} {
		if !strings.Contains(errOut, "warning: not listing "+kind+": API discovery failed for ray.io/v1") {
			t.Errorf("expected a warning naming %s, got %q", kind, errOut)
		}
	}
	if strings.Contains(errOut, "metrics.k8s.io") {
		t.Errorf("a group no Karta covers should stay quiet, got %q", errOut)
	}
}

// Discovery failing outright is not a partial result to warn about.
func TestGetAllTypesFailsWhenDiscoveryFails(t *testing.T) {
	fakeMixedCluster(t, mixedNamespace()...)
	server := discoveryServer(t, nil)
	server.Close()
	clusterAccess().(*genericclioptions.TestConfigFlags).WithDiscoveryClient(&fakeDiscovery{server: server})

	_, errOut, code := runGetCmd(t, "--all-types")
	if code == 0 || !strings.Contains(errOut, "kubernetes discovery:") || !strings.Contains(errOut, "connection refused") {
		t.Errorf("expected a discovery error, got exit %d\n%s", code, errOut)
	}
}

// A kind served only at an uncovered version is skipped, and must not hide its children.
func TestGetAllTypesWarnsOnAKindServedAtAnUncoveredVersion(t *testing.T) {
	fakeMixedCluster(t, jobSet("preprocess", 1),
		object(podGVK, "preprocess-0", controlledBy(jobSetGVK, "preprocess")))

	future := kartas.Jobset()
	future.Name = "jobset-v99"
	future.Spec.StructureDefinition.RootComponent.Kind.Version = "v99"
	embedded := slices.DeleteFunc(slices.Clone(catalog.List()), func(k *v1alpha1.Karta) bool { return k.Name == kartas.Jobset().Name })
	restore := loadDefinitions
	loadDefinitions = func(context.Context, genericclioptions.RESTClientGetter) (*definitions.Resolver, []definitions.Warning) {
		return definitions.New(embedded, []*v1alpha1.Karta{future}), nil
	}
	t.Cleanup(func() { loadDefinitions = restore })

	out, errOut, code := runGetCmd(t, "--all-types")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, errOut)
	}
	if got := rowNames(out); !slices.Equal(got, []string{"preprocess-0"}) {
		t.Errorf("expected the JobSet's pod to surface, got %v\n%s", got, out)
	}
	want := `warning: not listing JobSet: Karta "jobset-v99" covers jobset.x-k8s.io/v99/JobSet, ` +
		"but the cluster serves jobset.x-k8s.io/v1alpha2/JobSet"
	if !strings.Contains(errOut, want) {
		t.Errorf("expected %q, got %q", want, errOut)
	}
}
