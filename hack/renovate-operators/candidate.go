// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type candidate struct {
	Base        string               `json:"base"`
	Group       string               `json:"group"`
	Versions    map[string]string    `json:"versions"`
	Directories []string             `json:"directories"`
	Pins        map[string]pinChange `json:"pins"`
	Defaults    map[string]string    `json:"defaults"`
}

type pinChange struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

const (
	pinsPath       = "hack/e2e/global.env"
	recordingsPath = "test/e2e/recorded_data"
)

var safeVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]*$`)

func prepareCandidate(ctx context.Context, root string, plan updatePlan, base string) (candidate, error) {
	var result candidate
	before, err := gitOutput(ctx, root, "show", base+":"+pinsPath)
	if err != nil {
		return result, err
	}
	after, err := os.ReadFile(filepath.Join(root, pinsPath))
	if err != nil {
		return result, fmt.Errorf("read candidate pins: %w", err)
	}
	old, err := parseDefaults(before)
	if err != nil {
		return result, fmt.Errorf("read base defaults: %w", err)
	}
	updated, err := parseDefaults(after)
	if err != nil {
		return result, fmt.Errorf("read candidate defaults: %w", err)
	}
	allowed := make(map[string]bool, len(plan.Pins))
	for _, pin := range plan.Pins {
		allowed[pin.Variable] = true
	}
	changes := make(map[string]pinChange)
	keys := maps.Clone(old)
	maps.Copy(keys, updated)
	expected := string(before)
	for key := range keys {
		oldValue, hadOld := old[key]
		newValue, hasNew := updated[key]
		if hadOld == hasNew && oldValue == newValue {
			continue
		}
		if !allowed[key] {
			return result, fmt.Errorf("pin %q is outside operator group %q", key, plan.Group)
		}
		if !hadOld || !hasNew || !safeVersion.MatchString(newValue) {
			return result, fmt.Errorf("invalid candidate version for %q: %q", key, newValue)
		}
		previous := fmt.Sprintf(`%s="${%s:-%s}"`, key, key, oldValue)
		next := fmt.Sprintf(`%s="${%s:-%s}"`, key, key, newValue)
		expected = strings.ReplaceAll(expected, previous, next)
		changes[key] = pinChange{Before: oldValue, After: newValue}
	}
	if len(changes) == 0 {
		return result, fmt.Errorf("no changed pins for operator group %q", plan.Group)
	}
	if expected != string(after) {
		return result, fmt.Errorf("global.env contains changes other than the selected version pins")
	}
	versions := make(map[string]string, len(plan.Recordings))
	directories := make([]string, 0, len(plan.Recordings))
	for _, recording := range plan.Recordings {
		values := make([]string, 0, len(recording.VersionVariables))
		for _, variable := range recording.VersionVariables {
			value, ok := updated[variable]
			if !ok {
				return result, fmt.Errorf("missing recording version pin %q", variable)
			}
			values = append(values, value)
		}
		version := strings.Join(values, recording.VersionSeparator)
		if !safeVersion.MatchString(version) {
			return result, fmt.Errorf("unsafe recording version %q", version)
		}
		if !groupPattern.MatchString(recording.Operator) {
			return result, fmt.Errorf("invalid recording operator %q", recording.Operator)
		}
		if _, exists := versions[recording.Operator]; exists {
			return result, fmt.Errorf("duplicate recording operator %q", recording.Operator)
		}
		versions[recording.Operator] = version
		name := recordingsPath + "/" + recording.Operator + "/" + version
		if _, err := candidatePath(root, name); err != nil {
			return result, err
		}
		directories = append(directories, name)
	}
	if len(directories) == 0 {
		return result, fmt.Errorf("operator group %q has no recording coverage", plan.Group)
	}
	result = candidate{Base: base, Group: plan.Group, Versions: versions, Directories: directories, Pins: changes, Defaults: updated}
	return result, validateCandidateChanges(ctx, root, result)
}

func verifyInstalled(candidate candidate, plan updatePlan, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read installed versions: %w", err)
	}
	values := make(map[string]string)
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		name, version, ok := strings.Cut(line, "=")
		if !ok || name == "" || version == "" {
			return fmt.Errorf("invalid installed version entry %q", line)
		}
		if _, exists := values[name]; exists {
			return fmt.Errorf("duplicate installed version for %q", name)
		}
		values[name] = version
	}
	expected := make(map[string]string, len(candidate.Versions))
	maps.Copy(expected, candidate.Versions)
	switch plan.Group {
	case "knative":
		expected["kourier"] = candidate.Defaults["KOURIER_VERSION"]
	case "cert-manager", "fake-gpu-operator", "kai-scheduler":
		if len(plan.Pins) != 1 {
			return fmt.Errorf("expected one infrastructure pin for %q", plan.Group)
		}
		expected[plan.Group] = candidate.Defaults[plan.Pins[0].Variable]
	}
	for operator, version := range expected {
		if version == "" || values[operator] != version {
			return fmt.Errorf("installed version for %s is %q, expected %q", operator, values[operator], version)
		}
	}
	return nil
}

func candidateFiles(root string, candidate candidate) ([]string, error) {
	result := []string{pinsPath}
	for _, directory := range candidate.Directories {
		if _, err := candidatePath(root, directory); err != nil {
			return nil, err
		}
		entries, err := filepath.Glob(filepath.Join(root, directory, "*", "*.yaml"))
		if err != nil {
			return nil, fmt.Errorf("find recordings in %q: %w", directory, err)
		}
		if len(entries) == 0 {
			return nil, fmt.Errorf("no generated recordings in %s", directory)
		}
		for _, path := range entries {
			name, err := filepath.Rel(root, path)
			if err != nil {
				return nil, fmt.Errorf("make recording path relative: %w", err)
			}
			if _, err := candidatePath(root, name); err != nil {
				return nil, err
			}
			info, err := os.Lstat(path)
			if err != nil {
				return nil, fmt.Errorf("inspect recording: %w", err)
			}
			if !info.Mode().IsRegular() || info.Mode().Perm()&^0o644 != 0 {
				return nil, fmt.Errorf("recording is not a regular, non-executable file: %s", name)
			}
			result = append(result, filepath.ToSlash(name))
		}
	}
	slices.Sort(result[1:])
	return result, nil
}

func candidatePath(root, name string) (string, error) {
	if !filepath.IsLocal(name) {
		return "", fmt.Errorf("unsafe candidate path %q", name)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve candidate root: %w", err)
	}
	path := root
	for _, part := range strings.Split(filepath.Clean(name), string(filepath.Separator)) {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("inspect candidate path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("candidate path contains a symlink: %s", name)
		}
	}
	return path, nil
}

func addRecordingHeaders(root string, candidate candidate) error {
	files, err := candidateFiles(root, candidate)
	if err != nil {
		return err
	}
	const header = "# SPDX-License-Identifier: Apache-2.0\n# Copyright (c) 2026 NVIDIA Corporation\n\n"
	for _, name := range files[1:] {
		path := filepath.Join(root, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read recording header: %w", err)
		}
		if bytes.HasPrefix(data, []byte("# SPDX-License-Identifier:")) {
			continue
		}
		if err := os.WriteFile(path, append([]byte(header), data...), 0o644); err != nil {
			return fmt.Errorf("write recording header: %w", err)
		}
	}
	return nil
}

func validateCandidateChanges(ctx context.Context, root string, candidate candidate) error {
	for _, args := range [][]string{{"diff", "--name-only", "--no-renames", "-z", candidate.Base}, {"ls-files", "--others", "--exclude-standard", "-z"}} {
		changed, err := gitOutput(ctx, root, args...)
		if err != nil {
			return err
		}
		for name := range strings.SplitSeq(strings.TrimSuffix(string(changed), "\x00"), "\x00") {
			if name == "" {
				continue
			}
			allowed := name == pinsPath
			for _, directory := range candidate.Directories {
				relative, found := strings.CutPrefix(name, directory+"/")
				parts := strings.Split(relative, "/")
				if found && len(parts) == 2 && groupPattern.MatchString(parts[0]) && strings.HasSuffix(parts[1], ".yaml") && groupPattern.MatchString(strings.TrimSuffix(parts[1], ".yaml")) {
					allowed = true
				}
			}
			if !allowed {
				return fmt.Errorf("unexpected changed file: %s", name)
			}
			path, err := candidatePath(root, name)
			if err != nil {
				return err
			}
			info, err := os.Lstat(path)
			if err != nil {
				return fmt.Errorf("inspect changed file %s: %w", name, err)
			}
			if !info.Mode().IsRegular() || info.Mode().Perm()&^0o644 != 0 {
				return fmt.Errorf("candidate must contain regular, non-executable files: %s", name)
			}
		}
	}
	return nil
}
