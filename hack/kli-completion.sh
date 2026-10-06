#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Add or remove loading of kli shell completion in the shell rc file.
#
# Usage: kli-completion.sh install|uninstall [KLI_BINARY]
#
# The shell comes from $SHELL (bash or zsh); set KLI_SHELL to override it.
# install puts the directory of KLI_BINARY (default bin/kli) on PATH, so
# completion fires for a plain `kli`, and sources its completion script.
set -euo pipefail

BEGIN_MARKER="# >>> kli completion >>>"
END_MARKER="# <<< kli completion <<<"

action="${1:-}"
binary="${2:-bin/kli}"
shell_name="${KLI_SHELL:-$(basename "${SHELL:-bash}")}"

case "${shell_name}" in
  bash) rc_file="${HOME}/.bashrc" ;;
  zsh) rc_file="${ZDOTDIR:-${HOME}}/.zshrc" ;;
  *)
    echo "unsupported shell '${shell_name}': set KLI_SHELL to bash or zsh" >&2
    exit 1
    ;;
esac

# Drop a previously installed block, so install is idempotent.
remove_block() {
  [ -f "${rc_file}" ] || return 0
  local tmp
  tmp="$(mktemp)"
  awk -v begin="${BEGIN_MARKER}" -v end="${END_MARKER}" '
    $0 == begin { skip = 1; next }
    $0 == end { skip = 0; next }
    !skip
  ' "${rc_file}" >"${tmp}"
  cat "${tmp}" >"${rc_file}"
  rm -f "${tmp}"
}

case "${action}" in
  install)
    if [ ! -x "${binary}" ]; then
      echo "${binary} not found: run make build-cli first" >&2
      exit 1
    fi
    bin_dir="$(cd "$(dirname "${binary}")" && pwd)"
    remove_block
    {
      echo "${BEGIN_MARKER}"
      echo "export PATH=\"${bin_dir}:\${PATH}\""
      if [ "${shell_name}" = "zsh" ]; then
        # The zsh completion script calls compdef, which compinit defines.
        echo "(( \${+functions[compdef]} )) || { autoload -Uz compinit && compinit; }"
      fi
      # eval rather than source <(...), which bash 3.2 on macOS ignores.
      echo "eval \"\$(\"${bin_dir}/kli\" completion ${shell_name})\""
      echo "${END_MARKER}"
    } >>"${rc_file}"
    echo "kli completion added to ${rc_file}; open a new shell or run: source ${rc_file}"
    ;;
  uninstall)
    remove_block
    echo "kli completion removed from ${rc_file}; open a new shell to unload it"
    ;;
  *)
    echo "usage: $0 install|uninstall [KLI_BINARY]" >&2
    exit 1
    ;;
esac
