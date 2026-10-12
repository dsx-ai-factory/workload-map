// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

type scriptHarness struct {
	root   string
	binary string
}

type scriptCase struct {
	name       string
	script     string
	args       []string
	tags       map[string][]string
	pins       map[string]string
	wantRows   map[string]string
	wantOutput []string
	wantRepos  []string
	makeTarget bool
	noBinary   bool
}

type scriptResult struct {
	exitCode int
	output   string
	repos    []string
}

// The child process is reached only through the fake git executable installed by
// the harness. Unknown repositories fail locally instead of reaching the network.
func TestOperatorUpdatesFakeGit(t *testing.T) {
	if os.Getenv("KARTA_UPDATE_TEST_GIT") != "1" {
		return
	}
	if !slices.Contains(os.Args, "ls-remote") {
		os.Exit(0)
	}
	url := os.Args[len(os.Args)-1]
	if !strings.HasPrefix(url, "https://github.com/") || !strings.HasSuffix(url, ".git") {
		fmt.Fprintln(os.Stderr, "unexpected repository URL")
		os.Exit(128)
	}
	repository := strings.TrimSuffix(strings.TrimPrefix(url, "https://github.com/"), ".git")
	log, err := os.OpenFile(os.Getenv("KARTA_UPDATE_TEST_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(128)
	}
	if _, err := fmt.Fprintln(log, repository); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(128)
	}
	if err := log.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(128)
	}
	var fixtures map[string][]string
	if err := json.Unmarshal([]byte(os.Getenv("KARTA_UPDATE_TEST_TAGS")), &fixtures); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(128)
	}
	tags, found := fixtures[repository]
	if !found || tags == nil {
		fmt.Fprintln(os.Stderr, "simulated upstream lookup failure")
		os.Exit(128)
	}
	for _, tag := range tags {
		fmt.Printf("%s\trefs/tags/%s\n", strings.Repeat("a", 40), tag)
	}
	os.Exit(0)
}

func TestOperatorUpdatesScripts(t *testing.T) {
	harness := newScriptHarness(t)
	t.Run("success", harness.testSuccess)
	t.Run("updates", harness.testUpdates)
	t.Run("failures", harness.testFailures)
	t.Run("inventory", harness.testInventory)
}

func newScriptHarness(t *testing.T) scriptHarness {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	harness := scriptHarness{root: root, binary: filepath.Join(t.TempDir(), "operator-updates")}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	// go test prepends its toolchain's bin directory to PATH. Resolve that Go
	// executable before the harness substitutes fake tools.
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	build := exec.CommandContext(ctx, goBinary, "build", "-o", harness.binary, ".")
	build.Dir = filepath.Join(root, "hack", "operator-updates")
	build.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local",
		"GOFLAGS=-mod=readonly", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build checker: %v\n%s", err, output)
	}
	return harness
}

