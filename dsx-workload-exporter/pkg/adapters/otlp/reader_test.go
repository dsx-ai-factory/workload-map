// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package otlp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/otlptranslator"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	colmetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"k8s.io/apimachinery/pkg/runtime/schema"

	collector "github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/adapters/otel"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/config"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
)

var protocols = []string{config.ProtocolHTTPProtobuf, config.ProtocolGRPC}

// receiver is an in-process OTLP receiver. While hold is open it answers no
// request, so the exporter's timeout decides.
type receiver struct {
	endpoint string
	mu       sync.Mutex
	requests []*colmetricpb.ExportMetricsServiceRequest
	auth     []string
	hold     chan struct{}
}

type metricsService struct {
	colmetricpb.UnimplementedMetricsServiceServer
	r *receiver
}

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	dir  string
}

func TestExportsTheGaugesAsPrometheusServesThem(t *testing.T) {
	for _, protocol := range protocols {
		t.Run(protocol, func(t *testing.T) {
			g := NewWithT(t)
			s := fixture()
			r := startReceiver(t, protocol, nil)
			provider := push(t, r.config(protocol), s)
			g.Expect(provider.ForceFlush(t.Context())).To(Succeed())
			pushed := r.last()
			g.Expect(pushed).To(HaveKey(collector.MetricPodWorkloadInfo))
			g.Expect(pushed).To(HaveKey(collector.MetricWorkloadStatus))
			g.Expect(pushed).To(HaveKey(collector.MetricWorkloadCreatedTimestamp))
			g.Expect(pushed).To(HaveKey(collector.MetricExporterObservationValid))
			g.Expect(pushed).To(Equal(scrape(t, s)))
		})
	}
}

func TestSendsHeadersFromTheEnvironment(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", "authorization=Bearer%20secret-token")
	for _, protocol := range protocols {
		t.Run(protocol, func(t *testing.T) {
			g := NewWithT(t)
			r := startReceiver(t, protocol, nil)
			g.Expect(push(t, r.config(protocol), fixture()).ForceFlush(t.Context())).To(Succeed())
			g.Expect(r.authorization()).To(ConsistOf("Bearer secret-token"))
		})
	}
}

func TestVerifiesTheReceiverWithACustomCA(t *testing.T) {
	ca := newTestCA(t)
	for _, protocol := range protocols {
		t.Run(protocol, func(t *testing.T) {
			g := NewWithT(t)
			r := startReceiver(t, protocol, &tls.Config{Certificates: []tls.Certificate{ca.pair(t, "receiver")}, MinVersion: tls.VersionTLS12})
			cfg := r.config(protocol)
			g.Expect(push(t, cfg, fixture()).ForceFlush(t.Context())).NotTo(Succeed(), "the system roots must not trust the test CA")

			cfg.TLS.CAFile = ca.file(t)
			g.Expect(push(t, cfg, fixture()).ForceFlush(t.Context())).To(Succeed())
			g.Expect(r.count()).To(Equal(1))
		})
	}
}

func TestPresentsAClientCertificateForMutualTLS(t *testing.T) {
	ca := newTestCA(t)
	for _, protocol := range protocols {
		t.Run(protocol, func(t *testing.T) {
			g := NewWithT(t)
			r := startReceiver(t, protocol, &tls.Config{
				Certificates: []tls.Certificate{ca.pair(t, "receiver")},
				ClientAuth:   tls.RequireAndVerifyClientCert,
				ClientCAs:    ca.pool(),
				MinVersion:   tls.VersionTLS12,
			})
			cfg := r.config(protocol)
			cfg.TLS.CAFile = ca.file(t)
			g.Expect(push(t, cfg, fixture()).ForceFlush(t.Context())).NotTo(Succeed(), "the receiver must require a client certificate")

			cfg.TLS.CertFile, cfg.TLS.KeyFile = ca.files(t, "exporter")
			g.Expect(push(t, cfg, fixture()).ForceFlush(t.Context())).To(Succeed())
			g.Expect(r.count()).To(Equal(1))
		})
	}
}

func TestAbandonsASlowExportAndRecovers(t *testing.T) {
	for _, protocol := range protocols {
		t.Run(protocol, func(t *testing.T) {
			g := NewWithT(t)
			r := startReceiver(t, protocol, nil)
			r.hold = make(chan struct{})
			provider := push(t, r.config(protocol), fixture())

			start := time.Now()
			g.Expect(provider.ForceFlush(t.Context())).NotTo(Succeed())
			g.Expect(time.Since(start)).To(BeNumerically("<", 3*time.Second), "the export must give up at its timeout")

			close(r.hold)
			g.Expect(provider.ForceFlush(t.Context())).To(Succeed(), "the next export must reach the receiver once it answers")
			g.Expect(r.count()).To(BeNumerically(">=", 1))
		})
	}
}

