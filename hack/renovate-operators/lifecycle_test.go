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
			if err := run(t.Context(), test.args, strings.NewReader(""), &output, &diagnostics); err != nil {
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
		{"recordArgument", []string{"record", "jobset"}, "CI environment"},
		{"runOutsideCI", []string{"run"}, "GitHub Actions"},
		{"recordOutsideCI", []string{"record"}, "GitHub Actions"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output, diagnostics bytes.Buffer
			err := run(t.Context(), test.args, strings.NewReader(""), &output, &diagnostics)
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
	for _, publish := range []bool{false, true} {
		name := "record"
		if publish {
			name = "publish"
		}
		t.Run(name, func(t *testing.T) {
			opts, err := optionsFromEnvironment(t.Context(), publish)
			if err != nil {
				t.Fatal(err)
			}
			if opts.Group != "jobset" || opts.BaseSHA != fixture.base || opts.Artifacts != artifacts || opts.Repository != "example/workload-map" {
				t.Fatalf("unexpected CI options: %+v", opts)
			}
			for key := range values {
				if key == "GITHUB_ACTIONS" || !publish && (key == "RENOVATE_GIT_AUTHOR" || key == "E2E_OPERATOR_TRACKING_ISSUE" || key == "RENOVATE_TOKEN") {
					continue
				}
				t.Run("missing_"+key, func(t *testing.T) {
					t.Setenv(key, "")
					_, err := optionsFromEnvironment(t.Context(), publish)
					if err == nil || !strings.Contains(err.Error(), key) {
						t.Fatalf("optionsFromEnvironment() = %v, want missing %s", err, key)
					}
				})
			}
		})
	}
}

func TestRecordPullRequestLifecycle(t *testing.T) {
	for _, test := range []struct {
		name        string
		failInstall bool
		failCleanup bool
		failStage   string
		want        string
	}{
		{"completed", false, false, "", ""},
		{"installationFailed", true, false, "", "install.log"},
		{"cleanupFailed", false, true, "", "clean recording environment"},
		{"recordingFailed", false, false, "record-e2e", "recording.log"},
		{"verificationFailed", false, false, "verify", "verification.log"},
		{"replayFailed", false, false, "verify-recordings", "replay.log"},
		{"presubmitFailed", false, false, "check", "make-check.log"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newVerificationFixture(t)
			fixture.write(".gitignore", "hack/e2e/operators/.installed-versions-*\n")
			fixture.base = fixture.commit(true, false)
			fixture.write(pinsPath, verificationAfter)
			head := fixture.commit(true, false)
			plan := updatePlan{Group: "example", Branch: "renovate-operators/example/update", Pins: []operatorPin{{Variable: "EXAMPLE_VERSION"}}, Workloads: []string{"example"}, WebhookMode: "auto", Recordings: []recordingPlan{{Operator: "example", VersionVariables: []string{"EXAMPLE_VERSION"}}}}
			remote := filepath.Join(t.TempDir(), "remote.git")
			fixture.git("clone", "--bare", fixture.root, remote)
			fixture.git("remote", "add", "origin", remote)
			fixture.git("push", "origin", head+":refs/heads/"+plan.Branch)
			realGit, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("TEST_GIT_BINARY", realGit)
			temp := t.TempDir()
			artifacts := filepath.Join(temp, "evidence")
			if err := os.Mkdir(artifacts, 0o755); err != nil {
				t.Fatal(err)
			}
			installTestCommands(t, map[string]string{
				"git": `#!/bin/sh
set -eu
printf 'git %s\n' "$*" >> "$TEST_COMMAND_LOG"
exec "$TEST_GIT_BINARY" "$@"
`,
				"gh": `#!/bin/sh
set -eu
printf 'gh %s\n' "$*" >> "$TEST_COMMAND_LOG"
`,
				"kind": `#!/bin/sh
set -eu
printf 'kind %s\n' "$*" >> "$TEST_COMMAND_LOG"
if [ "$1" = delete ] && [ "$TEST_FAIL_CLEANUP" = true ]; then
  exit 1
fi
`,
				"make": `#!/bin/sh
set -eu
printf 'make %s\n' "$*" >> "$TEST_COMMAND_LOG"
if [ "$1" = "$TEST_FAIL_STAGE" ]; then exit 1; fi
case "$1" in
  e2e-up)
    test -z "${EXAMPLE_VERSION:-}"
    test -z "${FLOW:-}"
    test -z "${E2E_FOCUS:-}"
    test -z "${E2E_LABELS:-}"
    test -z "${KIND_NODE_IMAGE:-}"
    touch "$KUBECONFIG"
    if [ "$TEST_FAIL_INSTALL" = true ]; then exit 1; fi
    mkdir -p hack/e2e/operators
    printf 'example=v0.2.0\n' > "hack/e2e/operators/.installed-versions-$CLUSTER_NAME"
    ;;
  record-e2e)
    mkdir -p "$(dirname "$TEST_RECORDING")"
    printf 'schemaVersion: 1\nresult:\n  succeeded: true\n' > "$TEST_RECORDING"
    ;;
esac
`,
				"kubectl": `#!/bin/sh
set -eu
printf 'kubectl %s\n' "$*" >> "$TEST_COMMAND_LOG"
case "$1" in
  version) printf '{"serverVersion":{"gitVersion":"v1.35.0"}}\n' ;;
  get) printf '{"items":[]}\n' ;;
  *) exit 1 ;;
esac
`,
				"go": `#!/bin/sh
set -eu
printf 'go %s\n' "$*" >> "$TEST_COMMAND_LOG"
if [ "$1" = run ] && [ "$TEST_FAIL_STAGE" = verify ]; then exit 1; fi
`,
			})
			t.Setenv("TEST_COMMAND_LOG", filepath.Join(artifacts, "commands.log"))
			t.Setenv("TEST_RECORDING", filepath.Join(fixture.root, verificationRecording))
			t.Setenv("TEST_FAIL_INSTALL", "false")
			t.Setenv("TEST_FAIL_CLEANUP", "false")
			t.Setenv("TEST_FAIL_STAGE", test.failStage)
			if test.failInstall {
				t.Setenv("TEST_FAIL_INSTALL", "true")
			}
			if test.failCleanup {
				t.Setenv("TEST_FAIL_CLEANUP", "true")
			}
			for _, variable := range []string{"EXAMPLE_VERSION", "FLOW", "E2E_FOCUS", "E2E_LABELS", "KIND_NODE_IMAGE"} {
				t.Setenv(variable, "caller-override")
			}
			opts := updateOptions{Root: fixture.root, Group: "example", BaseSHA: fixture.base, Artifacts: artifacts, RunnerTemp: temp, RunID: "1", RunAttempt: "2", RunURL: "https://example.com/run", Author: verificationAuthor, Repository: "example/repo"}
			expected, err := prepareCandidate(t.Context(), fixture.root, plan, fixture.base)
			if err != nil {
				t.Fatal(err)
			}
			var output, diagnostics bytes.Buffer
			err = recordPullRequest(t.Context(), opts, plan, head, executor{dir: fixture.root, env: os.Environ(), stdout: &output, stderr: &diagnostics})
			if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("recordPullRequest() = %v, want %q\nstdout: %s\nstderr: %s", err, test.want, output.String(), diagnostics.String())
			}
			data, readErr := os.ReadFile(filepath.Join(artifacts, "candidate.json"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			var recorded candidate
			if err := json.Unmarshal(data, &recorded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(recorded, expected) {
				t.Fatalf("candidate evidence = %#v, want %#v", recorded, expected)
			}
			calls, readErr := os.ReadFile(filepath.Join(artifacts, "commands.log"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !strings.Contains(string(calls), "kind delete cluster --name renovate-example-1-2\n") {
				t.Fatalf("temporary cluster was not deleted: %s", calls)
			}
			if (test.failInstall || test.failStage != "" && test.failStage != "check") && !strings.Contains(string(calls), "kind export logs ") {
				t.Fatalf("failed recording did not collect diagnostics: %s", calls)
			}
			if !test.failInstall && test.failStage == "" && !strings.Contains(string(calls), "make verify-recordings test-replay") {
				t.Fatalf("recording did not reach replay before cleanup: %s", calls)
			}
			if _, err := os.Stat(filepath.Join(temp, "renovate-example-kubeconfig")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("temporary kubeconfig remains: %v", err)
			}
			got := fixture.git("-C", remote, "rev-parse", "refs/heads/"+plan.Branch)
			if test.want != "" && got != head {
				t.Fatalf("failed CI published a recording commit: %s", got)
			}
			if test.want == "" && got == head {
				t.Fatal("successful CI did not publish recordings")
			}
			if strings.Contains(string(calls), "state=success") != (test.want == "") {
				t.Fatalf("unexpected success status: %s", calls)
			}
			if test.want != "" && !strings.Contains(string(calls), "state=failure") {
				t.Fatalf("failed CI did not mark the draft head failed: %s", calls)
			}
			if test.want == "" {
				previous := -1
				for _, step := range []string{"make record-e2e", "go run ./cmd/verify-operator-update", "make verify-recordings test-replay", "kind delete cluster", "commit --allow-empty", "make check", "git push", "state=success"} {
					index := strings.Index(string(calls), step)
					if index <= previous {
						t.Fatalf("step %q missing or out of order: %s", step, calls)
					}
					previous = index
				}
			}
			if test.want != "" {
				return
			}
			data, err = os.ReadFile(filepath.Join(fixture.root, verificationRecording))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(data, []byte("# SPDX-License-Identifier: Apache-2.0\n")) {
				t.Fatalf("recording is missing its license header: %s", data)
			}
		})
	}
}
