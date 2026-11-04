// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type manifest struct {
	Base  string            `json:"base"`
	Group string            `json:"group"`
	Files map[string]string `json:"files"`
}

type verifyOptions struct {
	Root         string `json:"root"`
	ManifestPath string `json:"manifestPath"`
	BaseSHA      string `json:"baseSHA"`
	Author       string `json:"author"`
	Commit       string `json:"commit"`
}

var (
	shaPattern       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	groupPattern     = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	digestPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	authorPattern    = regexp.MustCompile(`^[^<>\r\n]+ <[^<>\s]+>$`)
	recordingPattern = regexp.MustCompile(`^test/e2e/recorded_data/[a-z0-9-]+/[A-Za-z0-9_+.-]+/[a-z0-9-]+/[a-z0-9-]+\.yaml$`)
)

func verifyCommit(ctx context.Context, opts verifyOptions) error {
	evidence, err := readManifest(opts.ManifestPath, opts.Root, opts.BaseSHA)
	if err != nil {
		return err
	}
	commit := opts.Commit
	if commit == "" {
		commit = "HEAD"
	}
	if commit != "HEAD" && !shaPattern.MatchString(commit) {
		return fmt.Errorf("commit must be HEAD or a full commit SHA")
	}
	if !authorPattern.MatchString(opts.Author) {
		return fmt.Errorf("expected author must have the form Name <email>")
	}
	data, err := gitOutput(ctx, opts.Root, "rev-parse", "--verify", commit+"^{commit}")
	if err != nil {
		return err
	}
	commit = strings.TrimSpace(string(data))
	data, err = gitOutput(ctx, opts.Root, "show", "-s", "--format=%P", commit)
	if err != nil {
		return err
	}
	if !slices.Equal(strings.Fields(string(data)), []string{opts.BaseSHA}) {
		return fmt.Errorf("candidate must have exactly one parent equal to the verified base SHA")
	}
	data, err = gitOutput(ctx, opts.Root, "show", "-s", "--format=%an <%ae>", commit)
	if err != nil {
		return err
	}
	if strings.TrimSuffix(string(data), "\n") != opts.Author {
		return fmt.Errorf("candidate must be authored by the configured Renovate bot")
	}
	data, err = gitOutput(ctx, opts.Root, "show", "-s", "--format=%(trailers:key=Signed-off-by,valueonly)", commit)
	if err != nil {
		return err
	}
	if !slices.Contains(strings.Split(strings.TrimSpace(string(data)), "\n"), opts.Author) {
		return fmt.Errorf("candidate must contain the configured bot's DCO trailer")
	}
	data, err = gitOutput(ctx, opts.Root, "diff-tree", "--no-commit-id", "--name-status", "-r", "-z", "--no-renames", opts.BaseSHA, commit, "--")
	if err != nil {
		return err
	}
	changes := strings.Split(string(data), "\x00")
	if len(changes)%2 != 1 || changes[len(changes)-1] != "" {
		return fmt.Errorf("could not read candidate file changes")
	}
	pinChanged := false
	for index := 0; index < len(changes)-1; index += 2 {
		status, name := changes[index], changes[index+1]
		if status != "A" && status != "M" {
			return fmt.Errorf("candidate may only add or modify tested files")
		}
		if _, exists := evidence.Files[name]; !exists {
			return fmt.Errorf("candidate changes file absent from the CI manifest: %s", name)
		}
		pinChanged = pinChanged || name == pinsPath && status == "M"
	}
	if !pinChanged {
		return fmt.Errorf("candidate must modify global.env")
	}
	paths := slices.Sorted(maps.Keys(evidence.Files))
	args := append([]string{"ls-tree", "-r", "-z", commit, "--"}, paths...)
	data, err = gitOutput(ctx, opts.Root, args...)
	if err != nil {
		return err
	}
	entries := strings.Split(string(data), "\x00")
	if len(entries) != len(paths)+1 || entries[len(entries)-1] != "" {
		return fmt.Errorf("candidate does not contain all tested files")
	}
	for _, entry := range entries[:len(entries)-1] {
		metadata, name, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(metadata)
		_, exists := evidence.Files[name]
		if !ok || len(fields) != 3 || fields[0] != "100644" || fields[1] != "blob" || !exists {
			return fmt.Errorf("candidate paths must be regular, non-executable Git files")
		}
	}
	// Identical regenerated recordings still belong in the evidence, even though
	// Git omits them from the diff. Check every manifest entry against its blob.
	for _, name := range paths {
		data, err := gitOutput(ctx, opts.Root, "show", commit+":"+name)
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != evidence.Files[name] {
			return fmt.Errorf("committed content differs from the tested content: %s", name)
		}
	}
	return nil
}

