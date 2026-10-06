// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// installKarta applies a Karta definition and waits for it to reconcile Ready. The definition is deleted
// after the spec tree that installed it. This is test setup - the recorder never installs Karta.
func installKarta(ctx context.Context, kartaFile, kartaName string) {
	karta := &kartav1alpha1.Karta{}
	Expect(yaml.Unmarshal(readE2E(kartaFile), karta)).To(Succeed())
	if karta.GetName() == "" {
		karta.SetName(kartaName) // some bundled definitions omit metadata.name
	}
	Expect(k8sClient.Create(ctx, karta)).To(Succeed())
	DeferCleanup(func(ctx SpecContext) { _ = k8sClient.Delete(ctx, karta) })

	Eventually(func(g Gomega) {
		got := &kartav1alpha1.Karta{}
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: karta.GetName()}, got)).To(Succeed())
		g.Expect(apimeta.IsStatusConditionTrue(got.Status.Conditions, "Ready")).To(BeTrue(), "Ready")
	}, time.Minute, 2*time.Second).Should(Succeed())
}

// readE2E reads a path relative to test/e2e (the flows package runs from test/e2e/flows).
func readE2E(path string) []byte {
	b, err := os.ReadFile(filepath.Join("..", path))
	Expect(err).NotTo(HaveOccurred(), "read %s", path)
	return b
}

// operatorVersion returns op's installed version from hack/e2e's per-cluster
// .installed-versions-<cluster> file, or the cluster's Kubernetes version for built-in types no operator
// provides. Per cluster so parallel clusters with different operator versions cannot mix.
func operatorVersion(op string) string {
	cluster := os.Getenv("CLUSTER_NAME")
	if cluster == "" {
		cluster = "karta-e2e"
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "hack", "e2e", "operators", ".installed-versions-"+cluster))
	if err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == op {
				return strings.TrimSpace(v)
			}
		}
	}
	return serverVersion
}

// ensureSecret creates an opaque Secret in the flow's namespace for a workload that references it. The
// k8s-nim-operator reads a NIMService's authSecret from the NIMService's own namespace, but up.sh only
// creates the dummy ngc-secret in default, so a flow recording in a throwaway namespace must seed its own.
// Deleted after the spec tree that created it.
func ensureSecret(ctx context.Context, name string, data map[string]string) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		StringData: data,
	}
	Expect(k8sClient.Create(ctx, sec)).To(Succeed(), "create secret %s", name)
	DeferCleanup(func(ctx SpecContext) { _ = k8sClient.Delete(ctx, sec) })
}
