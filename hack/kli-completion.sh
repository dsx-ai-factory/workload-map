#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Add or remove loading of kli shell completion in the shell rc file.
#
# Usage: kli-completion.sh install|uninstall [LOCAL_KLI] [INSTALLED_KLI]
#
# The shell comes from $SHELL (bash or zsh); set KLI_SHELL to override it.
# install sources the completion script of INSTALLED_KLI (the make install-cli
# target) when it exists at shell start. Otherwise it puts the directory of
# LOCAL_KLI (default bin/kli) on PATH, so completion fires for a plain `kli`,
# and sources its completion script instead.
set -euo pipefail

BEGIN_MARKER="# >>> kli completion >>>"
END_MARKER="# <<< kli completion <<<"

action="${1:-}"
local_kli="${2:-bin/kli}"
installed_kli="${3:-}"
shell_name="${KLI_SHELL:-$(basename "${SHELL:-bash}")}"

case "${shell_name}" in
  bash)
    # macOS terminals start bash as a login shell, which reads .bash_profile
    # and never .bashrc.
    if [ "$(uname -s)" = "Darwin" ]; then
      rc_file="${HOME}/.bash_profile"
    else
      rc_file="${HOME}/.bashrc"
    fi
    ;;
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
    if [ ! -x "${local_kli}" ] && { [ -z "${installed_kli}" ] || [ ! -x "${installed_kli}" ]; }; then
      echo "${local_kli} not found: run make build-cli first" >&2
      exit 1
    fi
    # The rc file outlives this shell, so both paths must be absolute.
    case "${local_kli}" in
      /*) ;;
      *) local_kli="${PWD}/${local_kli}" ;;
    esac
    case "${installed_kli}" in
      "" | /*) ;;
      *) installed_kli="${PWD}/${installed_kli}" ;;
    esac
    local_dir="$(dirname "${local_kli}")"
    # eval rather than source <(...), which bash 3.2 on macOS ignores. The
    # guards keep a new shell quiet once a binary or the clone is deleted.
    local_branch="[ -x \"${local_kli}\" ]; then
  export PATH=\"${local_dir}:\${PATH}\"
  eval \"\$(\"${local_kli}\" completion ${shell_name})\""
    # A here-string rather than a pipe: a pipe would run rewrite_rc in a
    # subshell, out of reach of the trap that removes its temporary file.
    block="$(
      echo "${BEGIN_MARKER}"
      if [ "${shell_name}" = "zsh" ]; then
        # The zsh completion script calls compdef, which compinit defines.
        echo "(( \${+functions[compdef]} )) || { autoload -Uz compinit && compinit; }"
      fi
      if [ -n "${installed_kli}" ]; then
        echo "if [ -x \"${installed_kli}\" ]; then"
        echo "  eval \"\$(\"${installed_kli}\" completion ${shell_name})\""
        echo "elif ${local_branch}"
      else
        echo "if ${local_branch}"
      fi
      echo "fi"
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
    echo "usage: $0 install|uninstall [LOCAL_KLI] [INSTALLED_KLI]" >&2
    exit 1
    ;;
esac
