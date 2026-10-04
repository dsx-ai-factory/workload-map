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

The root Go workspace contains the library, CLI, and operator modules. The
separate release helper, Karta WASM module, and other nested modules are
deliberately isolated. When running Go commands directly in those modules, set
`GOWORK=off`. Make targets already do this where required.

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

## Versioning

`charts/karta/Chart.yaml` keeps `version` and `appVersion` as placeholders (`0.0.0`). The values that actually get published are computed by the [push-artifacts workflow](.github/workflows/push-artifacts.yaml) and overridden at `helm package` time:

| Trigger | Published `version` and `appVersion` |
|---|---|
| Push to `main` (dev build) | `0.0.0-main-<short-sha>` |
| Tag push (release) | the tag (e.g. tag `v1.2.3` → `1.2.3`) |

Consumers pin a specific release by chart `version` (which equals the tag), e.g. `version: 1.2.3` in the consumer's `Chart.yaml` dependency entry.

This is the same model used by [ai-dynamo/grove](https://github.com/ai-dynamo/grove/blob/main/.github/workflows/push-artifacts.yaml), [NVIDIA/KAI-Scheduler](https://github.com/kai-scheduler/KAI-Scheduler/blob/main/.github/workflows/push-artifacts.yaml), [istio/istio](https://github.com/istio/istio), and [NVIDIA/gpu-operator](https://github.com/NVIDIA/gpu-operator).

### Releasing

The root library, CLI module, and operator module use one synchronized version.
The preparation, two-tag convention, local snapshot, guarded release command,
credentials, and recovery procedure are documented in
[RELEASE.md](RELEASE.md).

No `Chart.yaml` bump is needed. The release tag remains the source of truth for
the published chart version and app version.

## Code of Conduct

Please be respectful and professional in all interactions. We are committed to providing a welcoming and inclusive environment for all contributors. See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for the full code of conduct and how to report conduct concerns.

## Questions?

If you have questions about contributing, please open an issue or contact the maintainers.

Thank you for contributing to karta!
