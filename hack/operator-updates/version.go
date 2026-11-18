// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"cmp"
	"fmt"
	"regexp"
	"strings"
)

var (
	semverPattern  = regexp.MustCompile(`\A(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?\z`)
	alphaPattern   = regexp.MustCompile(`\A[0-9]+\.[0-9]+\.[0-9]+-alpha\.[0-9]+\z`)
	grovePattern   = regexp.MustCompile(`\A([0-9]+\.[0-9]+\.[0-9]+-alpha\.[0-9]+)(?:-rc([0-9]+))?\z`)
	counterPattern = regexp.MustCompile(`-(alpha|beta|rc)([0-9]+)\z`)
)

type version struct {
	core       [3]string
	prerelease []string
}

func parseRelease(raw, channel string) (version, error) {
	// Grove promotes alpha.N-rcM to alpha.N, so rcM must sort below N.
	if channel == "alpha" {
		if match := grovePattern.FindStringSubmatch(raw); match != nil {
			raw = match[1] + ".1"
			if match[2] != "" {
				raw = match[1] + ".0.rc." + match[2]
			}
		}
	}
	// Upstream release scripts increment undotted alphaN, betaN and rcN numerically.
	raw = counterPattern.ReplaceAllString(raw, "-${1}.${2}")
	match := semverPattern.FindStringSubmatch(raw)
	if match == nil {
		return version{}, fmt.Errorf("invalid semantic version: %s", raw)
	}
	v := version{core: [3]string{match[1], match[2], match[3]}}
	if match[4] != "" {
		v.prerelease = strings.Split(match[4], ".")
		for _, part := range v.prerelease {
			if isNumeric(part) && len(part) > 1 && part[0] == '0' {
				return version{}, fmt.Errorf("invalid numeric prerelease: %s", raw)
			}
		}
	}
	return v, nil
}

func (v version) compare(other version) int {
	for i, part := range v.core {
		if order := compareNumeric(part, other.core[i]); order != 0 {
			return order
		}
	}
	if len(v.prerelease) == 0 || len(other.prerelease) == 0 {
		return cmp.Compare(len(other.prerelease), len(v.prerelease))
	}
	for i := range min(len(v.prerelease), len(other.prerelease)) {
		left, right := v.prerelease[i], other.prerelease[i]
		var order int
		switch {
		case isNumeric(left) && isNumeric(right):
			order = compareNumeric(left, right)
		case isNumeric(left):
			order = -1
		case isNumeric(right):
			order = 1
		default:
			order = strings.Compare(left, right)
		}
		if order != 0 {
			return order
		}
	}
	return cmp.Compare(len(v.prerelease), len(other.prerelease))
}

func compareNumeric(left, right string) int {
	// Canonical decimal strings preserve SemVer's unbounded numeric identifiers.
	if order := cmp.Compare(len(left), len(right)); order != 0 {
		return order
	}
	return strings.Compare(left, right)
}

func isNumeric(value string) bool {
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return value != ""
}
