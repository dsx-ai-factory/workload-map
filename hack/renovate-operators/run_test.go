// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyTrackingIssue(t *testing.T) {
	for _, test := range []struct {
		name, url, repository, issue string
		wantError                    bool
	}{
		{"openPublic", "https://github.com/example/repo/issues/1", `{"private":false}`, `{"state":"open"}`, false},
		{"private", "https://github.com/example/repo/issues/1", `{"private":true}`, `{"state":"open"}`, true},
		{"closed", "https://github.com/example/repo/issues/1", `{"private":false}`, `{"state":"closed"}`, true},
		{"pullRequest", "https://github.com/example/repo/issues/1", `{"private":false}`, `{"state":"open","pull_request":{}}`, true},
		{"missingVisibility", "https://github.com/example/repo/issues/1", `{}`, `{"state":"open"}`, true},
		{"malformedResponse", "https://github.com/example/repo/issues/1", `invalid`, `{"state":"open"}`, true},
		{"invalidURL", "https://example.com/issues/1", `{"private":false}`, `{"state":"open"}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			binaries := t.TempDir()
			path := filepath.Join(binaries, "gh")
			if err := os.WriteFile(path, []byte(`#!/bin/sh
set -eu
case "$2" in
  repos/example/repo) printf '%s\n' "$TEST_REPOSITORY" ;;
  repos/example/repo/issues/1) printf '%s\n' "$TEST_ISSUE" ;;
  *) exit 2 ;;
esac
`), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binaries+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("TEST_REPOSITORY", test.repository)
			t.Setenv("TEST_ISSUE", test.issue)
			commands := executor{env: os.Environ(), stdout: io.Discard, stderr: io.Discard}
			err := verifyTrackingIssue(t.Context(), test.url, commands)
			if (err != nil) != test.wantError {
				t.Fatalf("verifyTrackingIssue() = %v, want error %v", err, test.wantError)
			}
		})
	}
}

func TestPublishPullRequest(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "createDraft"
		if existing {
			name = "updateExisting"
		}
		t.Run(name, func(t *testing.T) {
			artifacts := t.TempDir()
			binaries := t.TempDir()
			if err := os.WriteFile(filepath.Join(binaries, "gh"), []byte(`#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$TEST_COMMAND_LOG"
case "$1 $2" in
  'pr list') printf '%s\n' "$TEST_EXISTING" ;;
  'pr edit') ;;
  'pr create'|'pr view') printf '%s\n' 'https://github.com/example/repo/pull/2' ;;
  *) exit 2 ;;
esac
`), 0o755); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(artifacts, "commands.log")
			t.Setenv("PATH", binaries+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("TEST_COMMAND_LOG", log)
			t.Setenv("TEST_EXISTING", "[]")
			if existing {
				t.Setenv("TEST_EXISTING", `[{"number":2}]`)
			}
			summary := filepath.Join(artifacts, "summary.md")
			t.Setenv("GITHUB_STEP_SUMMARY", summary)
			update := candidate{Group: "jobset", Pins: map[string]pinChange{"JOBSET_VERSION": {Before: "v0.10.1", After: "v0.13.0"}}}
			if err := writeJSONFile(filepath.Join(artifacts, "candidate.json"), update); err != nil {
				t.Fatal(err)
			}
			opts := updateOptions{Group: "jobset", Repository: "example/repo", BaseBranch: "main", Artifacts: artifacts, RunURL: "https://github.com/example/repo/actions/runs/1", TrackingIssue: "https://github.com/example/repo/issues/1"}
			var output bytes.Buffer
			commands := executor{env: os.Environ(), stdout: &output, stderr: io.Discard}
			if err := publishPullRequest(t.Context(), opts, updatePlan{Branch: "renovate-operators/jobset/update"}, commands); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(filepath.Join(artifacts, "pr-body.md"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"`JOBSET_VERSION`: `v0.10.1` -> `v0.13.0`", opts.RunURL, opts.TrackingIssue} {
				if !strings.Contains(string(body), want) {
					t.Errorf("PR body missing %q", want)
				}
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if existing {
				if !strings.Contains(string(calls), "pr edit 2") || strings.Contains(string(calls), "pr create") {
					t.Fatalf("existing PR must be updated: %s", calls)
				}
			} else if !strings.Contains(string(calls), "pr create") || !strings.Contains(string(calls), "--draft") {
				t.Fatalf("new PR must be a draft: %s", calls)
			}
			written, err := os.ReadFile(summary)
			if err != nil {
				t.Fatal(err)
			}
			if string(written) != output.String() || !strings.Contains(string(written), "https://github.com/example/repo/pull/2") {
				t.Fatalf("invalid workflow summary: %s", written)
			}
		})
	}
}
