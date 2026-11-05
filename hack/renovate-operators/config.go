// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type updatePlan struct {
	Group       string            `json:"group"`
	Pins        []operatorPin     `json:"pins"`
	Workloads   []string          `json:"workloads"`
	Recordings  []recordingPlan   `json:"recordings"`
	WebhookMode string            `json:"webhookMode"`
	Branch      string            `json:"branch"`
	Defaults    map[string]string `json:"defaults"`
}

type operatorPin struct {
	Name         string   `json:"name"`
	Variable     string   `json:"variable"`
	CurrentValue string   `json:"currentValue"`
	Repository   string   `json:"repository"`
	Prefix       string   `json:"prefix"`
	Channel      string   `json:"channel"`
	Note         string   `json:"note"`
	ExcludedTags []string `json:"excludedTags"`
}

type recordingPlan struct {
	Operator         string   `json:"operator"`
	VersionVariables []string `json:"versionVariables"`
	VersionSeparator string   `json:"versionSeparator"`
}

type planOptions struct {
	Root       string
	Binary     string
	Group      string
	BaseBranch string
}

type operatorGroup struct {
	name        string
	pins        []pinVariable
	workloads   []string
	webhookMode string
}

type pinVariable struct {
	name     string
	variable string
}

var groups = []operatorGroup{
	{name: "cert-manager", pins: []pinVariable{{"cert-manager", "CERT_MANAGER_VERSION"}}, workloads: []string{"kserve"}, webhookMode: "cert-manager"},
	{name: "fake-gpu-operator", pins: []pinVariable{{"fake-gpu-operator", "FAKE_GPU_VERSION"}}, workloads: []string{"grove"}},
	{name: "kai-scheduler", pins: []pinVariable{{"kai-scheduler", "KAI_SCHEDULER_VERSION"}}, workloads: []string{"grove", "dynamo"}},
	{name: "lws", pins: []pinVariable{{"lws", "LWS_VERSION"}}},
	{name: "jobset", pins: []pinVariable{{"jobset", "JOBSET_VERSION"}}},
	{name: "kuberay", pins: []pinVariable{{"kuberay", "KUBERAY_VERSION"}}},
	{name: "kubeflow", pins: []pinVariable{{"kubeflow", "KUBEFLOW_VERSION"}, {"mpi-operator", "MPI_OPERATOR_VERSION"}}},
	{name: "knative", pins: []pinVariable{{"knative", "KNATIVE_VERSION"}, {"kourier", "KOURIER_VERSION"}}},
	{name: "kserve", pins: []pinVariable{{"kserve", "KSERVE_VERSION"}}},
	{name: "milvus", pins: []pinVariable{{"milvus", "MILVUS_OPERATOR_VERSION"}}},
	{name: "grove", pins: []pinVariable{{"grove", "GROVE_VERSION"}}},
	{name: "dynamo", pins: []pinVariable{{"dynamo", "DYNAMO_VERSION"}}},
	{name: "nim", pins: []pinVariable{{"nim", "NIM_OPERATOR_VERSION"}}},
	{name: "agent-sandbox", pins: []pinVariable{{"agent-sandbox", "AGENT_SANDBOX_VERSION"}}},
}

var defaultPattern = regexp.MustCompile(`(?m)^([A-Z_]+)="\$\{([A-Z_]+):-([^$"}\r\n]+)\}"$`)

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	branchPattern     = regexp.MustCompile(`[\s~^:?*\[\\]`)
	pinPrefixPattern  = regexp.MustCompile(`^[^0-9]*`)
)

func operatorGroups(group string) ([]string, error) {
	var names []string
	for _, candidate := range groups {
		if group == "all" || candidate.name == group {
			names = append(names, candidate.name)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("unknown operator group %q", group)
	}
	return names, nil
}

func makePlan(ctx context.Context, opts planOptions) (updatePlan, error) {
	index := slices.IndexFunc(groups, func(group operatorGroup) bool { return group.name == opts.Group })
	if index < 0 {
		return updatePlan{}, fmt.Errorf("unknown operator group %q", opts.Group)
	}
	selected := groups[index]
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return updatePlan{}, fmt.Errorf("resolve repository root: %w", err)
	}
	opts.Root = root
	data, err := os.ReadFile(filepath.Join(opts.Root, "hack/e2e/global.env"))
	if err != nil {
		return updatePlan{}, fmt.Errorf("read operator defaults: %w", err)
	}
	defaults, err := parseDefaults(data)
	if err != nil {
		return updatePlan{}, err
	}
	pins, err := capturePolicies(ctx, opts, selected, defaults)
	if err != nil {
		return updatePlan{}, err
	}
	workloads := slices.Clone(selected.workloads)
	if len(workloads) == 0 {
		workloads = []string{selected.name}
	}
	recordings := make([]recordingPlan, 0, len(workloads))
	for _, workload := range workloads {
		index := slices.IndexFunc(groups, func(group operatorGroup) bool { return group.name == workload })
		if index < 0 {
			return updatePlan{}, fmt.Errorf("unknown recording workload %q", workload)
		}
		recording := recordingPlan{Operator: workload, VersionVariables: []string{groups[index].pins[0].variable}}
		if workload == "kubeflow" {
			recording.VersionVariables = []string{"KUBEFLOW_VERSION", "MPI_OPERATOR_VERSION"}
			recording.VersionSeparator = "+mpi"
		}
		recordings = append(recordings, recording)
	}
	webhookMode := selected.webhookMode
	if webhookMode == "" {
		webhookMode = "auto"
	}
	suffix := ""
	if opts.BaseBranch != "" && opts.BaseBranch != "main" {
		digest := sha256.Sum256([]byte(opts.BaseBranch))
		suffix = fmt.Sprintf("%x/", digest[:4])
	}
	return updatePlan{
		Group: selected.name, Pins: pins, Workloads: workloads, Recordings: recordings,
		WebhookMode: webhookMode, Branch: "renovate-operators/" + selected.name + "/" + suffix + "update", Defaults: defaults,
	}, nil
}

