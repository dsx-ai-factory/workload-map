// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	t        *testing.T
	root     string
	base     string
	manifest string
	files    map[string]string
}

func newVerificationFixture(t *testing.T) *verificationFixture {
	t.Helper()
	dir := t.TempDir()
	fixture := &verificationFixture{t: t, root: filepath.Join(dir, "repo"), manifest: filepath.Join(dir, "manifest.json")}
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
	fixture.files = map[string]string{pinsPath: verificationAfter, verificationRecording: verificationBytes}
	fixture.writeManifest()
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

func (fixture *verificationFixture) writeManifest() {
	fixture.t.Helper()
	value := manifest{Base: fixture.base, Group: "example", Files: make(map[string]string)}
	for path, content := range fixture.files {
		value.Files[path] = digestString(content)
	}
	data, err := json.Marshal(value)
	if err != nil {
		fixture.t.Fatal(err)
	}
	if err := os.WriteFile(fixture.manifest, data, 0o644); err != nil {
		fixture.t.Fatal(err)
	}
}

func digestString(content string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
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

func (fixture *verificationFixture) makeCandidate(signoff bool) string {
	fixture.t.Helper()
	for path, content := range fixture.files {
		fixture.write(path, content)
	}
	return fixture.commit(signoff, false)
}

func (fixture *verificationFixture) options() verifyOptions {
	return verifyOptions{Root: fixture.root, ManifestPath: fixture.manifest, BaseSHA: fixture.base, Author: verificationAuthor}
}

func TestVerifyCommit(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*verificationFixture)
	}{
		{"tested", func(f *verificationFixture) { f.makeCandidate(true) }},
		{"dirtyWorktree", func(f *verificationFixture) {
			f.makeCandidate(true)
			f.write(verificationRecording, "uncommitted content")
		}},
		{"unchangedRecording", func(f *verificationFixture) {
			f.files = map[string]string{pinsPath: verificationAfter, verificationExisting: verificationBytes}
			f.writeManifest()
			f.makeCandidate(true)
		}},
		{"compositeVersion", func(f *verificationFixture) {
			f.files = map[string]string{pinsPath: verificationAfter, strings.ReplaceAll(verificationRecording, "v0.2.0", "v1.9.0+mpiv0.8.2"): verificationBytes}
			f.writeManifest()
			f.makeCandidate(true)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newVerificationFixture(t)
			test.prepare(fixture)
			if err := verifyCommit(t.Context(), fixture.options()); err != nil {
				t.Fatalf("verifyCommit() = %v, want nil", err)
			}
		})
	}
}

