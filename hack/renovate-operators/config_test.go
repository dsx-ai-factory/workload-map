// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

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

func TestConfigPublicationGuard(t *testing.T) {
	config := testConfig(t, "jobset")
	for name, want := range map[string]any{
		"requireConfig": "ignored", "platformCommit": "disabled", "prCreation": "status-success",
		"draftPR": true, "automerge": false, "ignoreTests": false, "dependencyDashboard": false,
	} {
		if config[name] != want {
			t.Errorf("config[%s] = %v, want %v", name, config[name], want)
		}
	}
	if !slices.Equal(config["enabledManagers"].([]string), []string{"custom.regex"}) || !slices.Equal(config["gitNoVerify"].([]string), []string{"commit"}) {
		t.Error("operator extraction and pre-push execution must remain enabled")
	}
	forwarded := config["customEnvVariables"].(map[string]string)
	for _, name := range []string{"GIT_CONFIG_GLOBAL", "RENOVATE_TOKEN"} {
		if _, ok := forwarded[name]; ok {
			t.Errorf("config forwarded %s", name)
		}
	}
	if forwarded["E2E_UPDATE_GROUP"] != "jobset" {
		t.Errorf("forwarded group = %q, want jobset", forwarded["E2E_UPDATE_GROUP"])
	}
	want := map[string]any{
		"commands": []string{"renovate-operators record"}, "executionMode": "branch",
		"fileFilters": []string{"test/e2e/recorded_data/jobset/**/*.yaml"},
	}
	if !reflect.DeepEqual(config["postUpgradeTasks"], want) {
		t.Errorf("postUpgradeTasks = %v, want %v", config["postUpgradeTasks"], want)
	}
	allowed := regexp.MustCompile(config["allowedCommands"].([]string)[0])
	if !allowed.MatchString("renovate-operators record") || allowed.MatchString("renovate-operators record; echo unexpected") {
		t.Error("post-upgrade command allowlist is not exact")
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
