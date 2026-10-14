#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation

set -euo pipefail
checker="$(cd "$(dirname "$0")" && pwd)/check-commit-attribution.sh"
fixture=$(mktemp -d)
trap 'rm -r "$fixture"' EXIT
# Fixture commits must not depend on the caller's Git identity or hooks.
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null
export GIT_AUTHOR_NAME='Example Contributor' GIT_AUTHOR_EMAIL='contributor@example.com'
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME" GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"
git -C "$fixture" init -q
cd "$fixture"

commit() {
  printf '%s\n' "$1" | git -c commit.gpgsign=false commit --allow-empty -q -F -
  git rev-parse HEAD
}

checks=0
expect() {
  local expected=$1 actual=0
  shift
  output=$(bash "$checker" "$@" 2>&1) || actual=$?
  if [[ $actual -ne $expected ]]; then
    printf 'Expected exit %s, got %s:\n%s\n' "$expected" "$actual" "$output" >&2
    exit 1
  fi
  checks=$((checks + 1))
}

ai=$'Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>'
signoff='Signed-off-by: Example Contributor <contributor@example.com>'
historical=$(commit "test: existing attribution"$'\n\n'"$ai")
clean=$(commit "docs: explain Claude integration"$'\n\nCo-authored-by: Example Contributor <person@example.com>\n'"$signoff")
expect 0 "$historical" "$clean"
expect 0 "$clean" "$clean"

before=$(git rev-parse HEAD)
prose=$'docs: explain attribution\n\nCo-authored-by: Claude is prohibited by policy.\nThis is ordinary prose.'
clean=$(commit "$prose")
expect 0 "$before" "$clean"
clean=$(commit "$prose"$'\n\nCo-authored-by: Example Contributor <person@example.com>\n'"$signoff")
expect 0 "$before" "$clean"
bad=$(commit "$prose"$'\n\n'"$ai")
expect 1 "$before" "$bad"

before=$(git rev-parse HEAD)
bad=$(commit $'fix: folded attribution\n\nCo-authored-by:\n Claude <noreply@anthropic.com>')
expect 1 "$before" "$bad"

for name in Claude ChatGPT Copilot Codex Devin Cursor Gemini Anthropic; do
  before=$(git rev-parse HEAD)
  bad=$(commit "fix: example"$'\n\n'"Co-authored-by:$name <tool@example.com>")
  expect 1 "$before" "$bad"
  [[ $output == *"${bad:0:12}"* ]]
done

for subject in 'Merge branch feature' 'Revert "fix: example"' 'fixup! fix: example'; do
  before=$(git rev-parse HEAD)
  bad=$(commit "$subject"$'\n\n'"$ai"$'\n'"$signoff")
  expect 1 "$before" "$bad"
done

before=$(git rev-parse HEAD)
bad=$(commit $'fix: mixed case\n\nco-AUTHORED-by : claude <noreply@anthropic.com>\r\n')
expect 1 "$before" "$bad"
expect 1 0000000000000000000000000000000000000000 "$historical"
expect 128 missing-base HEAD
expect 128 HEAD missing-head
expect 2 HEAD

before=$(git rev-parse HEAD)
for ((n = 1; n <= 101; n++)); do
  commit "test: clean commit $n" > /dev/null
done
expect 0 "$before" HEAD
[[ $output == *'101 commit(s)'* ]]
bad=$(commit "fix: bad commit beyond first 100"$'\n\n'"$ai")
commit "fix: clean final commit" > /dev/null
expect 1 "$before" HEAD
[[ $output == *"${bad:0:12}"* ]]
echo "$checks commit attribution checks passed."
