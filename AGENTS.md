<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# AGENTS.md - Guide for AI Coding Agents

You are contributing to Karta, an Apache 2.0 open-source Kubernetes project. Stack: Go 1.25, controller-runtime, Helm. Goal: a CRD that lets controllers and platforms inspect, modify, and manage workloads of any type without per-CRD adapters.

If you are an AI coding agent (Cursor, Codex, Claude Code, Aider, Continue, Copilot, etc.) opening this repo for the first time, read this file end to end before making changes. For Claude Code, the sibling `CLAUDE.md` imports this file via `@AGENTS.md`.

## Boundaries

Always do:
- Run `make check` before pushing.
- Sign off commits with `-s` (DCO).
- Add SPDX + copyright header to new source and Markdown files.
- Use Conventional Commits: `feat(scope):`, `fix(scope):`, `refactor(scope):`. Match an existing scope from `git log` when possible.
- Reference an open GitHub issue in the PR.
- Regenerate manifests after changing API types: `make lib-generate && make lib-manifests`.
- Use `git mv` when moving files so history is preserved.

Discuss in the PR description:
- Adding a new third-party Go module (license, alternatives considered).
- Breaking changes to public CRD fields or Helm values.
- Renaming or moving Go packages.
- Changes to `.github/workflows/`.

Never do:
- Hand-edit `charts/karta/crds/`. These are generated. Edit `pkg/api/runai/v1alpha1/` and run `make lib-manifests` instead.
- Disable a `golangci-lint` rule to silence a warning. Fix the underlying issue.
- Include URLs to private or login-gated resources (e.g., company Confluence, Jira, or wikis) in commits, code, or docs.
- Reference unannounced or non-public product features.
- Reference customer or partner names without their explicit permission.
- Push speculative commits to debug CI.

## Security and Secrets

Boundaries for anything touching credentials or security-relevant configuration:

- Never commit credentials of any kind: API keys, tokens, kubeconfig files, certificates, private keys, registry passwords. This applies to test fixtures and examples too.
- Use placeholder values in examples and fixtures. Never paste command output that may embed real cluster endpoints, node names, or internal hostnames.
- Code that needs a secret reads it from an environment variable or a mounted Kubernetes Secret. Document the variable name, never a value.
- Do not add new scripts or workflow steps that download and execute unpinned remote code. Pin the exact version, and verify it against the project's published checksums where upstream provides them. The golangci-lint installer the Makefiles invoke is pinned to `GOLANGCI_LINT_VERSION` and follows upstream's recommended install path; hardening it further is welcome as a separate change.
- Do not weaken webhook TLS or RBAC defaults in the Helm chart to simplify local development. Use the documented certificate modes instead (`docs/Webhook Certificates.md`).
- A secret committed by mistake is not fixed by a follow-up commit. History must be rewritten and the credential rotated. Report it per `SECURITY.md`.

Good and bad examples:

```text
Good example value:  image: ghcr.io/example/inference:v1.2.3
Bad example value:   image: registry.internal.corp/team/inference:latest

Good secret handling:  token := os.Getenv("GITHUB_TOKEN")
Bad secret handling:   token := "<a real token pasted here>" hardcoded in a test
```

For vulnerability handling and disclosure, `SECURITY.md` is authoritative.

## Repo Layout

- `pkg/` Karta Go library source
- `charts/` Helm chart (CRDs auto-generated)
- `docs/` Pre-built Karta definitions and guides

## Build, Test, Lint

One `Makefile` at the repository root is the only one in the repo; there is no per-component Makefile. Use it to build, test, lint, and generate code. `make help` lists everything.

`make check` is the full Go presubmit and CI runs it verbatim, but CI also runs `helm-lint`, `helm-validate`, `image-lock-verify`, `image-lock-test` and `lint-shell`, plus `pin-check` on pull requests only, so a green `check` alone does not guarantee a green CI. `lint` is read-only; `fmt` and the per-component `fmt-*` targets are the only ones that rewrite files. For a single test use `go test`.

