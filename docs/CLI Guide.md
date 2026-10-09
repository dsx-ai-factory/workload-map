<!--
SPDX-License-Identifier: Apache-2.0
Copyright (c) 2026 NVIDIA Corporation
-->

# CLI Guide

`kli` inspects Karta workloads from the command line. This guide covers how to
install it and how to set up shell completion.

## Install a release

Install the latest synchronized Go module release:

```bash
go install github.com/dsx-ai-factory/workload-map/cli/cmd/kli@latest
```

Or download a prebuilt archive from the
[releases page](https://github.com/dsx-ai-factory/workload-map/releases).
Each release publishes archives for Linux and macOS on amd64 and arm64, with a
`checksums.txt` manifest. Put the `kli` executable on your `PATH`, then run
`kli --version` to print the release version.

## Shell completion

Completion fills in commands, workload types, workload names, and namespaces
on TAB. Load the completion script from your shell rc file:

```bash
# ~/.zshrc (after compinit)
eval "$(kli completion zsh)"

# ~/.bashrc, or ~/.bash_profile on macOS (needs the bash-completion package)
eval "$(kli completion bash)"
```

`kli completion --help` lists the other shells and setup details.

## Install from a clone

In a clone of this repository, make targets build `kli`, install it, and add
its completion to your bash or zsh rc file.

| Target | What it does |
| --- | --- |
| `make install-cli` | Builds `kli`, copies it to `/usr/local/bin`, and loads its completion. |
| `make uninstall-cli` | Removes `kli` from `/usr/local/bin` and its completion. |
| `make install-cli-local` | Same as `install-cli`, with `$HOME/.local/bin`, which needs no root. |
| `make uninstall-cli-local` | Same as `uninstall-cli`, with `$HOME/.local/bin`. |
| `make install-cli-completion` | Builds `kli` into the repository's `./bin/` and loads only its completion. |
| `make install-cli-completion-local` | Same as `install-cli-completion`, preferring the `kli` in `$HOME/.local/bin`. |
| `make uninstall-cli-completion` | Removes the completion. |

Set `BINDIR` (or `PREFIX`) to install somewhere else, for example
`make install-cli BINDIR=/opt/tools/bin`.

The rc file entry loads completion from the installed `kli` when it exists.
Otherwise it falls back to `./bin/kli` in the repository. If `kli` was
installed with `make install-cli-local`, use `make install-cli-completion-local`
so the entry checks `$HOME/.local/bin`.
