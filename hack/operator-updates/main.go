// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package main reports upstream operator tags without changing pins or contacting Kubernetes.
package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
)

const header = "STATUS   OPERATOR             PINNED                       LATEST TAG"

const usage = `Usage: operator-updates --operator NAME PIN REPO PREFIX CHANNEL NOTE [options]

Report upstream operator tags without changing pins or contacting Kubernetes.

Options:
  --operator NAME PIN REPO PREFIX CHANNEL NOTE
                          operator to check (required, repeatable)
  --include-prereleases   also report prerelease tags (Grove includes alpha releases by default)
  --fail-on-update        exit 2 when updates are found; lookup errors always exit 1
  --exclude-tag TAG       exclude a known retracted upstream tag (repeatable)
  -h, --help              show this help
`

var repositoryPattern = regexp.MustCompile(`\A[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\z`)

type operator struct {
	name, pinned, repository, prefix, channel, note string
}

type options struct {
	operators          []operator
	excludedTags       []string
	includePrereleases bool
	failOnUpdate       bool
	noHeader           bool
	help               bool
}

type releaseSelection struct {
	latestTag string
	newer     int
	status    string
}

type tagLookup func(context.Context, string) ([]string, error)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, remoteTags))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, lookup tagLookup) int {
	opts, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "%s\nerror: %v\n", usage, err)
		return 1
	}
	if opts.help {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if !opts.noHeader {
		fmt.Fprintln(stdout, header)
	}
	var updated, failed bool
	for _, op := range opts.operators {
		newer, err := checkOperator(ctx, op, opts, stdout, lookup)
		if err != nil {
			fmt.Fprintf(stdout, "%-8s %-20s %-28s -\n", "ERROR", op.name, op.pinned)
			fmt.Fprintf(stderr, "  %s: %v\n", op.name, err)
			failed = true
			continue
		}
		updated = updated || newer
	}
	if failed {
		return 1
	}
	if updated && opts.failOnUpdate {
		return 2
	}
	return 0
}

func parseArgs(args []string) (options, error) {
	var opts options
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch {
		case arg == "--operator":
			op, err := parseOperatorArgs(args)
			if err != nil {
				return opts, err
			}
			opts.operators = append(opts.operators, op)
			args = args[6:]
		case arg == "--exclude-tag":
			if len(args) == 0 || strings.HasPrefix(args[0], "--") {
				return opts, fmt.Errorf("--exclude-tag requires TAG")
			}
			opts.excludedTags = append(opts.excludedTags, args[0])
			args = args[1:]
		case strings.HasPrefix(arg, "--exclude-tag="):
			opts.excludedTags = append(opts.excludedTags, strings.TrimPrefix(arg, "--exclude-tag="))
		case arg == "--include-prereleases":
			opts.includePrereleases = true
		case arg == "--fail-on-update":
			opts.failOnUpdate = true
		case arg == "--no-header":
			opts.noHeader = true
		case arg == "--help" || arg == "-h":
			opts.help = true
			return opts, nil
		default:
			return opts, fmt.Errorf("unknown argument: %s", arg)
		}
	}
	if len(opts.operators) == 0 {
		return opts, fmt.Errorf("--operator is required")
	}
	return opts, nil
}

func parseOperatorArgs(args []string) (operator, error) {
	if len(args) < 6 {
		return operator{}, fmt.Errorf("--operator requires NAME PIN REPO PREFIX CHANNEL NOTE")
	}
	for _, value := range args[:6] {
		if strings.HasPrefix(value, "--") {
			return operator{}, fmt.Errorf("--operator requires NAME PIN REPO PREFIX CHANNEL NOTE")
		}
	}
	return operator{
		name: args[0], pinned: args[1], repository: args[2],
		prefix: args[3], channel: args[4], note: args[5],
	}, nil
}

