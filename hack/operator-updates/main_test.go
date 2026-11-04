// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseArgs(t *testing.T) {
	first := operator{name: "one", pinned: "1.0.0", repository: "owner/one", prefix: "v", channel: "stable"}
	second := operator{name: "two", pinned: "0.1.0-alpha.1", repository: "owner/two", channel: "alpha", note: "release policy"}
	for _, tt := range []struct {
		name string
		args []string
		want options
	}{
		{
			name: "default options",
			args: []string{"--operator", "one", "1.0.0", "owner/one", "v", "stable", ""},
			want: options{operators: []operator{first}},
		},
		{
			name: "flags around repeated operators",
			args: []string{
				"--include-prereleases", "--exclude-tag", "v2.0.0",
				"--operator", "one", "1.0.0", "owner/one", "v", "stable", "",
				"--fail-on-update", "--exclude-tag=v2.1.0",
				"--operator", "two", "0.1.0-alpha.1", "owner/two", "", "alpha", "release policy",
				"--no-header",
			},
			want: options{
				operators: []operator{first, second}, excludedTags: []string{"v2.0.0", "v2.1.0"},
				includePrereleases: true, failOnUpdate: true, noHeader: true,
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseArgs(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseArgs() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCheckOperator(t *testing.T) {
	tests := []struct {
		name       string
		pinned     string
		prefix     string
		channel    string
		tags       []string
		prerelease bool
		excluded   []string
		status     string
		latest     string
		contains   []string
		absent     []string
	}{
		{
			name: "numeric_versions", tags: []string{"v1.9.0", "v1.9.2", "v1.10.0", "v1.8.99"},
			status: "NEW", latest: "v1.10.0", contains: []string{"2 newer tag(s)", "/compare/v1.9.0...v1.10.0"},
		},
		{
			name: "unordered_tags", tags: []string{"v2.0.0", "v1.10.0", "v1.9.1"},
			status: "NEW", latest: "v2.0.0",
		},
		{
			name: "stable_channel", tags: []string{"v1.9.0", "v1.10.0-rc.1", "v9.0.0-dev"},
			status: "CURRENT", latest: "v1.9.0", absent: []string{"/compare/"},
		},
		{
			name: "dotted_candidates", tags: []string{"v1.10.0-rc.2", "v1.10.0-rc.10", "v1.9.0"},
			prerelease: true, status: "NEW", latest: "v1.10.0-rc.10",
		},
		{
			name: "undotted_candidates", pinned: "v1.6.0-rc9", tags: []string{"v1.6.0-rc9", "v1.6.0-rc10"},
			prerelease: true, status: "NEW", latest: "v1.6.0-rc10", contains: []string{"1 newer tag(s)"},
		},
		{
			name: "undotted_promotion", pinned: "v1.6.0-rc9", tags: []string{"v1.6.0-rc9", "v1.6.0-rc10", "v1.6.0"},
			prerelease: true, status: "NEW", latest: "v1.6.0", contains: []string{"2 newer tag(s)"},
		},
		{
			name: "grove_alpha", pinned: "v0.1.0-alpha.1", channel: "alpha", tags: []string{"v0.1.0-alpha.1", "v0.1.0-alpha.2"},
			status: "NEW", latest: "v0.1.0-alpha.2",
		},
		{
			name: "grove_excludes_candidates", pinned: "v0.1.0-alpha.10-rc1", channel: "alpha",
			tags: []string{"v0.1.0-alpha.10", "v0.1.0-alpha.11-rc1"}, status: "NEW", latest: "v0.1.0-alpha.10",
		},
		{
			name: "grove_promotion", pinned: "v0.1.0-alpha.10-rc2", channel: "alpha", prerelease: true,
			tags:   []string{"v0.1.0-alpha.9", "v0.1.0-alpha.10-rc2", "v0.1.0-alpha.10-rc10", "v0.1.0-alpha.10"},
			status: "NEW", latest: "v0.1.0-alpha.10", contains: []string{"2 newer tag(s)"},
		},
		{
			name: "grove_candidate_order", pinned: "v0.1.0-alpha.10-rc2", channel: "alpha", prerelease: true,
			tags:   []string{"v0.1.0-alpha.9", "v0.1.0-alpha.10-rc2", "v0.1.0-alpha.10-rc10"},
			status: "NEW", latest: "v0.1.0-alpha.10-rc10",
		},
		{
			name: "grove_final", pinned: "v0.1.0-alpha.10", channel: "alpha", tags: []string{"v0.1.0-alpha.10", "v0.1.0"},
			status: "NEW", latest: "v0.1.0",
		},
		{
			name: "missing_pin", tags: []string{"v1.10.0"}, status: "NEW", latest: "v1.10.0",
			contains: []string{"Pinned tag v1.9.0 was not found"}, absent: []string{"/compare/"},
		},
		{
			name: "retracted_tags", pinned: "v0.15.2", tags: []string{"v0.15.2", "v0.20.0", "v0.20.1"},
			excluded: []string{"v0.20.0", "v0.20.1"}, status: "CURRENT", latest: "v0.15.2",
		},
		{
			name: "final_after_candidate", pinned: "v1.9.0-rc.9", tags: []string{"v1.9.0-rc.9", "v1.9.0"},
			prerelease: true, status: "NEW", latest: "v1.9.0",
		},
		{
			name: "milvus_namespace", pinned: "1.3.7", prefix: "milvus-operator-",
			tags:   []string{"v9.0.0", "config-tool-v9.0.0", "milvus-operator-1.3.7", "milvus-operator-1.3.11"},
			status: "NEW", latest: "milvus-operator-1.3.11",
		},
		{
			name: "kuberay_namespace", pinned: "1.4.0", prefix: "kuberay-operator-",
			tags:   []string{"ray-operator-9.0.0", "v9.0.0", "kuberay-operator-1.4.0", "kuberay-operator-1.5.0"},
			status: "NEW", latest: "kuberay-operator-1.5.0",
		},
		{
			name: "knative_namespace", pinned: "knative-v1.15.2", prefix: "knative-v",
			tags:   []string{"v9.0.0", "v0.50.0", "knative-v1.15.2", "knative-v1.23.0"},
			status: "NEW", latest: "knative-v1.23.0",
		},
		{
			name: "build_metadata", pinned: "1.9.0+build.1", tags: []string{"v1.9.0+build.99"},
			status: "CURRENT", latest: "v1.9.0+build.99", absent: []string{"/compare/"},
		},
		{
			name: "deterministic_tie", tags: []string{"v1.10.0+build.9", "v1.10.0+build.10"},
			status: "NEW", latest: "v1.10.0+build.9", contains: []string{"2 newer tag(s)"},
		},
		{
			name: "encoded_build_metadata", pinned: "v1.9.0+build.1", tags: []string{"v1.9.0+build.1", "v1.10.0+build.2"},
			status: "NEW", latest: "v1.10.0+build.2", contains: []string{"/compare/v1.9.0%2Bbuild.1...v1.10.0%2Bbuild.2"},
		},
		{
			name: "ahead", tags: []string{"v1.8.9"}, status: "AHEAD", latest: "v1.8.9",
		},
		{
			name: "invalid_tags", tags: []string{"v1.9.0", "v2.0", "v02.0.0", "v2.0.0-01", "v2.0.0^{}", "v2.0.0/extra", "nightly"},
			prerelease: true, status: "CURRENT", latest: "v1.9.0",
		},
		{
			name: "unbounded_numeric_version", pinned: "999999999999999999999999999999.0.0",
			tags:   []string{"v1000000000000000000000000000000.0.0", "v999999999999999999999999999999.0.0"},
			status: "NEW", latest: "v1000000000000000000000000000000.0.0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := operator{name: "example", pinned: tt.pinned, repository: "owner/operator", prefix: tt.prefix, channel: tt.channel, note: "release policy"}
			if op.pinned == "" {
				op.pinned = "1.9.0"
			}
			if op.prefix == "" {
				op.prefix = "v"
			}
			if op.channel == "" {
				op.channel = "stable"
			}
			var output bytes.Buffer
			lookup := func(context.Context, string) ([]string, error) { return tt.tags, nil }
			changed, err := checkOperator(t.Context(), op, options{includePrereleases: tt.prerelease, excludedTags: tt.excluded}, &output, lookup)
			if err != nil {
				t.Fatal(err)
			}
			if changed != (tt.status == "NEW") {
				t.Errorf("checkOperator(%v) changed = %v, want %v", tt.tags, changed, tt.status == "NEW")
			}
			row, _, _ := strings.Cut(output.String(), "\n")
			want := []string{tt.status, "example", op.pinned, tt.latest}
			if got := strings.Fields(row); !slices.Equal(got, want) {
				t.Errorf("checkOperator(%v) row = %v, want %v", tt.tags, got, want)
			}
			for _, fragment := range append(tt.contains, "  Tags: https://github.com/owner/operator/tags", "  Note: release policy") {
				if !strings.Contains(output.String(), fragment) {
					t.Errorf("checkOperator(%v) output = %q, want %q", tt.tags, output.String(), fragment)
				}
			}
			for _, fragment := range tt.absent {
				if strings.Contains(output.String(), fragment) {
					t.Errorf("checkOperator(%v) output = %q, unwanted %q", tt.tags, output.String(), fragment)
				}
			}
		})
	}
}

func TestInstallerPinPrefixes(t *testing.T) {
	for _, tt := range []struct{ pinned, prefix, tag string }{
		{"1.2.3", "v", "v1.2.3"}, {"v1.2.3", "v", "v1.2.3"},
		{"knative-v1.2.3", "knative-v", "knative-v1.2.3"},
		{"1.2.3", "milvus-operator-", "milvus-operator-1.2.3"},
		{"milvus-operator-1.2.3", "milvus-operator-", "milvus-operator-1.2.3"},
		{"1.2.3", "", "1.2.3"},
	} {
		t.Run(tt.pinned+"_"+tt.prefix, func(t *testing.T) {
			op := operator{name: "example", pinned: tt.pinned, repository: "owner/operator", prefix: tt.prefix, channel: "stable"}
			lookup := func(context.Context, string) ([]string, error) { return []string{tt.tag, tt.prefix + "1.3.0"}, nil }
			var output bytes.Buffer
			if _, err := checkOperator(t.Context(), op, options{}, &output, lookup); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), "/compare/"+tt.tag+"...") {
				t.Errorf("checkOperator(%q, %q) output = %q, want comparison with %s", tt.pinned, tt.prefix, output.String(), tt.tag)
			}
		})
	}
}

func TestInvalidOperatorDoesNotLookup(t *testing.T) {
	for _, op := range []operator{
		{pinned: "latest", repository: "owner/operator", channel: "stable"},
		{pinned: "v1.2", repository: "owner/operator", channel: "stable"},
		{pinned: "1.02.3", repository: "owner/operator", channel: "stable"},
		{pinned: "1.2.3-01", repository: "owner/operator", channel: "stable"},
		{pinned: "1.2.3", repository: "https://github.com/owner/operator", channel: "stable"},
		{pinned: "1.2.3", repository: "owner/operator/extra", channel: "stable"},
		{pinned: "1.2.3", repository: "owner/operator", channel: "unknown"},
	} {
		t.Run(op.pinned+"_"+op.repository+"_"+op.channel, func(t *testing.T) {
			lookup := func(context.Context, string) ([]string, error) {
				t.Error("lookup called for invalid operator")
				return nil, nil
			}
			if _, err := checkOperator(t.Context(), op, options{}, io.Discard, lookup); err == nil {
				t.Errorf("checkOperator(%+v) succeeded, want validation error", op)
			}
		})
	}
}

func TestNoMatchingTags(t *testing.T) {
	for _, tags := range [][]string{nil, {"other-2.0.0"}, {"v2.0.0-rc.1"}} {
		op := operator{pinned: "1.0.0", repository: "owner/operator", prefix: "v", channel: "stable"}
		lookup := func(context.Context, string) ([]string, error) { return tags, nil }
		if _, err := checkOperator(t.Context(), op, options{}, io.Discard, lookup); err == nil || !strings.Contains(err.Error(), "no matching stable tags") {
			t.Errorf("checkOperator(%v) error = %v, want no matching stable tags", tags, err)
		}
	}
}

func TestRunReportsVersions(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		tags []string
		want int
	}{
		{name: "advisory", tags: []string{"v2.0.0"}},
		{name: "fail_on_update", args: []string{"--fail-on-update"}, tags: []string{"v2.0.0"}, want: 2},
		{name: "current", args: []string{"--fail-on-update"}, tags: []string{"v1.0.0"}},
		{name: "no_header", args: []string{"--no-header"}, tags: []string{"v1.0.0"}},
		{name: "exclude", args: []string{"--fail-on-update", "--exclude-tag", "v2.0.0", "--exclude-tag=v3.0.0"}, tags: []string{"v1.0.0", "v2.0.0", "v3.0.0"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"--operator", "example", "1.0.0", "owner/operator", "v", "stable", ""}, tt.args...)
			lookup := func(context.Context, string) ([]string, error) { return tt.tags, nil }
			var stdout, stderr bytes.Buffer
			if got := run(t.Context(), args, &stdout, &stderr, lookup); got != tt.want {
				t.Errorf("run(%v) = %d, want %d; stderr: %s", args, got, tt.want, &stderr)
			}
			if stderr.Len() != 0 {
				t.Errorf("run(%v) stderr = %q, want empty", args, stderr.String())
			}
			if got, want := strings.HasPrefix(stdout.String(), header), !slices.Contains(tt.args, "--no-header"); got != want {
				t.Errorf("run(%v) has header = %v, want %v", args, got, want)
			}
		})
	}
}

