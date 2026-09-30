// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package definitions

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/catalog"
)

var kartasResource = schema.GroupResource{Group: "run.ai", Resource: "kartas"}

// noKubeconfigGetter is a RESTClientGetter with nothing configured, which is what
// running without a kubeconfig looks like to fetchCluster.
func noKubeconfigGetter() genericclioptions.RESTClientGetter {
	return genericclioptions.NewTestConfigFlags().WithClientConfig(
		clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{},
			&clientcmd.ConfigOverrides{},
		),
	)
}

// serverConfigGetter points a RESTClientGetter at an in-process test server.
func serverConfigGetter(server *httptest.Server) genericclioptions.RESTClientGetter {
	cfg := clientcmdapi.NewConfig()
	cfg.CurrentContext = "test"
	cfg.Clusters["test"] = &clientcmdapi.Cluster{Server: server.URL}
	cfg.Contexts["test"] = &clientcmdapi.Context{Cluster: "test", AuthInfo: "test"}
	cfg.AuthInfos["test"] = &clientcmdapi.AuthInfo{}

	return genericclioptions.NewTestConfigFlags().
		WithClientConfig(clientcmd.NewDefaultClientConfig(*cfg, &clientcmd.ConfigOverrides{}))
}

// kartaListJSON renders the API response for a list of Kartas that all claim
// apps/v1 Deployment as their root component.
func kartaListJSON(names ...string) string {
	items := make([]string, 0, len(names))
	for _, name := range names {
		items = append(items, fmt.Sprintf(`{"apiVersion":"run.ai/v1alpha1","kind":"Karta",`+
			`"metadata":{"name":%q},`+
			`"spec":{"structureDefinition":{"rootComponent":{"name":"root",`+
			`"kind":{"group":"apps","version":"v1","kind":"Deployment"}}}}}`, name))
	}
	body := `{"apiVersion":"run.ai/v1alpha1","kind":"KartaList","metadata":{"resourceVersion":"1"},"items":[`
	for i, item := range items {
		if i > 0 {
			body += ","
		}
		body += item
	}
	return body + `]}`
}

var _ = Describe("classify", func() {
	It("stays silent for a wrapped empty-config error from the real fetchCluster", func() {
		_, err := (&loader{}).fetchCluster(context.Background(), noKubeconfigGetter())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("kubernetes config"))
		Expect(err.Error()).To(ContainSubstring("no configuration has been provided"))

		// The wrapping is exactly what defeats clientcmd.IsEmptyConfig, so classify
		// has to unwrap before it can recognise the offline case.
		Expect(clientcmd.IsEmptyConfig(err)).To(BeFalse())
		Expect(clientcmd.IsEmptyConfig(errors.Unwrap(err))).To(BeTrue())

		_, ok := classify(err)
		Expect(ok).To(BeFalse())
	})

	It("warns when an explicit kubeconfig path does not exist", func() {
		missing := filepath.Join(GinkgoT().TempDir(), "missing-kubeconfig")
		flags := genericclioptions.NewConfigFlags(false)
		flags.KubeConfig = &missing

		_, err := (&loader{}).fetchCluster(context.Background(), flags)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("kubernetes config"))
		Expect(err.Error()).To(ContainSubstring("missing-kubeconfig"))

		got, ok := classify(err)
		Expect(ok).To(BeTrue())
		Expect(got.Reason).To(Equal(ReasonUnreachable))
		Expect(got.Message).To(ContainSubstring("missing-kubeconfig"))
	})

	It("warns when the Karta CRD is not installed", func() {
		err := fmt.Errorf("list Karta definitions: %w", apierrors.NewNotFound(kartasResource, ""))

		got, ok := classify(err)
		Expect(ok).To(BeTrue())
		Expect(got.Reason).To(Equal(ReasonNoCRD))
		Expect(got.Message).To(ContainSubstring("kartas.run.ai"))
	})

	It("warns and names the resource when listing is forbidden", func() {
		err := fmt.Errorf("list Karta definitions: %w",
			apierrors.NewForbidden(kartasResource, "", errors.New("RBAC: access denied")))

		got, ok := classify(err)
		Expect(ok).To(BeTrue())
		Expect(got.Reason).To(Equal(ReasonForbidden))
		Expect(got.Message).To(ContainSubstring("kartas.run.ai"))
		Expect(got.Message).To(ContainSubstring("list"))
	})

	It("warns with the cause for any other failure", func() {
		err := fmt.Errorf("list Karta definitions: %w", errors.New("connection refused"))

		got, ok := classify(err)
		Expect(ok).To(BeTrue())
		Expect(got.Reason).To(Equal(ReasonUnreachable))
		Expect(got.Message).To(ContainSubstring("connection refused"))
	})

	It("never leaks apimachinery no-match wording", func() {
		err := fmt.Errorf("list Karta definitions: %w", apierrors.NewNotFound(kartasResource, ""))

		got, _ := classify(err)
		Expect(got.Message).NotTo(ContainSubstring("no matches for kind"))
	})
})

