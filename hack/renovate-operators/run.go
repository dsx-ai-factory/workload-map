// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type updateOptions struct {
	Root, Group, BaseBranch, BaseSHA, Artifacts, RunURL string
	RunID, RunAttempt, RunnerTemp, Repository           string
	Author, TrackingIssue, Token                        string
}

type updatePR struct {
	HeadRefOid, URL            string
	IsDraft, IsCrossRepository bool
}

var (
	issuePattern = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/issues/([1-9][0-9]*)$`)
	ciRunPattern = regexp.MustCompile(`^[0-9]+$`)
)

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

func runUpdates(ctx context.Context, opts updateOptions, binary string, commands executor) error {
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
	plan, err := makePlan(ctx, planOptions{Root: opts.Root, Binary: binary, Group: opts.Group, BaseBranch: opts.BaseBranch})
	if err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(opts.Artifacts, "plan.json"), plan); err != nil {
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
	env["RENOVATE_CONFIG_FILE"] = configPath
	env["RENOVATE_BASE_DIR"] = filepath.Join(opts.RunnerTemp, "renovate-"+opts.Group)
	env["RENOVATE_EXIT_CODE_FOR_ERRORS"] = "true"
	commands.env = environmentList(env)
	if err := commands.logged(ctx, filepath.Join(opts.Artifacts, "renovate.log"), "renovate"); err != nil {
		return err
	}

	// Continue in this run: PRs made with the fork's GITHUB_TOKEN do not trigger CI.
	var prs []updatePR
	if err := commands.outputJSON(ctx, &prs, "gh", "pr", "list", "--repo", opts.Repository,
		"--base", opts.BaseBranch, "--head", plan.Branch, "--state", "open", "--json", "headRefOid,url,isDraft,isCrossRepository"); err != nil {
		return err
	}
	if len(prs) == 0 {
		return writeSummary("No open "+opts.Group+" update needs recording.\n", commands)
	}
	if len(prs) != 1 || !shaPattern.MatchString(prs[0].HeadRefOid) || prs[0].IsCrossRepository {
		return errors.New("expected one Renovate PR in this repository with a full head SHA")
	}
	head := prs[0].HeadRefOid
	if err := writeSummary("Operator PR: "+prs[0].URL+"\n\nRecording run: "+opts.RunURL+"\n", commands); err != nil {
		return err
	}
	needed, err := needsRecording(ctx, opts, prs[0], commands)
	if err != nil {
		return err
	}
	if !needed {
		return writeSummary("This PR head already has successful CI recordings.\n", commands)
	}
	base, err := checkoutCandidate(ctx, opts.BaseBranch, head, commands)
	if err != nil {
		return err
	}
	opts.BaseSHA = base
	return recordPullRequest(ctx, opts, plan, head, commands)
}

func needsRecording(ctx context.Context, opts updateOptions, pr updatePR, commands executor) (bool, error) {
	var statuses []struct{ Context, State string }
	if err := commands.outputJSON(ctx, &statuses, "gh", "api", "repos/"+opts.Repository+"/commits/"+pr.HeadRefOid+"/statuses"); err != nil {
		return false, err
	}
	for _, status := range statuses {
		if status.Context == "e2e/operator-recording" {
			if status.State == "success" {
				return false, nil
			}
			break
		}
	}
	// Renovate's draftPR setting applies only when a PR is first created.
	if !pr.IsDraft {
		if err := commands.run(ctx, "gh", "pr", "ready", "--undo", pr.URL); err != nil {
			return false, errors.Join(err, setRecordingStatus(ctx, opts, pr.HeadRefOid, "failure", commands))
		}
	}
	return true, nil
}

func checkoutCandidate(ctx context.Context, baseBranch, head string, commands executor) (string, error) {
	if err := commands.run(ctx, "git", "fetch", "origin", head); err != nil {
		return "", err
	}
	// Renovate may have used a newer base than the workflow's planning job.
	if err := commands.run(ctx, "git", "fetch", "origin", "refs/heads/"+baseBranch); err != nil {
		return "", err
	}
	base, err := commands.output(ctx, "git", "merge-base", "FETCH_HEAD", head)
	if err != nil {
		return "", err
	}
	if err := commands.run(ctx, "git", "checkout", "--detach", head); err != nil {
		return "", err
	}
	return strings.TrimSpace(string(base)), nil
}

func recordPullRequest(ctx context.Context, opts updateOptions, plan updatePlan, head string, commands executor) (result error) {
	if err := setRecordingStatus(ctx, opts, head, "pending", commands); err != nil {
		return err
	}
	defer func() {
		if result != nil {
			statusCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()
			result = errors.Join(result, setRecordingStatus(statusCtx, opts, head, "failure", commands))
		}
	}()
	record := commands
	env := environment(record.env)
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN", "RENOVATE_TOKEN"} {
		delete(env, key)
	}
	record.env = environmentList(env)
	if err := checkArtifactDirectory(opts.Root, opts.Artifacts); err != nil {
		return err
	}
	candidate, err := prepareCandidate(ctx, opts.Root, plan, opts.BaseSHA)
	if err != nil {
		return err
	}
	if err := recordUpdate(ctx, opts, plan, candidate, record); err != nil {
		return err
	}
	if err := commitRecordings(ctx, opts, candidate, record); err != nil {
		return err
	}
	if err := record.logged(ctx, filepath.Join(opts.Artifacts, "make-check.log"), "make", "check", "VERSION=0.0.0-main-"+opts.BaseSHA[:7]); err != nil {
		return err
	}
	if err := publishRecordings(ctx, opts, plan.Branch, head, commands); err != nil {
		return err
	}
	return writeSummary("Recordings, replay, presubmit and cluster cleanup passed; the bot recording commit is on the draft PR.\n", commands)
}

func commitRecordings(ctx context.Context, opts updateOptions, candidate candidate, commands executor) error {
	if err := validateCandidateChanges(ctx, opts.Root, candidate); err != nil {
		return err
	}
	files, err := candidateFiles(opts.Root, candidate)
	if err != nil {
		return err
	}
	if err := commands.run(ctx, append([]string{"git", "add", "--"}, files...)...); err != nil {
		return err
	}
	author := authorPattern.FindStringSubmatch(opts.Author)
	if author == nil {
		return errors.New("bot author must have the form Name <email>")
	}
	// An identical re-recording still gets a commit tying it to this CI run.
	return commands.run(ctx, "git", "-c", "user.name="+author[1], "-c", "user.email="+author[2],
		"-c", "commit.gpgsign=false", "commit", "--allow-empty", "-s", "-m", "test(e2e): record "+opts.Group+" operator update", "-m", "Recording and validation: "+opts.RunURL)
}

func publishRecordings(ctx context.Context, opts updateOptions, branch, head string, commands executor) error {
	dirty, err := commands.output(ctx, "git", "status", "--porcelain")
	if err != nil {
		return err
	}
	if len(dirty) != 0 {
		return errors.New("validation changed the tested commit; refusing to push")
	}
	commit, err := commands.output(ctx, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	parent, err := commands.output(ctx, "git", "show", "-s", "--format=%P", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(parent)) != head {
		return errors.New("recording commit must be directly on the fetched PR head")
	}

	if err := commands.run(ctx, "gh", "auth", "setup-git", "--hostname", "github.com"); err != nil {
		return err
	}
	// The lease rejects a concurrent update, including a rebase, without overwriting it.
	if err := commands.run(ctx, "git", "push", "--force-with-lease=refs/heads/"+branch+":"+head, "origin", strings.TrimSpace(string(commit))+":refs/heads/"+branch); err != nil {
		return err
	}
	return setRecordingStatus(ctx, opts, strings.TrimSpace(string(commit)), "success", commands)
}

func setRecordingStatus(ctx context.Context, opts updateOptions, sha, state string, commands executor) error {
	return commands.run(ctx, "gh", "api", "repos/"+opts.Repository+"/statuses/"+sha,
		"-f", "state="+state, "-f", "context=e2e/operator-recording", "-f", "target_url="+opts.RunURL,
		"-f", "description=CI operator recordings: "+state)
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
