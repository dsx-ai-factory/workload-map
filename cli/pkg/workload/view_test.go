// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package workload

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/definitions"
	"github.com/dsx-ai-factory/workload-map/pkg/catalog"
)

// resolveFixture reads a workload manifest from testdata and resolves it through
// the built-in catalog, the way the get command resolves a live object.
func resolveFixture(name string) (*View, error) {
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	Expect(err).NotTo(HaveOccurred())

	return resolveObject(raw)
}

// resolveObject resolves a manifest through the built-in catalog.
func resolveObject(manifest []byte) (*View, error) {
	obj := &unstructured.Unstructured{}
	Expect(yaml.Unmarshal(manifest, obj)).To(Succeed())

	def, err := definitions.New(catalog.List(), nil).Resolve(obj.GroupVersionKind())
	Expect(err).NotTo(HaveOccurred())

	return Resolve(context.Background(), obj, def)
}

var _ = Describe("Resolve", func() {
	It("carries the root object's identity and the definition that covered it", func() {
		view, err := resolveFixture("pytorchjob.yaml")
		Expect(err).NotTo(HaveOccurred())

		Expect(view.Name).To(Equal("llama-finetune"))
		Expect(view.Namespace).To(Equal("ml-team"))
		Expect(view.Kind).To(Equal("PyTorchJob"))
		Expect(view.APIVersion).To(Equal("kubeflow.org/v1"))
		Expect(view.Definition).To(Equal("kubeflow-org-pytorchjob-v1"))
		Expect(view.Origin).To(Equal(string(definitions.OriginCatalog)))
	})

	// Every kind the catalog covers has to reach a view through the definition
	// that claims it, not just the one the identity assertions above read. None
	// of the fixtures carries a status, so each resolves to Undefined.
	DescribeTable("resolves a catalog workload to a view",
		func(fixture, kind, definition string) {
			view, err := resolveFixture(fixture)
			Expect(err).NotTo(HaveOccurred())
			Expect(view.Kind).To(Equal(kind))
			Expect(view.Definition).To(Equal(definition))
			Expect(view.Phases).To(Equal([]string{undefinedPhase}))
		},
		Entry("Deployment", "deployment.yaml", "Deployment", "apps-deployment-v1"),
		Entry("PyTorchJob", "pytorchjob.yaml", "PyTorchJob", "kubeflow-org-pytorchjob-v1"),
		Entry("InferenceService", "inferenceservice.yaml", "InferenceService", "serving-kserve-io-inferenceservice-v1beta1"),
		Entry("LeaderWorkerSet", "leaderworkerset.yaml", "LeaderWorkerSet", "leaderworkerset-x-k8s-io-leaderworkerset-v1"),
		Entry("Milvus", "milvus.yaml", "Milvus", "milvus-io-milvus-v1beta1"),
		Entry("DynamoGraphDeployment", "dynamographdeployment.yaml", "DynamoGraphDeployment", "nvidia-com-dynamographdeployment-v1alpha1"),
	)
})

var _ = Describe("phase normalization", func() {
	// The Deployment definition maps a Progressing condition with the
	// NewReplicaSetAvailable reason onto Running.
	It("reports the phase its definition maps the status onto", func() {
		view, err := resolveObject([]byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: serve
  namespace: ml-team
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: serve
status:
  conditions:
    - type: Progressing
      status: "True"
      reason: NewReplicaSetAvailable
`))
		Expect(err).NotTo(HaveOccurred())

		Expect(view.Phases).To(ContainElement("Running"))
	})
})