func TestShutdownIsBoundedByTheTimeout(t *testing.T) {
	for _, protocol := range protocols {
		t.Run(protocol, func(t *testing.T) {
			g := NewWithT(t)
			r := startReceiver(t, protocol, nil)
			r.hold = make(chan struct{})
			defer close(r.hold)
			reader, err := NewReader(t.Context(), r.config(protocol))
			g.Expect(err).NotTo(HaveOccurred())
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			registration, err := collector.Register(provider.Meter("test"), fixture(), nil)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(registration.Unregister()).To(Succeed())

			start := time.Now()
			_ = provider.Shutdown(context.Background())
			g.Expect(time.Since(start)).To(BeNumerically("<", 3*time.Second))
		})
	}
}

func TestSuccessfulPushesDoNotRefreshObservationValidity(t *testing.T) {
	g := NewWithT(t)
	s := fixture()
	s.SetSourceHealth(jobSource, store.SourceHealth{GVK: jobGVK, Synced: true, Error: store.ReasonPermissionDenied})
	r := startReceiver(t, config.ProtocolHTTPProtobuf, nil)
	provider := push(t, r.config(config.ProtocolHTTPProtobuf), s)
	for range 2 {
		g.Expect(provider.ForceFlush(t.Context())).To(Succeed())
		pushed := r.last()
		for _, name := range []string{collector.MetricPodWorkloadInfo, collector.MetricWorkloadStatus} {
			g.Expect(pushed[name]).NotTo(BeEmpty())
			for _, point := range pushed[name] {
				g.Expect(point).To(ContainSubstring(`observation_valid="false"`), name)
			}
		}
		g.Expect(pushed[collector.MetricExporterObservationValid]).To(ContainElement(ContainSubstring(`reason="permission_denied"`)))
	}
}

func TestRejectsUnusableClientFiles(t *testing.T) {
	ca := newTestCA(t)
	certFile, keyFile := ca.files(t, "exporter")
	_, otherKey := ca.files(t, "other")
	empty := filepath.Join(t.TempDir(), "empty.crt")
	if err := os.WriteFile(empty, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		files   config.OTLPTLSConfig
		message string
	}{
		"missing CA":       {config.OTLPTLSConfig{CAFile: filepath.Join(t.TempDir(), "ca.crt")}, "read otlp CA"},
		"CA without PEM":   {config.OTLPTLSConfig{CAFile: empty}, "has no PEM certificate"},
		"mismatched key":   {config.OTLPTLSConfig{CertFile: certFile, KeyFile: otherKey}, "load otlp client certificate"},
		"missing key file": {config.OTLPTLSConfig{CertFile: certFile, KeyFile: keyFile + ".missing"}, "load otlp client certificate"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := config.OTLPDeliveryConfig{Protocol: config.ProtocolHTTPProtobuf, Endpoint: "https://127.0.0.1:4318/v1/metrics", Interval: "1h", Timeout: "1s", TLS: tc.files}
			_, err := NewReader(t.Context(), cfg)
			NewWithT(t).Expect(err).To(MatchError(ContainSubstring(tc.message)))
		})
	}
}

var (
	jobGVK    = schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
	jobSource = store.SourceKey{Resource: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, Namespace: "ns"}
)

// fixture holds one Running Job with one Pod, both verified.
func fixture() store.PublicationStore {
	s := store.New()
	root := store.ObjectRef{UID: "root", Namespace: "ns", Name: "job", GVK: jobGVK}
	s.SetSourceHealth(jobSource, store.SourceHealth{GVK: jobGVK, Synced: true})
	s.UpsertWorkload(&store.WorkloadResult{
		Record:   store.WorkloadRecord{UID: root.UID, Ref: root, OwnerResolution: store.Resolved, HasStatus: true, Phases: []string{"Running"}, CreatedTimestamp: 50},
		HasValue: true, Sources: []store.SourceKey{jobSource}, CreationSources: []store.SourceKey{jobSource},
	})
	s.UpsertPod(&store.PodResult{
		Record:   store.PodRecord{UID: "pod", Namespace: "ns", Name: "worker", Workload: root, OwnerResolution: store.Resolved},
		Input:    store.ObjectRef{UID: "pod", Namespace: "ns", Name: "worker", GVK: schema.GroupVersionKind{Version: "v1", Kind: "Pod"}},
		HasValue: true, Sources: []store.SourceKey{jobSource},
	})
	return s
}

// push registers the real collector with a reader for cfg. Exports happen on
// ForceFlush; the interval is long enough that none starts on its own.
func push(t *testing.T, cfg config.OTLPDeliveryConfig, s store.Capturer) *sdkmetric.MeterProvider {
	t.Helper()
	reader, err := NewReader(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithCardinalityLimit(0))
	registration, err := collector.Register(provider.Meter("test"), s, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = registration.Unregister()
		_ = provider.Shutdown(context.Background())
	})
	return provider
}