func parseDefaults(data []byte) (map[string]string, error) {
	defaults := make(map[string]string)
	for _, match := range defaultPattern.FindAllSubmatch(data, -1) {
		name := string(match[1])
		if name != string(match[2]) {
			return nil, fmt.Errorf("default for %s refers to %s", name, match[2])
		}
		if _, exists := defaults[name]; exists {
			return nil, fmt.Errorf("duplicate default for %s", name)
		}
		defaults[name] = string(match[3])
	}
	return defaults, nil
}

func capturePolicies(ctx context.Context, opts planOptions, group operatorGroup, defaults map[string]string) ([]operatorPin, error) {
	directory := "operators"
	if slices.Contains([]string{"cert-manager", "fake-gpu-operator", "kai-scheduler"}, group.name) {
		directory = "infra"
	}
	command := exec.CommandContext(ctx, "bash", filepath.Join(opts.Root, "hack/e2e", directory, group.name, "check-updates.sh"))
	command.Dir = opts.Root
	// The checked-in pins must win over version overrides inherited from a caller.
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, overridden := defaults[name]; !overridden && name != "E2E_OPERATOR_UPDATES_BIN" && name != "E2E_UPDATE_CAPTURE" {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "E2E_OPERATOR_UPDATES_BIN="+opts.Binary, "E2E_UPDATE_CAPTURE=true")
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("capture %s release policy: %w", group.name, err)
	}
	var args []string
	if err := json.Unmarshal(output, &args); err != nil {
		return nil, fmt.Errorf("decode %s release policy: %w", group.name, err)
	}
	return parsePolicies(group, defaults, args)
}

func parsePolicies(group operatorGroup, defaults map[string]string, args []string) ([]operatorPin, error) {
	pins := make([]operatorPin, 0, len(group.pins))
	excluded := []string{}
	seen := make(map[string]bool)
	for len(args) > 0 {
		flag := args[0]
		args = args[1:]
		switch flag {
		case "--exclude-tag":
			if len(args) == 0 || strings.HasPrefix(args[0], "--") {
				return nil, fmt.Errorf("missing tag after %s", flag)
			}
			excluded = append(excluded, args[0])
			args = args[1:]
		case "--operator":
			if len(args) < 6 {
				return nil, fmt.Errorf("incomplete operator policy for %s", group.name)
			}
			name, value, repository, prefix, channel, note := args[0], args[1], args[2], args[3], args[4], args[5]
			args = args[6:]
			index := slices.IndexFunc(group.pins, func(pin pinVariable) bool { return pin.name == name })
			if index < 0 || seen[name] || !slices.Contains([]string{"stable", "alpha"}, channel) {
				return nil, fmt.Errorf("unmapped or duplicate release policy for %s", name)
			}
			variable := group.pins[index].variable
			if current, ok := defaults[variable]; !ok || current == "" || current != value {
				return nil, fmt.Errorf("release policy for %s differs from its %s default", name, variable)
			}
			seen[name] = true
			pins = append(pins, operatorPin{Name: name, Variable: variable, CurrentValue: value, Repository: repository, Prefix: prefix, Channel: channel, Note: note})
		default:
			return nil, fmt.Errorf("unsupported checker flag %q", flag)
		}
	}
	if len(pins) != len(group.pins) {
		return nil, fmt.Errorf("incomplete release policy for %s", group.name)
	}
	for i := range pins {
		pins[i].ExcludedTags = slices.Clone(excluded)
	}
	return pins, nil
}

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
		"prCreation":             "immediate",
		"draftPR":                true,
		"dependencyDashboard":    false,
		"pruneStaleBranches":     false,
		"automerge":              false,
		"ignoreTests":            false,
		"ignoreUnstable":         ignoreUnstable,
		"binarySource":           "global",
		"platformCommit":         "disabled",
		"semanticCommits":        "enabled",
		"semanticCommitType":     "build",
		"semanticCommitScope":    "e2e",
		"commitBody":             "Signed-off-by: {{{gitAuthor}}}",
		"customManagers":         managers,
		"packageRules":           rules,
		"prBodyNotes": []string{
			"CI records and validates this update in a fresh kind cluster, then adds the recordings as a signed-off bot commit. The PR stays draft for review. Failed validation leaves the version bump here without a recording commit.",
			"[Recording and validation run](" + env["E2E_UPDATE_RUN_URL"] + "). Related to " + env["E2E_OPERATOR_TRACKING_ISSUE"] + ".",
		},
	}, nil
}
