// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	verificationAuthor    = "renovate-example[bot] <123+renovate-example[bot]@users.noreply.github.com>"
	verificationRecording = "test/e2e/recorded_data/example/v0.2.0/example-org-workload-v1/running.yaml"
	verificationExisting  = "test/e2e/recorded_data/example/v0.1.0/example-org-workload-v1/running.yaml"
	verificationBefore    = "EXAMPLE_VERSION=\"${EXAMPLE_VERSION:-v0.1.0}\"\n"
	verificationAfter     = "EXAMPLE_VERSION=\"${EXAMPLE_VERSION:-v0.2.0}\"\n"
	verificationBytes     = "schemaVersion: 1\nresult:\n  succeeded: true\n"
)

type verificationFixture struct {
	t    *testing.T
	root string
	base string
}

func newVerificationFixture(t *testing.T) *verificationFixture {
	t.Helper()
	dir := t.TempDir()
	fixture := &verificationFixture{t: t, root: filepath.Join(dir, "repo")}
	if err := os.Mkdir(fixture.root, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.git("init", "--quiet")
	fixture.git("config", "core.fileMode", "true")
	fixture.git("config", "user.name", "renovate-example[bot]")
	fixture.git("config", "user.email", "123+renovate-example[bot]@users.noreply.github.com")
	fixture.write(pinsPath, verificationBefore)
	fixture.write(verificationExisting, verificationBytes)
	fixture.base = fixture.commit(true, false)
	return fixture
}

func (fixture *verificationFixture) git(args ...string) string {
	fixture.t.Helper()
	args = append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)
	command := exec.CommandContext(fixture.t.Context(), "git", args...)
	command.Dir = fixture.root
	output, err := command.CombinedOutput()
	if err != nil {
		fixture.t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func (fixture *verificationFixture) write(name, content string) {
	fixture.t.Helper()
	path := filepath.Join(fixture.root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fixture.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *verificationFixture) commit(signoff, amend bool) string {
	fixture.t.Helper()
	fixture.git("add", "--all")
	args := []string{"commit", "--quiet", "-m", "build(e2e): update example"}
	if signoff {
		args = append(args, "--signoff")
	}
	if amend {
		args = append(args, "--amend")
	}
	fixture.git(args...)
	return fixture.git("rev-parse", "HEAD")
}

func candidateFixture(t *testing.T) (*verificationFixture, updatePlan) {
	t.Helper()
	fixture := newVerificationFixture(t)
	fixture.write(pinsPath, verificationAfter)
	fixture.commit(true, false)
	return fixture, updatePlan{Group: "example", Pins: []operatorPin{{Variable: "EXAMPLE_VERSION"}}, Workloads: []string{"example"}, Recordings: []recordingPlan{{Operator: "example", VersionVariables: []string{"EXAMPLE_VERSION"}}}}
}

func TestPrepareCandidate(t *testing.T) {
	fixture, plan := candidateFixture(t)
	actual, err := prepareCandidate(t.Context(), fixture.root, plan, fixture.base)
	if err != nil {
		t.Fatal(err)
	}
	want := candidate{Base: fixture.base, Group: "example", Versions: map[string]string{"example": "v0.2.0"}, Directories: []string{recordingsPath + "/example/v0.2.0"}, Pins: map[string]pinChange{"EXAMPLE_VERSION": {Before: "v0.1.0", After: "v0.2.0"}}, Defaults: map[string]string{"EXAMPLE_VERSION": "v0.2.0"}}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("prepareCandidate() = %#v, want %#v", actual, want)
	}
}

func TestPrepareCandidateErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*verificationFixture, *updatePlan)
		want   string
	}{
		{"staleBase", func(f *verificationFixture, _ *updatePlan) { f.base = strings.Repeat("0", 40) }, "git show"},
		{"wrongGroup", func(_ *verificationFixture, p *updatePlan) { p.Pins = []operatorPin{{Variable: "OTHER_VERSION"}} }, "outside operator group"},
		{"extraEnvEdit", func(f *verificationFixture, _ *updatePlan) { f.write(pinsPath, verificationAfter+"# extra\n") }, "other than"},
		{"untracked", func(f *verificationFixture, _ *updatePlan) { f.write("unexpected.go", "package main") }, "unexpected changed"},
		{"noChange", func(f *verificationFixture, _ *updatePlan) { f.write(pinsPath, verificationBefore) }, "no changed pins"},
		{"unsafeVersion", func(f *verificationFixture, _ *updatePlan) {
			f.write(pinsPath, strings.Replace(verificationAfter, "v0.2.0", "../../outside", 1))
		}, "invalid candidate version"},
		{"noCoverage", func(_ *verificationFixture, p *updatePlan) { p.Recordings = nil }, "no recording coverage"},
		{"missingVersion", func(_ *verificationFixture, p *updatePlan) {
			p.Recordings[0].VersionVariables = []string{"MISSING_VERSION"}
		}, "missing recording version"},
		{"duplicateOperator", func(_ *verificationFixture, p *updatePlan) { p.Recordings = append(p.Recordings, p.Recordings[0]) }, "duplicate recording operator"},
		{"symlink", func(f *verificationFixture, _ *updatePlan) {
			path := filepath.Join(f.root, recordingsPath, "example", "v0.2.0")
			if err := os.Symlink(filepath.Dir(f.root), path); err != nil {
				f.t.Fatal(err)
			}
		}, "symlink"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, plan := candidateFixture(t)
			test.change(fixture, &plan)
			_, err := prepareCandidate(t.Context(), fixture.root, plan, fixture.base)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("prepareCandidate() = %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestVerifyInstalled(t *testing.T) {
	for _, test := range []struct {
		name      string
		candidate candidate
		plan      updatePlan
		content   string
	}{
		{"nilVersions", candidate{Defaults: map[string]string{"KOURIER_VERSION": "knative-v1.16.0"}}, updatePlan{Group: "knative"}, "kourier=knative-v1.16.0\n"},
		{"workload", candidate{Versions: map[string]string{"jobset": "v0.13.0"}}, updatePlan{Group: "jobset"}, "jobset=v0.13.0\n"},
		{"composite", candidate{Versions: map[string]string{"kubeflow": "v1.9.0+mpiv0.8.2"}}, updatePlan{Group: "kubeflow"}, "kubeflow=v1.9.0+mpiv0.8.2\n"},
		{"kourier", candidate{Versions: map[string]string{"knative": "knative-v1.15.2"}, Defaults: map[string]string{"KOURIER_VERSION": "knative-v1.16.0"}}, updatePlan{Group: "knative"}, "knative=knative-v1.15.2\nkourier=knative-v1.16.0\n"},
		{"infrastructure", candidate{Versions: map[string]string{"grove": "v0.1.0-alpha.10"}, Defaults: map[string]string{"FAKE_GPU_VERSION": "0.0.72"}}, updatePlan{Group: "fake-gpu-operator", Pins: []operatorPin{{Variable: "FAKE_GPU_VERSION"}}}, "grove=v0.1.0-alpha.10\nfake-gpu-operator=0.0.72\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "installed")
			if err := os.WriteFile(path, []byte(test.content), 0o644); err != nil {
				t.Fatal(err)
			}
			original := maps.Clone(test.candidate.Versions)
			if err := verifyInstalled(test.candidate, test.plan, path); err != nil {
				t.Fatalf("verifyInstalled() = %v, want nil", err)
			}
			if !reflect.DeepEqual(test.candidate.Versions, original) {
				t.Fatalf("candidate versions changed: got %v, want %v", test.candidate.Versions, original)
			}
		})
	}
}

func TestVerifyInstalledErrors(t *testing.T) {
	value := candidate{Versions: map[string]string{"knative": "knative-v1.15.2"}, Defaults: map[string]string{"KOURIER_VERSION": "knative-v1.16.0"}}
	for _, test := range []struct{ name, content, want string }{
		{"fallback", "knative=v1.34.0\nkourier=knative-v1.16.0\n", "installed version"},
		{"missingKourier", "knative=knative-v1.15.2\n", "kourier"},
		{"duplicate", "knative=knative-v1.15.2\nknative=knative-v1.15.2\n", "duplicate"},
		{"malformed", "not an entry\n", "invalid installed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "installed")
			if err := os.WriteFile(path, []byte(test.content), 0o644); err != nil {
				t.Fatal(err)
			}
			err := verifyInstalled(value, updatePlan{Group: "knative"}, path)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("verifyInstalled() = %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestCandidateArtifacts(t *testing.T) {
	fixture, plan := candidateFixture(t)
	value, err := prepareCandidate(t.Context(), fixture.root, plan, fixture.base)
	if err != nil {
		t.Fatal(err)
	}
	fixture.write(verificationRecording, verificationBytes)
	files, err := candidateFiles(fixture.root, value)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{pinsPath, verificationRecording}; !reflect.DeepEqual(files, want) {
		t.Fatalf("candidateFiles() = %v, want %v", files, want)
	}
	if err := addRecordingHeaders(fixture.root, value); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(fixture.root, verificationRecording))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "# SPDX-License-Identifier: Apache-2.0\n") {
		t.Fatalf("addRecordingHeaders() produced no SPDX header")
	}
	if err := addRecordingHeaders(fixture.root, value); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(filepath.Join(fixture.root, verificationRecording))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(again) {
		t.Fatal("addRecordingHeaders() is not idempotent")
	}
	if err := validateCandidateChanges(t.Context(), fixture.root, value); err != nil {
		t.Fatal(err)
	}
	fixture.write("unexpected", "unrelated")
	if err := validateCandidateChanges(t.Context(), fixture.root, value); err == nil || !strings.Contains(err.Error(), "unexpected changed") {
		t.Fatalf("validateCandidateChanges(extra file) = %v, want rejection", err)
	}
}

func TestCandidateFilesErrors(t *testing.T) {
	fixture, plan := candidateFixture(t)
	value, err := prepareCandidate(t.Context(), fixture.root, plan, fixture.base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidateFiles(fixture.root, value); err == nil {
		t.Fatal("candidateFiles() accepted missing recordings")
	}
	fixture.write(verificationRecording, verificationBytes)
	path := filepath.Join(fixture.root, verificationRecording)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(fixture.root, verificationExisting), path); err != nil {
		t.Fatal(err)
	}
	if _, err := candidateFiles(fixture.root, value); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("candidateFiles(symlink) = %v, want rejection", err)
	}
}

func TestCandidatePathRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "recordings")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside", outside, "recordings/version/kind/running.yaml"} {
		t.Run(path, func(t *testing.T) {
			if _, err := candidatePath(root, path); err == nil {
				t.Fatalf("candidatePath(%q) accepted a path outside the checkout", path)
			}
		})
	}
}

