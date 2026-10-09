// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Command release validates and stages Karta release artifacts.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

const rootModule = "github.com/dsx-ai-factory/workload-map"

var semanticVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

type artifact struct {
	Name   string         `json:"name"`
	Path   string         `json:"path"`
	Goos   string         `json:"goos"`
	Goarch string         `json:"goarch"`
	Type   string         `json:"type"`
	Extra  map[string]any `json:"extra"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("a subcommand is required")
	}
	switch args[0] {
	case "validate-version":
		return runValidateVersion(args[1:])
	case "verify-artifacts":
		return runVerifyArtifacts(args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func runValidateVersion(args []string) error {
	flags := flag.NewFlagSet("validate-version", flag.ContinueOnError)
	root := flags.String("root", ".", "repository root")
	version := flags.String("version", "", "normalized product version, defaults to the cli/go.mod pin")
	requireTags := flags.Bool("require-tags", false, "require all synchronized tags at HEAD")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *version == "" {
		pinned, err := pinnedVersion(*root)
		if err != nil {
			return err
		}
		*version = pinned
	}
	if !semanticVersion.MatchString(*version) {
		return fmt.Errorf("version %q must match X.Y.Z without a leading v", *version)
	}
	if err := validateModuleVersions(*root, *version); err != nil {
		return err
	}
	if err := validateVersionIsUnreleased(*root, *version); err != nil {
		return err
	}
	if *requireTags {
		if err := validateTags(*root, *version); err != nil {
			return err
		}
	}
	fmt.Printf("synchronized module version v%s is valid\n", *version)
	return nil
}

func pinnedVersion(root string) (string, error) {
	path := filepath.Join(root, "cli", "go.mod")
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	parsed, err := modfile.Parse(path, contents, nil)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	for _, require := range parsed.Require {
		if require.Mod.Path == rootModule {
			return strings.TrimPrefix(require.Mod.Version, "v"), nil
		}
	}
	return "", fmt.Errorf("%s does not require %s", path, rootModule)
}

func validateModuleVersions(root, version string) error {
	want := "v" + version
	for _, moduleFile := range []string{"cli/go.mod", "operator/go.mod"} {
		contents, err := os.ReadFile(filepath.Join(root, moduleFile))
		if err != nil {
			return err
		}
		file, err := modfile.Parse(moduleFile, contents, nil)
		if err != nil {
			return fmt.Errorf("parse %s: %w", moduleFile, err)
		}
		if len(file.Replace) != 0 {
			return fmt.Errorf("%s contains a publication-unsafe replace directive", moduleFile)
		}
		if len(file.Exclude) != 0 {
			return fmt.Errorf("%s contains a publication-unsafe exclude directive", moduleFile)
		}
		got := ""
		for _, requirement := range file.Require {
			if requirement.Mod.Path == rootModule {
				got = requirement.Mod.Version
				break
			}
		}
		if got != want {
			return fmt.Errorf("%s requires %s %s, want %s", moduleFile, rootModule, got, want)
		}
	}
	contents, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		return err
	}
	workspace, err := modfile.ParseWork("go.work", contents, nil)
	if err != nil {
		return fmt.Errorf("parse go.work: %w", err)
	}
	for _, replacement := range workspace.Replace {
		if replacement.Old.Path == rootModule && replacement.Old.Version == want &&
			replacement.New.Path == "." && replacement.New.Version == "" {
			return nil
		}
	}
	return fmt.Errorf("go.work must replace %s %s with the local root module", rootModule, want)
}

// validateVersionIsUnreleased fails when the synchronized version is not ahead
// of every released one. The pin names the next release, so a version that has
// already shipped means the requirement went stale while the change waited, and
// its module path can no longer resolve. Tags at HEAD are skipped: during the
// release the tag being cut is the version under validation.
func validateVersionIsUnreleased(root, version string) error {
	head, err := commandOutput("git", "-C", root, "rev-parse", "HEAD")
	if err != nil {
		// No repository metadata, so there is no release history to compare
		// against. The tag checks below cover the cases that must have it.
		return nil
	}
	listed, err := commandOutput("git", "-C", root, "tag", "--list", "v[0-9]*.[0-9]*.[0-9]*")
	if err != nil {
		return err
	}
	// A newer line does not constrain this one. Reachability cannot stand in:
	// release tags are cut from the release branch, not from main.
	line := semver.MajorMinor("v" + version)
	newest := ""
	for _, tag := range strings.Fields(listed) {
		if !semver.IsValid(tag) || semver.Prerelease(tag) != "" {
			continue
		}
		if semver.Compare(semver.MajorMinor(tag), line) > 0 {
			continue
		}
		commit, err := commandOutput("git", "-C", root, "rev-list", "-n", "1", tag)
		if err != nil {
			return fmt.Errorf("resolve tag %s: %w", tag, err)
		}
		if commit == head {
			continue
		}
		if newest == "" || semver.Compare(tag, newest) > 0 {
			newest = tag
		}
	}
	if newest != "" && semver.Compare("v"+version, newest) <= 0 {
		return fmt.Errorf("version v%s is not newer than the released %s; bump the requirement in cli/go.mod, operator/go.mod and go.work", version, newest)
	}
	return nil
}

func validateTags(root, version string) error {
	head, err := commandOutput("git", "-C", root, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	for _, tag := range []string{"v" + version, "cli/v" + version} {
		commit, err := commandOutput("git", "-C", root, "rev-list", "-n", "1", tag)
		if err != nil {
			return fmt.Errorf("resolve tag %s: %w", tag, err)
		}
		if commit != head {
			return fmt.Errorf("tag %s points to %s, want HEAD %s", tag, commit, head)
		}
	}
	return nil
}

func readArtifacts(dist string) ([]artifact, error) {
	absoluteDist, err := filepath.Abs(dist)
	if err != nil {
		return nil, err
	}
	contents, err := os.ReadFile(filepath.Join(absoluteDist, "artifacts.json"))
	if err != nil {
		return nil, err
	}
	var artifacts []artifact
	if err := json.Unmarshal(contents, &artifacts); err != nil {
		return nil, fmt.Errorf("decode artifacts.json: %w", err)
	}
	projectRoot := filepath.Dir(absoluteDist)
	for i := range artifacts {
		if artifacts[i].Path != "" && !filepath.IsAbs(artifacts[i].Path) {
			artifacts[i].Path = filepath.Join(projectRoot, filepath.FromSlash(artifacts[i].Path))
		}
	}
	return artifacts, nil
}

func extraID(item artifact) string {
	value, _ := item.Extra["ID"].(string)
	return value
}

func runVerifyArtifacts(args []string) error {
	flags := flag.NewFlagSet("verify-artifacts", flag.ContinueOnError)
	dist := flags.String("dist", "dist", "GoReleaser dist directory")
	version := flags.String("version", "", "expected normalized version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	artifacts, err := readArtifacts(*dist)
	if err != nil {
		return err
	}
	archives := map[string]artifact{}
	for _, item := range artifacts {
		if item.Type != "Archive" {
			continue
		}
		if extraID(item) != "karta" {
			return fmt.Errorf("unexpected public archive %s from %s", item.Name, extraID(item))
		}
		archives[item.Name] = item
	}
	platforms := []string{"linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64"}
	if len(archives) != len(platforms) {
		return fmt.Errorf("found %d CLI archives, want %d", len(archives), len(platforms))
	}
	for _, platform := range platforms {
		name := "karta_" + *version + "_" + platform + ".tar.gz"
		if _, ok := archives[name]; !ok {
			return fmt.Errorf("missing archive %s", name)
		}
	}
	verified, skipped, err := verifyHostVersions(artifacts, *version)
	if err != nil {
		return err
	}
	fmt.Printf("verified %d CLI archives for %s\n", len(archives), *version)
	if len(verified) != 0 {
		fmt.Printf("verified host executable versions: %s\n", strings.Join(verified, ", "))
	}
	for _, id := range skipped {
		fmt.Printf("skipped %s --version: no %s/%s artifact\n", id, runtime.GOOS, runtime.GOARCH)
	}
	return nil
}

func verifyHostVersions(artifacts []artifact, version string) ([]string, []string, error) {
	var verified []string
	var skipped []string
	for _, id := range []string{"karta"} {
		var path string
		for _, item := range artifacts {
			if item.Type == "Binary" && extraID(item) == id && item.Goos == runtime.GOOS && item.Goarch == runtime.GOARCH {
				path = item.Path
				break
			}
		}
		if path == "" {
			skipped = append(skipped, id)
			continue
		}
		output, err := commandOutput(path, "--version")
		if err != nil {
			return nil, nil, fmt.Errorf("run %s --version: %w", id, err)
		}
		if output != version {
			return nil, nil, fmt.Errorf("%s --version = %q, want %q", id, output, version)
		}
		verified = append(verified, id)
	}
	return verified, skipped, nil
}

func commandOutput(name string, args ...string) (string, error) {
	output, err := commandOutputBytes(name, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func commandOutputBytes(name string, args ...string) ([]byte, error) {
	output, err := exec.Command(name, args...).Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			stderr := strings.TrimSpace(string(exitError.Stderr))
			if stderr != "" {
				return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, stderr)
			}
		}
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return output, nil
}
