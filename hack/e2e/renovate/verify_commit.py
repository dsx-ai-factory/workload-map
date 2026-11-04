#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
"""Permit publication only when the bot commit contains the CI-tested update."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys


ENV_PATH = "hack/e2e/global.env"
SHA_PATTERN = re.compile(r"[0-9a-f]{40}")
GROUP_PATTERN = re.compile(r"[a-z0-9]+(?:-[a-z0-9]+)*")
RECORDING_PATTERN = re.compile(
    r"test/e2e/recorded_data/[a-z0-9-]+/[A-Za-z0-9_+.-]+/"
    r"[a-z0-9-]+/[a-z0-9-]+\.yaml"
)


def git(repo, *args):
    return subprocess.run(
        ["git", *args], cwd=repo, check=True,
        stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    ).stdout


def unique_object(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError(f"duplicate manifest key: {key}")
        value[key] = item
    return value


def read_manifest(path, repo, base_sha):
    if not SHA_PATTERN.fullmatch(base_sha):
        raise ValueError("base SHA must contain 40 lowercase hexadecimal digits")
    if not path.is_absolute() or path.is_symlink() or not path.is_file():
        raise ValueError("manifest must be an absolute path to a regular file")
    if path.resolve().is_relative_to(repo.resolve()):
        raise ValueError("manifest must be outside the candidate checkout")
    manifest = json.loads(path.read_text(), object_pairs_hook=unique_object)
    if not isinstance(manifest, dict) or set(manifest) != {"base", "group", "files"}:
        raise ValueError("manifest must contain only base, group and files")
    if manifest["base"] != base_sha:
        raise ValueError("manifest does not describe the expected base SHA")
    if not isinstance(manifest["group"], str) or not GROUP_PATTERN.fullmatch(manifest["group"]):
        raise ValueError("manifest contains an invalid operator group")
    files = manifest["files"]
    if not isinstance(files, dict) or ENV_PATH not in files or len(files) < 2:
        raise ValueError("manifest must include global.env and generated recordings")
    for name, digest in files.items():
        if name != ENV_PATH and not RECORDING_PATTERN.fullmatch(name):
            raise ValueError(f"manifest contains an unexpected path: {name}")
        if any(part in {".", ".."} for part in name.split("/")):
            raise ValueError("manifest contains an unsafe path")
        if not isinstance(digest, str) or not re.fullmatch(r"[0-9a-f]{64}", digest):
            raise ValueError(f"manifest contains an invalid SHA-256 digest: {name}")
    return manifest


def verify(manifest_path, base_sha, author, repo, commit="HEAD"):
    manifest = read_manifest(manifest_path, repo, base_sha)
    if commit != "HEAD" and not SHA_PATTERN.fullmatch(commit):
        raise ValueError("commit must be HEAD or a full commit SHA")
    if not re.fullmatch(r"[^<>\r\n]+ <[^<>\s]+>", author):
        raise ValueError("expected author must have the form Name <email>")
    commit_sha = git(repo, "rev-parse", "--verify", f"{commit}^{{commit}}").decode().strip()
    parents = git(repo, "show", "-s", "--format=%P", commit_sha).decode().split()
    if parents != [base_sha]:
        raise ValueError("candidate must have exactly one parent equal to the verified base SHA")
    actual_author = git(repo, "show", "-s", "--format=%an <%ae>", commit_sha).decode().rstrip("\n")
    if actual_author != author:
        raise ValueError("candidate must be authored by the configured Renovate bot")
    trailers = git(
        repo, "show", "-s", "--format=%(trailers:key=Signed-off-by,valueonly)", commit_sha
    ).decode().splitlines()
    if author not in trailers:
        raise ValueError("candidate must contain the configured bot's DCO trailer")

    raw_changes = git(
        repo, "diff-tree", "--no-commit-id", "--name-status", "-r", "-z",
        "--no-renames", base_sha, commit_sha, "--",
    ).decode().split("\0")
    if raw_changes[-1] != "" or len(raw_changes) % 2 != 1:
        raise ValueError("could not read candidate file changes")
    changes = dict(zip(raw_changes[1:-1:2], raw_changes[:-1:2]))
    if changes.get(ENV_PATH) != "M":
        raise ValueError("candidate must modify global.env")
    if any(status not in {"A", "M"} for status in changes.values()):
        raise ValueError("candidate may only add or modify tested files")
    files = manifest["files"]
    if not changes.keys() <= files.keys():
        raise ValueError("candidate changes files absent from the CI manifest")

    # Regenerating a recording can produce identical bytes. Such files still
    # belong in the evidence and must be checked, even though Git omits them.
    entries = git(repo, "ls-tree", "-r", "-z", commit_sha, "--", *files).split(b"\0")
    if len(entries) != len(files) + 1 or entries[-1] != b"":
        raise ValueError("candidate does not contain all tested files")
    for entry in entries[:-1]:
        metadata, path = entry.split(b"\t", 1)
        mode, kind, _ = metadata.split()
        if mode != b"100644" or kind != b"blob" or path.decode() not in files:
            raise ValueError("candidate paths must be regular, non-executable Git files")
    for path, expected_hash in files.items():
        actual_hash = hashlib.sha256(git(repo, "show", f"{commit_sha}:{path}")).hexdigest()
        if actual_hash != expected_hash:
            raise ValueError(f"committed content differs from the tested content: {path}")


def verify_push(manifest_path, base_sha, author, repo, updates, branch=None):
    manifest = read_manifest(manifest_path, repo, base_sha)
    branch = branch or f"renovate/e2e-{manifest['group']}"
    lines = updates.splitlines()
    if len(lines) != 1 or len(lines[0].split()) != 4:
        raise ValueError("expected exactly one branch update on pre-push stdin")
    _, local_sha, remote_ref, remote_sha = lines[0].split()
    if remote_ref != f"refs/heads/{branch}":
        raise ValueError("push does not target the configured operator update branch")
    if not SHA_PATTERN.fullmatch(local_sha) or local_sha == "0" * 40:
        raise ValueError("push must publish a commit, not delete a branch")
    if not SHA_PATTERN.fullmatch(remote_sha):
        raise ValueError("push contains an invalid remote SHA")
    # Git may push a commit other than HEAD. Verify the actual object it sends.
    verify(manifest_path, base_sha, author, repo, local_sha)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", required=True, type=Path)
    parser.add_argument("--base-sha", required=True)
    parser.add_argument("--author", required=True)
    parser.add_argument("--repo", type=Path, default=Path.cwd())
    parser.add_argument("--commit", default="HEAD")
    parser.add_argument("--pre-push", action="store_true")
    parser.add_argument("--branch", help="expected remote branch for --pre-push")
    args = parser.parse_args(argv)
    try:
        if args.pre_push:
            verify_push(
                args.manifest, args.base_sha, args.author, args.repo,
                sys.stdin.read(), args.branch,
            )
        else:
            verify(args.manifest, args.base_sha, args.author, args.repo, args.commit)
    except (OSError, UnicodeError, ValueError, subprocess.CalledProcessError) as error:
        message = "Git could not inspect the candidate commit" if isinstance(
            error, subprocess.CalledProcessError
        ) else str(error)
        print(f"verify-commit: {message}", file=sys.stderr)
        return 1
    print("Verified the bot commit contains the CI-tested pin and recordings")
    return 0


if __name__ == "__main__":
    sys.exit(main())
