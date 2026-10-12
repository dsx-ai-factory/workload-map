<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Operator update checker

This Go command compares Karta's operator pins with public upstream Git tags.
It uses only the standard library and has its own Go module. It reports
candidate versions and links to changes; it does not certify upstream support
or Karta compatibility. Installation, smoke checks, recording, and replay remain
separate validation steps.

## Run

Run from the repository root with Bash, Git, and the Go version in
`hack/operator-updates/go.mod`:

```sh
make e2e-operators-check-latest-supported-versions
make e2e-operators-check-latest-supported-versions OPERATORS="jobset infra"
make e2e-operators-check-latest-supported-versions UPDATE_FLAGS=--fail-on-update
make e2e-operators-check-latest-supported-versions UPDATE_FLAGS=--include-prereleases
./hack/e2e/operators/jobset/check-updates.sh
./hack/e2e/infra/fake-gpu-operator/check-updates.sh
JOBSET_VERSION=v0.11.0 ./hack/e2e/operators/jobset/check-updates.sh
```

The Bash wrappers read `hack/e2e/global.env`, preserving environment overrides.
Their shared `_updates.sh` builds `bin/operator-updates` before running it. The
aggregate builds once and passes the executable to each selected wrapper.
Python, a cluster, Docker, Helm, and a GitHub token are not required.

To reuse a binary instead of building at invocation time:

```sh
make build-operator-updates
E2E_OPERATOR_UPDATES_BIN="$PWD/bin/operator-updates" \
  ./hack/e2e/check-updates.sh jobset
```

The Go CLI also accepts the component mapping directly:

```sh
. ./hack/e2e/global.env
./bin/operator-updates --fail-on-update \
  --operator jobset "$JOBSET_VERSION" kubernetes-sigs/jobset v stable ""
GOWORK=off go -C hack/operator-updates run . --include-prereleases \
  --operator jobset "$JOBSET_VERSION" kubernetes-sigs/jobset v stable ""
```

Repeat `--operator NAME PIN REPO PREFIX CHANNEL NOTE` to check several components.
Use `--exclude-tag TAG` for a known retraction (repeatable), `--no-header` to
suppress the column header, or `--help` to list options. The final empty argument
above is an optional note; its position is required.

`E2E_OPERATOR_UPDATES_BIN` must name an executable file; use an absolute path
because wrappers also run outside the repository directory. A prebuilt binary
requires only Bash and Git at runtime. Invoke the binary directly when its exit
code matters; `go run` does not preserve the command's exit code 2.

## Report and selection

| Status | Meaning |
| --- | --- |
| `NEW` | At least one matching tag is newer than the effective pin. |
| `CURRENT` | The pin has the same precedence as the latest matching tag. |
| `AHEAD` | The pin is newer than the latest matching tag. |
| `ERROR` | The input or tag lookup failed; other checks continue. |

For example, pin `v1.0.0` with latest matching tag `v1.1.0` is `NEW`;
swapping those versions is `AHEAD`. A pin can be ahead when its tag is absent
from the selected release channel.

With no selection, the aggregate checks all workload and infrastructure groups.
`OPERATORS="jobset infra"` selects a subset; an individual infrastructure name
such as `OPERATORS=cert-manager` works too. Run
`./hack/e2e/check-updates.sh --help` to list all accepted names.

The aggregate summary names groups with updates. Kubeflow checks Training
Operator and MPI Operator; Knative checks Serving and Kourier. The `infra`
selector covers cert-manager, Fake GPU Operator, and KAI Scheduler.

The checker uses the installed component's tag namespace and release channel,
including Grove alpha releases and known KAI Scheduler retractions. Stable
releases are otherwise the default. `--include-prereleases` includes prerelease
tags within that namespace. Each `check-updates.sh` declares its component's
repository, prefix, and channel. Comments beside non-obvious mappings link to
the upstream release sources that explain them.

Prerelease counters such as `rc9` and `rc10` sort numerically. Grove's
`alpha.N-rcM` sorts before `alpha.N`. Unrelated chart and Go module tag namespaces
are excluded. Branch commits without a new tag are outside the check's scope.

A matching tag does not guarantee its chart or release asset is available.
Review the linked changes, upstream support policy, and installer assumptions
before updating a pin. For example, Kubeflow Trainer v2 replaces the v1 APIs
used by the existing installer and requires migration work.

Script and binary exit codes are 0 for a successful report, 1 for input or
lookup errors, and 2 for detected updates when `--fail-on-update` is set. Errors
take precedence over updates. Make uses its own nonzero failure code. Git tag
lookups require public GitHub access, are noninteractive, and have a bounded
timeout. The checker does not change pins or contact Kubernetes.

## Add an operator

Every external workload and infrastructure operator needs an executable
`check-updates.sh` as well as installation and smoke verification coverage.
Kubernetes builtins and Karta built from the checkout are exempt.

1. Inspect a local clone of each upstream component's release workflow or guide.
   Confirm the repository, exact tag prefix, channel, and mapping to the chart
   or manifest the installer uses. Explain non-obvious release rules beside
   the wrapper mapping, with public source links pinned to the inspected commit.
2. Add the Bash wrapper at `hack/e2e/operators/<name>/check-updates.sh`, beside
   `install.sh` and `verify.sh`, or `hack/e2e/infra/<name>/check-updates.sh` for
   infrastructure. Source `_updates.sh` and call `check_updates` with one
   `--operator NAME PIN REPO PREFIX CHANNEL NOTE` per versioned component.
   Forward `"$@"` and make the wrapper executable. Existing wrappers show
   retracted-tag exclusions and groups with multiple components.
3. Register the group in `ALL_OPERATORS` in `hack/e2e/check-updates.sh`.
   Infrastructure groups must also join its `infra` selector. Infrastructure
   installation and verification live beside the checker and are invoked by
   `up.sh` before dependent workloads.
4. Add offline Go tests here for the repository and tag namespace, filtering,
   and any release exceptions. Preserve the regression that checks every
   workload installer module and infrastructure directory has an executable
   checker and aggregate registration. Run `make test-e2e-updates`,
   `make lint-shell`, installation and smoke verification for the affected
   module, and `make check` before pushing. For a catalog addition or operator
   version bump, record the affected workload flows and run replay validation;
   see [Catalog Definitions](../../CONTRIBUTING.md#catalog-definitions).
   Infrastructure modules do not need separate catalog definitions or recordings.

The offline tests exercise the CLI, Bash wrappers, and Make target with fake Git
responses. They do not query upstream or provision a cluster.

The root Makefile includes this module in `GO_MODULES` and `COMPONENTS`, so
`make check` runs its formatting, vet, lint, module, and test checks. Use
`make check-operator-updates` to run only those checks. Direct Go commands in
this directory use `GOWORK=off`; no workspace entry is needed.

The repository's Renovate scanner discovers the module from `go.mod`; no manual
`includePaths` entry is needed in `renovate.json`. The checker has no third-party
dependencies, so it does not need a `go.sum`.