func (h scriptHarness) testSuccess(t *testing.T) {
	jobset := "kubernetes-sigs/jobset"
	jobsetTags := map[string][]string{jobset: {"v1.0.0", "v1.1.0"}}
	h.runCases(t, []scriptCase{
		{
			name: "standalone respects environment pin", script: "operators/jobset/check-updates.sh",
			args: []string{"--fail-on-update"}, tags: jobsetTags,
			pins:     map[string]string{"JOBSET_VERSION": "v1.1.0"},
			wantRows: map[string]string{"jobset": "CURRENT"}, wantRepos: []string{jobset},
		},
		{
			name: "aggregate advisory exit", args: []string{"jobset"}, tags: jobsetTags,
			wantRows: map[string]string{"jobset": "NEW"}, wantRepos: []string{jobset},
			wantOutput: []string{"New tags in operator groups: jobset"},
		},
		{
			name: "repeated selectors are deduplicated", args: []string{"jobset", "jobset"},
			tags: jobsetTags, wantRows: map[string]string{"jobset": "NEW"}, wantRepos: []string{jobset},
		},
		{
			name: "standalone help skips lookup", script: "operators/jobset/check-updates.sh",
			args: []string{"--help"}, wantOutput: []string{"--operator", "--fail-on-update"},
		},
		{
			name: "aggregate help skips build and lookup", args: []string{"--help"}, noBinary: true,
			wantOutput: []string{"Operators:", "--fail-on-update"},
		},
		{
			name: "infra alias excludes retracted KAI tags", args: []string{"infra", "--fail-on-update"},
			tags: map[string][]string{
				"cert-manager/cert-manager": {"v1.0.0"}, "run-ai/fake-gpu-operator": {"v1.0.0"},
				"kai-scheduler/KAI-Scheduler": {"v0.15.2", "v0.20.0", "v0.20.1"},
			},
			pins:      map[string]string{"CERT_MANAGER_VERSION": "v1.0.0", "FAKE_GPU_VERSION": "1.0.0", "KAI_SCHEDULER_VERSION": "v0.15.2"},
			wantRows:  map[string]string{"cert-manager": "CURRENT", "fake-gpu-operator": "CURRENT", "kai-scheduler": "CURRENT"},
			wantRepos: []string{"cert-manager/cert-manager", "run-ai/fake-gpu-operator", "kai-scheduler/KAI-Scheduler"},
		},
		{
			name: "stable wrappers omit release candidates", script: "operators/jobset/check-updates.sh",
			args: []string{"--fail-on-update"}, tags: map[string][]string{jobset: {"v1.0.0", "v1.1.0-rc1"}},
			wantRows: map[string]string{"jobset": "CURRENT"}, wantRepos: []string{jobset},
		},
		{
			name: "Agent Sandbox uses stable manifest tags and effective pin", script: "operators/agent-sandbox/check-updates.sh",
			args: []string{"--fail-on-update"}, pins: map[string]string{"AGENT_SANDBOX_VERSION": "v1.0.5"},
			tags:     map[string][]string{"kubernetes-sigs/agent-sandbox": {"v1.0.0", "v1.0.5", "v1.1.0-rc.1", "v1.0.5.post1", "v1.1.0rc5", "clients/python/v9.0.0"}},
			wantRows: map[string]string{"agent-sandbox": "CURRENT"}, wantRepos: []string{"kubernetes-sigs/agent-sandbox"},
			wantOutput: []string{"v1.0.5"},
		},
		{
			name: "make forwards selectors and prerelease flag", makeTarget: true,
			args:     []string{"OPERATORS=jobset", "UPDATE_FLAGS=--include-prereleases"},
			tags:     map[string][]string{jobset: {"v1.0.0", "v1.1.0-rc9", "v1.1.0-rc10"}},
			wantRows: map[string]string{"jobset": "NEW"}, wantRepos: []string{jobset},
			wantOutput: []string{"v1.1.0-rc10", "New tags in operator groups: jobset"},
		},
	}, 0)
}

func (h scriptHarness) testUpdates(t *testing.T) {
	jobset := "kubernetes-sigs/jobset"
	grove := "ai-dynamo/grove"
	jobsetTags := map[string][]string{jobset: {"v1.0.0", "v1.1.0"}}
	h.runCases(t, []scriptCase{
		{
			name: "standalone update exit", script: "operators/jobset/check-updates.sh",
			args: []string{"--fail-on-update"}, tags: jobsetTags,
			wantRows: map[string]string{"jobset": "NEW"}, wantRepos: []string{jobset},
		},
		{
			name: "aggregate update exit", args: []string{"jobset", "--fail-on-update"},
			tags: jobsetTags, wantRows: map[string]string{"jobset": "NEW"},
			wantRepos: []string{jobset},
		},
		{
			name: "Grove defaults to promoted alpha releases", script: "operators/grove/check-updates.sh",
			args: []string{"--fail-on-update"}, pins: map[string]string{"GROVE_VERSION": "v0.1.0-alpha.10-rc1"},
			tags:     map[string][]string{grove: {"v0.1.0-alpha.10-rc1", "v0.1.0-alpha.10", "v0.1.0-alpha.11-rc1"}},
			wantRows: map[string]string{"grove": "NEW"}, wantRepos: []string{grove},
			wantOutput: []string{"1 newer tag(s)"},
		},
		{
			name: "Grove release candidates require flag", script: "operators/grove/check-updates.sh",
			args: []string{"--include-prereleases", "--fail-on-update"}, pins: map[string]string{"GROVE_VERSION": "v0.1.0-alpha.10"},
			tags:     map[string][]string{grove: {"v0.1.0-alpha.10", "v0.1.0-alpha.11-rc1"}},
			wantRows: map[string]string{"grove": "NEW"}, wantRepos: []string{grove},
			wantOutput: []string{"v0.1.0-alpha.11-rc1"},
		},
	}, 2)
}

