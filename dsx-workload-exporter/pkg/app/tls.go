// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package app

import (
	"crypto/tls"
	"fmt"
)

// ServerTLSConfig serves the key pair in certFile and keyFile. The pair is read
// again for each new connection, so a renewed certificate is picked up without
// a restart.
func ServerTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
		return nil, fmt.Errorf("load metrics TLS certificate: %w", err)
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			cert, err := tls.LoadX509KeyPair(certFile, keyFile)
			if err != nil {
				return nil, fmt.Errorf("load metrics TLS certificate: %w", err)
			}
			return &cert, nil
		},
	}, nil
}
