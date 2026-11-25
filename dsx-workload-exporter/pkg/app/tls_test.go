// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func TestServerTLSConfigRejectsUnusablePair(t *testing.T) {
	ca := newTestCA(t)
	certFile, keyFile := ca.writeServingPair(t, t.TempDir(), 2)
	_, otherKey := ca.writeServingPair(t, t.TempDir(), 3)
	for name, files := range map[string][2]string{
		"missing certificate": {filepath.Join(t.TempDir(), "tls.crt"), keyFile},
		"missing key":         {certFile, filepath.Join(t.TempDir(), "tls.key")},
		"mismatched key":      {certFile, otherKey},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ServerTLSConfig(files[0], files[1])
			require.ErrorContains(t, err, "load metrics TLS certificate")
		})
	}
}

func TestApplicationServesTLSWithRenewedCertificate(t *testing.T) {
	ca := newTestCA(t)
	dir := t.TempDir()
	certFile, keyFile := ca.writeServingPair(t, dir, 2)
	tlsConfig, err := ServerTLSConfig(certFile, keyFile)
	require.NoError(t, err)

	runtime := &httpTestRuntime{started: make(chan struct{}), stopped: make(chan struct{})}
	application, err := New(Components{Runtime: runtime, Store: store.New(), Definitions: definitionRunner{}})
	require.NoError(t, err)
	application.TLSConfig = tlsConfig
	listener := lifecycleListener(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- application.ServeListener(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("application did not shut down")
		}
	})
	select {
	case <-runtime.started:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not start")
	}

	get := func(scheme string) *http.Response {
		t.Helper()
		client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{RootCAs: ca.pool, MinVersion: tls.VersionTLS12},
			DisableKeepAlives: true,
		}}
		resp, err := client.Get(scheme + "://" + listener.Addr().String() + "/healthz")
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp
	}

	resp := get("https")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.EqualValues(t, 2, resp.TLS.PeerCertificates[0].SerialNumber.Int64())
	require.Equal(t, http.StatusBadRequest, get("http").StatusCode, "plain HTTP must not be served")

	ca.writeServingPair(t, dir, 3)
	resp = get("https")
	require.EqualValues(t, 3, resp.TLS.PeerCertificates[0].SerialNumber.Int64(), "a new connection must use the renewed certificate")
}

func newTestCA(t *testing.T) testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "exporter test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return testCA{cert: cert, key: key, pool: pool}
}

// writeServingPair writes tls.crt and tls.key for 127.0.0.1 into dir.
func (ca testCA) writeServingPair(t *testing.T, dir string, serial int64) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "dsx-workload-exporter"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	certFile, keyFile := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600))
	return certFile, keyFile
}