func (h scriptHarness) testFailures(t *testing.T) {
	jobset := "kubernetes-sigs/jobset"
	serving := "knative/serving"
	kourier := "knative-extensions/net-kourier"
	h.runCases(t, []scriptCase{
		{
			name: "invalid selector before lookup or build", args: []string{"jobset", "unknown"},
			wantOutput: []string{"unknown operator"}, noBinary: true,
		},
		{
			name: "invalid aggregate flag before lookup or build", args: []string{"jobset", "--unsupported"},
			wantOutput: []string{"unknown flag"}, noBinary: true,
		},
		{
			name: "standalone unknown flag is an error", script: "operators/jobset/check-updates.sh",
			args: []string{"--unsupported"},
		},
		{
			name: "standalone multi-component failure continues", script: "operators/knative/check-updates.sh",
			args: []string{"--fail-on-update"}, tags: map[string][]string{serving: nil, kourier: {"knative-v1.1.0"}},
			wantRows:  map[string]string{"knative": "ERROR", "kourier": "NEW"},
			wantRepos: []string{serving, kourier},
		},
		{
			name: "partial group retains update in summary", args: []string{"knative", "--fail-on-update"},
			tags:     map[string][]string{serving: {"knative-v1.1.0"}, kourier: nil},
			wantRows: map[string]string{"knative": "NEW", "kourier": "ERROR"}, wantRepos: []string{serving, kourier},
			wantOutput: []string{"New tags in operator groups: knative", "report is incomplete"},
		},
		{
			name: "aggregate failure precedes successful updates", args: []string{"jobset", "knative", "--fail-on-update"},
			tags:      map[string][]string{jobset: nil, serving: {"knative-v1.0.0"}, kourier: {"knative-v1.1.0"}},
			wantRows:  map[string]string{"jobset": "ERROR", "knative": "CURRENT", "kourier": "NEW"},
			wantRepos: []string{jobset, serving, kourier}, wantOutput: []string{"report is incomplete"},
		},
		{
			name: "aggregate failure follows successful updates", args: []string{"jobset", "knative", "--fail-on-update"},
			tags:      map[string][]string{jobset: {"v1.1.0"}, serving: nil, kourier: {"knative-v1.0.0"}},
			wantRows:  map[string]string{"jobset": "NEW", "knative": "ERROR", "kourier": "CURRENT"},
			wantRepos: []string{jobset, serving, kourier}, wantOutput: []string{"New tags in operator groups: jobset"},
		},
	}, 1)
}

func (h scriptHarness) runCases(t *testing.T, cases []scriptCase, wantExit int) {
	t.Helper()
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result := h.run(t, test)
			if result.exitCode != wantExit {
				t.Fatalf("exit = %d, want %d\n%s", result.exitCode, wantExit, result.output)
			}
			for _, want := range test.wantOutput {
				if !strings.Contains(result.output, want) {
					t.Errorf("output missing %q\n%s", want, result.output)
				}
			}
			for name, status := range test.wantRows {
				hasRow := false
				for line := range strings.SplitSeq(result.output, "\n") {
					fields := strings.Fields(line)
					if len(fields) >= 2 && fields[0] == status && fields[1] == name {
						hasRow = true
					}
				}
				if !hasRow {
					t.Errorf("missing %s row for %s\n%s", status, name, result.output)
				}
			}
			slices.Sort(result.repos)
			wantRepos := slices.Clone(test.wantRepos)
			slices.Sort(wantRepos)
			if !slices.Equal(result.repos, wantRepos) {
				t.Errorf("queried repositories = %v, want %v", result.repos, wantRepos)
			}
		})
	}
}

