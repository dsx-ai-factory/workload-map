// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package flows is the Karta end-to-end recording suite: one Ginkgo file per workload type, each installing
// the type's Karta definition and recording its flows through the recorder.
package flows

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

// Set in BeforeSuite; the recorder gets its setup through the cfg we pass to New.
var (
	k8sClient     client.Client
	testNamespace string
	serverVersion string
	cfg           recorder.Config
)

// recordedData is where recordings are written, relative to the flows package dir (test/e2e/flows).
const recordedData = "../recorded_data"

func TestFlows(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Karta E2E Flows")
}

var _ = BeforeSuite(func(ctx SpecContext) {
	scheme := runtime.NewScheme()
	utilruntime.Must(kartav1alpha1.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))

	restCfg := ctrl.GetConfigOrDie()
	var err error
	k8sClient, err = client.New(restCfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	dynClient, err := dynamic.NewForConfig(restCfg)
	Expect(err).NotTo(HaveOccurred())

	disco, err := discovery.NewDiscoveryClientForConfig(restCfg)
	Expect(err).NotTo(HaveOccurred())
	info, err := disco.ServerVersion()
	Expect(err).NotTo(HaveOccurred())

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "test-"}}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	testNamespace = ns.Name
	DeferCleanup(func(ctx SpecContext) { _ = k8sClient.Delete(ctx, ns) })

	// A builtin type has no operator; the Kubernetes version is what defines its behavior,
	// so its recordings are filed under it (operatorVersion falls back here).
	serverVersion = info.GitVersion
	cfg = recorder.Config{
		Cluster:   recorder.Cluster{Client: k8sClient, Dynamic: dynClient, Namespace: testNamespace},
		OutputDir: recordedData,
		Log:       GinkgoWriter,
	}
})
