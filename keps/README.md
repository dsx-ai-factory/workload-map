<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Karta Enhancement Proposals (KEPs)

A KEP records a proposed change and the reasons for it before implementation.
For example, a change to how Karta evaluates expressions needs agreement on
behavior, compatibility, and testing before code review.

## When to write one

Use a KEP for CRD schema changes, changes to expression semantics, new Go
packages intended for external consumers, or breaking changes to user-facing
behavior, including Go APIs, CLI commands, Helm values, and operator behavior.
Features that need design agreement across components also need a KEP.

Fixes that restore documented behavior, catalog additions, documentation, tests,
and internal refactors can go directly to a PR unless they change one of those
contracts. Other additive Go API changes can go directly to a PR. If the boundary
is unclear, ask a maintainer in the issue.

## Submit a KEP

1. Open an upstream issue describing the problem, or use an existing one.
2. Copy [TEMPLATE.md](TEMPLATE.md) to `keps/NNNN-short-name/README.md`.
   Start at `0001` and choose the next unused number, checking merged KEPs and
   open KEP PRs. Keep any diagrams beside the README.
3. Keep the proposal as short as the decision allows. For a section that does
   not apply, say why in one sentence. Add a link under the index below.
4. Open a PR with only the KEP, its supporting files, and the index entry.
   Start with status `provisional`. Reference the issue without a closing
   keyword; keep it open for implementation.
5. The author sets `implementable` before requesting final approval. A
   maintainer's approval of that revision accepts the design; merge follows the
   [contribution review process](../CONTRIBUTING.md#review-process).
   Close the PR if the proposal is declined or withdrawn.
6. Implementation PRs link the KEP and issue. The final implementation PR sets
   `implemented`, updates History, and closes the issue when it merges.

Design acceptance does not commit maintainers to implement or schedule the work.
Amendments follow the same approval process before implementation. An update
that adds unmerged work proposes status `implementable`; the merged design and
status remain current until the update merges. The author or a maintainer can
stop further work through a PR setting `withdrawn`, with the reason or replacement
recorded in History. This does not remove behavior already shipped. For later
updates, open a new issue if the original is closed.

## Status

| Status | Meaning |
|---|---|
| `provisional` | The design is under discussion. |
| `implementable` | The design is accepted but not fully implemented. |
| `implemented` | All planned work is merged; this does not imply it has been released. |
| `withdrawn` | Further implementation is no longer planned. History explains why. |

## Index

No KEPs have been added yet.
