// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/yaml"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/definitions"
	"github.com/dsx-ai-factory/workload-map/cli/pkg/generator"
	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

var (
	deploymentGVK = v1alpha1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	jobGVK        = v1alpha1.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
	pytorchGVK    = v1alpha1.GroupVersionKind{Group: "kubeflow.org", Version: "v1", Kind: "PyTorchJob"}
)

// countingGetter proves a code path never reached the cluster.
type countingGetter struct {
	genericclioptions.RESTClientGetter
	calls int
}

// noClusterGetter is what running without a kubeconfig looks like. NewConfigFlags
// cannot stand in for it: it defaults the server to http://localhost:8080, turning
// "no kubeconfig" into a connection attempt against whatever listens there.
func noClusterGetter() genericclioptions.RESTClientGetter {
	return genericclioptions.NewTestConfigFlags().WithClientConfig(
		clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{},
			&clientcmd.ConfigOverrides{},
		),
	)
}

func (g *countingGetter) ToRESTConfig() (*rest.Config, error) {
	g.calls++
	return g.RESTClientGetter.ToRESTConfig()
}

// kartaListServer answers the Karta list with one definition claiming gvk.
func kartaListServer(name string, gvk v1alpha1.GroupVersionKind) *httptest.Server {
	body := fmt.Sprintf(`{"apiVersion":"run.ai/v1alpha1","kind":"KartaList",`+
		`"metadata":{"resourceVersion":"1"},"items":[`+
		`{"apiVersion":"run.ai/v1alpha1","kind":"Karta","metadata":{"name":%q},`+
		`"spec":{"structureDefinition":{"rootComponent":{"name":"root",`+
		`"kind":{"group":%q,"version":%q,"kind":%q}}}}}]}`,
		name, gvk.Group, gvk.Version, gvk.Kind)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
}

func clusterGetter(server *httptest.Server) genericclioptions.RESTClientGetter {
	api := clientcmdapi.NewConfig()
	api.Clusters["test"] = &clientcmdapi.Cluster{Server: server.URL}
	api.Contexts["test"] = &clientcmdapi.Context{Cluster: "test", AuthInfo: "test"}
	api.AuthInfos["test"] = &clientcmdapi.AuthInfo{}
	api.CurrentContext = "test"

	return genericclioptions.NewTestConfigFlags().
		WithClientConfig(clientcmd.NewDefaultClientConfig(*api, &clientcmd.ConfigOverrides{}))
}

// runDefinitions captures stdout and stderr separately, so a spec can assert
// warnings never land on stdout.
func runDefinitions(rcg genericclioptions.RESTClientGetter, args []string) (string, string, error) {
	cmd := newDefinitionsCommand(rcg)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetFlagErrorFunc(usageError)

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetContext(context.Background())
	// Cobra reads os.Args when handed a nil slice, which a zero-argument variadic
	// call produces, so it would parse the go test and ginkgo flags.
	cmd.SetArgs(append([]string{}, args...))

	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

// exitStatus reports the status a failure produces, the way main classifies it.
// unusedTable fails the spec if a machine format reaches the table callback.
func unusedTable(io.Writer) error {
	Fail("the table renderer must not run for a machine format")
	return nil
}

func exitStatus(err error) int {
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return ExitError
}

func tableNames(table string) []string {
	lines := strings.Split(strings.TrimSpace(table), "\n")
	names := make([]string, 0, len(lines))
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		names = append(names, fields[0])
	}
	return names
}

// newTestKarta claims gvk as its root component, the minimum to be indexed.
func newTestKarta(name string, gvk v1alpha1.GroupVersionKind, root string, children []string) *v1alpha1.Karta {
	structure := v1alpha1.StructureDefinition{
		RootComponent: v1alpha1.ComponentDefinition{Name: root, Kind: &gvk},
	}
	for _, child := range children {
		structure.ChildComponents = append(structure.ChildComponents,
			v1alpha1.ComponentDefinition{Name: child})
	}
	return &v1alpha1.Karta{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.KartaSpec{StructureDefinition: structure},
	}
}

