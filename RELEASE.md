<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Release Process

This document describes how Karta is versioned and released. The mechanics of
cutting a release are documented in [CONTRIBUTING.md](CONTRIBUTING.md#versioning);
this document covers the policy around them.

## Versioning

Karta follows [Semantic Versioning](https://semver.org/). Releases are tagged
`vMAJOR.MINOR.PATCH`.

While Karta is pre-1.0 (`0.y.z`), the API (`run.ai/v1alpha1`) and the Go library
surface may change between minor versions. Breaking changes are called out in the
release notes. Consumers should pin to a specific released version.

## Cadence

Karta releases on an as-needed basis rather than a fixed calendar. A release is
cut when a meaningful set of changes has accumulated on `main`, or when a fix needs
to ship. Minor releases are tagged from `main`. Each minor line then gets a release
branch (`v0.1`, `v0.2`), and patch releases are tagged from that branch, so patch
fixes ship without waiting on `main`.

## Who can cut a release

Releases are cut by project [maintainers](MAINTAINERS.md). Release artifacts are
built by CI from the pushed tag, never from a local machine.

## Synchronized modules and tags

The root library, CLI, and operator are separate Go modules on one product
release train:

- `github.com/dsx-ai-factory/workload-map`
- `github.com/dsx-ai-factory/workload-map/cli`
- `github.com/dsx-ai-factory/workload-map/operator`

Release `1.2.3` uses the tags `v1.2.3` and `cli/v1.2.3`. Both tags must point
to the same commit. Only the root tag starts the release workflow. The operator
ships as a container image and does not need its own module tag.

The CLI and operator `go.mod` files must require the matching root module
version, and `go.work` must replace that same version with the local root
module. Set all three in the release preparation change with one command:

```bash
make pin-bump VERSION=1.2.3
make release-validate VERSION=1.2.3
```

Do not edit the three files by hand. `pin-bump` drops the previous `go.work`
replacement before adding the new one. A bare
`go work edit -replace=...@v1.2.3=.` does not: it keys on the version, so the
old entry stays and a second one is added. Nothing rejects that, because the
validation returns on the first matching replacement, so the stale entries
accumulate one per release.

`pin-check` validates the pin on every pull request. Once a release ships, the
pin names a version that is now released, so pull requests fail until the bump
for the next version lands. Make that bump the first change merged after a
release.

Do not add a relative `replace` directive to either nested `go.mod` file. The
version-specific replacement in the root `go.work` file supplies the local root
module during development and CI, and must be updated with the two requirements.
The matching root version does not exist until the synchronized tags are pushed,
so release preparation cannot run the nested modules with `GOWORK=off` or
precompute their matching root-module checksums.

The release-preparation change can still pass CI before those tags exist.
License generation removes the Karta requirement only from a temporary copy of
the CLI module metadata. Karta itself is excluded from the CLI license templates,
so the third-party inventory does not change and the source `go.mod` remains
untouched.

## Local release validation

The root Makefile is the release interface. GoReleaser is installed locally at
the pinned version declared in the Makefile.

```bash
make goreleaser-check
make release-build VERSION="$(git describe --tags --always --dirty --match 'v[0-9]*.[0-9]*.[0-9]*')"
make release-snapshot VERSION=1.2.3
make release-verify VERSION=1.2.3
```

`release-build` compiles both executable definitions only for the current
runner target. `release-snapshot` builds the complete CLI release matrix
without publishing. It does not need release credentials.

## How a release is cut

Before tagging, add the version entry to [CHANGELOG.md](CHANGELOG.md), update the
two root-module requirements, and run the checks above. After that preparation
change is merged, create both tags from the same commit and push them in one
operation:

```bash
git tag v1.2.3
git tag cli/v1.2.3
git push origin v1.2.3 cli/v1.2.3
```

The root tag runs the coordinated workflow. The workflow builds and pushes the
multi-architecture operator image from source and publishes the Helm chart.
GoReleaser then builds the four CLI archives and checksum manifest and creates
the GitHub Release. Finally, the workflow generates the two image locks and
attaches the chart and locks to the existing release.

The guarded publishing command used by the workflow is:

```bash
make release VERSION=1.2.3
```

It fails unless the checkout is clean and at the matching root tag, both tags
point to `HEAD`, the nested module requirements match, and the required
credentials are present. It must normally run only in the release workflow.

## Release credentials

The normal workflow `GITHUB_TOKEN` is used for the Karta GitHub Release, GHCR
packages, and release attachments. The release needs no other credential.

## Recovery after a partial release

If a tagged workflow fails after publishing the operator image or Helm chart, do
not create a second release or move any tag. Correct the failure and rerun the
same workflow. A later attempt validates and reuses the chart and image from the
first attempt instead of overwriting them. GoReleaser replaces matching assets
on an existing GitHub Release. After it succeeds, confirm that the workflow
attached the chart and both image locks to the same release, and that the assets
match `checksums.txt`.

## Release notes and breaking changes

The GitHub Release body is written from the version's CHANGELOG.md entry; the
changelog is the source, the release body is the copy. Every breaking change
(API field changes, removed or renamed library surface, behavioral changes that
require consumer action) must be documented in the release notes with migration
guidance so that downstream consumers can upgrade predictably.
