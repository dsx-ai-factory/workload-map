// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOutputJSON(t *testing.T) {
	for _, test := range []struct {
		name, script, wantError string
		wantValue               int
	}{
		{"success", `printf '{"value":7}'`, "", 7},
		{"invalidJSON", `printf 'invalid'`, "invalid character 'i' looking for beginning of value", 0},
		{"commandFailure", `printf '{"value":7}'; printf 'failed\n' >&2; exit 2`, "sh: failed: exit status 2", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value struct{ Value int }
			err := (executor{}).outputJSON(t.Context(), &value, "sh", "-c", test.script)
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != test.wantError {
				t.Fatalf("outputJSON() error = %v, want %q", err, test.wantError)
			}
			if value.Value != test.wantValue {
				t.Fatalf("decoded value = %d, want %d", value.Value, test.wantValue)
			}
			switch test.name {
			case "invalidJSON":
				if _, ok := err.(*json.SyntaxError); !ok {
					t.Fatalf("decode error must remain unwrapped, got %T", err)
				}
			case "commandFailure":
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 2 {
					t.Fatalf("command error must retain exit status: %v", err)
				}
			}
		})
	}
}

func TestRunMatrix(t *testing.T) {
	t.Setenv("E2E_UPDATE_CAPTURE", "")
	t.Setenv("E2E_UPDATE_GROUP", "")
	for _, test := range []struct {
		name string
		args []string
		want []string
	}{
		{"selected", []string{"matrix", "--operator", "jobset"}, []string{"jobset"}},
		{"environment", []string{"matrix"}, []string{"jobset"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "environment" {
				t.Setenv("E2E_UPDATE_GROUP", "jobset")
			}
			var output, diagnostics bytes.Buffer
			if err := run(t.Context(), test.args, &output, &diagnostics); err != nil {
				t.Fatal(err)
			}
			var actual []string
			if err := json.Unmarshal(output.Bytes(), &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, test.want) || diagnostics.Len() != 0 {
				t.Fatalf("matrix = %v, diagnostics %q; want %v, no diagnostics", actual, diagnostics.String(), test.want)
			}
		})
	}
}

func TestRunErrors(t *testing.T) {
	t.Setenv("E2E_UPDATE_CAPTURE", "")
	t.Setenv("GITHUB_ACTIONS", "")
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"missingCommand", nil, "usage:"},
		{"unknownCommand", []string{"unknown"}, "unknown command"},
		{"unknownFlag", []string{"matrix", "--unknown"}, "flag provided but not defined"},
		{"unexpectedArgument", []string{"matrix", "jobset"}, "unexpected arguments"},
		{"unknownOperator", []string{"matrix", "--operator", "../jobset"}, "operator group"},
		{"runArgument", []string{"run", "jobset"}, "CI environment"},
		{"removedPlan", []string{"plan"}, "unknown command"},
		{"runOutsideCI", []string{"run"}, "GitHub Actions"},
		{"removedRecord", []string{"record"}, "unknown command"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output, diagnostics bytes.Buffer
			err := run(t.Context(), test.args, &output, &diagnostics)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run(%v) = %v, want error containing %q", test.args, err, test.want)
			}
			if output.Len() != 0 {
				t.Fatalf("failed command emitted output: %q", output.String())
			}
		})
	}
}

func TestOptionsFromEnvironment(t *testing.T) {
	fixture := newVerificationFixture(t)
	t.Chdir(fixture.root)
	temp := t.TempDir()
	artifacts := filepath.Join(temp, "evidence")
	if err := os.Mkdir(artifacts, 0o755); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"GITHUB_ACTIONS":              "true",
		"E2E_UPDATE_GROUP":            "jobset",
		"E2E_UPDATE_BASE_BRANCH":      "main",
		"E2E_UPDATE_BASE_SHA":         fixture.base,
		"E2E_UPDATE_ARTIFACTS":        artifacts,
		"E2E_UPDATE_RUN_URL":          "https://github.com/example/workload-map/actions/runs/1",
		"GITHUB_RUN_ID":               "1",
		"GITHUB_RUN_ATTEMPT":          "2",
		"RUNNER_TEMP":                 temp,
		"GITHUB_REPOSITORY":           "example/workload-map",
		"RENOVATE_GIT_AUTHOR":         verificationAuthor,
		"E2E_OPERATOR_TRACKING_ISSUE": "https://github.com/example/workload-map/issues/1",
		"RENOVATE_TOKEN":              "test-token",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
	opts, err := optionsFromEnvironment(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if opts.Group != "jobset" || opts.BaseSHA != fixture.base || opts.Artifacts != artifacts || opts.Repository != "example/workload-map" {
		t.Fatalf("unexpected CI options: %+v", opts)
	}
	for key := range values {
		if key == "GITHUB_ACTIONS" {
			continue
		}
		t.Run("missing_"+key, func(t *testing.T) {
			t.Setenv(key, "")
			_, err := optionsFromEnvironment(t.Context())
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("optionsFromEnvironment() = %v, want missing %s", err, key)
			}
		})
	}
}
