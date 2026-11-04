// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"
)

type updateOptions struct {
	Root, Group, BaseBranch, BaseSHA, Artifacts, RunURL string
	RunID, RunAttempt, RunnerTemp, Repository           string
	Author, TrackingIssue, Token                        string
}

type hookSettings struct {
	ManifestPath string `json:"manifestPath"`
	BaseSHA      string `json:"baseSHA"`
	Author       string `json:"author"`
	Branch       string `json:"branch"`
}

var (
	issuePattern = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/issues/([1-9][0-9]*)$`)
	ciRunPattern = regexp.MustCompile(`^[0-9]+$`)
)

//go:embed pull_request.md.tmpl
var pullRequestTemplate string

func optionsFromEnvironment(ctx context.Context, publish bool) (updateOptions, error) {
	var opts updateOptions
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		return opts, errors.New("operator updates run in GitHub Actions")
	}
	required := map[string]*string{
		"E2E_UPDATE_GROUP": &opts.Group, "E2E_UPDATE_BASE_BRANCH": &opts.BaseBranch,
		"E2E_UPDATE_BASE_SHA": &opts.BaseSHA, "E2E_UPDATE_ARTIFACTS": &opts.Artifacts,
		"E2E_UPDATE_RUN_URL": &opts.RunURL, "GITHUB_RUN_ID": &opts.RunID,
		"GITHUB_RUN_ATTEMPT": &opts.RunAttempt, "RUNNER_TEMP": &opts.RunnerTemp,
		"GITHUB_REPOSITORY": &opts.Repository,
	}
	if publish {
		required["RENOVATE_GIT_AUTHOR"] = &opts.Author
		required["E2E_OPERATOR_TRACKING_ISSUE"] = &opts.TrackingIssue
		required["RENOVATE_TOKEN"] = &opts.Token
	}
	for name, value := range required {
		*value = os.Getenv(name)
		if *value == "" {
			return opts, fmt.Errorf("%s is required", name)
		}
	}
	if !shaPattern.MatchString(opts.BaseSHA) {
		return opts, errors.New("E2E_UPDATE_BASE_SHA must be a full commit SHA")
	}
	if !ciRunPattern.MatchString(opts.RunID) || !ciRunPattern.MatchString(opts.RunAttempt) {
		return opts, errors.New("GitHub run ID and attempt must be numeric")
	}
	if _, err := operatorGroups(opts.Group); err != nil {
		return opts, err
	}
	if opts.Group == "all" {
		return opts, errors.New("run and record require one operator group")
	}
	root, err := gitOutput(ctx, ".", "rev-parse", "--show-toplevel")
	if err != nil {
		return opts, err
	}
	opts.Root = strings.TrimSpace(string(root))
	return opts, nil
}

func checkArtifactDirectory(root, artifacts string) error {
	if !filepath.IsAbs(artifacts) {
		return errors.New("artifact directory must be absolute")
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	artifacts, err = filepath.EvalSymlinks(artifacts)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, artifacts)
	if err != nil {
		return err
	}
	if relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("artifact directory must be outside the repository")
	}
	return nil
}

func runUpdates(ctx context.Context, opts updateOptions, binary string, commands executor) (result error) {
	env := environment(commands.env)
	env["GH_TOKEN"] = opts.Token
	commands.env = environmentList(env)
	if err := verifyTrackingIssue(ctx, opts.TrackingIssue, commands); err != nil {
		return err
	}
	if err := os.MkdirAll(opts.Artifacts, 0o755); err != nil {
		return err
	}
	if err := checkArtifactDirectory(opts.Root, opts.Artifacts); err != nil {
		return err
	}
	planPath := filepath.Join(opts.Artifacts, "plan.json")
	if _, err := os.Lstat(planPath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("evidence directory must be new for each job attempt")
	}
	plan, err := makePlan(ctx, planOptions{Root: opts.Root, Binary: binary, Group: opts.Group, BaseBranch: opts.BaseBranch})
	if err != nil {
		return err
	}
	if err := writeJSONFile(planPath, plan); err != nil {
		return err
	}
	config, err := renovateConfig(plan, env)
	if err != nil {
		return err
	}
	configPath := filepath.Join(opts.Artifacts, "renovate.json")
	if err := writeJSONFile(configPath, config); err != nil {
		return err
	}
	cleanup, err := installPushGuard(ctx, opts, binary, plan.Branch, commands)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, cleanup()) }()
	env["RENOVATE_CONFIG_FILE"] = configPath
	env["RENOVATE_BASE_DIR"] = filepath.Join(opts.RunnerTemp, "renovate-"+opts.Group)
	env["RENOVATE_EXIT_CODE_FOR_ERRORS"] = "true"
	commands.env = environmentList(env)
	if err := commands.logged(ctx, filepath.Join(opts.Artifacts, "renovate.log"), "renovate"); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(opts.Artifacts, "record-started")); errors.Is(err, os.ErrNotExist) {
		return writeSummary(fmt.Sprintf("No new %s candidate required recording.\n", opts.Group), commands)
	} else if err != nil {
		return err
	}
	shaOutput, err := commands.output(ctx, "gh", "api", "repos/"+opts.Repository+"/git/ref/heads/"+plan.Branch, "--jq", ".object.sha")
	if err != nil {
		return err
	}
	sha := strings.TrimSpace(string(shaOutput))
	if !shaPattern.MatchString(sha) {
		return fmt.Errorf("invalid candidate commit %q", sha)
	}
	if err := commands.run(ctx, "git", "fetch", "origin", sha); err != nil {
		return err
	}
	if err := verifyCommit(ctx, verifyOptions{Root: opts.Root, ManifestPath: filepath.Join(opts.Artifacts, "manifest.json"), BaseSHA: opts.BaseSHA, Author: opts.Author, Commit: sha}); err != nil {
		return err
	}
	if err := commands.run(ctx, "gh", "api", "repos/"+opts.Repository+"/statuses/"+sha,
		"-f", "state=success", "-f", "context=e2e/operator-recording", "-f", "target_url="+opts.RunURL,
		"-f", "description=Fresh kind install, complete recordings, replay, presubmit and cleanup passed"); err != nil {
		return err
	}
	return publishPullRequest(ctx, opts, plan, commands)
}

func verifyTrackingIssue(ctx context.Context, url string, commands executor) error {
	match := issuePattern.FindStringSubmatch(url)
	if match == nil {
		return errors.New("tracking issue must be a public GitHub issue URL")
	}
	var repository struct {
		Private *bool `json:"private"`
	}
	if err := commands.outputJSON(ctx, &repository, "gh", "api", "repos/"+match[1]); err != nil {
		return err
	}
	var issue struct {
		State       string          `json:"state"`
		PullRequest json.RawMessage `json:"pull_request"`
	}
	if err := commands.outputJSON(ctx, &issue, "gh", "api", "repos/"+match[1]+"/issues/"+match[2]); err != nil {
		return err
	}
	if repository.Private == nil || *repository.Private || issue.State != "open" || len(issue.PullRequest) != 0 {
		return errors.New("tracking issue must be public and open")
	}
	return nil
}

func installPushGuard(ctx context.Context, opts updateOptions, binary, branch string, commands executor) (func() error, error) {
	previous, err := commands.output(ctx, "git", "config", "--global", "--get", "core.hooksPath")
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return nil, err
		}
	}
	if strings.TrimSpace(string(previous)) != "" {
		return nil, errors.New("runner already has a global Git hook path")
	}
	hooks, err := os.MkdirTemp(opts.RunnerTemp, "operator-hooks-")
	if err != nil {
		return nil, err
	}
	settings := hookSettings{ManifestPath: filepath.Join(opts.Artifacts, "manifest.json"), BaseSHA: opts.BaseSHA, Author: opts.Author, Branch: branch}
	if err = writeJSONFile(filepath.Join(hooks, "settings.json"), settings); err == nil {
		err = copyExecutable(binary, filepath.Join(hooks, "pre-push"))
	}
	if err == nil {
		// Renovate rejects custom GIT_CONFIG_* paths. Use the hosted runner's normal configuration.
		err = commands.run(ctx, "git", "config", "--global", "core.hooksPath", hooks)
	}
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(hooks))
	}
	return func() error {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		err := commands.run(cleanupCtx, "git", "config", "--global", "--unset", "core.hooksPath")
		return errors.Join(err, os.RemoveAll(hooks))
	}, nil
}

func publishPullRequest(ctx context.Context, opts updateOptions, plan updatePlan, commands executor) error {
	var update candidate
	if err := readJSONFile(filepath.Join(opts.Artifacts, "candidate.json"), &update); err != nil {
		return err
	}
	bodyTemplate, err := template.New("pull-request").Parse(pullRequestTemplate)
	if err != nil {
		return err
	}
	var body bytes.Buffer
	if err := bodyTemplate.Execute(&body, struct {
		Candidate     candidate
		RunURL, Issue string
	}{update, opts.RunURL, opts.TrackingIssue}); err != nil {
		return err
	}
	bodyPath := filepath.Join(opts.Artifacts, "pr-body.md")
	if err := os.WriteFile(bodyPath, body.Bytes(), 0o644); err != nil {
		return err
	}
	var existing []struct{ Number int }
	if err := commands.outputJSON(ctx, &existing, "gh", "pr", "list", "--repo", opts.Repository, "--base", opts.BaseBranch, "--head", plan.Branch, "--state", "open", "--json", "number"); err != nil {
		return err
	}
	var url []byte
	if len(existing) > 0 {
		number := strconv.Itoa(existing[0].Number)
		if err := commands.run(ctx, "gh", "pr", "edit", number, "--repo", opts.Repository, "--body-file", bodyPath); err != nil {
			return err
		}
		url, err = commands.output(ctx, "gh", "pr", "view", number, "--repo", opts.Repository, "--json", "url", "--jq", ".url")
	} else {
		url, err = commands.output(ctx, "gh", "pr", "create", "--repo", opts.Repository, "--base", opts.BaseBranch, "--head", plan.Branch, "--draft", "--title", "build(e2e): update "+opts.Group+" with CI recordings", "--body-file", bodyPath)
	}
	if err != nil {
		return err
	}
	prURL := strings.TrimSpace(string(url))
	if err := os.WriteFile(filepath.Join(opts.Artifacts, "pr-url.txt"), []byte(prURL+"\n"), 0o644); err != nil {
		return err
	}
	return writeSummary(fmt.Sprintf("Validated operator update: %s\n\nRecording run: %s\n", prURL, opts.RunURL), commands)
}

func writeSummary(message string, commands executor) error {
	if _, err := fmt.Fprint(commands.stdout, message); err != nil {
		return err
	}
	path := environment(commands.env)["GITHUB_STEP_SUMMARY"]
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = file.WriteString(message)
	return errors.Join(err, file.Close())
}
