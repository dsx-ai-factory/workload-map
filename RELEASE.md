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
the Homebrew cask, `dist/homebrew/Casks/kli.rb`, without publishing. It does
not need release credentials. `release-verify` checks the archives and that
the cask installs exactly those archives, by checksum and download URL.

The release, the cask download URLs, and the tap follow `GITHUB_REPOSITORY`,
which the workflow always sets, so a fork releases to itself with no edits. A
local snapshot without it renders the upstream values.

## How a release is cut

Before tagging, add the version entry to [CHANGELOG.md](CHANGELOG.md) and run
the checks above. After that preparation change is merged, a release takes two
steps.

First, create both tags on the merged commit and push them in one atomic
operation:

```bash
git tag -m "v1.2.3" v1.2.3
git tag -m "karta/sdk/v1.2.3" karta/sdk/v1.2.3
git push --atomic origin v1.2.3 karta/sdk/v1.2.3
```

Pushing the tags starts no workflow. With `--atomic`, the remote accepts both
tags or neither. The library can be fetched with `go get` from this point.

Then publish the GitHub Release from the existing `v1.2.3` tag, with the
version's CHANGELOG.md entry as its notes. Use the Releases page and choose the
existing tag, or:

```bash
gh release create v1.2.3 --verify-tag --title v1.2.3 --notes-file notes.md
```

`--verify-tag` refuses to create `v1.2.3` when the first step did not push it.
The workflow runs on the `released` event, which fires when a full release is
published or a prerelease is changed to a full release. A draft or a prerelease
starts nothing. A release published as a prerelease by mistake starts the
workflow once "Set as a pre-release" is unchecked; nothing needs to be deleted.

Publishing the release runs the release workflow. Before it publishes anything,
the workflow checks that both tags point to the release's commit and that
`karta/sdk/go.mod` carries no `replace` or `exclude` directive. It also runs
`make release-snapshot` and `make release-verify`, a dry run of the CLI release
that stops a release whose archives or cask would be wrong. It then builds and
pushes the multi-architecture operator image from source and publishes the Helm
chart. GoReleaser then builds the four CLI archives and checksum manifest,
uploads them to the release, keeping its notes, pushes the Homebrew cask to a
`kli-cask-<version>` branch, and opens a pull request for it. Finally, the
workflow generates the two image locks and attaches the chart and locks to the
release. The release shows no assets until the workflow finishes.

The workflow uploads to a release that is already published, so the repository
must not enable GitHub's immutable releases. An immutable release accepts no new
assets, and GoReleaser stops with "already exists and is immutable".

The cask reaches Homebrew users when that pull request merges, so the release
is not complete until a maintainer approves and merges it. If the cask pull
request of an earlier release is still open when a later release opens its
own, merge the later one and close the earlier one. Merging the earlier one
afterwards would move the cask back to the older version.

The guarded publishing command used by the workflow is:

```bash
make release VERSION=1.2.3
```

It fails unless the checkout is clean and at the matching `vX.Y.Z` tag, both
tags point to `HEAD`, `karta/sdk/go.mod` carries no `replace` or `exclude`
directive, and both `GITHUB_TOKEN` and `HOMEBREW_TAP_TOKEN` are set. It must
normally run only in the release workflow.

## Release credentials

The normal workflow `GITHUB_TOKEN` is used for the Karta GitHub Release, GHCR
packages, and release attachments.

The Homebrew cask pull request uses a GitHub App installation token instead.
A pull request opened with `GITHUB_TOKEN` does not trigger workflows, so the
required `CI` check would never report and it could never merge. A pull
request opened by an App runs `CI` like any other.

The App needs these repository permissions and nothing else:

- Contents: read and write, to create the cask branch and commit `Casks/kli.rb`.
- Pull requests: read and write, to open the pull request.
- Metadata: read, which every App has.

It needs no webhook, and it is installed on this repository only. The
repository holds two settings for it:

| Name | Kind | Value |
|---|---|---|
| `HOMEBREW_TAP_APP_CLIENT_ID` | Actions variable | The App's client ID |
| `HOMEBREW_TAP_APP_PRIVATE_KEY` | Actions secret | A private key generated for the App |

The workflow mints the token with `actions/create-github-app-token` after the
tag checks and before anything is published. The token is limited to this
repository and to the two permissions above, expires after an hour, and is
revoked when the job ends. If either setting is missing, or the App is not
installed, the run fails at that step with nothing published. A fork that cuts
a release needs its own App installed on the fork.

GoReleaser commits the cask through the GitHub API without a committer, so
GitHub signs the commit as the App, as the signed-commit rule on the default
branch requires.

The repository rules decide what else the App and the pull request need:

- Branch creation is restricted on every branch except `dependabot/**` and
  `renovate/**`, so the App must be a bypass actor on that ruleset to create
  `kli-cask-<version>`.
- The default branch requires a code owner approval, the `CI` check, and a
  squash merge, so a maintainer reviews and merges every cask update.

## Recovery after a partial release

If the workflow fails before it publishes any image, chart, or asset, nothing
else needs cleaning up. Use Re-run jobs if the failure was transient. If the
fix needs a code change, for example a release snapshot that fails
verification, merge the fix and release the next patch version, because the
tags do not move.

If the release workflow fails after publishing the operator image or Helm chart,
do not create a second release, unpublish the release, or move any tag. Correct
the failure and use Re-run jobs on the same workflow run. A later attempt
validates and reuses the chart and image from the first attempt instead of
overwriting them. GoReleaser replaces matching assets on an existing GitHub
Release and updates `Casks/kli.rb` in place on the existing `kli-cask-<version>`
branch. If the cask pull request is already open, GoReleaser logs a warning
instead of opening a second one. After it succeeds, confirm that the workflow
attached the chart and both image locks to the same release, that the assets
match `checksums.txt`, and that a cask pull request for the version is open or
merged.

The cask is published after the release assets, so a failure there, such as an
App that lacks a permission, fails the run after the assets are uploaded. Fix
the App and use Re-run jobs on the same workflow run.

## Release notes and breaking changes

The GitHub Release body is written from the version's CHANGELOG.md entry when
the release is published, and GoReleaser keeps it; the changelog is the source,
the release body is the copy. Every breaking change
(API field changes, removed or renamed library surface, behavioral changes that
require consumer action) must be documented in the release notes with migration
guidance so that downstream consumers can upgrade predictably.
