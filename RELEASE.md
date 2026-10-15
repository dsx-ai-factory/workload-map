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
built by CI when a maintainer publishes the GitHub Release, never from a local
machine.

## Modules and tags

The library is the only published Go module:

- `github.com/dsx-ai-factory/workload-map/karta/sdk`, in `karta/sdk/`

The CLI, operator, and the other modules in the repository require it at
`v0.0.0` with a relative `replace` directive, so they always build against the
library in the same commit. Those modules are not published: the CLI ships as
the GoReleaser archives on the GitHub Release, and the operator ships as a
container image. `go install` of the CLI is not supported, because the go
command refuses a module whose `go.mod` carries a `replace`.

Release `1.2.3` uses the tags `v1.2.3` and `karta/sdk/v1.2.3`, and both must point
to the same commit. `v1.2.3` names the GitHub Release, the operator image, the
Helm chart, and the CLI archives. `karta/sdk/v1.2.3` is the library's Go
module version; the go command requires the `karta/sdk/` prefix for a module that
lives in a subdirectory.

`karta/sdk/go.mod` must never carry a `replace` or `exclude` directive. Both apply
only when the module is the one being built, so a consumer's `go get` would
resolve a different dependency graph than the repository tests.
`make modules-check-lib` enforces this in `make check`, and the release guard
checks it again.

Releases up to v0.2.x published the library as
`github.com/dsx-ai-factory/workload-map`, from the repository root. Patch
releases on the `v0.2` branch keep that path. On `main`, the root `go.mod` is a
stub with no packages and a `Deprecated:` comment, so every later `vX.Y.Z` tag
is a version of the old path that `go get` and `go list -u` report as
deprecated, with a pointer to the new path. Upgrading to it still fails to
build, so the notes for the first release after the move must carry the
migration to the new import path.

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
runner target. `release-snapshot` builds the complete CLI release matrix and
the Homebrew cask without publishing. It does not need release credentials.
`release-verify` checks the archives and that the cask installs exactly those
archives.

The release and the cask follow `GITHUB_REPOSITORY`, so a fork releases to
itself. A local snapshot renders the upstream values.

## How a release is cut

Before releasing, add the version entry to [CHANGELOG.md](CHANGELOG.md) and run
the checks above. After that preparation change is merged, publish the GitHub
Release from the merged commit, with a new `v1.2.3` tag and the version's
CHANGELOG.md entry as its notes. Use the Releases page, or:

```bash
gh release create v1.2.3 --target <commit> --title v1.2.3 --notes-file notes.md
```

Publishing creates the tag and starts the release workflow. Drafts and
prereleases start nothing; a prerelease starts it once changed to a full
release.

The tag must be exactly `vX.Y.Z`. Any other tag fails the first step, before
anything is tagged or published. Delete that release and its tag, and publish
again.

Once `make check` and a dry run of the CLI release pass, the workflow creates
`karta/sdk/v1.2.3` on the same commit. It waits for the checks because a
fetched module version stays in the Go module proxy even if its tag is deleted.
If the workflow fails before this step, Re-run jobs creates the tag.

The workflow then checks both tags, pushes the operator image, and publishes
the Helm chart. GoReleaser uploads the CLI archives and checksums to the
release, keeping its notes, and opens a pull request that updates
`Casks/kli.rb`. Finally, the workflow attaches the chart and the two image
locks. The release shows no assets until the workflow finishes.

The workflow uploads to a release that is already published, so the repository
must not enable GitHub's immutable releases. An immutable release accepts no new
assets, and GoReleaser stops with "already exists and is immutable".

The cask reaches Homebrew users when its pull request merges. A maintainer
approves the held `CI` run, reviews, and squash-merges. If an older cask pull
request is still open, close it; merging it would move the cask back.

The guarded publishing command used by the workflow is:

```bash
make release VERSION=1.2.3
```

It fails unless the checkout is clean and at the matching `vX.Y.Z` tag, both
tags point to `HEAD`, `karta/sdk/go.mod` carries no `replace` or `exclude`
directive, and the required credentials are present. It must normally run
only in the release workflow.

## Release credentials

The normal workflow `GITHUB_TOKEN` is used for the Karta GitHub Release, GHCR
packages, release attachments, and the cask pull request. The release needs no
other credential. GitHub signs the cask commit as `github-actions[bot]`, and its
`Signed-off-by` trailer satisfies `dco`.

The repository, and any fork that releases, needs:

- "Allow GitHub Actions to create and approve pull requests" on. Without it, the
  run fails after the assets are uploaded.
- `refs/heads/kli-cask/**` excluded from the branch-creation ruleset.
- `refs/tags/karta/sdk/**` excluded from the tag-protection ruleset.

## Recovery after a partial release

If the workflow fails before it publishes any image, chart, or asset, use
Re-run jobs if the failure was transient. If the fix needs a code change, merge
it and release the next patch version, because the tags do not move. Delete the
failed GitHub Release so it does not stay marked as the latest.

If the release workflow fails after publishing the operator image or Helm chart,
do not create a second release, unpublish the release, or move any tag. Correct
the failure and use Re-run jobs on the same workflow run. A later attempt
validates and reuses the chart and image from the first attempt instead of
overwriting them. GoReleaser replaces matching assets on an existing GitHub
Release and updates an open cask pull request. If the cask pull request already
merged, the rerun opens a second one; close it. After it succeeds, confirm that
the workflow attached the chart and both image locks to the same release, that
the assets match `checksums.txt`, and that the cask pull request is open or
merged.

## Release notes and breaking changes

The GitHub Release body is written from the version's CHANGELOG.md entry when
the release is published, and GoReleaser keeps it; the changelog is the source,
the release body is the copy. Every breaking change
(API field changes, removed or renamed library surface, behavioral changes that
require consumer action) must be documented in the release notes with migration
guidance so that downstream consumers can upgrade predictably.
