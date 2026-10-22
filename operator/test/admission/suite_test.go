// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package admission tests the chart's submitter admission policies against a
// real apiserver (envtest). The chart is rendered with the helm binary, and the
// apiserver must serve MutatingAdmissionPolicy v1 (Kubernetes 1.36+).
package admission

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	eventuallyTimeout  = 15 * time.Second
	eventuallyInterval = 250 * time.Millisecond
	submittedBy        = "karta.run.ai/submitted-by"
	serviceAccountUser = "system:serviceaccount:team:runner"
)

var (
	chartDir = filepath.Join("..", "..", "..", "charts", "karta")

	testEnv     *envtest.Environment
	testCtx     context.Context
	testCancel  context.CancelFunc
	adminClient client.Client
	// userClients maps a username to a client authenticated as that user.
	userClients = map[string]client.Client{}

	// policyObjects are every object the submitter templates can render, taken
	// from a render with every policy on, so an install can delete the ones a
	// new render drops, as helm upgrade does.
	policyObjects []*unstructured.Unstructured
)

// allPoliciesValues turns on every submitter policy.
const allPoliciesValues = `
admissionPolicies:
  submitter:
    enabled: true
    immutable: true
    apiVersion: v1
    resources: [{apiGroups: [batch], apiVersions: [v1], resources: [jobs]}]
`

func TestAdmission(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Karta Submitter Admission Suite")
}

var _ = BeforeSuite(func() {
	testCtx, testCancel = context.WithCancel(context.Background())

	testEnv = &envtest.Environment{}
	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	policyObjects, err = renderPolicies(allPoliciesValues)
	Expect(err).NotTo(HaveOccurred())
	Expect(policyObjects).To(HaveLen(6))

	adminClient, err = client.New(cfg, client.Options{Scheme: clientgoscheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	// system:masters skips RBAC setup; admission policies still apply to it.
	for _, name := range []string{"alice", "bob", serviceAccountUser} {
		user, err := testEnv.AddUser(envtest.User{Name: name, Groups: []string{"system:masters"}}, nil)
		Expect(err).NotTo(HaveOccurred())
		userClients[name], err = client.New(user.Config(), client.Options{Scheme: clientgoscheme.Scheme})
		Expect(err).NotTo(HaveOccurred())
	}
})

var _ = AfterSuite(func() {
	if testCancel != nil {
		testCancel()
	}
	if testEnv != nil {
		Expect(testEnv.Stop()).To(Succeed())
	}
})

// render runs helm template with the given values and returns the rendered
// objects, or helm's error output.
func render(values string, args ...string) ([]*unstructured.Unstructured, error) {
	valuesFile := filepath.Join(GinkgoT().TempDir(), "values.yaml")
	if err := os.WriteFile(valuesFile, []byte(values), 0o600); err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("helm", append([]string{"template", "karta", chartDir, "-f", valuesFile}, args...)...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, errors.New(stderr.String())
	}

	var objs []*unstructured.Unstructured
	decoder := utilyaml.NewYAMLOrJSONDecoder(&stdout, 4096)
	for {
		obj := &unstructured.Unstructured{}
		err := decoder.Decode(&obj.Object)
		if errors.Is(err, io.EOF) {
			return objs, nil
		}
		if err != nil {
			return nil, err
		}
		if obj.Object != nil {
			objs = append(objs, obj)
		}
	}
}

// renderPolicies renders values and keeps only the admission policies and
// their bindings.
func renderPolicies(values string, args ...string) ([]*unstructured.Unstructured, error) {
	objs, err := render(values, args...)
	if err != nil {
		return nil, err
	}
	var policies []*unstructured.Unstructured
	for _, obj := range objs {
		if strings.HasSuffix(obj.GetKind(), "AdmissionPolicy") || strings.HasSuffix(obj.GetKind(), "AdmissionPolicyBinding") {
			policies = append(policies, obj)
		}
	}
	return policies, nil
}

// install applies the admission policies rendered from values and deletes the
// ones the render no longer produces, mirroring helm install and upgrade.
func install(values string) {
	objs, err := renderPolicies(values)
	Expect(err).NotTo(HaveOccurred())

	rendered := map[string]bool{}
	for _, obj := range objs {
		rendered[obj.GetKind()+"/"+obj.GetName()] = true
		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(obj.GroupVersionKind())
		err := adminClient.Get(testCtx, client.ObjectKeyFromObject(obj), existing)
		switch {
		case apierrors.IsNotFound(err):
			Expect(adminClient.Create(testCtx, obj)).To(Succeed())
		case err == nil:
			obj.SetResourceVersion(existing.GetResourceVersion())
			Expect(adminClient.Update(testCtx, obj)).To(Succeed())
		default:
			Expect(err).NotTo(HaveOccurred())
		}
	}

	for _, p := range policyObjects {
		if rendered[p.GetKind()+"/"+p.GetName()] {
			continue
		}
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(p.GroupVersionKind())
		obj.SetName(p.GetName())
		Expect(client.IgnoreNotFound(adminClient.Delete(testCtx, obj))).To(Succeed())
	}
}
