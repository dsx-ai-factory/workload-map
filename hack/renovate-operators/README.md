<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Operator update automation

This Go tool generates Renovate's configuration and adds validated CI recordings
to its draft operator update PRs. It has its own module and uses only the
standard library.

The workflow and repository setup are described in
[Dependency Updates](../../docs/Dependency%20Updates.md#operator-updates-and-recordings).
The existing [operator checker](../operator-updates/README.md) reports candidate
tags. The automation uses those checkers' release mappings and calls the existing
installation, smoke verification, recording, and replay commands.

## Build and test

Run from the repository root with the Go version in this module's `go.mod`:

```sh
make build-renovate-operators
make test-renovate-operators
make check-renovate-operators
./bin/renovate-operators matrix --operator all
```

The offline tests use Go, Git, and Bash. They do not create a cluster or contact
GitHub. The root Makefile includes this module in `COMPONENTS` and `GO_MODULES`,
so `make check` runs formatting, vet, lint, module verification, and tests.
Renovate discovers its `go.mod` automatically. No `go.sum` is needed because the
tool has no third-party dependencies.

## CI commands

The workflow builds the executable outside Renovate's checkout and puts it on
`PATH`. `renovate-operators config` writes the configuration for the selected
operator group. The pinned Renovate configuration validator checks that JSON
before the job creates an App token.

`renovate-operators run` starts Renovate, which opens a draft PR for the selected
version pins. The same CI job checks out that PR's commit, records and validates
the update in a fresh kind cluster, and removes the cluster. It then commits the
recordings with the bot identity, runs `make check`, and pushes only if the PR
branch has not changed. A failed check leaves the draft PR without a new
recording commit. Node runs Renovate itself; the orchestration uses Go.

## Adding an operator

Follow [Extending and testing operator automation](../../docs/Dependency%20Updates.md#extending-and-testing-operator-automation)
for the pin mapping, workload coverage, and fork validation requirements.
