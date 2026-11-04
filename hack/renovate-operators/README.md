<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Operator update automation

This Go tool runs the operator update workflow. It generates Renovate's JSON
configuration, orchestrates CI recording, verifies the bot commit, and publishes
the update PR. It has its own module and uses only the standard library.

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

`renovate-operators run` starts Renovate with the publication guard installed.
Renovate invokes `renovate-operators record` after changing the selected pins.
Both commands require GitHub Actions. The same executable verifies Git pushes
against the tested commit and recording hashes. Node runs Renovate itself.

## Adding an operator

Add the pin variables and workload coverage in `plan.go`. Keep release rules in
the operator's `check-updates.sh` wrapper. Add each recorded kind to
`test/e2e/cmd/verify-operator-update`; flow names are discovered from the suite.
Run the offline checks above and validate an update through the fork workflow
before enabling new coverage.