func (h scriptHarness) testInventory(t *testing.T) {
	prefixes := map[string]string{
		"cert-manager/cert-manager": "v", "run-ai/fake-gpu-operator": "v", "kai-scheduler/KAI-Scheduler": "v",
		"kubernetes-sigs/lws": "v", "kubernetes-sigs/jobset": "v", "ray-project/kuberay-helm": "kuberay-operator-",
		"kubeflow/trainer": "v", "kubeflow/mpi-operator": "v", "knative/serving": "knative-v",
		"knative-extensions/net-kourier": "knative-v", "kserve/kserve": "v", "zilliztech/milvus-operator": "milvus-operator-",
		"ai-dynamo/grove": "v", "ai-dynamo/dynamo": "v", "NVIDIA/k8s-nim-operator": "v",
		"kubernetes-sigs/agent-sandbox": "v",
	}
	tags := make(map[string][]string, len(prefixes))
	for repository, prefix := range prefixes {
		tags[repository] = []string{prefix + "999.0.0", "unrelated-component-9999.0.0"}
	}
	t.Run("all components use installer release namespaces", func(t *testing.T) {
		for _, args := range [][]string{nil, {"all"}} {
			result := h.run(t, scriptCase{tags: tags, args: args})
			if result.exitCode != 0 || len(result.repos) != len(prefixes) {
				t.Fatalf("exit = %d, queries = %v\n%s", result.exitCode, result.repos, result.output)
			}
			if strings.Count(result.output, "NEW ") != len(prefixes) {
				t.Errorf("expected an update for all components\n%s", result.output)
			}
			seen := make(map[string]bool)
			for _, repository := range result.repos {
				if _, exists := prefixes[repository]; !exists || seen[repository] {
					t.Errorf("unexpected or duplicate repository %s", repository)
				}
				seen[repository] = true
			}
		}
	})
	t.Run("every operator has an executable registered checker", func(t *testing.T) {
		installers, err := filepath.Glob(filepath.Join(h.root, "hack", "e2e", "operators", "*", "install.sh"))
		if err != nil {
			t.Fatal(err)
		}
		if len(installers) == 0 {
			t.Fatal("no workload installers found")
		}
		directories := make([]string, 0, len(installers))
		for _, installer := range installers {
			directories = append(directories, filepath.Dir(installer))
		}
		infra := filepath.Join(h.root, "hack", "e2e", "infra")
		entries, err := os.ReadDir(infra)
		if err != nil {
			t.Fatal(err)
		}
		var expectedInfra []string
		for _, entry := range entries {
			if entry.IsDir() {
				expectedInfra = append(expectedInfra, entry.Name())
				directories = append(directories, filepath.Join(infra, entry.Name()))
			}
		}
		result := h.run(t, scriptCase{args: []string{"--help"}, noBinary: true})
		if result.exitCode != 0 {
			t.Fatalf("read aggregate inventory: %s", result.output)
		}
		var registered []string
		for line := range strings.SplitSeq(result.output, "\n") {
			if line, found := strings.CutPrefix(strings.TrimSpace(line), "Operators:"); found {
				registered = strings.Fields(line)
			}
		}
		expected := make([]string, 0, len(directories))
		for _, directory := range directories {
			name := filepath.Base(directory)
			expected = append(expected, name)
			info, err := os.Stat(filepath.Join(directory, "check-updates.sh"))
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
				t.Errorf("operator %s needs an executable check-updates.sh: %v", name, err)
			}
		}
		slices.Sort(expected)
		slices.Sort(registered)
		if !slices.Equal(expected, registered) {
			t.Errorf("aggregate registered groups = %v, installer/infra groups = %v", registered, expected)
		}
		infraResult := h.run(t, scriptCase{args: []string{"infra"}, tags: tags})
		if infraResult.exitCode != 0 {
			t.Fatalf("run infra alias: exit %d\n%s", infraResult.exitCode, infraResult.output)
		}
		var reportedInfra []string
		for line := range strings.SplitSeq(infraResult.output, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			switch fields[0] {
			case "NEW", "CURRENT", "AHEAD", "ERROR":
				reportedInfra = append(reportedInfra, fields[1])
			}
		}
		slices.Sort(expectedInfra)
		slices.Sort(reportedInfra)
		if !slices.Equal(expectedInfra, reportedInfra) {
			t.Errorf("infra alias reports = %v, infrastructure directories = %v", reportedInfra, expectedInfra)
		}
	})
}

