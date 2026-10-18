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
