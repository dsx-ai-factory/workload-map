// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("E2E_UPDATE_CAPTURE") == "true" {
		if err := json.NewEncoder(os.Stdout).Encode(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func testPlan(t *testing.T, group, base string) updatePlan {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := makePlan(t.Context(), planOptions{Root: root, Binary: binary, Group: group, BaseBranch: base})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestOperatorGroups(t *testing.T) {
	data, err := os.ReadFile("../e2e/check-updates.sh")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^ALL_OPERATORS=\(([^)]+)\)$`).FindSubmatch(data)
	if match == nil {
		t.Fatal("checker has no operator inventory")
	}
	want := strings.Fields(string(match[1]))
	got, err := operatorGroups("all")
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("operatorGroups(all) = %v, %v; want %v, nil", got, err, want)
	}
	components := 0
	for _, group := range got {
		t.Run(group, func(t *testing.T) {
			plan := testPlan(t, group, "main")
			components += len(plan.Pins)
			if plan.Branch != "renovate-operators/"+group+"/update" || len(plan.Workloads) == 0 {
				t.Fatalf("unexpected plan: %+v", plan)
			}
			var operators []string
			for _, recording := range plan.Recordings {
				operators = append(operators, recording.Operator)
			}
			if !slices.Equal(operators, plan.Workloads) {
				t.Errorf("recorded operators = %v, want %v", operators, plan.Workloads)
			}
			for _, pin := range plan.Pins {
				if pin.CurrentValue != plan.Defaults[pin.Variable] {
					t.Errorf("pin %s = %s, want source default %s", pin.Variable, pin.CurrentValue, plan.Defaults[pin.Variable])
				}
			}
			one, err := operatorGroups(group)
			if err != nil || !slices.Equal(one, []string{group}) {
				t.Errorf("operatorGroups(%s) = %v, %v", group, one, err)
			}
		})
	}
	if components != 16 {
		t.Errorf("component count = %d, want 16", components)
	}
}

func TestPlanCoverage(t *testing.T) {
	cases := []struct {
		group     string
		workloads []string
		webhook   string
	}{
		{"cert-manager", []string{"kserve"}, "cert-manager"},
		{"fake-gpu-operator", []string{"grove"}, "auto"},
		{"kai-scheduler", []string{"grove", "dynamo"}, "auto"},
		{"knative", []string{"knative"}, "auto"},
	}
	for _, tc := range cases {
		t.Run(tc.group, func(t *testing.T) {
			plan := testPlan(t, tc.group, "main")
			if !slices.Equal(plan.Workloads, tc.workloads) || plan.WebhookMode != tc.webhook {
				t.Errorf("plan coverage = %v, %s; want %v, %s", plan.Workloads, plan.WebhookMode, tc.workloads, tc.webhook)
			}
		})
	}
	want := []recordingPlan{{Operator: "kubeflow", VersionVariables: []string{"KUBEFLOW_VERSION", "MPI_OPERATOR_VERSION"}, VersionSeparator: "+mpi"}}
	if got := testPlan(t, "kubeflow", "main").Recordings; !reflect.DeepEqual(got, want) {
		t.Errorf("Kubeflow recordings = %+v, want %+v", got, want)
	}
	if got := testPlan(t, "knative", "main").Recordings[0].VersionVariables; !slices.Equal(got, []string{"KNATIVE_VERSION"}) {
		t.Errorf("Knative recording variables = %v, want KNATIVE_VERSION", got)
	}
}

func TestPlanForkBranches(t *testing.T) {
	first := testPlan(t, "jobset", "validation-one")
	second := testPlan(t, "jobset", "validation-two")
	if first.Branch == second.Branch || !regexp.MustCompile(`^renovate-operators/jobset/[a-f0-9]{8}/update$`).MatchString(first.Branch) {
		t.Fatalf("fork branches are not isolated: %s and %s", first.Branch, second.Branch)
	}
	if got := testPlan(t, "jobset", "renovate-operator-ci-validation").Branch; got != "renovate-operators/jobset/27d4c1e9/update" {
		t.Errorf("fork branch = %s, want existing validation branch", got)
	}
}

func TestPlanIgnoresVersionOverrides(t *testing.T) {
	t.Setenv("JOBSET_VERSION", "v99.99.99")
	t.Setenv("E2E_OPERATOR_UPDATES_BIN", "/not-the-capture-binary")
	plan := testPlan(t, "jobset", "main")
	if pin := plan.Pins[0]; pin.CurrentValue != plan.Defaults[pin.Variable] || pin.CurrentValue == "v99.99.99" {
		t.Fatalf("captured pin inherited caller override: %+v", pin)
	}
}

func TestPlanRelativeRoot(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := makePlan(t.Context(), planOptions{Root: "../..", Binary: binary, Group: "jobset", BaseBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Group != "jobset" {
		t.Errorf("plan group = %s, want jobset", plan.Group)
	}
}

func TestPlanErrors(t *testing.T) {
	if _, err := operatorGroups("../jobset"); err == nil {
		t.Error("operatorGroups accepted an unknown group")
	}
	if _, err := makePlan(t.Context(), planOptions{Group: "../jobset"}); err == nil {
		t.Error("makePlan accepted an unknown group")
	}
	if _, err := makePlan(t.Context(), planOptions{Group: "jobset", Root: t.TempDir()}); err == nil {
		t.Error("makePlan accepted missing defaults")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := makePlan(t.Context(), planOptions{Group: "jobset", Root: root, Binary: "/missing-capture-helper"}); err == nil {
		t.Error("makePlan accepted a failed capture command")
	}
}

func TestParseDefaults(t *testing.T) {
	data := []byte("# comment\nJOBSET_VERSION=\"${JOBSET_VERSION:-v0.10.1}\"\nGO_VERSION=\"${GO_VERSION:-$(awk 'x')}\"\n")
	got, err := parseDefaults(data)
	want := map[string]string{"JOBSET_VERSION": "v0.10.1"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("parseDefaults() = %v, %v; want %v, nil", got, err, want)
	}
}

func TestParseDefaultsErrors(t *testing.T) {
	for name, source := range map[string]string{
		"DifferentVariable": "PIN=\"${OTHER:-v1.2.3}\"\n",
		"DuplicateVariable": "PIN=\"${PIN:-v1.2.3}\"\nPIN=\"${PIN:-v1.2.4}\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseDefaults([]byte(source)); err == nil {
				t.Error("parseDefaults accepted invalid defaults")
			}
		})
	}
}

func TestParsePoliciesErrors(t *testing.T) {
	group := operatorGroup{name: "example", pins: []pinVariable{{"example", "EXAMPLE_VERSION"}}}
	defaults := map[string]string{"EXAMPLE_VERSION": "v1.2.3"}
	valid := []string{"--operator", "example", "v1.2.3", "example/operator", "v", "stable", ""}
	cases := map[string][]string{
		"Missing":           {},
		"UnknownFlag":       {"--unknown"},
		"MissingTag":        {"--exclude-tag"},
		"FlagInsteadOfTag":  {"--exclude-tag", "--operator"},
		"IncompletePolicy":  {"--operator", "example"},
		"UnknownOperator":   {"--operator", "other", "v1.2.3", "example/operator", "v", "stable", ""},
		"WrongPin":          {"--operator", "example", "v9.9.9", "example/operator", "v", "stable", ""},
		"UnsupportedPolicy": {"--operator", "example", "v1.2.3", "example/operator", "v", "nightly", ""},
		"DuplicatePolicy":   append(slices.Clone(valid), valid...),
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parsePolicies(group, defaults, args); err == nil {
				t.Errorf("parsePolicies(%v) accepted invalid policy", args)
			}
		})
	}
}

func testConfig(t *testing.T, group string) map[string]any {
	t.Helper()
	plan := testPlan(t, group, "validation")
	config, err := renovateConfig(plan, map[string]string{
		"GITHUB_REPOSITORY": "example/workload-map", "E2E_UPDATE_BASE_BRANCH": "validation",
		"E2E_UPDATE_GROUP": group, "GIT_CONFIG_GLOBAL": "must-not-forward", "RENOVATE_TOKEN": "must-not-forward",
	})
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func TestConfigMatchesPins(t *testing.T) {
	data, err := os.ReadFile("../e2e/global.env")
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		t.Run(group.name, func(t *testing.T) {
			plan := testPlan(t, group.name, "validation")
			config := testConfig(t, group.name)
			if _, err := json.Marshal(config); err != nil {
				t.Fatalf("config is not JSON: %v", err)
			}
			managers := config["customManagers"].([]map[string]any)
			for index, manager := range managers {
				pattern := regexp.MustCompile(manager["matchStrings"].([]string)[0])
				matches := pattern.FindAllStringSubmatch(string(data), -1)
				if len(matches) != 1 {
					t.Fatalf("%s: matches = %d, want 1", plan.Pins[index].Variable, len(matches))
				}
				value := matches[0][pattern.SubexpIndex("currentValue")]
				newText := strings.Replace(matches[0][0], value, "9.8.7", 1)
				prefix := pinPrefixPattern.FindString(plan.Pins[index].CurrentValue)
				if !strings.HasSuffix(newText, ":-"+prefix+"9.8.7}\"") {
					t.Errorf("replacement changed the install prefix: %s", newText)
				}
			}
		})
	}
}

func TestConfigReleasePolicies(t *testing.T) {
	cases := []struct {
		group string
		tag   string
		want  string
	}{
		{"kuberay", "kuberay-operator-1.7.0", "1.7.0"},
		{"kuberay", "ray-2.40.0", ""},
		{"milvus", "milvus-operator-1.4.0", "1.4.0"},
		{"milvus", "v1.4.0", ""},
		{"agent-sandbox", "v1.0.6.post1", ""},
		{"jobset", "v0.12.0-rc.1", ""},
		{"grove", "v0.1.0-alpha.11", "0.1.0-alpha.11"},
		{"grove", "v0.1.0-alpha.11-rc1", ""},
		{"grove", "operator/v0.1.0", ""},
	}
	for _, tc := range cases {
		t.Run(tc.group+"_"+tc.tag, func(t *testing.T) {
			manager := testConfig(t, tc.group)["customManagers"].([]map[string]any)[0]
			pattern := regexp.MustCompile(manager["extractVersionTemplate"].(string))
			match := pattern.FindStringSubmatch(tc.tag)
			got := ""
			if match != nil {
				got = match[pattern.SubexpIndex("version")]
			}
			if got != tc.want {
				t.Errorf("extracted version = %q, want %q", got, tc.want)
			}
		})
	}
	grove := testConfig(t, "grove")
	if grove["ignoreUnstable"] != false || grove["customManagers"].([]map[string]any)[0]["versioningTemplate"] != "maven" {
		t.Error("Grove config must use Maven ordering and allow alpha releases")
	}
	trainer := testConfig(t, "kubeflow")["packageRules"].([]map[string]any)
	if got := trainer[0]["allowedVersions"]; got != "<2.0.0" {
		t.Errorf("Trainer allowedVersions = %v, want <2.0.0", got)
	}
	kai := testConfig(t, "kai-scheduler")["packageRules"].([]map[string]any)
	if got := kai[0]["allowedVersions"]; got != `!/^(0\.20\.0|0\.20\.1)$/` {
		t.Errorf("KAI allowedVersions = %v, want retracted versions excluded", got)
	}
	if got := len(testConfig(t, "knative")["customManagers"].([]map[string]any)); got != 2 {
		t.Errorf("Knative manager count = %d, want 2", got)
	}
}

func TestConfigDraftPR(t *testing.T) {
	config := testConfig(t, "jobset")
	for name, want := range map[string]any{
		"requireConfig": "ignored", "platformCommit": "disabled", "prCreation": "immediate",
		"draftPR": true, "automerge": false, "ignoreTests": false, "dependencyDashboard": false,
	} {
		if config[name] != want {
			t.Errorf("config[%s] = %v, want %v", name, config[name], want)
		}
	}
	if !slices.Equal(config["enabledManagers"].([]string), []string{"custom.regex"}) {
		t.Error("only operator extraction must be enabled")
	}
	for _, name := range []string{"postUpgradeTasks", "allowedCommands", "customEnvVariables", "gitNoVerify"} {
		if _, exists := config[name]; exists {
			t.Errorf("obsolete publication setting %s", name)
		}
	}
	plan := testPlan(t, "jobset", "validation")
	if got := config["branchPrefix"].(string) + config["group"].(map[string]string)["branchTopic"]; got != plan.Branch {
		t.Errorf("configured branch = %s, want %s", got, plan.Branch)
	}
}

func TestConfigErrors(t *testing.T) {
	plan := testPlan(t, "jobset", "main")
	cases := map[string]map[string]string{
		"MissingRepository": {"E2E_UPDATE_BASE_BRANCH": "main"},
		"InvalidRepository": {"GITHUB_REPOSITORY": "example/repo/extra", "E2E_UPDATE_BASE_BRANCH": "main"},
		"MissingBase":       {"GITHUB_REPOSITORY": "example/repo"},
		"InvalidBase":       {"GITHUB_REPOSITORY": "example/repo", "E2E_UPDATE_BASE_BRANCH": "invalid branch"},
		"UpstreamOtherBase": {"GITHUB_REPOSITORY": "dsx-ai-factory/workload-map", "E2E_UPDATE_BASE_BRANCH": "other"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := renovateConfig(plan, env); err == nil {
				t.Errorf("renovateConfig accepted invalid environment: %v", env)
			}
		})
	}
	plan.Pins[0].ExcludedTags = []string{"wrong-prefix-1.2.3"}
	if _, err := renovateConfig(plan, map[string]string{"GITHUB_REPOSITORY": "example/repo", "E2E_UPDATE_BASE_BRANCH": "main"}); err == nil {
		t.Error("renovateConfig accepted an excluded tag with a different prefix")
	}
}
