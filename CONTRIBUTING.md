# Contributing to karta

Thank you for your interest in contributing to karta! This document provides guidelines and instructions for contributing to this project.

## Developer Certificate of Origin (DCO)

All contributions to this project must comply with the Developer Certificate of Origin (DCO) version 1.1. This is a lightweight way for contributors to certify that they wrote or otherwise have the right to submit the code they are contributing to the project.

### DCO Sign-off

By contributing to this project, you agree to the DCO. You must sign off your commits to indicate that you agree to the DCO. You can do this by adding a `Signed-off-by` line to your commit messages:

```text
Signed-off-by: Your Name <your.email@example.com>
```

You can sign off automatically by using the `-s` or `--signoff` flag when committing:

```bash
git commit -s -m "Your commit message"
```

You must use your real name (sorry, no pseudonyms or anonymous contributions).

### DCO Text

The full text of the DCO version 1.1 is as follows:

```text
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.
1 Letterman Drive
Suite D4700
San Francisco, CA, 94129

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.

Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

For more information about the DCO, please visit: https://developercertificate.org/

## Contribution Guidelines

### Before You Start

1. Check existing issues and pull requests to see if your contribution is already being addressed
2. Ensure your code follows the project's coding standards and conventions
3. Every pull request must reference at least one open GitHub issue in its description. This ensures that all changes are tracked and linked to project requirements or bug reports.
4. For major changes, please open an issue first to discuss the proposed changes

### Development Environment Setup

Karta is a Go project. To set up a local environment:

```bash
# 1. Clone your fork
git clone https://github.com/<your-username>/karta.git
cd karta

# 2. Build the packages (uses the Go version pinned in go.mod)
go build ./...

# 3. Run the full check pipeline (fmt, vet, lint, codegen, manifests,
#    licenses, and tests for every component) - the same target CI runs
make check

# 4. Lint the Helm chart
make helm-lint
make helm-validate
```

`make check` is the complete Go presubmit for the library, CLI, operator,
Karta WASM module, and release helper, and CI runs it verbatim. CI covers the
Helm chart, air-gap image lock, and shell scripts in separate steps, so run
those targets too before pushing if you touched them.

There is one Makefile, at the repository root. Bare targets act on every
component, and a component suffix narrows them:

```bash
make test              # every Go component, including the release helper
make test-cli          # just the CLI
make check-operator    # operator checks, e2e compilation, tests, and version smoke
make help              # every target, grouped
```

`make build-operator` writes a binary for the host OS and architecture to
`bin/karta-operator`. The operator version smoke test runs that host binary on
every supported development host. The operator e2e suite is compiled during
`make check`; its live cluster run remains a separate target.

There is no Go workspace. The library is the `karta/` module, and the CLI,
operator, and other modules reach it through a relative `replace` directive in
their own `go.mod`. Run Go commands from a module's directory, for example
`cd karta && go test ./pkg/...`. The root `go.mod` only marks the library's old
module path as deprecated and has no packages.

`make lint` never rewrites your files. `make fmt` and the per-component
`fmt-*` targets are the only ones that reformat, and nothing depends on them.

### Commit Messages

Commit messages follow [Conventional Commits v1.0.0](https://www.conventionalcommits.org/en/v1.0.0/):

```text
<type>(<scope>): <short description>

