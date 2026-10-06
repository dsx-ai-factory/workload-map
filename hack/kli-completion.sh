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

# Follow a symlinked rc file, as dotfile managers create, so the replacement
# lands on the real file and the link survives.
target="${rc_file}"
while [ -L "${target}" ]; do
  link="$(readlink "${target}")"
  case "${link}" in
    /*) target="${link}" ;;
    *) target="$(dirname "${target}")/${link}" ;;
  esac
done

# rewrite_rc replaces the rc file with its content minus any installed block,
# followed by stdin. The result is built in a temporary file beside the target
# and moved over it in one step, so a failed write leaves the rc file intact.
tmp=""
trap 'rm -f "${tmp}"' EXIT
rewrite_rc() {
  tmp="$(mktemp "$(dirname "${target}")/.kli-completion.XXXXXX")"
  if [ -f "${target}" ]; then
    # Copy first so the replacement keeps the rc file's permissions.
    cp -p "${target}" "${tmp}"
    awk -v begin="${BEGIN_MARKER}" -v end="${END_MARKER}" '
      $0 == begin { skip = 1; next }
      $0 == end { skip = 0; next }
      !skip
    ' "${target}" >"${tmp}"
  fi
  cat >>"${tmp}"
  mv -f "${tmp}" "${target}"
}

case "${action}" in
  install)
    if [ ! -x "${binary}" ]; then
      echo "${binary} not found: run make build-cli first" >&2
      exit 1
    fi
    bin_dir="$(cd "$(dirname "${binary}")" && pwd)"
    # A here-string rather than a pipe: a pipe would run rewrite_rc in a
    # subshell, out of reach of the trap that removes its temporary file.
    block="$(
      echo "${BEGIN_MARKER}"
      echo "export PATH=\"${bin_dir}:\${PATH}\""
      if [ "${shell_name}" = "zsh" ]; then
        # The zsh completion script calls compdef, which compinit defines.
        echo "(( \${+functions[compdef]} )) || { autoload -Uz compinit && compinit; }"
      fi
      # eval rather than source <(...), which bash 3.2 on macOS ignores.
      echo "eval \"\$(\"${bin_dir}/kli\" completion ${shell_name})\""
      echo "${END_MARKER}"
    )"
    rewrite_rc <<<"${block}"
    echo "kli completion added to ${rc_file}; open a new shell or run: source ${rc_file}"
    ;;
  uninstall)
    if [ -f "${target}" ]; then
      rewrite_rc </dev/null
    fi
    echo "kli completion removed from ${rc_file}; open a new shell to unload it"
    ;;
  *)
    echo "usage: $0 install|uninstall [KLI_BINARY]" >&2
    exit 1
    ;;
esac
