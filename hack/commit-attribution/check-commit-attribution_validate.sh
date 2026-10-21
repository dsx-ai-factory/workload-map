#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation

set -euo pipefail
checker="$(cd "$(dirname "$0")" && pwd)/check-commit-attribution.sh"
fetcher="$(dirname "$checker")/fetch-commit-base.sh"
fixture=$(mktemp -d)
trap 'rm -r "$fixture"' EXIT
# Fixture commits must not depend on the caller's Git identity or hooks.
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE
unset COMMIT_DEFAULT_BRANCH
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
expect_command() {
  local expected=$1 actual=0
  shift
  output=$("$@" 2>&1) || actual=$?
  if [[ $actual -ne $expected ]]; then
    printf 'Expected exit %s, got %s:\n%s\n' "$expected" "$actual" "$output" >&2
    exit 1
  fi
  checks=$((checks + 1))
}

expect() {
  local expected=$1
  shift
  expect_command "$expected" bash "$checker" "$@"
}

ai=$'Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>'
signoff='Signed-off-by: Example Contributor <contributor@example.com>'
historical=$(commit "test: existing attribution"$'\n\n'"$ai")
clean=$(commit "docs: explain Claude integration"$'\n\nCo-authored-by: Example Contributor <person@example.com>\n'"$signoff")
expect 0 "$historical" "$clean"
expect 0 "$clean" "$clean"

# Creating a release branch at the default tip must not recheck old trailers.
zero=0000000000000000000000000000000000000000
git update-ref refs/remotes/origin/main "$clean"
expect 0 "$zero" "$clean"
[[ $output == *'0 commit(s)'* ]]
expect 0 "$zero" "$historical"
new_clean=$(commit 'test: first release-only commit')
expect 0 "$zero" "$new_clean"
[[ $output == *'1 commit(s)'* ]]
new_bad=$(commit "test: new release attribution"$'\n\n'"$ai")
commit 'test: clean release tip' > /dev/null
expect 1 "$zero" HEAD
[[ $output == *"${new_bad:0:12}"* && $output != *"${historical:0:12}"* ]]
git update-ref refs/remotes/origin/trunk "$new_clean"
COMMIT_DEFAULT_BRANCH=refs/remotes/origin/trunk expect 0 "$zero" "$new_clean"
COMMIT_DEFAULT_BRANCH=refs/remotes/origin/trunk expect 1 "$zero" HEAD
COMMIT_DEFAULT_BRANCH=refs/remotes/origin/missing expect 128 "$zero" HEAD
git update-ref -d refs/remotes/origin/main
expect 128 "$zero" HEAD
git update-ref refs/remotes/origin/main "$clean"

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

# Clone only advertised history after a force-push, leaving the old tip absent.
git init --bare -q --initial-branch=main "$fixture/remote.git"
git init -q --initial-branch=main "$fixture/writer"
cd "$fixture/writer"
git remote add origin "$fixture/remote.git"
shared=$(commit "test: shared history"$'\n\n'"$ai")
old=$(commit 'test: previous branch tip')
git push -q origin HEAD:main
git reset --hard -q "$shared"
bad=$(commit "test: rewritten attribution"$'\n\n'"$ai")
commit 'test: clean rewritten tip' >/dev/null
git push -q --force origin HEAD:main
git clone -q --no-local "$fixture/remote.git" "$fixture/bad-checkout"
cd "$fixture/bad-checkout"
if git cat-file -e "$old^{commit}" 2>/dev/null; then
  echo 'Expected the old tip to be absent from the fresh clone.' >&2
  exit 1
fi
expect_command 0 bash "$fetcher" "$old"
git cat-file -e "$old^{commit}"
expect 1 "$old" HEAD
[[ $output == *"${bad:0:12}"* && $output != *"${shared:0:12}"* ]]

cd "$fixture/writer"
git reset --hard -q "$shared"
commit 'test: clean rewritten commit' >/dev/null
commit 'test: clean final rewritten commit' >/dev/null
git push -q --force origin HEAD:main
git clone -q --no-local "$fixture/remote.git" "$fixture/clean-checkout"
cd "$fixture/clean-checkout"
if git cat-file -e "$old^{commit}" 2>/dev/null; then
  echo 'Expected the old tip to be absent from the fresh clone.' >&2
  exit 1
fi
expect_command 0 bash "$fetcher" "$old"
expect 0 "$old" HEAD
[[ $output == *'2 commit(s)'* ]]

# Skips must work without contacting origin; a missing commit must fail closed.
git remote set-url origin "$fixture/unavailable.git"
expect_command 0 bash "$fetcher" "$old"
expect_command 0 bash "$fetcher" "$zero"
git remote set-url origin "$fixture/remote.git"
expect_command 128 bash "$fetcher" 1111111111111111111111111111111111111111
expect_command 2 bash "$fetcher" malformed
expect_command 2 bash "$fetcher"
expect_command 2 bash "$fetcher" "$old" "$old"
echo "$checks commit attribution checks passed."