[optional body]
```

Types: `feat`, `fix`, `refactor`, `docs`, `test`, `build`, `ci`, `chore`. When picking a scope, match an existing one from `git log`. Combine the format with the DCO sign-off described above:

```bash
git commit -s -m "fix(api): validate status mapping expressions before applying them"
```

### Making Changes

1. Fork the repository
2. Create a feature branch from the main branch
3. Make your changes
4. Ensure all tests pass
5. Sign off your commits with the DCO (see above)
6. Submit a pull request

### Pull Request Process

1. Ensure your pull request includes:
   - A clear description of the changes
   - Reference to any related issues
   - All commits signed off with DCO
   - Tests for new functionality (if applicable)
   - Updated documentation (if applicable)

2. Your pull request will be reviewed by maintainers
3. Address any feedback or requested changes
4. Once approved, your changes will be merged

### Review Process

- Who reviews: pull requests are reviewed by the project
  [maintainers](MAINTAINERS.md). Relevant maintainers are added automatically;
  you do not need to request reviewers manually.
- Turnaround: maintainers aim to provide an initial response within 5
  business days. Smaller, focused pull requests are reviewed faster.
- Approvals: at least one maintainer approval is required before merging,
  and all CI checks must pass.
- Following up: if your pull request has not received a response within the
  expected window, please leave a comment to nudge the maintainers, or mention
  it in the related issue.

## Catalog Definitions

A new catalog definition ships with a recorded flow. The recording is how the
path is seen: it drives a real workload through the states the definition maps
and stores every CR the operator wrote, and the replay suite then proves the
definition reads each recorded frame on every CI run. A definition without a
recording is a claim; the recording is the evidence.

Adding one means, in order:

1. The builder under `karta/pkg/catalog/kartas/`, registered in
   `karta/pkg/catalog/catalog.go`, then `make generate-samples` for the generated
   file under `docs/catalog/`. Never hand-edit the generated file. Add the
   workload to the Pre-built Karta Definitions table in `README.md`, unless it
   is a Kubernetes builtin (apps, batch, core): the table lists operator-backed
   kinds only.
2. A flow under `karta/test/e2e/flows/` with its workload manifests under
   `karta/test/e2e/flows/testdata/<workload>/`. See `karta/test/e2e/recorder/README.md`.
   - Flow: record every mapped state, and each spec value a rule branches on
     (a StatefulSet's `OnDelete`, paused, a partition, a restart policy), that
     the kind's webhook accepts and a kind cluster can reach. Name the rest
     unproven in the builder comment. When the definition can suspend a
     running workload, one flow fires the suspend action from Running; a CR
     created suspended does not prove it. State predicates read the CR's own
     fields, never Karta; `AddState` order decides only a frame two
     predicates match, least to most advanced.
   - Manifests: pin image tags, declare resource requests and limits, set
     `namespace: default` (the recorder overrides it), set
     `automountServiceAccountToken: false` unless the pods call the API
     server, and name objects `karta-e2e-<workload>-<flow>`.
   - Install: if the operator is new or its install needs new pieces, follow
     Adding an operator in `hack/e2e/README.md` (`install.sh`, `verify.sh`,
     `smoke.yaml`, the version pin in `global.env`, `ALL_WORKLOADS` and
     `version_of` in `up.sh`) so `make e2e-up` still provisions everything. A
     new kind for an operator already installed keeps its directory, pin, and
     `up.sh` entries; it extends any flag in `install.sh` that gates which
     kinds the controller serves and adds a `<kind>-smoke.yaml` plus a
     `run_smoke` line in `verify.sh`. A Kubernetes builtin needs none of
     this.
   - Naming: the operator directory takes the upstream project's short name
     (`kuberay`, `spark-operator`). `Fixture.Operator` in the flow must equal
     that directory name; that is how the recording is filed under the
     operator version. The operator name is also the first `Label`; add the
     kind as a second label when the operator ships several kinds. A builtin
     has no directory: `Fixture.Operator` equals the first `Label`, and the
     second label is `builtin`. Name the testdata directory and the
     `<workload>` in object names after the kind in lowercase (`mpijob`,
     `rayjob`; `pytorch` is a legacy name); single-kind operators may use the
     operator name (`nim`). When the kind collides with a builtin or another
     catalog entry (a Volcano `Job`), use the upstream short name (`vcjob`)
     for the label, the directory, the object names, and the root component
     name alike.
3. The recorded fixtures from a live run, committed under
   `karta/test/e2e/recorded_data/<operator>/<version>/<kartaName>/`:

   ```sh
   make e2e-up CLUSTER_NAME=<name> WORKLOADS=<operator>
   make record-e2e CLUSTER_NAME=<name> WORKLOADS=<operator>
   ```

   A non-default `CLUSTER_NAME` keeps the run on its own kubeconfig, so the
   shared current-context is never switched. Use the same `CLUSTER_NAME` on
   both commands: the recorder reads
   `hack/e2e/operators/.installed-versions-<cluster>` to pick the version
   directory, and without it files the fixtures under the Kubernetes version.
   The fixtures already committed sit under `v1.34.0`; new ones go under the
   operator's `version_of` string from `hack/e2e/up.sh`, which can be
   composite (`v1.9.0+mpiv0.8.2` for kubeflow). For a new kind on an operator
   that ships several, pass the kind label to `record-e2e`
   (`WORKLOADS=tfjob`) so the sibling flows are not re-recorded. A Kubernetes
   builtin has no operator: run `e2e-up` with `WORKLOADS=none` (`up.sh`
   rejects other names) and `record-e2e` with the flow label; its fixtures
   land under the cluster's Kubernetes version.
   `FLOW=<name>` (or `FLOW="<a>|<b>"`) re-records only those flows, which is
   the normal loop after a failure in a flow or its manifest. A change to a
   status rule or a predicate re-records every flow of the kind. Fixtures are
   recorder output and carry no SPDX header. `phases` lists the flow
   predicates that matched a frame. In a new fixture, every state frame after
   the controller's first status write should list one; the frames before it
   read Undefined and match none, which is normal. Two mean the predicates
   overlap, and since they mirror the status rules, karta-verify on that frame
   shows whether the rules overlap too.
4. `make test-replay` and `make verify-recordings` green. `make lint-shell`
   green, and `GOWORK=off go vet ./...` and `gofmt -l .` clean in `karta/test/e2e`;
   `make check` covers none of these three. Commit the new files before
   `make check`: the `validate` target treats untracked files as stale
   generator output.

## Versioning

`charts/karta/Chart.yaml` keeps `version` and `appVersion` as placeholders (`0.0.0`). The values that actually get published are computed by the [push-artifacts workflow](.github/workflows/push-artifacts.yaml) and overridden at `helm package` time:

| Trigger | Published `version` and `appVersion` |
|---|---|
| Push to `main` (dev build) | `0.0.0-main-<short-sha>` |
| Published GitHub Release | the release's tag (e.g. tag `v1.2.3` -> `1.2.3`) |

Consumers pin a specific release by chart `version` (which equals the tag), e.g. `version: 1.2.3` in the consumer's `Chart.yaml` dependency entry.

This is the same model used by [ai-dynamo/grove](https://github.com/ai-dynamo/grove/blob/main/.github/workflows/push-artifacts.yaml), [NVIDIA/KAI-Scheduler](https://github.com/kai-scheduler/KAI-Scheduler/blob/main/.github/workflows/push-artifacts.yaml), [istio/istio](https://github.com/istio/istio), and [NVIDIA/gpu-operator](https://github.com/NVIDIA/gpu-operator).

### Releasing

A release tags the product as `vX.Y.Z` and the `karta/` library module as
`karta/vX.Y.Z` on the same commit, then publishes the GitHub Release for
`vX.Y.Z`, which starts the release workflow. The two-tag convention, local
snapshot, guarded release command, credentials, and recovery procedure are
documented in [RELEASE.md](RELEASE.md).

No `Chart.yaml` bump is needed. The release tag remains the source of truth for
the published chart version and app version.

## Code of Conduct

Please be respectful and professional in all interactions. We are committed to providing a welcoming and inclusive environment for all contributors. See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for the full code of conduct and how to report conduct concerns.

## Questions?

If you have questions about contributing, please open an issue or contact the maintainers.

Thank you for contributing to karta!
