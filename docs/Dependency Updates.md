<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Dependency Updates

Renovate keeps dependencies current. It runs as a GitHub Actions workflow,
`.github/workflows/renovate.yaml`, every Sunday at 03:00 UTC. A maintainer can
also start it from the Actions tab (Run workflow), optionally at `debug` log
level. The update policy lives in `renovate.json`.

## What it updates

- Go modules in every `go.mod`, including `karta/sdk`, `cli`, `operator`,
  `test/e2e`, `hack/*` and `docs/examples`. Each update runs `go mod tidy`,
  then `make lib-generate lib-manifests lib-generate-mocks generate-licenses`
  so every module stays tidy and the generated code, CRDs, mocks and license
  files land in the same commit.
- The Go version, as one PR across every `go.mod` and the `GO_VERSION`
  argument of the Dockerfiles.
- GitHub Actions, pinned to commit SHAs.
- Container base images in the Dockerfiles.
- npm packages of `headlamp-plugin`.
- Tool versions in the Makefile and workflow `env` blocks that carry a
  `# renovate: datasource=... depName=...` comment on the line above.

The library module, `github.com/dsx-ai-factory/workload-map/karta/sdk`, is
ignored. The other modules require it at `v0.0.0` through a relative `replace`,
so there is no version to bump.

## Branches and PRs

Each run targets `main` and the three newest release branches. Branches named
`vX.Y` and `release-X.Y` are sorted together by version.

On `main`, minor and patch updates are grouped into one PR per ecosystem, and
each major update gets its own PR. Release branches only receive patch, digest
and security updates. Nothing merges automatically. Reviewers come from
`CODEOWNERS`.

The Dependency Dashboard issue lists pending and rate-limited updates, and
lets a maintainer trigger or retry one.

Every commit carries a `Signed-off-by` trailer for the bot identity, so the DCO
check passes.

## Operator updates and recordings

`Renovate operators` runs separately every Sunday at 04:00 UTC. It updates the
operator pins in `hack/e2e/global.env` on `main`. Each operator group gets its
own job, with at most two jobs running together. The workflow can also run one
group manually through its `operator` input.

The configuration reads release repositories, tag prefixes, channels, and
retractions from the existing `check-updates.sh` wrappers. Knative/Kourier and
Kubeflow/MPI stay grouped. Trainer v2 requires an API migration and is excluded.
Tool pins, Ray runtime, and Gateway API are outside this operator update job.

For each candidate, Renovate changes the pins and runs a post-upgrade hook:

1. Create a fresh kind cluster and run the existing install/verify scripts.
2. Check provisioning metadata against the pins and discover every selected flow
   through Ginkgo.
3. Record those flows and reject missing, skipped, failed, or stale fixtures.
4. Replay the recordings, run `make check`, and remove the temporary cluster.
5. Commit the pins and recordings with the Renovate App identity and DCO.
6. Verify the pushed commit against the tested file hashes, then open or update
   a draft PR with a link to the CI run. Nothing merges automatically.

A Git pre-push hook is installed before Renovate starts. A failed post-upgrade
command cannot publish an update without successful evidence. Evidence is
specific to the source commit and generated file contents. Logs, JUnit reports,
installed versions, image references, and hashes remain available as Actions
artifacts for 14 days.

Infrastructure updates also exercise workloads: cert-manager runs KServe and
Karta's cert-manager webhook tests; Fake GPU Operator runs Grove; KAI runs
Grove and Dynamo. The infrastructure smoke tests still verify certificate
issuance, GPU allocation, and scheduling. Recordings keep their workload
operator version paths; the CI evidence identifies the infrastructure versions.
Passing these flows proves the tested configuration, not every deployment.

### Repository setup

The upstream workflow reuses `RENOVATE_APP_CLIENT_ID` and
`RENOVATE_APP_PRIVATE_KEY`. The App needs contents, pull requests and commit
statuses write access, plus issues read access. Set the repository variable
`E2E_OPERATOR_TRACKING_ISSUE` to the public, open issue that tracks recording
updates. The launcher checks it before publishing.

Forks use `GITHUB_TOKEN` and the `github-actions[bot]` identity. Enable GitHub
Actions to create pull requests in the fork's Actions settings. Fork schedules
are disabled, but manual and reusable workflow calls are supported. Their
`base-branch` input can target a validation branch. Upstream always uses `main`.
Recordings run in CI; the launcher rejects local invocation.

### Extending and testing operator automation

Add the operator's pin variables and workload coverage to
`hack/e2e/renovate/config.cjs`. Release rules still belong in its checker wrapper.
Add each recorded kind to `test/e2e/cmd/verify-operator-update`; flow names are
discovered from the suite, so adding a flow does not require another list.
Test a real update in a fork before enabling or changing its coverage.

```sh
make test-renovate-operators
make check
make lint-shell
```

The automation tests need Python 3.9+ and Node 18+. CI runs them separately from
`make check`, which also includes the Go recording validator tests.

## Testing a configuration change

### Dry run

A dry run reads `renovate.json` from the working tree instead of the default
branch, looks up every dependency, and logs the branches, PRs and commit
messages it would create. It pushes nothing and opens nothing. It needs a
GitHub token that can read the repository. Renovate requires Node 24, so the
command fetches it through `npx` whatever the local Node version is. Run it
from the repository root:

```bash
RENOVATE_TOKEN=$(gh auth token) \
RENOVATE_CONFIG_FILE=renovate.json \
RENOVATE_REQUIRE_CONFIG=ignored \
RENOVATE_FORCE='{"baseBranchPatterns":["main","v0.2"]}' \
LOG_LEVEL=info \
npx --yes --package=node@24 --package=renovate@44.132.2 -- \
  renovate --platform=github --dry-run=full dsx-ai-factory/workload-map
```

List in `baseBranchPatterns` the branches to check, and use the Renovate
version pinned in `.github/workflows/renovate.yaml`. A dry run does not run
the post-upgrade `make` command and does not exercise the workflow or the
GitHub App token. Two warnings are expected: the post-upgrade task does not
match `allowedCommands`, and the default `gitAuthor` is used. The workflow
sets both.

### Workflow run

The workflow can only be started by hand once it is on the default branch.
Start it from Actions, Renovate, Run workflow, with the `debug` log level,
and check:

1. The token step passes. A failure here means the App is not installed on
   the repository, or a repository secret is missing or wrong.
2. The base branch step prints `main` and the three newest release branches.
3. The identity step prints the App's bot name and its noreply address.
4. The Dependency Dashboard issue exists and lists the pending updates.
5. On a Go modules PR, the commit carries the regenerated files when they
   changed, and the DCO and CI checks pass.
