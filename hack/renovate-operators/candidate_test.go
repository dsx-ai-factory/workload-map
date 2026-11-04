// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func candidateFixture(t *testing.T) (*verificationFixture, updatePlan) {
	t.Helper()
	fixture := newVerificationFixture(t)
	fixture.write(pinsPath, verificationAfter)
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
		{"staleBase", func(f *verificationFixture, _ *updatePlan) { f.base = strings.Repeat("0", 40) }, "base commit"},
		{"wrongGroup", func(_ *verificationFixture, p *updatePlan) { p.Pins = []operatorPin{{Variable: "OTHER_VERSION"}} }, "outside operator group"},
		{"extraEnvEdit", func(f *verificationFixture, _ *updatePlan) { f.write(pinsPath, verificationAfter+"# extra\n") }, "other than"},
		{"untracked", func(f *verificationFixture, _ *updatePlan) { f.write("unexpected.go", "package main") }, "untracked"},
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
		}, "unexpected untracked"},
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
			if err := verifyInstalled(test.candidate, test.plan, path); err != nil {
				t.Fatalf("verifyInstalled() = %v, want nil", err)
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
	actual, err := candidateManifest(t.Context(), fixture.root, value)
	if err != nil {
		t.Fatal(err)
	}
	want := manifest{Base: fixture.base, Group: "example", Files: map[string]string{pinsPath: digestString(verificationAfter), verificationRecording: digestString(string(data))}}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("candidateManifest() = %#v, want %#v", actual, want)
	}
	destination := t.TempDir()
	stale := filepath.Join(destination, value.Directories[0], "stale-kind", "stale.yaml")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyCandidate(fixture.root, destination, value); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("copyCandidate() left stale file: %v", err)
	}
	for _, name := range files {
		copied, err := os.ReadFile(filepath.Join(destination, name))
		if err != nil {
			t.Fatal(err)
		}
		source, err := os.ReadFile(filepath.Join(fixture.root, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(copied) != string(source) {
			t.Fatalf("copyCandidate() changed %s", name)
		}
	}
	fixture.write("unexpected", "unrelated")
	if _, err := candidateManifest(t.Context(), fixture.root, value); err == nil || !strings.Contains(err.Error(), "unexpected changed") {
		t.Fatalf("candidateManifest(extra file) = %v, want rejection", err)
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
