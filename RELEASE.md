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

Before releasing, add the version entry to [CHANGELOG.md](CHANGELOG.md) and run
the checks above. After that preparation change is merged, a release is one
step: publish the GitHub Release for the new version from the merged commit,
with the version's CHANGELOG.md entry as its notes. Use the Releases page,
choose the target commit, and type the new tag `v1.2.3`, or:

```bash
gh release create v1.2.3 --target <commit> --title v1.2.3 --notes-file notes.md
```

Publishing creates the `v1.2.3` tag on that commit and runs the release
workflow. The workflow runs on the `released` event, which fires when a full
release is published or a prerelease is changed to a full release. A draft or a
prerelease starts nothing. A release published as a prerelease by mistake starts
the workflow once "Set as a pre-release" is unchecked; nothing needs to be
deleted.

The tag must be exactly `vX.Y.Z`. A tag without the leading `v`, or with a
suffix such as `-rc.1`, fails the workflow at its first step, before anything is
tagged or published. Delete that release and its tag, then publish again with
the right name.

The library module is tagged by the workflow, not by hand. Once `make check`
and the dry run of the CLI release pass, the workflow creates `karta/sdk/v1.2.3`
on the release's commit, so the library can be fetched with `go get` once the
workflow reaches that step. The tag waits for the checks because a module
version stays in the Go module proxy once fetched, even if its tag is deleted.
The product tag `v1.2.3` and the library tag are therefore not pushed together:
if the workflow fails before the tag step, the release exists with no library
tag, and rerunning the job creates it. No image, chart, or asset is published
before it.

Creating `karta/sdk/v1.2.3` is a tag push by the workflow's `GITHUB_TOKEN`. The
tag-protection ruleset restricts who may create tags, so `refs/tags/karta/sdk/**`
must be in that ruleset's bypass or exclude list for the step to succeed.

Before it publishes anything, the workflow runs `make check`, then
`make release-snapshot` and `make release-verify`, a dry run of the CLI release
that checks `karta/sdk/go.mod` carries no `replace` or `exclude` directive and
stops a release whose archives or cask would be wrong. After the library tag
step, it validates that both tags point to the release's commit. It then builds
and pushes the multi-architecture operator image from source and publishes the
Helm chart. GoReleaser then builds the four CLI archives and checksum manifest,
uploads them to the release, keeping its notes, pushes the Homebrew cask to a
`kli-cask/<version>` branch, and opens a pull request for it. Finally, the
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
directive, and `GITHUB_TOKEN` is set. It must normally run only in the release
workflow.

## Release credentials

The release needs no credential beyond the workflow's own `GITHUB_TOKEN`, which
covers the Karta GitHub Release, GHCR packages, release attachments, and the
Homebrew cask pull request. There is no App, personal access token, or stored
secret for the cask.

GoReleaser commits `Casks/kli.rb` to a `kli-cask/<version>` branch and opens the
pull request, both with `GITHUB_TOKEN`. Three details make that work under the
default branch's rules:

- The commit is signed. GoReleaser omits the committer (the `use_github_app_token`
  option, whose only effect is that omission), so GitHub signs the API commit as
  `github-actions[bot]`. A bot's unattributed commit is verified, which the
  signed-commit rule requires.
- The pull request runs `CI`. A pull request opened with `GITHUB_TOKEN` starts
  its workflows in an approval-required state, so a maintainer clicks to run `CI`
  on it, then reviews and squash-merges. The repository setting "Allow GitHub
  Actions to create and approve pull requests" must be on, or opening the pull
  request fails with 403.
- The commit carries a `Signed-off-by: github-actions[bot]` trailer, from the
  cask commit message template, so the `dco` check on the pull request passes.

The workflow needs `pull-requests: write` in addition to `contents: write`, both
already declared. No credential is minted; if the Actions pull-request setting is
off, the release fails when GoReleaser opens the pull request, after the image,
chart, and release assets are already published, so the setting must be on before
the first release.

The repository rules decide what else the pull request needs:

- Branch creation is restricted, so `refs/heads/kli-cask/**` must be in that
  ruleset's exclude list (next to `dependabot/**` and `renovate/**`) for the
  cask branch to be created.
- The default branch requires a code owner approval, the `CI` check, the `dco`
  check, and a squash merge, so a maintainer reviews and merges every cask
  update.

A fork that cuts a release needs the same Actions pull-request setting on and
the same branch-creation exclude; it needs no secret.

## Recovery after a partial release

If the workflow fails before it publishes any image, chart, or asset, nothing
else needs cleaning up. Use Re-run jobs if the failure was transient. If the
fix needs a code change, for example a release snapshot that fails
verification, merge the fix and release the next patch version, because the
tags do not move. Delete the GitHub Release of the failed version, which has
notes but no assets, so it does not stay marked as the latest release.

If the release workflow fails after publishing the operator image or Helm chart,
do not create a second release, unpublish the release, or move any tag. Correct
the failure and use Re-run jobs on the same workflow run. A later attempt
validates and reuses the chart and image from the first attempt instead of
overwriting them. GoReleaser replaces matching assets on an existing GitHub
Release and updates `Casks/kli.rb` in place on the existing `kli-cask/<version>`
branch. If the cask pull request is already open, GoReleaser logs a warning
instead of opening a second one. If it was already merged, GoReleaser opens a
new cask pull request for the same version. Its cask is already on the default
branch, so close it without merging. After it succeeds, confirm that the workflow
attached the chart and both image locks to the same release, that the assets
match `checksums.txt`, and that a cask pull request for the version is open or
merged.

The cask is published after the release assets, so a failure there, such as the
Actions pull-request setting being off, fails the run after the assets are
uploaded. Correct it and use Re-run jobs on the same workflow run.

## Release notes and breaking changes

The GitHub Release body is written from the version's CHANGELOG.md entry when
the release is published, and GoReleaser keeps it; the changelog is the source,
the release body is the copy. Every breaking change
(API field changes, removed or renamed library surface, behavioral changes that
require consumer action) must be documented in the release notes with migration
guidance so that downstream consumers can upgrade predictably.