// scrape collects s through the Prometheus reader /metrics uses, in the same
// form as receiver.last.
func scrape(t *testing.T, s store.Capturer) map[string][]string {
	t.Helper()
	registry := prom.NewRegistry()
	reader, err := promexporter.New(promexporter.WithRegisterer(registry), promexporter.WithTranslationStrategy(otlptranslator.NoTranslation), promexporter.WithoutScopeInfo(), promexporter.WithoutTargetInfo())
	if err != nil {
		t.Fatal(err)
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithCardinalityLimit(0))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	registration, err := collector.Register(provider.Meter("test"), s, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registration.Unregister() }()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	scraped := map[string][]string{}
	for _, family := range families {
		for _, m := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range m.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			scraped[family.GetName()] = append(scraped[family.GetName()], format(labels, m.GetGauge().GetValue()))
		}
	}
	for name := range scraped {
		sort.Strings(scraped[name])
	}
	return scraped
}

func format(labels map[string]string, value float64) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := ""
	for _, key := range keys {
		out += fmt.Sprintf("%s=%q,", key, labels[key])
	}
	return fmt.Sprintf("{%s} %g", out, value)
}

func startReceiver(t *testing.T, protocol string, serverTLS *tls.Config) *receiver {
	t.Helper()
	r := &receiver{}
	scheme := "http"
	if serverTLS != nil {
		scheme = "https"
	}
	switch protocol {
	case config.ProtocolGRPC:
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		var options []grpc.ServerOption
		if serverTLS != nil {
			options = append(options, grpc.Creds(credentials.NewTLS(serverTLS)))
		}
		server := grpc.NewServer(options...)
		colmetricpb.RegisterMetricsServiceServer(server, metricsService{r: r})
		go func() { _ = server.Serve(listener) }()
		t.Cleanup(server.Stop)
		r.endpoint = scheme + "://" + listener.Addr().String()
	default:
		server := httptest.NewUnstartedServer(http.HandlerFunc(r.serveHTTP))
		if serverTLS != nil {
			server.TLS = serverTLS
			server.StartTLS()
		} else {
			server.Start()
		}
		t.Cleanup(server.Close)
		r.endpoint = scheme + "://" + server.Listener.Addr().String() + "/v1/metrics"
	}
	return r
}

func (r *receiver) config(protocol string) config.OTLPDeliveryConfig {
	return config.OTLPDeliveryConfig{Protocol: protocol, Endpoint: r.endpoint, Interval: "1h", Timeout: "500ms"}
}

func (r *receiver) serveHTTP(w http.ResponseWriter, req *http.Request) {
	if r.wait(req.Context()) != nil {
		return
	}
	body, err := io.ReadAll(req.Body)
	request := &colmetricpb.ExportMetricsServiceRequest{}
	if err == nil {
		err = proto.Unmarshal(body, request)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	r.record(request, req.Header.Get("Authorization"))
	response, _ := proto.Marshal(&colmetricpb.ExportMetricsServiceResponse{})
	w.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = w.Write(response)
}

func (s metricsService) Export(ctx context.Context, request *colmetricpb.ExportMetricsServiceRequest) (*colmetricpb.ExportMetricsServiceResponse, error) {
	if err := s.r.wait(ctx); err != nil {
		return nil, err
	}
	md, _ := metadata.FromIncomingContext(ctx)
	s.r.record(request, slices.Concat(md.Get("authorization"), []string{""})[0])
	return &colmetricpb.ExportMetricsServiceResponse{}, nil
}

func (r *receiver) wait(ctx context.Context) error {
	if r.hold == nil {
		return nil
	}
	select {
	case <-r.hold:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *receiver) record(request *colmetricpb.ExportMetricsServiceRequest, authorization string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
	if authorization != "" {
		r.auth = append(r.auth, authorization)
	}
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *receiver) authorization() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.auth)
}

// last flattens the latest request into metric name to sorted points.
func (r *receiver) last() map[string][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) == 0 {
		return nil
	}
	pushed := map[string][]string{}
	for _, rm := range r.requests[len(r.requests)-1].GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				for _, point := range m.GetGauge().GetDataPoints() {
					labels := map[string]string{}
					for _, kv := range point.GetAttributes() {
						labels[kv.GetKey()] = kv.GetValue().GetStringValue()
					}
					pushed[m.GetName()] = append(pushed[m.GetName()], format(labels, point.GetAsDouble()))
				}
			}
		}
	}
	for name := range pushed {
		sort.Strings(pushed[name])
	}
	return pushed
}

func newTestCA(t *testing.T) testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "otlp test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return testCA{cert: cert, key: key, dir: t.TempDir()}
}

func (ca testCA) pool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	return pool
}

func (ca testCA) file(t *testing.T) string {
	t.Helper()
	path := filepath.Join(ca.dir, "ca.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// files writes a certificate for 127.0.0.1, usable by either side, and its key.
func (ca testCA) files(t *testing.T, name string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: name},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile := filepath.Join(ca.dir, name+".crt"), filepath.Join(ca.dir, name+".key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func (ca testCA) pair(t *testing.T, name string) tls.Certificate {
	t.Helper()
	certFile, keyFile := ca.files(t, name)
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	return pair
}
