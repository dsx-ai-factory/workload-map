#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

: "${E2E_UPDATE_GROUP:?}"
: "${E2E_UPDATE_BASE_BRANCH:?}"
: "${E2E_UPDATE_BASE_SHA:?}"
: "${E2E_UPDATE_ARTIFACTS:?}"
: "${E2E_UPDATE_RUN_URL:?}"
: "${RENOVATE_TOKEN:?}"
: "${RENOVATE_GIT_AUTHOR:?}"
: "${RENOVATE_VERSION:?}"
: "${E2E_OPERATOR_TRACKING_ISSUE:?Set the public tracking issue repository variable}"
[ "${GITHUB_ACTIONS:-}" = true ] || { echo 'Operator updates run in GitHub Actions' >&2; exit 1; }
ROOT=$(git rev-parse --show-toplevel)
cd "${ROOT}"
export GH_TOKEN="${RENOVATE_TOKEN}"
python3 - <<'PY'
import json
import os
import re
import subprocess
match = re.fullmatch(r'https://github.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/issues/([1-9][0-9]*)', os.environ['E2E_OPERATOR_TRACKING_ISSUE'])
if not match:
    raise SystemExit('Tracking issue must be a public GitHub issue URL')
repo, number = match.groups()
info = json.loads(subprocess.check_output(['gh', 'api', f'repos/{repo}']))
issue = json.loads(subprocess.check_output(['gh', 'api', f'repos/{repo}/issues/{number}']))
if info['private'] or issue['state'] != 'open' or 'pull_request' in issue:
    raise SystemExit('Tracking issue must be public and open')
PY
mkdir -p "${E2E_UPDATE_ARTIFACTS}"
if [ -e "${E2E_UPDATE_ARTIFACTS}/plan.json" ]; then
  echo 'Evidence directory must be new for each job attempt' >&2
  exit 1
fi
node hack/e2e/renovate/config.cjs --plan "${E2E_UPDATE_GROUP}" > "${E2E_UPDATE_ARTIFACTS}/plan.json"
branch=$(jq -r .branch "${E2E_UPDATE_ARTIFACTS}/plan.json")

# Renovate can continue after a failed post-upgrade task. Install the push guard
# before it starts, so even failure to launch the recording hook blocks a push.
mkdir "${E2E_UPDATE_ARTIFACTS}/hooks"
python3 - "${ROOT}" "${branch}" <<'PY'
import os
from pathlib import Path
import shlex
import sys
root, branch = sys.argv[1:]
artifacts = Path(os.environ['E2E_UPDATE_ARTIFACTS'])
command = ['python3', f'{root}/hack/e2e/renovate/verify_commit.py', '--manifest', str(artifacts / 'manifest.json'),
           '--base-sha', os.environ['E2E_UPDATE_BASE_SHA'], '--author', os.environ['RENOVATE_GIT_AUTHOR'],
           '--pre-push', '--branch', branch]
hook = artifacts / 'hooks/pre-push'
hook.write_text('#!/bin/sh\nexec ' + shlex.join(command) + '\n')
hook.chmod(0o755)
PY
# This workflow uses a fresh hosted runner. Renovate's Git library rejects
# custom GIT_CONFIG_* paths, so use the runner's normal configuration.
previous_hooks=$(git config --global --get core.hooksPath || true)
[ -z "${previous_hooks}" ] || { echo 'Runner already has a global Git hook path' >&2; exit 1; }
git config --global core.hooksPath "${E2E_UPDATE_ARTIFACTS}/hooks"
trap 'git config --global --unset core.hooksPath || true' EXIT
export RENOVATE_CONFIG_FILE="${ROOT}/hack/e2e/renovate/config.cjs"
export RENOVATE_BASE_DIR="${RUNNER_TEMP}/renovate-${E2E_UPDATE_GROUP}"
export RENOVATE_EXIT_CODE_FOR_ERRORS=true
renovate 2>&1 | tee "${E2E_UPDATE_ARTIFACTS}/renovate.log"
if [ ! -e "${E2E_UPDATE_ARTIFACTS}/record-started" ]; then
  printf 'No new %s candidate required recording.\n' "${E2E_UPDATE_GROUP}" | tee -a "${GITHUB_STEP_SUMMARY}"
  exit 0
