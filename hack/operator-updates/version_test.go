// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import "testing"

func TestVersionPrecedence(t *testing.T) {
	for _, tt := range []struct {
		name, channel string
		ordered       []string
	}{
		{"semver", "stable", []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0"}},
		{"undotted", "stable", []string{"1.0.0-alpha9", "1.0.0-alpha10", "1.0.0-beta9", "1.0.0-beta10", "1.0.0-rc9", "1.0.0-rc10", "1.0.0"}},
		{"grove", "alpha", []string{"0.1.0-alpha.9", "0.1.0-alpha.10-rc2", "0.1.0-alpha.10-rc10", "0.1.0-alpha.10", "0.1.0-alpha.11-rc1", "0.1.0-alpha.11", "0.1.0"}},
		{"large_identifiers", "stable", []string{"1.0.0-99999999999999999999999999999", "1.0.0-100000000000000000000000000000", "1.0.0-a", "1.0.0", "1.0.99999999999999999999999999999", "1.0.100000000000000000000000000000", "1.99999999999999999999999999999.0", "1.100000000000000000000000000000.0", "99999999999999999999999999999.0.0", "100000000000000000000000000000.0.0"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for i, left := range tt.ordered {
				a, err := parseRelease(left, tt.channel)
				if err != nil {
					t.Fatal(err)
				}
				for j, right := range tt.ordered {
					b, err := parseRelease(right, tt.channel)
					if err != nil {
						t.Fatal(err)
					}
					got := a.compare(b)
					if (i < j && got != -1) || (i == j && got != 0) || (i > j && got != 1) {
						t.Errorf("compare(%q, %q) = %d, want order of %d and %d", left, right, got, i, j)
					}
				}
			}
		})
	}
}

func TestEquivalentVersions(t *testing.T) {
	for _, pair := range [][2]string{{"1.2.3+build.01", "1.2.3+other"}, {"1.2.3-rc10", "1.2.3-rc.10"}, {"1.2.3-beta10", "1.2.3-beta.10"}, {"1.2.3-alpha10", "1.2.3-alpha.10"}} {
		a, err := parseRelease(pair[0], "stable")
		if err != nil {
			t.Fatal(err)
		}
		b, err := parseRelease(pair[1], "stable")
		if err != nil {
			t.Fatal(err)
		}
		if got := a.compare(b); got != 0 {
			t.Errorf("compare(%q, %q) = %d, want 0", pair[0], pair[1], got)
		}
	}
}

func TestInvalidVersions(t *testing.T) {
	for _, raw := range []string{"", "latest", "v1.2.3", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03", "-1.2.3", "1.2.3-01", "1.2.3-a.01", "1.2.3-rc01", "1.2.3-", "1.2.3+", "1.2.3-a..b", "1.2.3+a..b", "1.2.3/a", "1.2.3\n", "1.2.3-\u0661", "1.2.3-alpha.01", "1.2.3-alpha.1-rc01"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := parseRelease(raw, "alpha"); err == nil {
				t.Errorf("parseRelease(%q) succeeded, want invalid version", raw)
			}
		})
	}
}

func FuzzReleaseComparison(f *testing.F) {
	for _, seed := range [][2]string{{"1.0.0", "1.1.0"}, {"1.0.0-alpha.10-rc2", "1.0.0-alpha.10"}, {"1.0.0-rc9", "1.0.0-rc10"}, {"1.0.0+build", "1.0.0+other"}, {"99999999999999999999999999999.0.0", "100000000000000000000000000000.0.0"}} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, left, right string) {
		a, err := parseRelease(left, "alpha")
		if err != nil {
			return
		}
		b, err := parseRelease(right, "alpha")
		if err != nil {
			return
		}
		if a.compare(a) != 0 || b.compare(b) != 0 || a.compare(b) != -b.compare(a) {
			t.Errorf("comparison is not reflexive and antisymmetric for %q and %q", left, right)
		}
	})
}
