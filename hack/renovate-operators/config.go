// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	branchPattern     = regexp.MustCompile(`[\s~^:?*\[\\]`)
	pinPrefixPattern  = regexp.MustCompile(`^[^0-9]*`)
)

func renovateConfig(plan updatePlan, env map[string]string) (map[string]any, error) {
	repository := env["GITHUB_REPOSITORY"]
	if !repositoryPattern.MatchString(repository) {
		return nil, fmt.Errorf("invalid GITHUB_REPOSITORY %q", repository)
	}
	base := env["E2E_UPDATE_BASE_BRANCH"]
	if base == "" || branchPattern.MatchString(base) {
		return nil, fmt.Errorf("invalid E2E_UPDATE_BASE_BRANCH %q", base)
	}
	if repository == "dsx-ai-factory/workload-map" && base != "main" {
		return nil, fmt.Errorf("upstream operator updates target main only")
	}
	managers := make([]map[string]any, 0, len(plan.Pins))
	rules := []map[string]any{}
	ignoreUnstable := true
	for _, pin := range plan.Pins {
		version := `[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?`
		release := `[0-9]+\.[0-9]+\.[0-9]+`
		versioning := "semver"
		if pin.Channel == "alpha" {
			release += `(?:-alpha\.[0-9]+)?`
			// Grove promotes alpha.N-rcM to alpha.N. Maven orders that promotion
			// correctly; SemVer ranks the RC above the promoted alpha.
			versioning = "maven"
			ignoreUnstable = false
		}
		pinPrefix := regexp.QuoteMeta(pinPrefixPattern.FindString(pin.CurrentValue))
		managers = append(managers, map[string]any{
			"customType":             "regex",
			"managerFilePatterns":    []string{`/^hack\/e2e\/global\.env$/`},
			"matchStrings":           []string{`(?:^|\n)` + pin.Variable + `="\$\{` + pin.Variable + `:-` + pinPrefix + `(?<currentValue>` + version + `)\}"`},
			"depNameTemplate":        pin.Repository,
			"datasourceTemplate":     "github-tags",
			"versioningTemplate":     versioning,
			"extractVersionTemplate": `^` + regexp.QuoteMeta(pin.Prefix) + `(?<version>` + release + `)$`,
			"depTypeTemplate":        "e2e-operator",
		})
		if pin.Name == "kubeflow" {
			rules = append(rules, map[string]any{"matchPackageNames": []string{pin.Repository}, "allowedVersions": "<2.0.0"})
		}
		if len(pin.ExcludedTags) > 0 {
			excluded := make([]string, 0, len(pin.ExcludedTags))
			for _, tag := range pin.ExcludedTags {
				version, ok := strings.CutPrefix(tag, pin.Prefix)
				if !ok {
					return nil, fmt.Errorf("excluded tag %q does not use prefix %q", tag, pin.Prefix)
				}
				excluded = append(excluded, regexp.QuoteMeta(version))
			}
			rules = append(rules, map[string]any{"matchPackageNames": []string{pin.Repository}, "allowedVersions": "!/^(" + strings.Join(excluded, "|") + ")$/"})
		}
	}
	forwarded := map[string]string{}
	for _, name := range []string{
		"GITHUB_ACTIONS", "GITHUB_REPOSITORY", "GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT", "GITHUB_STEP_SUMMARY",
		"E2E_UPDATE_GROUP", "E2E_UPDATE_BASE_BRANCH", "E2E_UPDATE_BASE_SHA", "E2E_UPDATE_ARTIFACTS", "E2E_UPDATE_RUN_URL", "RUNNER_TEMP",
	} {
		forwarded[name] = env[name]
	}
	filters := make([]string, 0, len(plan.Workloads))
	for _, workload := range plan.Workloads {
		filters = append(filters, "test/e2e/recorded_data/"+workload+"/**/*.yaml")
	}
	return map[string]any{
		"platform":               "github",
		"repositories":           []string{repository},
		"onboarding":             false,
		"requireConfig":          "ignored",
		"forkProcessing":         "enabled",
		"enabledManagers":        []string{"custom.regex"},
		"includePaths":           []string{"hack/e2e/global.env"},
		"baseBranchPatterns":     []string{base},
		"branchPrefix":           strings.TrimSuffix(plan.Branch, "update"),
		"additionalBranchPrefix": "",
		"branchTopic":            "update",
		"groupName":              "e2e " + plan.Group,
		"groupSlug":              plan.Group,
		"group":                  map[string]string{"branchTopic": "update"},
		"separateMajorMinor":     false,
		"separateMinorPatch":     false,
		"prCreation":             "status-success",
		"draftPR":                true,
		"dependencyDashboard":    false,
		"pruneStaleBranches":     false,
		"automerge":              false,
		"ignoreTests":            false,
		"ignoreUnstable":         ignoreUnstable,
		"binarySource":           "global",
		"platformCommit":         "disabled",
		"gitNoVerify":            []string{"commit"},
		"executionTimeout":       45,
		"allowedCommands":        []string{`^renovate-operators record$`},
		"customEnvVariables":     forwarded,
		"semanticCommits":        "enabled",
		"semanticCommitType":     "build",
		"semanticCommitScope":    "e2e",
		"commitBody":             "Signed-off-by: {{{gitAuthor}}}",
		"customManagers":         managers,
		"packageRules":           rules,
		"postUpgradeTasks": map[string]any{
			"commands":      []string{"renovate-operators record"},
			"executionMode": "branch",
			"fileFilters":   filters,
		},
	}, nil
}