func TestVerifyCommitErrors(t *testing.T) {
	tests := []struct {
		name   string
		change func(*verificationFixture)
		want   string
	}{
		{"missingManifest", func(f *verificationFixture) {
			if err := os.Remove(f.manifest); err != nil {
				f.t.Fatal(err)
			}
		}, "manifest"},
		{"insideManifest", func(f *verificationFixture) {
			data, err := os.ReadFile(f.manifest)
			if err != nil {
				f.t.Fatal(err)
			}
			f.write("manifest.json", string(data))
			f.manifest = filepath.Join(f.root, "manifest.json")
		}, "outside"},
		{"symlinkManifest", func(f *verificationFixture) {
			link := filepath.Join(filepath.Dir(f.root), "link.json")
			if err := os.Symlink(f.manifest, link); err != nil {
				f.t.Fatal(err)
			}
			f.manifest = link
		}, "regular file"},
		{"staleBase", func(f *verificationFixture) { f.base = strings.Repeat("a", 40); f.writeManifest() }, "exactly one parent"},
		{"extraCommit", func(f *verificationFixture) { f.write(verificationRecording, "another commit"); f.commit(true, false) }, "exactly one parent"},
		{"wrongAuthor", func(f *verificationFixture) {
			f.git("commit", "--amend", "--no-edit", "--author", "Other Person <other@example.com>")
		}, "authored"},
		{"missingDCO", func(f *verificationFixture) { f.git("commit", "--amend", "-m", "no signoff") }, "DCO"},
		{"changedBytes", func(f *verificationFixture) { f.write(verificationRecording, "not tested"); f.commit(true, true) }, "differs from the tested content"},
		{"extraFile", func(f *verificationFixture) { f.write("README.md", "unrelated"); f.commit(true, true) }, "absent from the CI manifest"},
		{"removedFile", func(f *verificationFixture) {
			if err := os.Remove(filepath.Join(f.root, verificationExisting)); err != nil {
				f.t.Fatal(err)
			}
			f.commit(true, true)
		}, "only add or modify"},
		{"missingRecording", func(f *verificationFixture) {
			if err := os.Remove(filepath.Join(f.root, verificationRecording)); err != nil {
				f.t.Fatal(err)
			}
			f.commit(true, true)
		}, "all tested files"},
		{"unchangedPin", func(f *verificationFixture) { f.write(pinsPath, verificationBefore); f.commit(true, true) }, "modify global.env"},
		{"executableRecording", func(f *verificationFixture) {
			if err := os.Chmod(filepath.Join(f.root, verificationRecording), 0o755); err != nil {
				f.t.Fatal(err)
			}
			f.git("update-index", "--chmod=+x", verificationRecording)
			f.commit(true, true)
		}, "regular, non-executable"},
		{"symlinkRecording", func(f *verificationFixture) {
			path := filepath.Join(f.root, verificationRecording)
			if err := os.Remove(path); err != nil {
				f.t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(f.root, verificationExisting), path); err != nil {
				f.t.Fatal(err)
			}
			f.commit(true, true)
		}, "regular, non-executable"},
		{"unchangedWrongHash", func(f *verificationFixture) { f.files[verificationExisting] = "other bytes"; f.writeManifest() }, "differs from the tested content"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newVerificationFixture(t)
			fixture.makeCandidate(true)
			test.change(fixture)
			err := verifyCommit(t.Context(), fixture.options())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("verifyCommit() = %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestReadManifestErrors(t *testing.T) {
	fixture := newVerificationFixture(t)
	valid := `{"base":"` + fixture.base + `","group":"example","files":{"` + pinsPath + `":"` + strings.Repeat("a", 64) + `","` + verificationRecording + `":"` + strings.Repeat("b", 64) + `"}}`
	for _, test := range []struct{ name, content, want string }{
		{"duplicateField", strings.TrimSuffix(valid, "}") + `,"group":"example"}`, "duplicate"},
		{"duplicateFile", strings.Replace(valid, `"files":{`, `"files":{"`+pinsPath+`":"`+strings.Repeat("a", 64)+`",`, 1), "duplicate"},
		{"unknownField", strings.TrimSuffix(valid, "}") + `,"extra":true}`, "only base"},
		{"invalidBase", strings.Replace(valid, fixture.base, strings.Repeat("c", 40), 1), "expected base"},
		{"uppercaseField", strings.Replace(valid, `"base":`, `"Base":`, 1), "only base"},
		{"unsafePath", strings.Replace(valid, verificationRecording, "test/e2e/recorded_data/example/../kind/running.yaml", 1), "unsafe path"},
		{"extraPath", strings.Replace(valid, verificationRecording, "README.md", 1), "unexpected path"},
		{"badDigest", strings.Replace(valid, strings.Repeat("b", 64), "bad", 1), "invalid SHA-256"},
		{"nonStringDigest", strings.Replace(valid, `"`+strings.Repeat("b", 64)+`"`, `false`, 1), "parse digest"},
		{"noRecordings", `{"base":"` + fixture.base + `","group":"example","files":{"` + pinsPath + `":"` + strings.Repeat("a", 64) + `"}}`, "generated recordings"},
		{"trailingJSON", valid + `{}`, "unexpected data"},
		{"nullFiles", `{"base":"` + fixture.base + `","group":"example","files":null}`, "JSON object"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(fixture.manifest, []byte(test.content), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := readManifest(fixture.manifest, fixture.root, fixture.base)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("readManifest() = %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestVerifyPush(t *testing.T) {
	fixture := newVerificationFixture(t)
	sha := fixture.makeCandidate(true)
	fixture.write("README.md", "unrelated HEAD")
	fixture.commit(true, false)
	branch := "renovate-operators/example/update"
	updates := "refs/heads/candidate " + sha + " refs/heads/" + branch + " " + strings.Repeat("0", 40) + "\n"
	if err := verifyPush(t.Context(), fixture.options(), branch, strings.NewReader(updates)); err != nil {
		t.Fatalf("verifyPush(actual object) = %v, want nil", err)
	}
	for _, test := range []struct{ name, updates, want string }{
		{"HEAD", strings.Replace(updates, sha, fixture.git("rev-parse", "HEAD"), 1), "exactly one parent"},
		{"otherBranch", strings.Replace(updates, "refs/heads/"+branch, "refs/heads/main", 1), "configured operator"},
		{"tag", strings.Replace(updates, "refs/heads/"+branch, "refs/tags/example", 1), "configured operator"},
		{"deletion", strings.Replace(updates, sha, strings.Repeat("0", 40), 1), "not delete"},
		{"badRemote", strings.Replace(updates, strings.Repeat("0", 40), "invalid", 1), "remote SHA"},
		{"empty", "", "exactly one branch"},
		{"multiple", updates + updates, "exactly one branch"},
		{"malformed", "invalid\n", "exactly one branch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := verifyPush(t.Context(), fixture.options(), branch, strings.NewReader(test.updates))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("verifyPush() = %v, want error containing %q", err, test.want)
			}
		})
	}
}