func checkOperator(ctx context.Context, op operator, opts options, stdout io.Writer, lookup tagLookup) (bool, error) {
	if op.channel != "stable" && op.channel != "alpha" {
		return false, fmt.Errorf("invalid release channel: %s", op.channel)
	}
	if !repositoryPattern.MatchString(op.repository) {
		return false, fmt.Errorf("invalid GitHub repository: %s", op.repository)
	}
	pinned := strings.TrimPrefix(op.pinned, op.prefix)
	if op.prefix == "" || pinned == op.pinned {
		pinned = strings.TrimPrefix(op.pinned, "v")
	}
	current, err := parseRelease(pinned, op.channel)
	if err != nil {
		return false, err
	}
	tags, err := lookup(ctx, op.repository)
	if err != nil {
		return false, err
	}
	selected, err := selectLatestRelease(op, current, tags, opts)
	if err != nil {
		return false, err
	}
	writeOperatorReport(stdout, op, pinned, tags, selected)
	return selected.newer > 0, nil
}

func selectLatestRelease(op operator, current version, tags []string, opts options) (releaseSelection, error) {
	var latest version
	selected := releaseSelection{status: "CURRENT"}
	for _, tag := range tags {
		raw, matches := strings.CutPrefix(tag, op.prefix)
		if !matches || slices.Contains(opts.excludedTags, tag) {
			continue
		}
		candidate, err := parseRelease(raw, op.channel)
		if err != nil {
			continue
		}
		isAlpha := op.channel == "alpha" && alphaPattern.MatchString(raw)
		if len(candidate.prerelease) > 0 && !isAlpha && !opts.includePrereleases {
			continue
		}
		if selected.latestTag == "" || candidate.compare(latest) > 0 || (candidate.compare(latest) == 0 && tag > selected.latestTag) {
			latest, selected.latestTag = candidate, tag
		}
		if candidate.compare(current) > 0 {
			selected.newer++
		}
	}
	if selected.latestTag == "" {
		return selected, fmt.Errorf("no matching %s tags in %s (prefix '%s')", op.channel, op.repository, op.prefix)
	}
	if selected.newer > 0 {
		selected.status = "NEW"
	} else if current.compare(latest) > 0 {
		selected.status = "AHEAD"
	}
	return selected, nil
}

func writeOperatorReport(stdout io.Writer, op operator, pinned string, tags []string, selected releaseSelection) {
	fmt.Fprintf(stdout, "%-8s %-20s %-28s %s\n", selected.status, op.name, op.pinned, selected.latestTag)
	base := "https://github.com/" + op.repository
	fmt.Fprintf(stdout, "  Tags: %s/tags\n", base)
	if selected.newer > 0 {
		currentTag := op.prefix + pinned
		fmt.Fprintf(stdout, "  %d newer tag(s)\n", selected.newer)
		if slices.Contains(tags, currentTag) {
			fmt.Fprintf(stdout, "  Changes: %s/compare/%s...%s\n", base, escapeTag(currentTag), escapeTag(selected.latestTag))
		} else {
			fmt.Fprintf(stdout, "  Pinned tag %s was not found; no comparison is available.\n", currentTag)
		}
	}
	if op.note != "" {
		fmt.Fprintf(stdout, "  Note: %s\n", op.note)
	}
}

func escapeTag(tag string) string {
	return strings.ReplaceAll(url.QueryEscape(tag), "+", "%20")
}

func remoteTags(ctx context.Context, repository string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-c", "credential.helper=", "ls-remote", "--tags", "--refs",
		"https://github.com/"+repository+".git")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("cannot read tags from %s: %w", repository, ctx.Err())
		}
		return nil, fmt.Errorf("cannot read tags from %s; check network/repository access", repository)
	}
	var tags []string
	for line := range strings.Lines(string(output)) {
		if _, tag, found := strings.Cut(strings.TrimRight(line, "\r\n"), "refs/tags/"); found {
			tags = append(tags, tag)
		}
	}
	return tags, nil
}