fi
[ -f "${E2E_UPDATE_ARTIFACTS}/manifest.json" ] || { echo 'Recording failed; no validated PR can be published' >&2; exit 1; }
sha=$(gh api "repos/${GITHUB_REPOSITORY}/git/ref/heads/${branch}" --jq .object.sha)
git fetch origin "${sha}"
python3 hack/e2e/renovate/verify_commit.py --manifest "${E2E_UPDATE_ARTIFACTS}/manifest.json" \
  --base-sha "${E2E_UPDATE_BASE_SHA}" --author "${RENOVATE_GIT_AUTHOR}" --commit "${sha}"

# Only the exact commit checked above receives a successful recording status.
gh api "repos/${GITHUB_REPOSITORY}/statuses/${sha}" \
  -f state=success -f context='e2e/operator-recording' -f target_url="${E2E_UPDATE_RUN_URL}" \
  -f description='Fresh kind install, complete recordings, replay, presubmit and cleanup passed'
python3 - <<'PY'
import json
import os
from pathlib import Path
artifacts = Path(os.environ['E2E_UPDATE_ARTIFACTS'])
candidate = json.loads((artifacts / 'candidate.json').read_text())
changes = '\n'.join(f'- `{key}`: `{value["before"]}` -> `{value["after"]}`' for key, value in candidate['pins'].items())
body = f'''## What does this PR do?

Update the {candidate['group']} operator group and include the recordings generated in GitHub Actions.

{changes}

CI created a fresh kind cluster, installed and verified the candidate, recorded every selected flow, replayed the recordings, ran `make check`, and deleted the cluster. The bot commit was checked against the exact file hashes produced by that run.

[Recording and validation run]({os.environ['E2E_UPDATE_RUN_URL']}). Artifacts include installed versions, controller images, Kubernetes version, JUnit reports, logs, and file hashes. This proves the selected flows on the pinned Kubernetes version; it does not establish compatibility with every deployment.

## Related issue(s)

Related to {os.environ['E2E_OPERATOR_TRACKING_ISSUE']}.

## Checklist

- [x] All commits are signed off with DCO
- [x] Generated recordings have SPDX license and copyright headers
- [x] CI evidence is linked above
- [x] Tests pass: live flows, replay, and make check
- [x] No proprietary or internal information included
'''
(artifacts / 'pr-body.md').write_text(body)
PY
existing=$(gh pr list --repo "${GITHUB_REPOSITORY}" --base "${E2E_UPDATE_BASE_BRANCH}" --head "${branch}" --state open --json number --jq '.[0].number // empty')
if [ -n "${existing}" ]; then
  gh pr edit "${existing}" --repo "${GITHUB_REPOSITORY}" --body-file "${E2E_UPDATE_ARTIFACTS}/pr-body.md"
  pr_url=$(gh pr view "${existing}" --repo "${GITHUB_REPOSITORY}" --json url --jq .url)
else
  pr_url=$(gh pr create --repo "${GITHUB_REPOSITORY}" --base "${E2E_UPDATE_BASE_BRANCH}" --head "${branch}" --draft \
    --title "build(e2e): update ${E2E_UPDATE_GROUP} with CI recordings" --body-file "${E2E_UPDATE_ARTIFACTS}/pr-body.md")
fi
printf '%s\n' "${pr_url}" > "${E2E_UPDATE_ARTIFACTS}/pr-url.txt"
printf 'Validated operator update: %s\n\nRecording run: %s\n' "${pr_url}" "${E2E_UPDATE_RUN_URL}" >> "${GITHUB_STEP_SUMMARY}"
