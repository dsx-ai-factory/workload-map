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
		{"verifyArgument", []string{"verify-commit", "unexpected"}, "unexpected verify-commit arguments"},
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

func TestRecordingLifecycle(t *testing.T) {
	for _, test := range []struct {
		name        string
		failInstall bool
		failCleanup bool
		want        string
	}{
		{"completed", false, false, ""},
		{"installationFailed", true, false, "install.log"},
		{"cleanupFailed", false, true, "clean recording environment"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newVerificationFixture(t)
			fixture.write(".gitignore", "hack/e2e/operators/.installed-versions-*\n")
			fixture.base = fixture.commit(true, false)
			fixture.write(pinsPath, verificationAfter)
			plan := updatePlan{Group: "example", Pins: []operatorPin{{Variable: "EXAMPLE_VERSION"}}, Workloads: []string{"example"}, WebhookMode: "auto", Recordings: []recordingPlan{{Operator: "example", VersionVariables: []string{"EXAMPLE_VERSION"}}}}
			temp := t.TempDir()
			artifacts := filepath.Join(temp, "evidence")
			if err := os.Mkdir(artifacts, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := writeJSONFile(filepath.Join(artifacts, "plan.json"), plan); err != nil {
				t.Fatal(err)
			}
			binaries := filepath.Join(temp, "bin")
			if err := os.Mkdir(binaries, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, script := range map[string]string{
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
`,
			} {
				if err := os.WriteFile(filepath.Join(binaries, name), []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", binaries+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("TEST_COMMAND_LOG", filepath.Join(artifacts, "commands.log"))
			t.Setenv("TEST_RECORDING", filepath.Join(fixture.root, verificationRecording))
			t.Setenv("TEST_FAIL_INSTALL", "false")
			t.Setenv("TEST_FAIL_CLEANUP", "false")
			if test.failInstall {
				t.Setenv("TEST_FAIL_INSTALL", "true")
			}
			if test.failCleanup {
				t.Setenv("TEST_FAIL_CLEANUP", "true")
			}
			for _, variable := range []string{"EXAMPLE_VERSION", "FLOW", "E2E_FOCUS", "E2E_LABELS", "KIND_NODE_IMAGE"} {
				t.Setenv(variable, "caller-override")
			}
			opts := updateOptions{Root: fixture.root, Group: "example", BaseSHA: fixture.base, Artifacts: artifacts, RunnerTemp: temp, RunID: "1", RunAttempt: "2", RunURL: "https://example.com/run"}
			var output, diagnostics bytes.Buffer
			err := recordUpdate(t.Context(), opts, executor{dir: fixture.root, env: os.Environ(), stdout: &output, stderr: &diagnostics})
			if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("recordUpdate() = %v, want %q\nstdout: %s\nstderr: %s", err, test.want, output.String(), diagnostics.String())
			}
			calls, readErr := os.ReadFile(filepath.Join(artifacts, "commands.log"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !strings.Contains(string(calls), "kind delete cluster --name renovate-example-1-2\n") {
				t.Fatalf("temporary cluster was not deleted: %s", calls)
			}
			if test.failInstall && !strings.Contains(string(calls), "kind export logs ") {
				t.Fatalf("failed recording did not collect diagnostics: %s", calls)
			}
			if !test.failInstall && !strings.Contains(string(calls), "make check VERSION=0.0.0-main-") {
				t.Fatalf("recording did not reach presubmit before cleanup: %s", calls)
			}
			if _, err := os.Stat(filepath.Join(temp, "renovate-example-kubeconfig")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("temporary kubeconfig remains: %v", err)
			}
			checkouts, err := filepath.Glob(filepath.Join(temp, "operator-check-*"))
			if err != nil || len(checkouts) != 0 {
				t.Fatalf("temporary presubmit checkouts remain: %v, %v", checkouts, err)
			}
			manifestPath := filepath.Join(artifacts, "manifest.json")
			if test.want != "" {
				if _, err := os.Stat(manifestPath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed recording left publication evidence: %v", err)
				}
				return
			}
			evidence, err := readManifest(manifestPath, fixture.root, fixture.base)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(fixture.root, verificationRecording))
			if err != nil {
				t.Fatal(err)
			}
			if evidence.Files[verificationRecording] != digestString(string(data)) || evidence.Files[pinsPath] != digestString(verificationAfter) {
				t.Fatalf("manifest does not describe final candidate bytes: %+v", evidence)
			}
			if !bytes.HasPrefix(data, []byte("# SPDX-License-Identifier: Apache-2.0\n")) {
				t.Fatalf("recording is missing its license header: %s", data)
			}
		})
	}
}

func TestPrePushBinary(t *testing.T) {
	fixture := newVerificationFixture(t)
	sha := fixture.makeCandidate(true)
	temp := t.TempDir()
	binary := filepath.Join(temp, "renovate-operators")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build hook binary: %v\n%s", err, output)
	}
	hooks := filepath.Join(temp, "hooks")
	if err := os.Mkdir(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyExecutable(binary, filepath.Join(hooks, "pre-push")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	branch := "renovate-operators/example/update"
	settings := hookSettings{ManifestPath: fixture.manifest, BaseSHA: fixture.base, Author: verificationAuthor, Branch: branch}
	if err := writeJSONFile(filepath.Join(hooks, "settings.json"), settings); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(temp, "remote.git")
	fixture.git("init", "--bare", "--quiet", remote)
	fixture.git("remote", "add", "test", remote)
	if err := os.Remove(fixture.manifest); err != nil {
		t.Fatal(err)
	}
	push := func() ([]byte, error) {
		command := exec.CommandContext(t.Context(), "git", "-c", "core.hooksPath="+hooks, "push", "test", "HEAD:refs/heads/"+branch)
		command.Dir = fixture.root
		return command.CombinedOutput()
	}
	if output, err := push(); err == nil || !bytes.Contains(output, []byte("manifest")) {
		t.Fatalf("push without evidence = %v, want manifest rejection\n%s", err, output)
	}
	if refs := fixture.git("ls-remote", "--heads", "test"); refs != "" {
		t.Fatalf("rejected push published a ref: %s", refs)
	}
	fixture.writeManifest()
	if output, err := push(); err != nil {
		t.Fatalf("push with verified evidence: %v\n%s", err, output)
	}
	if refs := fixture.git("ls-remote", "--heads", "test"); refs != sha+"\trefs/heads/"+branch {
		t.Fatalf("published ref = %q, want candidate %s", refs, sha)
	}
}
