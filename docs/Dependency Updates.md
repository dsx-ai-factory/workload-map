<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Dependency Updates

Renovate keeps dependencies current through `.github/workflows/renovate.yaml`.
The workflow runs every Sunday at 03:00 UTC. It also runs on January 1, April 1,
July 1, and October 1 at 03:00 UTC. A maintainer can start it from the Actions tab
(Run workflow), optionally at `debug` log level. The update policy lives in
`renovate.json`.

## What it updates

- Go modules in every `go.mod`, including `karta/sdk`, `cli`, `operator`,
  `test/e2e`, `hack/*` and `docs/examples`. Renovate tidies each updated module.
  The license target then tidies every module, including local consumers.
  Renovate also regenerates SDK code, CRDs, SDK and exporter mocks, catalog files, and licenses
  in the same commit. Indirect dependencies join the same Go update group.
  For major updates, Renovate also attempts to migrate Go import paths.
  API and behavior changes still need review.
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

The `gomodTidy` option updates each changed module's requirements and checksums.
The `generate-licenses` Make target tidies every module after Renovate applies
all dependency updates. This order prevents one module's generated files from
overwriting another module's pending update. The target also regenerates `NOTICE` and
`THIRD_PARTY_LICENSES` from Go dependencies. License generation runs for every
Renovate PR, including npm, container, and GitHub Actions updates. The generated
files are committed when their contents change. Go updates also run the broader
generation command. CI regenerates these files again and rejects stale contents.

## Branches and PRs

Each run targets `main` and the three newest release branches. Branches named
`vX.Y` and `release-X.Y` are sorted together by version.

On `main`, minor and patch updates are grouped into one PR per ecosystem.
Routine major updates on `main` run on January 1, April 1, July 1, and October 1, in UTC.
Existing major-update branches stay unchanged outside that window. Major updates
remain disabled on release branches.
Release branches only receive patch, digest and security updates. Security alerts
remain enabled for non-major fixes. Security alerts that require a major upgrade
need a manual migration or the regular quarterly major update.
Nothing merges automatically. Reviewers come from
`CODEOWNERS`.

Minor and patch releases can still require code changes, especially before version
1.0. CI remains required. A failed post-upgrade task can still produce a PR with an
artifact error, so reviewers must check both generation and CI results.

The Dependency Dashboard issue lists pending and rate-limited updates, and
lets a maintainer trigger or retry one.

Every commit carries a `Signed-off-by` trailer for the bot identity, so the DCO
check passes.

## Testing a configuration change

### Validate the configuration

Run the validator with the same Renovate version as the workflow:

```bash
npx --yes --package=node@26.13.0 --package=renovate@44.222.3 -- \
  renovate-config-validator --strict renovate.json
```

### Dry run

A dry run reads `renovate.json` from the working tree instead of the default
branch, looks up every dependency, and logs the branches, PRs and commit
messages it would create. It pushes nothing and opens nothing. It needs a
GitHub token that can read the repository. The workflow uses Node 26, so the
command fetches it through `npx` whatever the local Node version is. Run it
from the repository root:

```bash
RENOVATE_TOKEN=$(gh auth token) \
RENOVATE_CONFIG_FILE=renovate.json \
RENOVATE_REQUIRE_CONFIG=ignored \
RENOVATE_FORCE='{"baseBranchPatterns":["main","v0.2"]}' \
LOG_LEVEL=info \
npx --yes --package=node@26.13.0 --package=renovate@44.222.3 -- \
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