var _ = Describe("Load", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("falls back to the built-in catalog when no cluster is reachable", func() {
		resolver, warnings := Load(ctx, noKubeconfigGetter())

		Expect(warnings).To(BeEmpty())

		list := resolver.List()
		Expect(list).To(HaveLen(len(catalog.List())))
		for _, def := range list {
			Expect(def.Origin).To(Equal(OriginCatalog))
		}
	})

	It("merges cluster definitions and reads them cluster-scoped", func() {
		var requestPaths []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestPaths = append(requestPaths, r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, kartaListJSON("cluster-deployment"))
		}))
		DeferCleanup(server.Close)

		resolver, warnings := Load(ctx, serverConfigGetter(server))

		Expect(warnings).To(BeEmpty())
		Expect(requestPaths).To(Equal([]string{"/apis/run.ai/v1alpha1/kartas"}))

		def, err := resolver.Resolve(deploymentGVK)
		Expect(err).NotTo(HaveOccurred())
		Expect(def.Karta.Name).To(Equal("cluster-deployment"))
		Expect(def.Origin).To(Equal(OriginCluster))
	})

	DescribeTable("warns once per cluster collision",
		func(names []string, want string) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, kartaListJSON(names...))
			}))
			DeferCleanup(server.Close)

			resolver, warnings := Load(ctx, serverConfigGetter(server))

			Expect(warnings).To(HaveLen(1))
			Expect(warnings[0].Reason).To(Equal(ReasonCollision))
			Expect(warnings[0].Message).To(Equal(want))

			_, err := resolver.Resolve(deploymentGVK)
			Expect(err).To(MatchError(ErrAmbiguous))
		},
		Entry("two claimants",
			[]string{"aaa-deployment", "zzz-deployment"},
			"2 cluster Karta definitions claim apps/v1, Kind=Deployment: "+
				`"aaa-deployment", "zzz-deployment"`,
		),
		Entry("four claimants",
			[]string{"zzz-deployment", "aaa-deployment", "ccc-deployment", "bbb-deployment"},
			"4 cluster Karta definitions claim apps/v1, Kind=Deployment: "+
				`"aaa-deployment", "bbb-deployment", "ccc-deployment", "zzz-deployment"`,
		),
	)
})

// newFakeDynamic builds a dynamic fake over a hand-rolled unstructured scheme.
// The Karta types must map to unstructured here: the object tracker builds the
// list result with meta.SetList, which cannot assign []*unstructured.Unstructured
// into the typed KartaList that v1alpha1.AddToScheme would register.
func newFakeDynamic(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(v1alpha1.GroupVersion.WithKind("Karta"), &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(v1alpha1.GroupVersion.WithKind("KartaList"), &unstructured.UnstructuredList{})

	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{GVR: "KartaList"},
		objects...,
	)
}

