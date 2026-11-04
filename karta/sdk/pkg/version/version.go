// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package version exposes the shared Karta product version.
package version

import (
	"runtime/debug"
	"strings"
)

const unknownVersion = "unknown"

// Populated at link time by Make and GoReleaser.
var version string

// String returns the build version.
func String() string {
	return resolve(version, debug.ReadBuildInfo)
}

func resolve(explicit string, readBuildInfo func() (*debug.BuildInfo, bool)) string {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return explicit
	}
	if info, ok := readBuildInfo(); ok && info != nil {
		if normalized := normalize(info.Main.Version); normalized != "" {
			return normalized
		}
	}
	return unknownVersion
}

func normalize(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "(devel)" {
		return ""
	}
	return strings.TrimPrefix(value, "v")
}