func TestRunContinuesAfterError(t *testing.T) {
	for _, failedRepository := range []string{"owner/one", "owner/two"} {
		t.Run(failedRepository, func(t *testing.T) {
			var called []string
			lookup := func(_ context.Context, repository string) ([]string, error) {
				called = append(called, repository)
				if repository == failedRepository {
					return nil, errors.New("lookup failed")
				}
				return []string{"v2.0.0"}, nil
			}
			args := []string{"--operator", "one", "1.0.0", "owner/one", "v", "stable", "", "--operator", "two", "1.0.0", "owner/two", "v", "stable", "", "--fail-on-update"}
			var stdout, stderr bytes.Buffer
			if got := run(t.Context(), args, &stdout, &stderr, lookup); got != 1 {
				t.Errorf("run() = %d, want 1", got)
			}
			if !slices.Equal(called, []string{"owner/one", "owner/two"}) {
				t.Errorf("run() lookups = %v, want both repositories", called)
			}
			if !strings.Contains(stdout.String(), "ERROR") || !strings.Contains(stdout.String(), "NEW") || !strings.Contains(stderr.String(), "lookup failed") {
				t.Errorf("run() stdout = %q, stderr = %q, want both results and lookup error", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunInvalidArguments(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
	}{
		{name: "missing operator"},
		{name: "incomplete operator", args: []string{"--operator", "example"}},
		{
			name: "flag instead of operator value",
			args: []string{"--operator", "name", "1.0.0", "owner/repo", "v", "stable", "--fail-on-update"},
		},
		{name: "missing excluded tag", args: []string{"--exclude-tag"}},
		{name: "unknown flag", args: []string{"--unsupported"}},
		{
			name: "unknown flag after operator",
			args: []string{"--operator", "name", "1.0.0", "owner/repo", "v", "stable", "", "--unsupported"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			lookup := func(context.Context, string) ([]string, error) {
				t.Error("lookup called for invalid arguments")
				return nil, nil
			}
			var stdout, stderr bytes.Buffer
			if got := run(t.Context(), tt.args, &stdout, &stderr, lookup); got != 1 || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Errorf("run(%v) = %d, stdout %q, stderr %q; want 1 with usage on stderr only", tt.args, got, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		var stdout, stderr bytes.Buffer
		if got := run(t.Context(), []string{flag}, &stdout, &stderr, nil); got != 0 || !strings.Contains(stdout.String(), "--operator") || stderr.Len() != 0 {
			t.Errorf("run(%q) = %d, stdout %q, stderr %q; want help with exit 0", flag, got, stdout.String(), stderr.String())
		}
	}
}

func TestRemoteTagsContext(t *testing.T) {
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := remoteTags(ctx, "owner/repo"); !errors.Is(err, context.Canceled) {
			t.Errorf("remoteTags() error = %v, want context.Canceled", err)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(t.Context(), time.Time{})
		defer cancel()
		if _, err := remoteTags(ctx, "owner/repo"); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("remoteTags() error = %v, want context.DeadlineExceeded", err)
		}
	})
	t.Run("missing_git", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if _, err := remoteTags(t.Context(), "owner/repo"); err == nil || !strings.Contains(err.Error(), "cannot read tags") {
			t.Errorf("remoteTags() error = %v, want lookup error", err)
		}
	})
}
