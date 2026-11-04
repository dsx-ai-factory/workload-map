// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
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
			installTestCommands(t, map[string]string{"gh": `#!/bin/sh
set -eu
case "$2" in
  repos/example/repo) printf '%s\n' "$TEST_REPOSITORY" ;;
  repos/example/repo/issues/1) printf '%s\n' "$TEST_ISSUE" ;;
  *) exit 2 ;;
esac
`})
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

func TestPublishRecordings(t *testing.T) {
	for _, scenario := range []string{"success", "concurrentUpdate", "dirtyValidation", "unexpectedParent"} {
		t.Run(scenario, func(t *testing.T) {
			fixture, plan := candidateFixture(t)
			head := fixture.git("rev-parse", "HEAD")
			candidate, err := prepareCandidate(t.Context(), fixture.root, plan, fixture.base)
			if err != nil {
				t.Fatal(err)
			}
			remote := filepath.Join(t.TempDir(), "remote.git")
			fixture.git("clone", "--bare", fixture.root, remote)
			fixture.git("remote", "add", "origin", remote)
			branch := "renovate-operators/example/update"
			fixture.git("push", "origin", head+":refs/heads/"+branch)
			fixture.write(verificationRecording, verificationBytes)
			commands := executor{dir: fixture.root, env: os.Environ(), stdout: io.Discard, stderr: io.Discard}
			opts := updateOptions{Root: fixture.root, Group: "example", Author: verificationAuthor, Repository: "example/repo", RunURL: "https://github.com/example/repo/actions/runs/1"}
			if err := commitRecordings(t.Context(), opts, candidate, commands); err != nil {
				t.Fatal(err)
			}
			published := fixture.git("rev-parse", "HEAD")
			if got := fixture.git("show", "-s", "--format=%an <%ae>", "HEAD"); got != opts.Author {
				t.Fatalf("author = %s", got)
			}
			if got := fixture.git("show", "-s", "--format=%B", "HEAD"); !strings.Contains(got, "Signed-off-by: "+opts.Author) || !strings.Contains(got, opts.RunURL) {
				t.Fatalf("commit message = %s", got)
			}
			switch scenario {
			case "concurrentUpdate":
				fixture.git("-C", remote, "update-ref", "refs/heads/"+branch, fixture.base, head)
			case "dirtyValidation":
				fixture.write("unexpected.txt", "validation generated a file")
			case "unexpectedParent":
				fixture.git("commit", "--allow-empty", "-m", "concurrent local commit")
			}
			log := filepath.Join(t.TempDir(), "commands.log")
			t.Setenv("TEST_COMMAND_LOG", log)
			installTestCommands(t, map[string]string{"gh": "#!/bin/sh\nprintf '%s\n' \"$*\" >> \"$TEST_COMMAND_LOG\"\n"})
			commands.env = os.Environ()
			err = publishRecordings(t.Context(), opts, branch, head, commands)
			if (err == nil) != (scenario == "success") {
				t.Fatalf("publishRecordings = %v", err)
			}
			got := fixture.git("-C", remote, "rev-parse", "refs/heads/"+branch)
			want := head
			if scenario == "concurrentUpdate" {
				want = fixture.base
			}
			if scenario == "success" {
				want = published
			}
			if got != want {
				t.Fatalf("remote SHA = %s, want %s", got, want)
			}
			calls, _ := os.ReadFile(log)
			if strings.Contains(string(calls), "state=success") != (scenario == "success") {
				t.Fatalf("unexpected status calls: %s", calls)
			}
		})
	}
}

func TestCheckoutCandidateUsesCurrentBase(t *testing.T) {
	fixture := newVerificationFixture(t)
	oldBase := fixture.base
	fixture.write("README.md", "base advanced during workflow setup")
	currentBase := fixture.commit(true, false)
	fixture.git("branch", "validation-base", currentBase)
	fixture.write(pinsPath, verificationAfter)
	head := fixture.commit(true, false)
	remote := filepath.Join(t.TempDir(), "remote.git")
	fixture.git("clone", "--bare", fixture.root, remote)
	fixture.git("remote", "add", "origin", remote)
	fixture.git("checkout", "--detach", oldBase)
	commands := executor{dir: fixture.root, env: os.Environ(), stdout: io.Discard, stderr: io.Discard}
	base, err := checkoutCandidate(t.Context(), "validation-base", head, commands)
	if err != nil {
		t.Fatal(err)
	}
	if base != currentBase || fixture.git("rev-parse", "HEAD") != head {
		t.Fatalf("base = %s, expected %s at candidate %s", base, currentBase, head)
	}
}

func installTestCommands(t *testing.T, scripts map[string]string) {
	t.Helper()
	binaries := t.TempDir()
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(binaries, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binaries+string(os.PathListSeparator)+os.Getenv("PATH"))
}