var _ = Describe("kli definitions", func() {
	It("rejects a positional argument", func() {
		_, _, err := runDefinitions(noClusterGetter(), []string{"bogus"})
		Expect(err).To(HaveOccurred())
	})

	Context("without cluster access", func() {
		It("lists the catalog and exits without error", func() {
			stdout, _, err := runDefinitions(noClusterGetter(), nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(stdout).To(ContainSubstring(pytorchDefinition))
			Expect(stdout).To(ContainSubstring(string(definitions.OriginCatalog)))
		})

		It("keeps stdout free of warnings", func() {
			stdout, _, err := runDefinitions(noClusterGetter(), nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(stdout).NotTo(ContainSubstring("warning"))
		})
	})

	Context("table output", func() {
		It("prints the documented header", func() {
			stdout, _, err := runDefinitions(noClusterGetter(), nil)
			Expect(err).NotTo(HaveOccurred())

			header := strings.Fields(strings.SplitN(stdout, "\n", 2)[0])
			Expect(header).To(Equal([]string{"NAME", "KIND", "ORIGIN", "COMPONENTS"}))
		})

		It("sorts rows by name ascending", func() {
			stdout, _, err := runDefinitions(noClusterGetter(), nil)
			Expect(err).NotTo(HaveOccurred())

			names := tableNames(stdout)
			Expect(names).NotTo(BeEmpty())
			Expect(names).To(Equal(slices.Sorted(slices.Values(names))))
		})
	})

	Context("machine-readable output", func() {
		It("emits the definitions themselves, not table rows", func() {
			stdout, _, err := runDefinitions(noClusterGetter(), []string{"-o", "json"})
			Expect(err).NotTo(HaveOccurred())

			kartas := decodeKartas(stdout)
			Expect(kartas).NotTo(BeEmpty())
			for _, karta := range kartas {
				Expect(karta.APIVersion).To(Equal("run.ai/v1alpha1"))
				Expect(karta.Kind).To(Equal("Karta"))
			}
			Expect(stdout).NotTo(ContainSubstring(`"origin"`))
			Expect(stdout).NotTo(ContainSubstring(`"components"`))
		})

		It("agrees with json on the same definitions", func() {
			yamlOut, _, err := runDefinitions(noClusterGetter(), []string{"-o", "yaml"})
			Expect(err).NotTo(HaveOccurred())
			jsonOut, _, err := runDefinitions(noClusterGetter(), []string{"-o", "json"})
			Expect(err).NotTo(HaveOccurred())

			Expect(decodeKartas(yamlOut)).To(Equal(decodeKartas(jsonOut)))
		})

		It("rejects wide, which the root enum accepts for other commands", func() {
			_, _, err := runDefinitions(noClusterGetter(), []string{"-o", "wide"})
			Expect(exitStatus(err)).To(Equal(ExitUsage))
			// The flag lists what this command renders, so wide never parses here.
			Expect(err.Error()).To(ContainSubstring("must be one of table, json, yaml"))
		})

		It("rejects an unsupported format before reading the cluster", func() {
			getter := &countingGetter{RESTClientGetter: noClusterGetter()}
			_, _, err := runDefinitions(getter, []string{"-o", "wide"})
			Expect(exitStatus(err)).To(Equal(ExitUsage))
			// A format the command cannot render must cost no round trip.
			Expect(getter.calls).To(BeZero())
		})
	})
})

var _ = Describe("kli definitions against a cluster", func() {
	// Only a spec driving a real cluster read shows the cluster half of the merge
	// reaching the output. Serving a GVK the catalog already covers pins both
	// halves: the cluster row appears and the catalog row it overrides is gone.
	var server *httptest.Server

	BeforeEach(func() {
		server = kartaListServer("cluster-pytorch", pytorchGVK)
		DeferCleanup(server.Close)
	})

	// Only the table carries ORIGIN; the definitions do not record their source.
	rowsFrom := func(table string) map[string]definitionRow {
		GinkgoHelper()
		lines := strings.Split(strings.TrimSpace(table), "\n")
		byName := make(map[string]definitionRow, len(lines))
		for _, line := range lines[1:] {
			columns := strings.Fields(line)
			Expect(len(columns)).To(BeNumerically(">=", 3))
			byName[columns[0]] = definitionRow{Name: columns[0], Kind: columns[1], Origin: columns[2]}
		}
		return byName
	}

	It("lists the cluster definition, as cluster", func() {
		stdout, _, err := runDefinitions(clusterGetter(server), nil)
		Expect(err).NotTo(HaveOccurred())

		rows := rowsFrom(stdout)
		Expect(rows).To(HaveKey("cluster-pytorch"))
		Expect(rows["cluster-pytorch"].Origin).To(Equal(string(definitions.OriginCluster)))
		Expect(rows["cluster-pytorch"].Kind).To(Equal("PyTorchJob"))
	})

	It("drops the catalog definition the cluster one overrides", func() {
		stdout, _, err := runDefinitions(clusterGetter(server), nil)
		Expect(err).NotTo(HaveOccurred())

		Expect(rowsFrom(stdout)).NotTo(HaveKey(pytorchDefinition))
	})

	It("narrows to the cluster definition", func() {
		stdout, _, err := runDefinitions(clusterGetter(server), []string{"--group", "kubeflow.org", "--kind", "PyTorchJob"})
		Expect(err).NotTo(HaveOccurred())
		Expect(tableNames(stdout)).To(Equal([]string{"cluster-pytorch"}))
		Expect(stdout).To(ContainSubstring(string(definitions.OriginCluster)))
	})

	It("still lists the catalog definitions the cluster says nothing about", func() {
		stdout, _, err := runDefinitions(clusterGetter(server), nil)
		Expect(err).NotTo(HaveOccurred())

		rows := rowsFrom(stdout)
		Expect(rows).To(HaveKey(jobsetDefinition))
		Expect(rows[jobsetDefinition].Origin).To(Equal(string(definitions.OriginCatalog)))
	})
})

var _ = Describe("a definition that names no workload type", func() {
	// rootComponent.kind is optional per the CRD, so this is valid and must list.
	rootless := func(name string) *v1alpha1.Karta {
		return &v1alpha1.Karta{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.KartaSpec{StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{Name: "mystery"}}},
		}
	}

	It("appears in the table with a placeholder kind", func() {
		var stdout, stderr bytes.Buffer
		rows := definitionRows(definitions.New(nil, []*v1alpha1.Karta{rootless("broken-karta")}).List())
		Expect(renderDefinitions(&stdout, &stderr, rows)).To(Succeed())

		Expect(tableNames(stdout.String())).To(Equal([]string{"broken-karta"}))
		Expect(stdout.String()).To(ContainSubstring("<none>"))
	})

	It("never leaks the table placeholder into a machine format", func() {
		var stdout bytes.Buffer
		kartas := []*v1alpha1.Karta{rootless("broken-karta")}
		Expect(generator.Render(&stdout, generator.OutputJSON, kartas, false, unusedTable)).To(Succeed())

		// "<none>" is a column affordance, never part of the data.
		Expect(stdout.String()).NotTo(ContainSubstring("<none>"))
		Expect(stdout.String()).To(ContainSubstring("broken-karta"))
	})
})

var _ = Describe("definitionRows over a merged resolver", func() {
	It("lists a cluster override of a catalog GVK once, as cluster", func() {
		resolver := definitions.New(
			[]*v1alpha1.Karta{newTestKarta("catalog-pytorch", pytorchGVK, "pytorchjob", []string{"master", "worker"})},
			[]*v1alpha1.Karta{newTestKarta("cluster-pytorch", pytorchGVK, "pytorchjob", []string{"runner"})},
		)

		Expect(definitionRows(resolver.List())).To(Equal([]definitionRow{{
			Name:       "cluster-pytorch",
			Kind:       "PyTorchJob",
			Origin:     string(definitions.OriginCluster),
			Components: []string{"pytorchjob", "runner"},
		}}))
	})

	It("lists every cluster definition claiming one GVK", func() {
		resolver := definitions.New(nil, []*v1alpha1.Karta{
			newTestKarta("zulu-job", jobGVK, "job", nil),
			newTestKarta("alpha-job", jobGVK, "job", nil),
		})

		rows := definitionRows(resolver.List())
		Expect(rows).To(HaveLen(2))
		Expect([]string{rows[0].Name, rows[1].Name}).To(Equal([]string{"alpha-job", "zulu-job"}))
		Expect(rows[0].Origin).To(Equal(string(definitions.OriginCluster)))
		Expect(rows[1].Origin).To(Equal(string(definitions.OriginCluster)))
	})

	It("sorts by name even though the resolver lists by GVK", func() {
		// GVK order puts apps/v1 ahead of batch/v1, so the resolver hands over
		// zulu first and only the row builder can produce name order.
		resolver := definitions.New([]*v1alpha1.Karta{
			newTestKarta("zulu", deploymentGVK, "deployment", nil),
			newTestKarta("alpha", jobGVK, "job", nil),
		}, nil)

		Expect(resolver.List()[0].Karta.Name).To(Equal("zulu"))

		rows := definitionRows(resolver.List())
		Expect([]string{rows[0].Name, rows[1].Name}).To(Equal([]string{"alpha", "zulu"}))
	})
})

var _ = Describe("rendering an empty definition list", func() {
	It("notes the empty table on stderr and writes nothing to stdout", func() {
		var stdout, stderr bytes.Buffer
		Expect(renderDefinitions(&stdout, &stderr, definitionRows(nil))).To(Succeed())
		Expect(stdout.String()).To(BeEmpty())
		Expect(stderr.String()).To(ContainSubstring("No Karta definitions found."))
	})

	It("emits an empty json envelope with no note", func() {
		var stdout bytes.Buffer
		Expect(generator.Render[*v1alpha1.Karta](&stdout, generator.OutputJSON, nil, false, unusedTable)).To(Succeed())
		Expect(decodeKartas(stdout.String())).To(BeEmpty())
	})
})

// Asserted verbatim: only the name tells one definition of a kind from another.
const (
	jobsetDefinition         = "jobset-x-k8s-io-jobset-v1alpha2"
	pytorchDefinition        = "kubeflow-org-pytorchjob-v1"
	dynamoV1alpha1Definition = "nvidia-com-dynamographdeployment-v1alpha1"
	dynamoV1beta1Definition  = "nvidia-com-dynamographdeployment-v1beta1"
)

var _ = Describe("kli definitions NAME", func() {
	It("narrows the table to the named definition", func() {
		stdout, _, err := runDefinitions(noClusterGetter(), []string{pytorchDefinition})
		Expect(err).NotTo(HaveOccurred())
		Expect(tableNames(stdout)).To(Equal([]string{pytorchDefinition}))
	})

	DescribeTable("emits that definition on its own, with no envelope around it",
		func(format string) {
			stdout, _, err := runDefinitions(noClusterGetter(),
				[]string{pytorchDefinition, "-o", format})
			Expect(err).NotTo(HaveOccurred())

			karta := decodeKarta(stdout)
			Expect(karta.Name).To(Equal(pytorchDefinition))
			Expect(karta.Kind).To(Equal("Karta"))
			Expect(karta.APIVersion).To(Equal(v1alpha1.GroupVersion.String()))
			Expect(stdout).NotTo(ContainSubstring("items"))
		},
		Entry("json", "json"),
		Entry("yaml", "yaml"),
	)

	It("emits the same definition in json and yaml", func() {
		jsonOut, _, err := runDefinitions(noClusterGetter(),
			[]string{pytorchDefinition, "-o", "json"})
		Expect(err).NotTo(HaveOccurred())
		yamlOut, _, err := runDefinitions(noClusterGetter(),
			[]string{pytorchDefinition, "-o", "yaml"})
		Expect(err).NotTo(HaveOccurred())

		Expect(decodeKarta(yamlOut)).To(Equal(decodeKarta(jsonOut)))
	})

	It("emits a definition that validate accepts as it stands", func() {
		stdout, _, err := runDefinitions(noClusterGetter(),
			[]string{pytorchDefinition, "-o", "yaml"})
		Expect(err).NotTo(HaveOccurred())

		var karta v1alpha1.Karta
		Expect(yaml.Unmarshal([]byte(stdout), &karta)).To(Succeed())
		Expect(v1alpha1.NewKartaValidator(&karta).Validate()).To(Succeed())
	})

	It("prefers the cluster definition when a name exists in both sources", func() {
		server := kartaListServer(pytorchDefinition, pytorchGVK)
		DeferCleanup(server.Close)

		stdout, _, err := runDefinitions(clusterGetter(server), []string{pytorchDefinition})
		Expect(err).NotTo(HaveOccurred())
		Expect(stdout).To(ContainSubstring(string(definitions.OriginCluster)))
		Expect(stdout).NotTo(ContainSubstring(string(definitions.OriginCatalog)))
	})

	It("reports a name no definition carries", func() {
		stdout, _, err := runDefinitions(noClusterGetter(), []string{"nosuchname"})
		Expect(exitStatus(err)).To(Equal(ExitError))
		Expect(err.Error()).To(ContainSubstring(`no Karta definition named "nosuchname"`))
		Expect(err.Error()).To(ContainSubstring(`Run "kli definitions" to see what is available`))
		Expect(stdout).To(BeEmpty())
	})

	DescribeTable("rejects a name given alongside a filter",
		func(args []string) {
			getter := &countingGetter{RESTClientGetter: noClusterGetter()}
			_, _, err := runDefinitions(getter, args)
			Expect(exitStatus(err)).To(Equal(ExitUsage))
			Expect(err.Error()).To(ContainSubstring("give one or the other"))
			Expect(getter.calls).To(BeZero())
		},
		Entry("a group", []string{pytorchDefinition, "--group", "kubeflow.org"}),
		Entry("the core group, which is the empty string",
			[]string{pytorchDefinition, "--group", ""}),
	)

	It("rejects a second name", func() {
		_, _, err := runDefinitions(noClusterGetter(), []string{"one", "two"})
		Expect(exitStatus(err)).To(Equal(ExitUsage))
	})
})

var _ = Describe("kli definitions --group --kind --version", func() {
	DescribeTable("narrows to the definitions the filter covers",
		func(args []string, expected []string) {
			stdout, _, err := runDefinitions(noClusterGetter(), args)
			Expect(err).NotTo(HaveOccurred())
			Expect(tableNames(stdout)).To(Equal(expected))
		},
		Entry("a group alone",
			[]string{"--group", "jobset.x-k8s.io"}, []string{jobsetDefinition}),
		Entry("a group and kind",
			[]string{"--group", "jobset.x-k8s.io", "--kind", "JobSet"}, []string{jobsetDefinition}),
		Entry("a kind in the user's casing",
			[]string{"--group", "jobset.x-k8s.io", "--kind", "jobset"}, []string{jobsetDefinition}),
		Entry("a kind covered at two versions",
			[]string{"--group", "nvidia.com", "--kind", "DynamoGraphDeployment"},
			[]string{dynamoV1alpha1Definition, dynamoV1beta1Definition}),
		Entry("that kind pinned to one version",
			[]string{"--group", "nvidia.com", "--kind", "DynamoGraphDeployment", "--version", "v1beta1"},
			[]string{dynamoV1beta1Definition}),
		Entry("the core group, which is the empty string",
			[]string{"--group", "", "--kind", "Pod"}, []string{"core-pod-v1"}),
	)

	It("prints every definition covering a kind rather than picking one", func() {
		stdout, _, err := runDefinitions(noClusterGetter(), []string{"--group", "nvidia.com", "--kind", "DynamoGraphDeployment"})
		Expect(err).NotTo(HaveOccurred())
		Expect(tableNames(stdout)).To(Equal([]string{dynamoV1alpha1Definition, dynamoV1beta1Definition}))
		Expect(strings.Count(stdout, "DynamoGraphDeployment")).To(Equal(2))
	})

	DescribeTable("reports a workload type no definition covers",
		func(args []string, named string) {
			stdout, _, err := runDefinitions(noClusterGetter(), args)
			Expect(err).To(HaveOccurred())
			Expect(exitStatus(err)).To(Equal(ExitError))
			Expect(err.Error()).To(ContainSubstring(fmt.Sprintf("no Karta definition covers %q", named)))
			Expect(err.Error()).To(ContainSubstring(`Run "kli definitions" to see what is available`))
			Expect(stdout).To(BeEmpty())
		},
		Entry("an unknown group", []string{"--group", "nosuch.io"}, "nosuch.io"),
		Entry("a pluralized kind, which the root kind no longer matches",
			[]string{"--group", "jobset.x-k8s.io", "--kind", "jobsets"}, "jobset.x-k8s.io, Kind=jobsets"),
		Entry("a known kind at an unknown version",
			[]string{"--group", "nvidia.com", "--kind", "DynamoGraphDeployment", "--version", "v9"},
			"nvidia.com/v9, Kind=DynamoGraphDeployment"),
		Entry("a known kind in the wrong group",
			[]string{"--group", "wrong.group", "--kind", "JobSet"}, "wrong.group, Kind=JobSet"),
	)

	DescribeTable("rejects a filter that addresses nothing",
		func(args []string, wanted string) {
			getter := &countingGetter{RESTClientGetter: noClusterGetter()}
			_, _, err := runDefinitions(getter, args)
			Expect(exitStatus(err)).To(Equal(ExitUsage))
			Expect(err.Error()).To(ContainSubstring(wanted))
			Expect(getter.calls).To(BeZero())
		},
		Entry("a kind with no group", []string{"--kind", "JobSet"}, "--kind needs --group"),
		Entry("a version with no kind", []string{"--group", "nvidia.com", "--version", "v1"}, "--version needs --kind"),
		Entry("a version with neither", []string{"--version", "v1"}, "--version needs --kind"),
		Entry("an empty kind", []string{"--group", "nvidia.com", "--kind", ""}, "--kind needs a value"),
		Entry("an empty version",
			[]string{"--group", "nvidia.com", "--kind", "DynamoGraphDeployment", "--version", ""},
			"--version needs a value"),
	)

	Context("machine-readable output", func() {
		It("carries several matches in one yaml envelope", func() {
			stdout, _, err := runDefinitions(noClusterGetter(), []string{"--group", "nvidia.com", "--kind", "DynamoGraphDeployment", "-o", "yaml"})
			Expect(err).NotTo(HaveOccurred())

			kartas := decodeKartas(stdout)
			Expect(kartas).To(HaveLen(2))

			names := make([]string, 0, len(kartas))
			for _, karta := range kartas {
				Expect(karta.Kind).To(Equal("Karta"))
				names = append(names, karta.Name)
			}
			Expect(names).To(Equal([]string{dynamoV1alpha1Definition, dynamoV1beta1Definition}))
		})

		It("emits the raw definitions in the json envelope", func() {
			stdout, _, err := runDefinitions(noClusterGetter(), []string{"--group", "jobset.x-k8s.io", "-o", "json"})
			Expect(err).NotTo(HaveOccurred())

			kartas := decodeKartas(stdout)
			Expect(kartas).To(HaveLen(1))
			Expect(kartas[0].Name).To(Equal(jobsetDefinition))
			Expect(kartas[0].Spec.StructureDefinition.RootComponent.Name).NotTo(BeEmpty())
		})
	})
})

var _ = Describe("definitionFilter", func() {
	It("narrows a kind covered at two versions down to the requested one", func() {
		defs := definitions.New(nil, []*v1alpha1.Karta{
			newTestKarta("job-v1", jobGVK, "job", nil),
			newTestKarta("job-v2", v1alpha1.GroupVersionKind{Group: "batch", Version: "v2", Kind: "Job"}, "job", nil),
		}).List()

		Expect(definitionFilter{group: "batch", kind: "Job", set: true}.narrow(defs)).To(HaveLen(2))

		pinned := definitionFilter{group: "batch", kind: "Job", version: "v2", set: true}.narrow(defs)
		Expect(pinned).To(HaveLen(1))
		Expect(pinned[0].Karta.Name).To(Equal("job-v2"))
		Expect(pinned[0].Origin).To(Equal(definitions.OriginCluster))
	})

	It("matches a core workload whose root group is empty", func() {
		defs := definitions.New([]*v1alpha1.Karta{
			newTestKarta("core-pod", v1alpha1.GroupVersionKind{Version: "v1", Kind: "Pod"}, "pod", nil),
		}, nil).List()

		Expect(definitionFilter{group: "", kind: "Pod", set: true}.narrow(defs)).To(HaveLen(1))
		Expect(definitionFilter{group: "core", kind: "Pod", set: true}.narrow(defs)).To(BeEmpty())
	})

	It("never matches a definition that names no workload type", func() {
		defs := definitions.New(nil, []*v1alpha1.Karta{{
			ObjectMeta: metav1.ObjectMeta{Name: "rootless"},
			Spec: v1alpha1.KartaSpec{StructureDefinition: v1alpha1.StructureDefinition{
				RootComponent: v1alpha1.ComponentDefinition{Name: "mystery"}}},
		}}).List()

		// Its root GVK is the zero value, which --group "" would otherwise sweep up.
		Expect(definitionFilter{group: "", set: true}.narrow(defs)).To(BeEmpty())
	})

	DescribeTable("names the filter the way apimachinery names a GVK",
		func(filter definitionFilter, want string) {
			Expect(filter.String()).To(Equal(want))
		},
		Entry("group only", definitionFilter{group: "nvidia.com"}, "nvidia.com"),
		Entry("group and kind", definitionFilter{group: "nvidia.com", kind: "Dynamo"}, "nvidia.com, Kind=Dynamo"),
		Entry("all three", definitionFilter{group: "nvidia.com", version: "v1", kind: "Dynamo"},
			"nvidia.com/v1, Kind=Dynamo"),
		Entry("the core group", definitionFilter{group: "", version: "v1", kind: "Pod"}, "/v1, Kind=Pod"),
	)
})

// decodeKartas reads the items the machine formats wrap their result in. yaml
// decodes through json, so one decoder serves both.
func decodeKartas(out string) []v1alpha1.Karta {
	GinkgoHelper()
	var envelope struct {
		Items []v1alpha1.Karta `json:"items"`
		Count int              `json:"count"`
	}
	Expect(yaml.Unmarshal([]byte(out), &envelope)).To(Succeed())
	Expect(envelope.Count).To(Equal(len(envelope.Items)))
	return envelope.Items
}

// decodeKarta reads the definition the machine formats emit on their own for a
// request that named one. yaml decodes through json, so one decoder serves both.
func decodeKarta(out string) v1alpha1.Karta {
	GinkgoHelper()
	var karta v1alpha1.Karta
	Expect(yaml.Unmarshal([]byte(out), &karta)).To(Succeed())
	return karta
}
