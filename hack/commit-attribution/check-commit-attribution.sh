#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation

set -euo pipefail
export LC_ALL=C

if [[ $# -ne 2 ]]; then
  echo "Usage: $0 <exclusive-base> <inclusive-head>" >&2
  exit 2
fi

head=$(git rev-parse --verify --end-of-options "$2^{commit}")
revision_range=$head
# A new branch push has no previous tip; check all reachable commits.
if [[ $1 != 0000000000000000000000000000000000000000 ]]; then
  base=$(git rev-parse --verify --end-of-options "$1^{commit}")
  revision_range="$base..$head"
fi
commits=$(git rev-list --reverse "$revision_range")

status=0
checked=0
while IFS= read -r commit; do
  [[ -n $commit ]] || continue
  message=$(git show -s --format=%B "$commit")
  trailers=$(git interpret-trailers --parse <<< "$message")
  checked=$((checked + 1))
  if printf '%s\n' "$trailers" |
    grep -Ei '^[[:blank:]]*Co-authored-by[[:blank:]]*:' |
    grep -Ei '(^|[^[:alnum:]_])(Claude|ChatGPT|Copilot|Codex|Devin|Cursor|Gemini|Anthropic)([^[:alnum:]_]|$)' > /dev/null; then
    echo "${commit:0:12}: remove the AI Co-authored-by line."
    status=1
  fi
done <<< "$commits"

if [[ $status -ne 0 ]]; then
  echo 'Keep human co-authors and the required human Signed-off-by lines.'
else
  echo "Commit attribution passed for $checked commit(s)."
fi
exit "$status"
