// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package otlp pushes the collector's gauges to an OTLP receiver.
package otlp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"google.golang.org/grpc/credentials"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/config"
)

// NewReader exports to the receiver in cfg once per interval. Each export,
// including its retries, is abandoned after the timeout and tried again on the
// next interval. Headers come from OTEL_EXPORTER_OTLP_METRICS_HEADERS or
// OTEL_EXPORTER_OTLP_HEADERS.
func NewReader(ctx context.Context, cfg config.OTLPDeliveryConfig) (sdkmetric.Reader, error) {
	interval, err := time.ParseDuration(cfg.Interval)
	if err != nil {
		return nil, fmt.Errorf("otlp interval: %w", err)
	}
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("otlp timeout: %w", err)
	}
	tlsConfig, err := clientTLSConfig(cfg.TLS)
	if err != nil {
		return nil, err
	}
	var exporter sdkmetric.Exporter
	switch cfg.Protocol {
	case config.ProtocolGRPC:
		options := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpointURL(cfg.Endpoint), otlpmetricgrpc.WithTimeout(timeout)}
		if tlsConfig != nil {
			options = append(options, otlpmetricgrpc.WithTLSCredentials(credentials.NewTLS(tlsConfig)))
		}
		exporter, err = otlpmetricgrpc.New(ctx, options...)
	case config.ProtocolHTTPProtobuf:
		options := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpointURL(cfg.Endpoint), otlpmetrichttp.WithTimeout(timeout)}
		if tlsConfig != nil {
			options = append(options, otlpmetrichttp.WithTLSClientConfig(tlsConfig))
		}
		exporter, err = otlpmetrichttp.New(ctx, options...)
	default:
		return nil, fmt.Errorf("unknown otlp protocol %q", cfg.Protocol)
	}
	if err != nil {
		return nil, fmt.Errorf("create %s exporter: %w", cfg.Protocol, err)
	}
	return sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(interval), sdkmetric.WithTimeout(timeout)), nil
}

// clientTLSConfig returns nil when no files are set, keeping the system roots.
// The client certificate is read for each handshake, so a renewed one is used
// on the next connection.
func clientTLSConfig(files config.OTLPTLSConfig) (*tls.Config, error) {
	if files == (config.OTLPTLSConfig{}) {
		return nil, nil
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if files.CAFile != "" {
		ca, err := os.ReadFile(files.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read otlp CA: %w", err)
		}
		tlsConfig.RootCAs = x509.NewCertPool()
		if !tlsConfig.RootCAs.AppendCertsFromPEM(ca) {
			return nil, fmt.Errorf("otlp CA %s has no PEM certificate", files.CAFile)
		}
	}
	if files.CertFile != "" {
		if _, err := tls.LoadX509KeyPair(files.CertFile, files.KeyFile); err != nil {
			return nil, fmt.Errorf("load otlp client certificate: %w", err)
		}
		tlsConfig.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			cert, err := tls.LoadX509KeyPair(files.CertFile, files.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("load otlp client certificate: %w", err)
			}
			return &cert, nil
		}
	}
	return tlsConfig, nil
}