func TestOperatorUpdatesStandaloneBuild(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fixture := t.TempDir()
	files := []string{
		"hack/operator-updates/go.mod",
		"hack/e2e/_updates.sh",
		"hack/e2e/global.env",
		"hack/e2e/operators/jobset/check-updates.sh",
	}
	sources, err := filepath.Glob(filepath.Join(root, "hack", "operator-updates", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if !strings.HasSuffix(source, "_test.go") {
			files = append(files, filepath.Join("hack", "operator-updates", filepath.Base(source)))
		}
	}
	for _, file := range files {
		content, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(fixture, file)
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// An unavailable product module must not prevent this standalone tool from building.
	workspace := filepath.Join(fixture, "go.work")
	if err := os.WriteFile(workspace, []byte("go 1.25.0\nuse ./missing-product\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeGit := "#!/bin/sh\nprintf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\trefs/tags/v1.1.0\\n'\n"
	if err := os.WriteFile(filepath.Join(fixture, "git"), []byte(fakeGit), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", filepath.Join(fixture, "hack", "e2e", "operators", "jobset", "check-updates.sh"))
	command.Dir = t.TempDir()
	command.Env = append(os.Environ(),
		"PATH="+fixture+string(os.PathListSeparator)+os.Getenv("PATH"),
		"E2E_OPERATOR_UPDATES_BIN=", "JOBSET_VERSION=v1.0.0", "GO_VERSION="+strings.TrimPrefix(runtime.Version(), "go"),
		"GOWORK="+workspace, "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local",
		"GOFLAGS=-mod=readonly", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run standalone wrapper: %v\n%s", err, output)
	}
	for _, want := range []string{"NEW", "jobset", "v1.1.0"} {
		if !strings.Contains(string(output), want) {
			t.Errorf("output missing %q\n%s", want, output)
		}
	}
}

func (h scriptHarness) run(t *testing.T, test scriptCase) scriptResult {
	t.Helper()
	directory := t.TempDir()
	logPath := filepath.Join(directory, "git-queries")
	forbiddenPath := filepath.Join(directory, "forbidden-tools")
	process, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fakeGit := "#!/bin/sh\nexec \"$KARTA_UPDATE_TEST_PROCESS\" -test.run='^TestOperatorUpdatesFakeGit$' -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(directory, "git"), []byte(fakeGit), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"kubectl", "helm", "kind", "docker", "curl", "wget", "go", "python3"} {
		script := "#!/bin/sh\nprintf '%s\\n' \"$0\" >> \"$KARTA_UPDATE_TEST_FORBIDDEN\"\nexit 97\n"
		if err := os.WriteFile(filepath.Join(directory, tool), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fixtures, err := json.Marshal(test.tags)
	if err != nil {
		t.Fatal(err)
	}
	binary := h.binary
	if test.noBinary {
		binary = ""
	}
	env := append(os.Environ(), "PATH="+directory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"KARTA_UPDATE_TEST_GIT=1", "KARTA_UPDATE_TEST_PROCESS="+process, "KARTA_UPDATE_TEST_LOG="+logPath,
		"KARTA_UPDATE_TEST_TAGS="+string(fixtures), "KARTA_UPDATE_TEST_FORBIDDEN="+forbiddenPath,
		"E2E_OPERATOR_UPDATES_BIN="+binary, "JOBSET_VERSION=v1.0.0", "KNATIVE_VERSION=knative-v1.0.0",
		"KOURIER_VERSION=knative-v1.0.0", "CERT_MANAGER_VERSION=v1.0.0", "FAKE_GPU_VERSION=1.0.0",
		"KAI_SCHEDULER_VERSION=v0.15.2", "LWS_VERSION=v1.0.0", "KUBERAY_VERSION=1.0.0",
		"KUBEFLOW_VERSION=v1.0.0", "MPI_OPERATOR_VERSION=v1.0.0", "KSERVE_VERSION=v1.0.0",
		"MILVUS_OPERATOR_VERSION=1.0.0", "GROVE_VERSION=v0.1.0-alpha.1", "DYNAMO_VERSION=1.0.0",
		"NIM_OPERATOR_VERSION=v1.0.0", "AGENT_SANDBOX_VERSION=v1.0.0", "GOPROXY=off", "GOSUMDB=off")
	for name, value := range test.pins {
		env = append(env, name+"="+value)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var command *exec.Cmd
	if test.makeTarget {
		args := append([]string{"-C", h.root, "e2e-operators-check-latest-supported-versions"}, test.args...)
		command = exec.CommandContext(ctx, "make", args...)
	} else {
		script := test.script
		if script == "" {
			script = "check-updates.sh"
		}
		args := append([]string{filepath.Join(h.root, "hack", "e2e", script)}, test.args...)
		command = exec.CommandContext(ctx, "bash", args...)
	}
	command.Dir = directory
	command.Env = env
	output, err := command.CombinedOutput()
	result := scriptResult{output: string(output)}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || ctx.Err() != nil {
			t.Fatalf("run checker: %v\n%s", err, output)
		}
		result.exitCode = exit.ExitCode()
	}
	if content, err := os.ReadFile(forbiddenPath); err == nil {
		t.Fatalf("checker attempted to invoke forbidden tools:\n%s\n%s", content, output)
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(logPath); err == nil {
		result.repos = strings.Fields(string(content))
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return result
}
