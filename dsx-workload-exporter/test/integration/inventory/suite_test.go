// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package inventory runs the Karta definition inventory against a real
// apiserver and etcd started by envtest, so it needs the envtest binaries
// (KUBEBUILDER_ASSETS). Unit tests live next to the code and do not.
package inventory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	eventuallyTimeout  = 10 * time.Second
	eventuallyInterval = 200 * time.Millisecond
)

var (
	testEnv     *envtest.Environment
	adminConfig *rest.Config
	adminClient dynamic.Interface
	crdPaths    = []string{filepath.Join("..", "..", "..", "..", "charts", "karta", "crds")}
	testCtx     context.Context
	testCancel  context.CancelFunc
)

func TestInventory(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Karta Definition Inventory Integration Suite")
}

var _ = BeforeSuite(func() {
	testCtx, testCancel = context.WithCancel(context.Background())

	// Never the kubeconfig's cluster: specs delete every Karta and the CRD.
	testEnv = &envtest.Environment{CRDDirectoryPaths: crdPaths, ErrorIfCRDPathMissing: true, UseExistingCluster: new(bool)}
	apiServer := testEnv.ControlPlane.GetAPIServer()
	// Kubernetes 1.31 serves streaming lists only behind this alpha gate; client-go uses them by default.
	apiServer.Configure().Append("feature-gates", "WatchList=true")
	// A spec restarts the API server: keep its certificates, and stop it without waiting out open watches.
	apiServer.CertDir = GinkgoT().TempDir()
	apiServer.Configure().Append("shutdown-watch-termination-grace-period", "1s")
	apiServer.StopTimeout = time.Minute
	var err error
	adminConfig, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	adminClient, err = dynamic.NewForConfig(adminConfig)
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	if testCancel != nil {
		testCancel()
	}
	if testEnv != nil {
		Expect(testEnv.Stop()).To(Succeed())
	}
})