func TestPrepareCandidateWithRecordings(t *testing.T) {
	fixture, plan := candidateFixture(t)
	fixture.write(verificationRecording, verificationBytes)
	fixture.commit(true, false)
	if _, err := prepareCandidate(t.Context(), fixture.root, plan, fixture.base); err != nil {
		t.Fatalf("prepareCandidate(existing recorded update) = %v", err)
	}
}

func TestValidateCandidateChangesErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*verificationFixture)
		want   string
	}{
		{"unrelatedCommit", func(f *verificationFixture) {
			f.write("Makefile", "unrelated")
			f.commit(true, false)
		}, "unexpected changed"},
		{"otherVersion", func(f *verificationFixture) {
			f.write(verificationExisting, "changed old recording")
		}, "unexpected changed"},
		{"nestedFile", func(f *verificationFixture) {
			f.write(strings.Replace(verificationRecording, "running.yaml", "extra/running.yaml", 1), verificationBytes)
		}, "unexpected changed"},
		{"executable", func(f *verificationFixture) {
			if err := os.Chmod(filepath.Join(f.root, verificationRecording), 0o755); err != nil {
				f.t.Fatal(err)
			}
		}, "non-executable"},
		{"deletedFile", func(f *verificationFixture) {
			f.base = f.commit(true, false)
			if err := os.Remove(filepath.Join(f.root, verificationRecording)); err != nil {
				f.t.Fatal(err)
			}
		}, "inspect changed file"},
		{"symlinkDirectory", func(f *verificationFixture) {
			directory := filepath.Dir(filepath.Join(f.root, verificationRecording))
			outside := f.t.TempDir()
			if err := os.Rename(directory, filepath.Join(outside, "files")); err != nil {
				f.t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, "files"), directory); err != nil {
				f.t.Fatal(err)
			}
		}, "unexpected changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, plan := candidateFixture(t)
			value, err := prepareCandidate(t.Context(), fixture.root, plan, fixture.base)
			if err != nil {
				t.Fatal(err)
			}
			fixture.write(verificationRecording, verificationBytes)
			test.change(fixture)
			value.Base = fixture.base
			if err := validateCandidateChanges(t.Context(), fixture.root, value); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateCandidateChanges() = %v, want %q", err, test.want)
			}
		})
	}
}
