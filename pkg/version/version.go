// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package version exposes the build version stamped into karta binaries at link
// time via -ldflags "-X github.com/dsx-ai-factory/workload-map/pkg/version.version=<value>".
// Both the CLI and the operator import this package so they can never report
// different versions.
package version

// Populated at link time. The default applies to a plain `go build`.
var version = "unknown"

// String returns the build version.
func String() string { return version }
