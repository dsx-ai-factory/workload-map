# Karta Roadmap

This document describes where Karta is headed. It is a **living document** — the
direction is set by the community and reviewed regularly, so items move, change,
or drop as the project learns. Dates are intentionally avoided in favor of **Now / Next /
Later** horizons; concrete delivery is tracked in
[GitHub issues](https://github.com/dsx-ai-factory/workload-map/issues) and milestones.

For the day-to-day picture of what is actively being built, the issue tracker is
the source of truth. This page is the *why* and the *order*.

## Vision

Karta aims to be **the standard, vendor-neutral way to describe the structure of
any Kubernetes workload** — so that schedulers, controllers, dashboards, and
platforms can inspect, modify, and manage any workload type without writing
bespoke per-CRD code.

This removes the burden on workload-agnostic tools of diving into the internals
of each workload type's operator. A tested, verified Karta encodes that structure
once, so every consumer relies on the same vetted description.

Success looks like: a workload type (built-in, Kubeflow, Ray, KServe, a custom
CRD, or one that does not exist yet) can be described once with a Karta, and any
Karta-aware tool understands it for free.

## How this roadmap works

- **Now** — actively in progress; tracked by open issues with current activity.
- **Next** — agreed direction, scheduled after the *Now* set lands.
- **Later** — directional intent; shape and timing still open to community input.

Anyone can propose a change to this roadmap by opening a GitHub issue or raising
it in a community discussion. Roadmap changes are reviewed by the maintainers
(see [MAINTAINERS.md](MAINTAINERS.md)) and merged via pull request, so the history
of the roadmap is itself public and reviewable.

---

## Now

**Theme: complete workload visibility and land the metrics exporter.**

- **Workload visibility.** The CLI (`kli`) and the Headlamp plugin shipped in
  v0.3.0. What remains is the first-class workload tree: traversal of a
  workload's full resource tree, from the root component down through child
  components to the pods, so consumers can reason about and render the whole
  structure rather than individual fields. Tracked by
  [#32](https://github.com/dsx-ai-factory/workload-map/issues/32).
- **Metrics exporter.** An exporter that publishes workload state and
  pod-to-workload attribution derived from Karta descriptions, making any
  cluster's existing metrics addressable at workload and role scope, without
  hand-authored queries per workload type. The design is merged as
  [KEP-0001](docs/keps/0001-metrics-exporter/); implementation is tracked by
  [#336](https://github.com/dsx-ai-factory/workload-map/issues/336) and its
  sub-issues.
- **Cross-resource expressions.** Let a Karta reference values from related
  resources in its path expressions
  ([#80](https://github.com/dsx-ai-factory/workload-map/issues/80)).

## Next

Theme: broader coverage.

- **Broader, tested workload coverage.** Keep pace with upstream APIs of the
  workloads Karta describes and grow the tested catalog, including
  **Kubeflow Trainer v2**.

## Later

**Theme: ecosystem.**

*Later items are directional, not commitments — order and shape will change with community input.*

- **Karta-aware tooling ecosystem.** Karta is most valuable when many tools read
  and act on the same description. Karta itself stays a thin, neutral contract;
  richer behavior lives in separate projects across the Karta ecosystem. Examples
  of such ecosystem projects that read and act on the contract:
  - **Mutation runtime** — a controller that submits and mutates workloads and
    provisions secondary resources from a Karta description, without per-CRD code.
  - **Governance / policy engine** — admission- and runtime-level rules
    (filter / condition / action) applied to Karta-described workloads, including
    descheduling and rebalancing.

  Clear integration patterns will be documented so anyone can build their own
  Karta-aware tool.
- **Shared Karta registry.** Beyond the built-in catalog, a discoverable,
  versioned registry where the community can publish, find, and reuse Karta
  definitions for common workload types, instead of every consumer re-bundling
  its own copies.
- **Community engagement with related projects.** Engage the relevant upstream
  communities (workload and scheduling SIGs/TAGs, queueing and gang-scheduling
  projects) to position Karta as complementary infrastructure — *how to read and
  modify the structure of a workload* — rather than as a competing scheduler or
  queue.

---

## Non-goals

Karta deliberately stays narrow. It describes workload **structure**; it does not
schedule, queue, autoscale, or own a workload's lifecycle. Those belong to the
tools that consume a Karta description. Keeping Karta a thin contract is what lets
it stay vendor-neutral and broadly adoptable.

## Changelog

Completed milestones, most recent first. The GitHub issue is the permanent record;
this section is a quick reference for what has already shipped.

- **v0.3.0 (Oct 2026).** Full toolchain around the CRD and library; see the
  [v0.3.0 changelog entry](CHANGELOG.md#v030---2026-10-21). Highlights:
  - `kli` CLI for read-only workload visibility, with a Homebrew cask.
  - Headlamp plugin workloads page, backed by a WebAssembly build of the
    engine.
  - Built-in catalog of 23 workload definitions and a resolver API
    ([#86](https://github.com/dsx-ai-factory/workload-map/issues/86), Jul 2026).
  - Automated tests that validate the bundled definitions against upstream
    CRDs, plus recorded end-to-end flows that replay offline in CI
    ([#79](https://github.com/dsx-ai-factory/workload-map/issues/79), Sep 2026).
  - Admission webhook and FIPS 140-3 runtime support.
- Karta controller / operator: standalone controller that reconciles Karta
  resources and the workloads they describe, turning Karta from a Go library
  into a deployable project
  ([#34](https://github.com/dsx-ai-factory/workload-map/issues/34), Aug 2026).
- Helm chart / CRD upgrade hook: Helm chart hardening and CRD upgrader hook
  shipped ([#43](https://github.com/dsx-ai-factory/workload-map/issues/43), Aug 2026).
- Dynamo `v1beta1` support in the Dynamo example
  ([#78](https://github.com/dsx-ai-factory/workload-map/issues/78), Jul 2026).

---

## Get involved

- Browse [open issues](https://github.com/dsx-ai-factory/workload-map/issues) and look for
  `good first issue` and `help wanted` labels.
- Have a workload type Karta should describe? Open an issue or contribute a
  typed definition under [`karta/sdk/pkg/catalog/kartas/`](karta/sdk/pkg/catalog/kartas/); the
  generated YAML lands in [`docs/catalog/`](docs/catalog/).
- Read [CONTRIBUTING.md](CONTRIBUTING.md) to get started (DCO sign-off required).

*This roadmap is reviewed regularly and updated as priorities shift with
community input.*