func verifyPush(ctx context.Context, opts verifyOptions, branch string, updates io.Reader) error {
	if branch == "" {
		return fmt.Errorf("expected remote branch is required")
	}
	data, err := io.ReadAll(updates)
	if err != nil {
		return fmt.Errorf("read Git pre-push updates: %w", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 1 {
		return fmt.Errorf("expected exactly one branch update on pre-push stdin")
	}
	fields := strings.Fields(lines[0])
	if len(fields) != 4 {
		return fmt.Errorf("expected exactly one branch update on pre-push stdin")
	}
	if fields[2] != "refs/heads/"+branch {
		return fmt.Errorf("push does not target the configured operator update branch")
	}
	if !shaPattern.MatchString(fields[1]) || fields[1] == strings.Repeat("0", 40) {
		return fmt.Errorf("push must publish a commit, not delete a branch")
	}
	if !shaPattern.MatchString(fields[3]) {
		return fmt.Errorf("push contains an invalid remote SHA")
	}
	// A refspec can push a commit other than HEAD.
	opts.Commit = fields[1]
	return verifyCommit(ctx, opts)
}

func readManifest(path, root, base string) (manifest, error) {
	var result manifest
	if !shaPattern.MatchString(base) {
		return result, fmt.Errorf("base SHA must contain 40 lowercase hexadecimal digits")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return result, fmt.Errorf("inspect manifest regular file: %w", err)
	}
	if !filepath.IsAbs(path) || !info.Mode().IsRegular() {
		return result, fmt.Errorf("manifest must be an absolute path to a regular file")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return result, fmt.Errorf("resolve manifest: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return result, fmt.Errorf("resolve repository root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return result, fmt.Errorf("resolve repository symlinks: %w", err)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil {
		return result, fmt.Errorf("compare manifest location: %w", err)
	}
	if relative == "." || filepath.IsLocal(relative) {
		return result, fmt.Errorf("manifest must be outside the candidate checkout")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return result, fmt.Errorf("read manifest: %w", err)
	}
	object, err := decodeJSONObject(data)
	if err != nil {
		return result, fmt.Errorf("parse manifest: %w", err)
	}
	if len(object) != 3 || object["base"] == nil || object["group"] == nil || object["files"] == nil {
		return result, fmt.Errorf("manifest must contain only base, group and files")
	}
	if err := json.Unmarshal(object["base"], &result.Base); err != nil {
		return result, fmt.Errorf("parse manifest base: %w", err)
	}
	if result.Base != base {
		return result, fmt.Errorf("manifest does not describe the expected base SHA")
	}
	if err := json.Unmarshal(object["group"], &result.Group); err != nil {
		return result, fmt.Errorf("parse manifest group: %w", err)
	}
	if !groupPattern.MatchString(result.Group) {
		return result, fmt.Errorf("manifest contains an invalid operator group")
	}
	files, err := decodeJSONObject(object["files"])
	if err != nil {
		return result, fmt.Errorf("parse manifest files: %w", err)
	}
	if len(files) < 2 || files[pinsPath] == nil {
		return result, fmt.Errorf("manifest must include global.env and generated recordings")
	}
	result.Files = make(map[string]string, len(files))
	for name, raw := range files {
		if name != pinsPath && !recordingPattern.MatchString(name) {
			return result, fmt.Errorf("manifest contains an unexpected path: %s", name)
		}
		for _, part := range strings.Split(name, "/") {
			if part == "." || part == ".." {
				return result, fmt.Errorf("manifest contains an unsafe path: %s", name)
			}
		}
		var digest string
		if err := json.Unmarshal(raw, &digest); err != nil {
			return result, fmt.Errorf("parse digest for %s: %w", name, err)
		}
		if !digestPattern.MatchString(digest) {
			return result, fmt.Errorf("manifest contains an invalid SHA-256 digest: %s", name)
		}
		result.Files[name] = digest
	}
	return result, nil
}

func decodeJSONObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("expected a JSON object")
	}
	result := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("expected a JSON object key")
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("duplicate manifest key: %s", key)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		result[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("unexpected data after JSON object")
	}
	return result, nil
}

func gitOutput(ctx context.Context, root string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = root
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return output, nil
}