// clusterKartas renders every catalog definition the way the API server hands it
// back: the typed spec as unstructured, plus the metadata and status only a
// server sets. It returns the objects to serve and the Kartas they must convert
// back into, so the whole spec tree is covered rather than a sample of fields.
func clusterKartas() ([]runtime.Object, []*v1alpha1.Karta) {
	GinkgoHelper()

	stamped := catalog.List()
	// Local, not UTC: metav1.Time unmarshals to local time, so a UTC stamp would
	// come back in a different location and defeat the comparison.
	served := metav1.Date(2026, 1, 15, 10, 0, 0, 0, time.Local)
	objects := make([]runtime.Object, 0, len(stamped))
	for i, karta := range stamped {
		karta.TypeMeta = metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "Karta"}
		karta.UID = types.UID(fmt.Sprintf("4c9f4b6e-9f2e-4b2f-9d3a-%012d", i))
		karta.ResourceVersion = strconv.Itoa(4711 + i)
		karta.Generation = 3
		karta.CreationTimestamp = served
		karta.Status = v1alpha1.KartaStatus{Conditions: []metav1.Condition{{
			Type:               "Validated",
			Status:             metav1.ConditionTrue,
			Reason:             "SchemaAccepted",
			LastTransitionTime: served,
			ObservedGeneration: 3,
		}}}

		raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(karta)
		Expect(err).NotTo(HaveOccurred())
		objects = append(objects, &unstructured.Unstructured{Object: raw})
	}
	return objects, stamped
}

var _ = Describe("listWithClient", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("converts the cluster payload into typed Kartas", func() {
		objects, want := clusterKartas()

		kartas, err := (&loader{}).listWithClient(ctx, newFakeDynamic(objects...))
		Expect(err).NotTo(HaveOccurred())
		Expect(kartas).To(ConsistOf(want))
	})

	It("skips a definition it cannot read and keeps the rest", func() {
		// A field whose type this build does not expect, as CRD schema skew
		// against a newer operator would produce.
		skewed := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "run.ai/v1alpha1", "kind": "Karta",
			"metadata": map[string]any{"name": "skewed"},
			"spec": map[string]any{"structureDefinition": map[string]any{
				"rootComponent": map[string]any{
					"name": "dep",
					"kind": map[string]any{"group": "apps", "version": int64(1), "kind": "Deployment"},
				},
			}},
		}}
		objects, want := clusterKartas()

		l := &loader{}
		kartas, err := l.listWithClient(ctx, newFakeDynamic(append(objects, skewed)...))
		Expect(err).NotTo(HaveOccurred())
		Expect(kartas).To(ConsistOf(want))

		Expect(l.warnings).To(HaveLen(1))
		Expect(l.warnings[0].Reason).To(Equal(ReasonInvalid))
		Expect(l.warnings[0].Message).To(ContainSubstring(`skipping Karta definition "skewed"`))
	})

	It("issues a cluster-scoped list against the kartas resource", func() {
		objects, _ := clusterKartas()
		client := newFakeDynamic(objects...)

		var recorded []k8stesting.ListAction
		client.PrependReactor("list", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
			recorded = append(recorded, action.(k8stesting.ListAction))
			return false, nil, nil
		})

		_, err := (&loader{}).listWithClient(ctx, client)
		Expect(err).NotTo(HaveOccurred())

		Expect(recorded).To(HaveLen(1))
		Expect(recorded[0].GetResource()).To(Equal(GVR))
		Expect(recorded[0].GetNamespace()).To(BeEmpty())
		Expect(recorded[0].GetListRestrictions().Labels.Empty()).To(BeTrue())
		Expect(recorded[0].GetListRestrictions().Fields.Empty()).To(BeTrue())
	})

	It("returns no definitions and no error for an empty cluster", func() {
		kartas, err := (&loader{}).listWithClient(ctx, newFakeDynamic())
		Expect(err).NotTo(HaveOccurred())
		Expect(kartas).To(BeEmpty())
	})
})