The root `go.work` contains the library, CLI, and operator modules that share the
product release dependency graph. The internal `hack/release` helper has its own
module outside the workspace so release-only imports do not become direct
product dependencies and its pinned dependency graph remains isolated. Run
direct Go commands inside nested modules such as `hack/release`, `karta-wasm`,
`test/e2e`, `hack/imagelock`, and `docs/examples` with `GOWORK=off`. The root
Makefile already sets this for its targets.

## Code Style

### Naming and Go patterns

- Files `snake_case.go`; types `PascalCase`; interfaces `-er` suffix or `Interface`; boolean predicates use `is`/`has`/`should` prefix.
- File layout: types, consts, and vars first, functions after, in rough call order. The main type leads, and a type is declared right below its first user (never after a function that uses it).
- `context.Context` first parameter; pointer receivers for state-mutating methods; constructors return interface types when an interface exists; wrap errors with `%w`; do not log and return the same error.
- Test files live next to the code (`*_test.go` in the same package).
- Prefer idiomatic go and effective go best practices (switch/case blocks, sentinel error types etc) 
- Keep code inline, only write helper functions if you test them later or they are re-used elsewhere  
- Prefer idiomatic go and effective go best practices (switch/case blocks, sentinel error types etc).
- Keep code inline, only write helper functions if you test them later or they are re-used elsewhere.

### Linter

`golangci-lint v2` (config: `.golangci.yml`). Active: `gofmt`, `goimports`, `errcheck`, `govet`, `unused`, plus standard presets. `errcheck` is relaxed for `*_test.go`; generated files (`zz_generated.*`) are skipped. Do not disable a rule to silence a warning; fix the issue.

### Comments and Markdown

- Self-documenting code; add a comment only when the *why* is non-obvious. No first-person pronouns (`I`, `we`).
- Markdown: short sentences, no bold for emphasis, no emojis, no em-dash (U+2014), ASCII only.

## Commits and Pull Requests

Commit messages follow Conventional Commits v1.0.0 with a DCO sign-off (`git commit -s`, real name required). The format, allowed types, and scope conventions are documented in `CONTRIBUTING.md` under Commit Messages; that section is the single source of truth. Do not restate the format here.

Use the PR template at `.github/PULL_REQUEST_TEMPLATE.md`. Required: link to an open issue, run `make check`, add or update tests (or explain why none).

## SPDX Headers

Every new source/Markdown file starts with `SPDX-License-Identifier: Apache-2.0` and `Copyright (c) 2026 NVIDIA Corporation`, in the file's comment syntax (Go `//`, Markdown `<!-- -->`, YAML/shell `#`).

Exception: a Markdown file whose only content is an `@` import directive (for example, a sibling `CLAUDE.md` containing only `@AGENTS.md`) has no header.

## Third-Party Dependencies

Allowed licenses: MIT, BSD-2/3, Apache 2.0, ISC. Disallowed: GPL, LGPL, AGPL, any copyleft. After adding a module:

1. `make generate-licenses` to refresh `NOTICE` and `THIRD_PARTY_LICENSES`.
2. Name the dependency and version in the commit body.

`go-licenses` runs in `make validate` and CI. License drift fails the build.

## Tests

Unit tests live next to the code (`*_test.go` in the same package). Mocks are regenerated by `make lib-generate-mocks` (also run by `make test`). Integration fixtures live under `test/`.

## Where to Ask

- Contributing process: `CONTRIBUTING.md` (DCO text, PR flow).
- Project owners: `OWNERS`, `MAINTAINERS.md`.
- Code-area owners: `.github/CODEOWNERS`.
- Security disclosures: `SECURITY.md`.
- Conduct concerns: `CODE_OF_CONDUCT.md`.

For feature discussion or bug reports, open a GitHub issue using the templates under `.github/ISSUE_TEMPLATE/`.
