//go:build e2e

// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package e2e

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

const (
	tlsRelease    = "exporter-e2e-tls"
	tlsServerName = "dsx-workload-exporter." + tlsRelease + ".svc"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

var _ = Describe("HTTPS scrape", Serial, Ordered, func() {
	var trusted, unrelated *x509.CertPool
	var address string

	BeforeAll(func() {
		By("generating a CA, a serving certificate for the exporter Service and an unrelated CA")
		ca := newTestCA()
		certPEM, keyPEM := ca.servingPair(tlsServerName)
		trusted, unrelated = ca.pool(), newTestCA().pool()

		By("installing an exporter that serves HTTPS from a TLS Secret")
		command(fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %[1]s
---
apiVersion: v1
kind: Secret
metadata:
  name: dsx-exporter-tls
  namespace: %[1]s
type: kubernetes.io/tls
data:
  tls.crt: %[2]s
  tls.key: %[3]s
`, tlsRelease, base64.StdEncoding.EncodeToString(certPEM), base64.StdEncoding.EncodeToString(keyPEM)), "apply", "-f", "-")
		harness := filepath.Join("..", "..", "..", "hack", "e2e", "dsx-workload-exporter")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		out, err := exec.CommandContext(ctx, "bash", filepath.Join(harness, "install.sh"),
			tlsRelease, tlsRelease, filepath.Join(harness, "values.yaml"),
			"--set", "exporter.metrics.delivery.prometheus.tls.existingSecret=dsx-exporter-tls",
			"--set", "exporter.selection.namespaceSelector.matchLabels.exporter-e2e-shard=tls",
			"--set", "exporter.resources.requests.memory=128Mi",
		).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out))

		By("forwarding a local port to the exporter Service")
		address = portForward(tlsRelease)
	})

	It("serves metrics over HTTPS that verify only against the test CA", func() {
		By("checking liveness and readiness over HTTPS")
		for _, path := range []string{"/healthz", "/readyz"} {
			_, err := httpsGet(address, path, trusted)
			Expect(err).NotTo(HaveOccurred())
		}

		By("creating a Pod for the HTTPS exporter to observe")
		submitWorkload("tls")
		uid := strings.TrimSpace(command("", "get", "pod", "scraped", "-n", "exporter-workloads-tls", "-o", "jsonpath={.metadata.uid}"))

		By("scraping /metrics with the test CA")
		eventually("Pod info over HTTPS", func() bool {
			body, err := httpsGet(address, "/metrics", trusted)
			if err != nil {
				fmt.Fprintf(GinkgoWriter, "retrying HTTPS scrape: %v\n", err)
				return false
			}
			parser := expfmt.NewTextParser(model.UTF8Validation)
			fs, err := parser.TextToMetricFamilies(strings.NewReader(body))
			Expect(err).NotTo(HaveOccurred(), "parse metrics")
			return hasSingleGauge(matching(fs, "dsx_workload_pod_info", map[string]string{"pod_uid": uid, "workload_uid": uid}), 1)
		})

		By("rejecting the exporter's certificate when only an unrelated CA is trusted")
		_, err := httpsGet(address, "/metrics", unrelated)
		var unknownAuthority x509.UnknownAuthorityError
		Expect(errors.As(err, &unknownAuthority)).To(BeTrue(), "expected an unknown authority error, got %v", err)
	})
})

// portForward forwards a free local port to the exporter Service in namespace.
func portForward(namespace string) string {
	GinkgoHelper()
	cmd := exec.Command("kubectl", "port-forward", "-n", namespace, "service/dsx-workload-exporter", ":8080")
	stdout, err := cmd.StdoutPipe()
	Expect(err).NotTo(HaveOccurred())
	Expect(cmd.Start()).To(Succeed())
	DeferCleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	output := bufio.NewReader(stdout)
	line, err := output.ReadString('\n')
	Expect(err).NotTo(HaveOccurred())
	var port int
	_, err = fmt.Sscanf(line, "Forwarding from 127.0.0.1:%d ->", &port)
	Expect(err).NotTo(HaveOccurred(), "unexpected port-forward output %q", line)
	// kubectl logs every forwarded connection; keep reading so it never blocks.
	go func() { _, _ = io.Copy(io.Discard, output) }()
	return fmt.Sprintf("127.0.0.1:%d", port)
}

func httpsGet(address, path string, roots *x509.CertPool) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: tlsServerName, MinVersion: tls.VersionTLS12},
	}}
	defer client.CloseIdleConnections()
	resp, err := client.Get("https://" + address + path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err == nil && resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("%s returned HTTP %d", path, resp.StatusCode)
	}
	return string(body), err
}

func newTestCA() testCA {
	GinkgoHelper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "exporter e2e CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	Expect(err).NotTo(HaveOccurred())
	cert, err := x509.ParseCertificate(der)
	Expect(err).NotTo(HaveOccurred())
	return testCA{cert: cert, key: key}
}

func (ca testCA) pool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	return pool
}

func (ca testCA) servingPair(dnsName string) ([]byte, []byte) {
	GinkgoHelper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	Expect(err).NotTo(HaveOccurred())
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	Expect(err).NotTo(HaveOccurred())
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}
