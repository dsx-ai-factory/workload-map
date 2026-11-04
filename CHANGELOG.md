<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Changelog

Notable changes per release. This file is the in-repo source of record: a release's entry is added here before the tag is pushed (see [RELEASE.md](RELEASE.md)), and the [GitHub Release](https://github.com/dsx-ai-factory/workload-map/releases) body carries the same content, normalized to the repository Markdown rules in [AGENTS.md](AGENTS.md).

Scope: this file covers releases tagged from `main`. Patch releases on a minor line are tagged from that line's release branch and are documented in the changelog there. The v0.0.x releases predate that scheme and are summarized at the end.

## v0.3.0 - 2026-10-21

This release turns Karta into a full toolchain around the CRD and the library. It lands the `kli` CLI with its release pipeline and Homebrew cask, the Headlamp plugin backed by a WebAssembly build of the engine, a generated built-in catalog of 23 workload definitions, an admission webhook and a CRD upgrade hook in the chart, FIPS 140-3 runtime support for the operator, and a recorded end-to-end conformance suite that replays offline.

The CRD schema is unchanged. The Go library moved into a nested module, so Go consumers must update their import paths. See [Breaking changes](#breaking-changes).

---

### Highlights

| | |
|---|---|
| `kli` CLI | A workload-aware CLI over any Karta definition: `get`, `describe`, `definitions`, `validate`, with table, JSON and YAML output, shell completion, a config file, and distinguishable exit codes. Released as signed archives and a Homebrew cask. ([#307](https://github.com/dsx-ai-factory/workload-map/pull/307), [#327](https://github.com/dsx-ai-factory/workload-map/pull/327), [#397](https://github.com/dsx-ai-factory/workload-map/pull/397), [#466](https://github.com/dsx-ai-factory/workload-map/pull/466)) |
| Headlamp plugin | A workloads page for Headlamp that resolves any workload type through the Karta engine compiled to WebAssembly, with no server-side component. ([#247](https://github.com/dsx-ai-factory/workload-map/pull/247), [#303](https://github.com/dsx-ai-factory/workload-map/pull/303), [#414](https://github.com/dsx-ai-factory/workload-map/pull/414)) |
| Built-in catalog | Definitions are now Go code in `pkg/catalog`, generated into `docs/catalog/`. 23 definitions ship in-tree, including KServe `LLMInferenceService` and the SIG Agent Sandbox. ([#158](https://github.com/dsx-ai-factory/workload-map/pull/158), [#189](https://github.com/dsx-ai-factory/workload-map/pull/189), [#394](https://github.com/dsx-ai-factory/workload-map/pull/394), [#443](https://github.com/dsx-ai-factory/workload-map/pull/443)) |
| Recorded e2e conformance | Every catalog definition ships with a recorded flow against the real controller, and the recordings replay offline in CI as a regression suite. ([#220](https://github.com/dsx-ai-factory/workload-map/pull/220), [#260](https://github.com/dsx-ai-factory/workload-map/pull/260)) |
| Admission webhook and CRD upgrader | The operator validates Kartas on admission, stamps GVK labels, rejects a duplicate Karta per group/kind, and the chart upgrades the CRD through a pre-install hook. ([#218](https://github.com/dsx-ai-factory/workload-map/pull/218), [#219](https://github.com/dsx-ai-factory/workload-map/pull/219), [#199](https://github.com/dsx-ai-factory/workload-map/pull/199)) |
| FIPS 140-3 | The operator image links Go's CMVP-validated cryptographic module, and `global.fipsMode` selects the runtime mode. ([#335](https://github.com/dsx-ai-factory/workload-map/pull/335), [#359](https://github.com/dsx-ai-factory/workload-map/pull/359)) |

---

### Install

CLI (Homebrew on macOS and Linux):

```bash
brew tap dsx-ai-factory/kli https://github.com/dsx-ai-factory/workload-map
brew install dsx-ai-factory/kli/kli
```

Prebuilt `kli` archives for linux and darwin on amd64 and arm64, with `checksums.txt`, are attached to this release.

Helm chart (from GHCR):

```bash
helm upgrade --install karta oci://ghcr.io/dsx-ai-factory/workload-map/karta \
  --version 0.3.0 -n karta-system --create-namespace
```

Go consumers:

```bash
go get github.com/dsx-ai-factory/workload-map/karta/sdk@v0.3.0
```

```go
import karta "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
```

---

### Compatibility

| | |
|---|---|
| Kubernetes | built against `k8s.io/api v0.37.1`; e2e runs on kind `v1.34.0`, controller tests on envtest `1.31.0` |
| Go | 1.27.3 or later |
| Helm | 3.14 or later |

---

### Breaking changes

#### The Go library moved to a nested module

The library is now the `karta/sdk/` module, published as
`github.com/dsx-ai-factory/workload-map/karta/sdk`. The repository root `go.mod`
is a stub with no packages and a `Deprecated:` comment, so `go get` and
`go list -u` on the old path report where it went. Upgrading the old path to
v0.3.0 still fails to build, so Go consumers must move. ([#447](https://github.com/dsx-ai-factory/workload-map/pull/447))

```diff
- require github.com/dsx-ai-factory/workload-map v0.2.0
+ require github.com/dsx-ai-factory/workload-map/karta/sdk v0.3.0
```

```diff
- import "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
+ import "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
```

The release is tagged twice on the same commit: `v0.3.0` names the GitHub
Release, the operator image, the chart and the CLI archives, and
`karta/sdk/v0.3.0` is the library's Go module version.

#### Repository, image and chart coordinates moved

The repository moved from the run-ai organization to
[`dsx-ai-factory/workload-map`](https://github.com/dsx-ai-factory/workload-map)
and the project was renamed from Karta to Workload-Map. Git operations keep
working through GitHub redirects, but pinned automation does not: the chart and
the operator image are published under `ghcr.io/dsx-ai-factory/workload-map/`
rather than `ghcr.io/run-ai/karta/`. ([#365](https://github.com/dsx-ai-factory/workload-map/pull/365), [#367](https://github.com/dsx-ai-factory/workload-map/pull/367), [#370](https://github.com/dsx-ai-factory/workload-map/pull/370), [#375](https://github.com/dsx-ai-factory/workload-map/pull/375))

#### Sample definitions moved to the generated catalog

`docs/samples/` is replaced by `docs/catalog/`, generated from the Go
definitions in `pkg/catalog`. File names now follow the GVK, for example
`docs/catalog/leaderworkerset-x-k8s-io-leaderworkerset-v1.yaml`. Links to the
old sample paths need updating. ([#189](https://github.com/dsx-ai-factory/workload-map/pull/189), [#373](https://github.com/dsx-ai-factory/workload-map/pull/373))

The CRD itself is unchanged in this release. `gangScheduling.podGroups` remains
deprecated in favor of `gangScheduling.podGroup`, with the caveats recorded in
the v0.2.0 notes.

---

### New features

#### CLI

- `get` command for workloads, with table, JSON and YAML output. ([#307](https://github.com/dsx-ai-factory/workload-map/pull/307), [#306](https://github.com/dsx-ai-factory/workload-map/pull/306) by @rogirun)
- `describe` command: the resolved view, the renderer, and `-f` for an unsubmitted manifest. ([#327](https://github.com/dsx-ai-factory/workload-map/pull/327), [#325](https://github.com/dsx-ai-factory/workload-map/pull/325), [#326](https://github.com/dsx-ai-factory/workload-map/pull/326), [#328](https://github.com/dsx-ai-factory/workload-map/pull/328) by @rogirun)
- `definitions` command and the definition resolver that backs it. ([#309](https://github.com/dsx-ai-factory/workload-map/pull/309), [#257](https://github.com/dsx-ai-factory/workload-map/pull/257) by @shaked-bouktus)
- `validate` command for Karta definition files. ([#319](https://github.com/dsx-ai-factory/workload-map/pull/319) by @shaked-bouktus)
- `get --all-types` for cross-type listing. ([#448](https://github.com/dsx-ai-factory/workload-map/pull/448) by @rogirun)
- Resolved workload view and live pod attribution. ([#305](https://github.com/dsx-ai-factory/workload-map/pull/305), [#324](https://github.com/dsx-ai-factory/workload-map/pull/324) by @rogirun)
- Shell completion for workload types and names. ([#425](https://github.com/dsx-ai-factory/workload-map/pull/425) by @rogirun)
- `--component-limit` on `describe` and `definitions`. ([#424](https://github.com/dsx-ai-factory/workload-map/pull/424) by @rogirun)
- Warnings printed after the command output. ([#423](https://github.com/dsx-ai-factory/workload-map/pull/423) by @rogirun)
- Distinguishable exit codes, including a dedicated code for a missing workload. ([#304](https://github.com/dsx-ai-factory/workload-map/pull/304), [#323](https://github.com/dsx-ai-factory/workload-map/pull/323) by @rogirun)
- Emit the object itself when a request named exactly one resource. ([#372](https://github.com/dsx-ai-factory/workload-map/pull/372) by @shaked-bouktus)
- Viper-backed config, shared version package and `--version`. ([#221](https://github.com/dsx-ai-factory/workload-map/pull/221), [#223](https://github.com/dsx-ai-factory/workload-map/pull/223) by @shaked-bouktus)

#### Headlamp plugin

- Plugin scaffold and the Karta hooks. ([#247](https://github.com/dsx-ai-factory/workload-map/pull/247), [#355](https://github.com/dsx-ai-factory/workload-map/pull/355) by @moshe-runai)
- `karta-wasm`: the Karta engine exposed to the plugin as WebAssembly. ([#303](https://github.com/dsx-ai-factory/workload-map/pull/303) by @moshe-runai)
- Workloads page, served-kind discovery and the workload row pipeline. ([#414](https://github.com/dsx-ai-factory/workload-map/pull/414), [#409](https://github.com/dsx-ai-factory/workload-map/pull/409) by @moshe-runai)
- Engine phase helper and cell formatters. ([#408](https://github.com/dsx-ai-factory/workload-map/pull/408) by @moshe-runai)

#### Catalog

- Catalog package, generator and seed definitions. ([#158](https://github.com/dsx-ai-factory/workload-map/pull/158) by @rogirun)
- Kubeflow and Ray definitions ported into the catalog. ([#170](https://github.com/dsx-ai-factory/workload-map/pull/170) by @rogirun)
- Serving definitions ported into the catalog. ([#184](https://github.com/dsx-ai-factory/workload-map/pull/184) by @rogirun)
- Milvus, Dynamo and Grove definitions ported into the catalog. ([#188](https://github.com/dsx-ai-factory/workload-map/pull/188) by @rogirun)
- KServe `LLMInferenceService` for both served versions. ([#394](https://github.com/dsx-ai-factory/workload-map/pull/394) by @ntheanh201)
- SIG Agent Sandbox definition, with suspend and resume semantics. ([#443](https://github.com/dsx-ai-factory/workload-map/pull/443) by @ozbarshalom)

#### Operator and chart

- Admission webhook that stamps GVK labels and validates Kartas. ([#218](https://github.com/dsx-ai-factory/workload-map/pull/218) by @AviadHayumi)
- Reject a duplicate Karta per group/kind. ([#219](https://github.com/dsx-ai-factory/workload-map/pull/219) by @AviadHayumi)
- Root component GVK is immutable, and its labels are protected. ([#225](https://github.com/dsx-ai-factory/workload-map/pull/225) by @AviadHayumi)
- CRD upgrader hook in the chart. ([#199](https://github.com/dsx-ai-factory/workload-map/pull/199) by @AviadHayumi)
- FIPS 140-3 runtime mode, selected by `global.fipsMode`. ([#335](https://github.com/dsx-ai-factory/workload-map/pull/335), [#359](https://github.com/dsx-ai-factory/workload-map/pull/359) by @nshidlansik)
- Operator runtime image built on scratch. ([#378](https://github.com/dsx-ai-factory/workload-map/pull/378) by @yuval-gr)

#### End-to-end testing

- Workload conformance recorder package. ([#220](https://github.com/dsx-ai-factory/workload-map/pull/220) by @AviadHayumi)
- Offline replay suite for the recorded fixtures. ([#260](https://github.com/dsx-ai-factory/workload-map/pull/260) by @AviadHayumi)
- Recorded flows for pod, batch job, cronjob, deployment, statefulset, jobset, mpijob, pytorchjob, rayjob, raycluster, lws, knative, kserve, nim, dynamo, milvus and grove. ([#256](https://github.com/dsx-ai-factory/workload-map/pull/256), [#285](https://github.com/dsx-ai-factory/workload-map/pull/285), [#279](https://github.com/dsx-ai-factory/workload-map/pull/279), [#286](https://github.com/dsx-ai-factory/workload-map/pull/286), [#287](https://github.com/dsx-ai-factory/workload-map/pull/287), [#284](https://github.com/dsx-ai-factory/workload-map/pull/284), [#281](https://github.com/dsx-ai-factory/workload-map/pull/281), [#280](https://github.com/dsx-ai-factory/workload-map/pull/280), [#288](https://github.com/dsx-ai-factory/workload-map/pull/288), [#289](https://github.com/dsx-ai-factory/workload-map/pull/289), [#290](https://github.com/dsx-ai-factory/workload-map/pull/290), [#291](https://github.com/dsx-ai-factory/workload-map/pull/291), [#292](https://github.com/dsx-ai-factory/workload-map/pull/292), [#293](https://github.com/dsx-ai-factory/workload-map/pull/293), [#294](https://github.com/dsx-ai-factory/workload-map/pull/294), [#295](https://github.com/dsx-ai-factory/workload-map/pull/295), [#282](https://github.com/dsx-ai-factory/workload-map/pull/282) by @AviadHayumi)
- Operator e2e suite, and installing Karta three ways for it. ([#374](https://github.com/dsx-ai-factory/workload-map/pull/374), [#340](https://github.com/dsx-ai-factory/workload-map/pull/340) by @shaked-bouktus)

#### Release and tooling

- GoReleaser release pipeline for the CLI. ([#397](https://github.com/dsx-ai-factory/workload-map/pull/397) by @shaked-bouktus)
- Homebrew cask for `kli`. ([#466](https://github.com/dsx-ai-factory/workload-map/pull/466) by @shaked-bouktus)
- Air-gap image locks generated per release. ([#229](https://github.com/dsx-ai-factory/workload-map/pull/229) by @AviadHayumi)
- `add-workload-type` authoring skill, with a benchmark and sharpened eval assertions. ([#187](https://github.com/dsx-ai-factory/workload-map/pull/187), [#253](https://github.com/dsx-ai-factory/workload-map/pull/253) by @rogirun)

---

### Bug fixes

- Parse numeric strings when extracting scale. ([#235](https://github.com/dsx-ai-factory/workload-map/pull/235) by @AviadHayumi)
- Compare the `byExpression` result to the expected value directly. ([#299](https://github.com/dsx-ai-factory/workload-map/pull/299) by @AviadHayumi)
- Omit empty optional pointer fields in `ConditionsDefinition`. ([#176](https://github.com/dsx-ai-factory/workload-map/pull/176) by @rogirun)
- Print a GVK as group/version/Kind. ([#422](https://github.com/dsx-ai-factory/workload-map/pull/422) by @rogirun)
- Fix the CRD cache transform. ([#165](https://github.com/dsx-ai-factory/workload-map/pull/165) by @shaked-bouktus)
- Set container resource limits on the CRD upgrader job. ([#368](https://github.com/dsx-ai-factory/workload-map/pull/368) by @rogirun)
- Bump the operator memory limit to 256Mi. ([#163](https://github.com/dsx-ai-factory/workload-map/pull/163) by @shaked-bouktus)
- Set `GODEBUG` explicitly even when `fipsMode=off`. ([#349](https://github.com/dsx-ai-factory/workload-map/pull/349) by @nshidlansik)
- Make the workloads table filters work in the Headlamp plugin. ([#454](https://github.com/dsx-ai-factory/workload-map/pull/454) by @moshe-runai)
- Correct the label keys in the `ensureLabels` comment. ([#396](https://github.com/dsx-ai-factory/workload-map/pull/396) by @ntheanh201)
- Act on the review of the definition resolver. ([#297](https://github.com/dsx-ai-factory/workload-map/pull/297) by @shaked-bouktus)
- Disable service account token automount in workload fixtures. ([#387](https://github.com/dsx-ai-factory/workload-map/pull/387) by @rogirun)

---

### Maintenance

- One Makefile for every component. ([#314](https://github.com/dsx-ai-factory/workload-map/pull/314) by @shaked-bouktus)
- `clean`, `clean-all`, `install` and `uninstall` make targets. ([#445](https://github.com/dsx-ai-factory/workload-map/pull/445) by @rogirun)
- Render generated catalog YAML in struct declaration order. ([#174](https://github.com/dsx-ai-factory/workload-map/pull/174) by @ronlv10)
- Update dependencies with Renovate instead of Dependabot, and self-host it. ([#420](https://github.com/dsx-ai-factory/workload-map/pull/420) by @Snirx1, [#469](https://github.com/dsx-ai-factory/workload-map/pull/469) by @yuval-gr)
- Take the Go image version from `go.mod`. ([#421](https://github.com/dsx-ai-factory/workload-map/pull/421) by @Snirx1)
- Run the release branches' own regeneration targets in Renovate, and bump npm overrides entries rather than the lockfile alone. ([#501](https://github.com/dsx-ai-factory/workload-map/pull/501), [#511](https://github.com/dsx-ai-factory/workload-map/pull/511) by @yuval-gr)
- Check DCO sign-off on pull request commits, and reject AI co-author trailers. ([#455](https://github.com/dsx-ai-factory/workload-map/pull/455) by @yuval-gr, [#377](https://github.com/dsx-ai-factory/workload-map/pull/377) by @AviadHayumi)
- Run CI and dependency workflows on the release branches. ([#385](https://github.com/dsx-ai-factory/workload-map/pull/385) by @yuval-gr, [#178](https://github.com/dsx-ai-factory/workload-map/pull/178) by @shaked-bouktus)
- Track the cli and `hack/imagelock` Go modules in dependency updates. ([#232](https://github.com/dsx-ai-factory/workload-map/pull/232) by @Isan-Rivkin, [#238](https://github.com/dsx-ai-factory/workload-map/pull/238) by @AviadHayumi)
- Drop the unneeded workload columns from the Headlamp table. ([#451](https://github.com/dsx-ai-factory/workload-map/pull/451) by @moshe-runai)
- Harden the e2e manifests. ([#376](https://github.com/dsx-ai-factory/workload-map/pull/376) by @AviadHayumi)

---

### Documentation

- CHANGELOG.md for releases tagged from main, linked from the README together with the roadmap. ([#243](https://github.com/dsx-ai-factory/workload-map/pull/243), [#244](https://github.com/dsx-ai-factory/workload-map/pull/244) by @lavianalon)
- Repository move and rename notices, and the stale catalog links that followed. ([#365](https://github.com/dsx-ai-factory/workload-map/pull/365), [#375](https://github.com/dsx-ai-factory/workload-map/pull/375), [#373](https://github.com/dsx-ai-factory/workload-map/pull/373))
- Karta enhancement proposal structure, and the first KEP: the Karta metrics exporter. ([#459](https://github.com/dsx-ai-factory/workload-map/pull/459), [#462](https://github.com/dsx-ai-factory/workload-map/pull/462) by @AviadHayumi)
- Detailed design for the built-in catalog, and the resource references high-level design. ([#143](https://github.com/dsx-ai-factory/workload-map/pull/143) by @rogirun, [#157](https://github.com/dsx-ai-factory/workload-map/pull/157) by @ronlv10)
- Headlamp plugin design doc. ([#248](https://github.com/dsx-ai-factory/workload-map/pull/248) by @moshe-runai)
- Catalog definitions ship with a recording, and the authoring skill is rebuilt around it. ([#450](https://github.com/dsx-ai-factory/workload-map/pull/450) by @AviadHayumi)
- Document the `describe` command in the CLI design. ([#329](https://github.com/dsx-ai-factory/workload-map/pull/329) by @rogirun)
- Security and secrets guidance in AGENTS.md. ([#242](https://github.com/dsx-ai-factory/workload-map/pull/242), [#332](https://github.com/dsx-ai-factory/workload-map/pull/332) by @lavianalon)
- ADOPTERS.md, with KAI Scheduler, Nova, NVCF and Run:ai. ([#160](https://github.com/dsx-ai-factory/workload-map/pull/160), [#168](https://github.com/dsx-ai-factory/workload-map/pull/168), [#249](https://github.com/dsx-ai-factory/workload-map/pull/249), [#465](https://github.com/dsx-ai-factory/workload-map/pull/465) by @lavianalon)
- README community guide, LWS workload tree example, and an aligned samples table. ([#167](https://github.com/dsx-ai-factory/workload-map/pull/167), [#162](https://github.com/dsx-ai-factory/workload-map/pull/162), [#182](https://github.com/dsx-ai-factory/workload-map/pull/182) by @lavianalon)
- Roadmap updates, including the controller and operator moving to done. ([#226](https://github.com/dsx-ai-factory/workload-map/pull/226), [#245](https://github.com/dsx-ai-factory/workload-map/pull/245) by @lavianalon)

---

### Dependencies

#### Changed

- Go `1.26.3` -> `1.27.3`
- `k8s.io/api` `v0.36.2` -> `v0.37.1`
- `golang.org/x/net` -> `v0.60.0` (security)
- Node `22` -> `24` and `webpack-dev-middleware` -> `v8` for the Headlamp plugin

---

### Contributors

thanks to everyone who shipped this release:

[@AviadHayumi](https://github.com/AviadHayumi), [@Isan-Rivkin](https://github.com/Isan-Rivkin), [@lavianalon](https://github.com/lavianalon), [@moshe-runai](https://github.com/moshe-runai), [@nshidlansik](https://github.com/nshidlansik), [@ntheanh201](https://github.com/ntheanh201), [@ozbarshalom](https://github.com/ozbarshalom), [@rogirun](https://github.com/rogirun), [@ronlv10](https://github.com/ronlv10), [@shaked-bouktus](https://github.com/shaked-bouktus), [@Snirx1](https://github.com/Snirx1), [@yuval-gr](https://github.com/yuval-gr)

plus automated dependency updates from [@dependabot](https://github.com/dependabot) and [@renovate](https://github.com/renovatebot).

---

Full changelog: https://github.com/dsx-ai-factory/workload-map/compare/v0.2.0...v0.3.0
Helm chart: `oci://ghcr.io/dsx-ai-factory/workload-map/karta:0.3.0`
Container images: see the [karta-operator package](https://github.com/dsx-ai-factory/workload-map/pkgs/container/workload-map%2Fkarta-operator)

## v0.2.0 - 2026-07-15

This release extends Karta from a CRD and Go library into an optional runnable system. It lands the controller / operator (reconcile core, workload-tree model, Helm chart, container images), and a new pod-grouping API for better schedulers integration, alongside a much larger catalog of built-in workload samples. There are no breaking API changes; the previous grouping format is deprecated but still works. See [Deprecations](#deprecations) for the recommended migration.

---

### Highlights

| | |
|---|---|
| Controller / operator | The reconcile core, an operator Helm chart, and container images all land, so Karta can now run as a controller rather than only being a CRD plus library. ([#77](https://github.com/dsx-ai-factory/workload-map/pull/77), [#97](https://github.com/dsx-ai-factory/workload-map/pull/97), [#91](https://github.com/dsx-ai-factory/workload-map/pull/91)) |
| WorkloadTree | A `WorkloadTree` is the raw component hierarchy of a workload (its desired structure, scale, specs, and status) produced by the Karta library, giving clients a single shared tree to traverse and render. ([#87](https://github.com/dsx-ai-factory/workload-map/pull/87))|
| New pod-grouping API | `gangScheduling.podGroup` adds subgroups and topology constraints for better schedulers integration; the old `gangScheduling.podGroups` is deprecated but still honored. ([#145](https://github.com/dsx-ai-factory/workload-map/pull/145)) |

---

### Install

Helm chart (from GHCR):

```bash
helm install karta oci://ghcr.io/run-ai/karta/karta --version 0.2.0
```

Go consumers:

```bash
go get github.com/run-ai/karta@v0.2.0
```

```go
import karta "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
```

---

### Compatibility

| | |
|---|---|
| Kubernetes | tested against v1.31 - v1.35 (`k8s.io/api v0.36.2`) |
| Go | 1.26.3 or later |
| Helm | 3.14 or later |

---

### Deprecations

#### `gangScheduling.podGroups` -> `gangScheduling.podGroup`

The alpha grouping format `podGroups` (a list) is deprecated in favor of the new `podGroup` mapping, which adds subgroups and topology constraints. Existing Kartas using `podGroups` continue to validate and run.

Correction added after release: this is not a drop-in migration at v0.2.0, and the original release notes did not say so.

- Karta's own instruction helpers read only `podGroups`. `pkg/instructions/summary.go` builds gang-scheduling candidates from `GangScheduling.PodGroups` and never reads `GangScheduling.PodGroup`. That is true at v0.2.0 and still true today. A consumer that relies on those helpers for grouping loses its grouping if it switches formats.
- `podGroup` is honored by the scheduler integration instead. The KAI Karta podgrouper plugin prefers `podGroup` and falls back to `podGroups`. It shipped in KAI v0.17.0 on 2026-08-03, after this release, so at v0.2.0 nothing consumed the new field yet.
- The two formats are not equivalent. A `podGroups` member carries `groupByKeyPaths` and `filters`; a `subGroups` entry carries only `componentName` and `topology`, so per-component grouping keys and filters have no direct counterpart.

Target shape of the new format:

```diff
  optimizationInstructions:
    gangScheduling:
-     podGroups:
-       - name: job
-         members:
-           - componentName: launcher
-             groupByKeyPaths:
-               - .metadata.labels["training.kubeflow.org/job-name"]
-           - componentName: worker
-             groupByKeyPaths:
-               - .metadata.labels["training.kubeflow.org/job-name"]
+     podGroup:
+       name: job
+       subGroups:
+         - componentName: launcher
+         - componentName: worker
```

---

### New features

- Controller reconcile core - the operator's central reconcile logic. ([#77](https://github.com/dsx-ai-factory/workload-map/pull/77) by @shaked-bouktus)
- `WorkloadTree` data model and builder - a typed tree that assembles a workload's root and child components. ([#87](https://github.com/dsx-ai-factory/workload-map/pull/87) by @rogirun)
- Operator Helm chart - deploy the karta operator via Helm. ([#97](https://github.com/dsx-ai-factory/workload-map/pull/97) by @shaked-bouktus)
- Operator container images - Dockerfiles for the karta operator. ([#91](https://github.com/dsx-ai-factory/workload-map/pull/91) by @shaked-bouktus)
- Operator Makefile - build / run targets for the operator. ([#96](https://github.com/dsx-ai-factory/workload-map/pull/96) by @shaked-bouktus)
- `karta` CLI - CLI entrypoint and Cobra root command. ([#127](https://github.com/dsx-ai-factory/workload-map/pull/127) by @rogirun)
- New pod-grouping API - `gangScheduling.podGroup` with subgroups and topology. ([#145](https://github.com/dsx-ai-factory/workload-map/pull/145) by @davidLif)
- Kind-based e2e provisioner - `hack/e2e` spins up a kind cluster for end-to-end tests. ([#144](https://github.com/dsx-ai-factory/workload-map/pull/144) by @AviadHayumi)
- Expanded workload sample catalog - many additional built-in Karta samples across core, batch, training, serving, and Ray workloads. ([#155](https://github.com/dsx-ai-factory/workload-map/pull/155) by @AviadHayumi)
- Grove `PodCliqueSet` sample - `grove.io/v1alpha1` Karta example. ([#66](https://github.com/dsx-ai-factory/workload-map/pull/66) by @shmuel-runai)
- Milvus sample - `milvus.io/v1beta1` Karta example. ([#103](https://github.com/dsx-ai-factory/workload-map/pull/103) by @ronlv10)
- Controller example with LWS - a worked controller example over LeaderWorkerSet. ([#92](https://github.com/dsx-ai-factory/workload-map/pull/92) by @yuval-gr)
- Quickstart controller example - minimal controller to get started. ([#84](https://github.com/dsx-ai-factory/workload-map/pull/84) by @yuval-gr)

### Bug fixes

- Fix native resource discovery. ([#132](https://github.com/dsx-ai-factory/workload-map/pull/132) by @shaked-bouktus)
- Avoid append aliasing of caller-owned component slices. ([#129](https://github.com/dsx-ai-factory/workload-map/pull/129) by @ronlv10)
- Fix rayjob worker `instanceIdPath` nesting in the sample. ([#133](https://github.com/dsx-ai-factory/workload-map/pull/133) by @lavianalon)
- Fix dependabot missing code generation failing CI. ([#150](https://github.com/dsx-ai-factory/workload-map/pull/150) by @Isan-Rivkin)

### Maintenance

- Bump Go to 1.26.3 and update dependencies. ([#75](https://github.com/dsx-ai-factory/workload-map/pull/75) by @yuval-gr)
- Update `golang.org/x` dependencies to latest. ([#93](https://github.com/dsx-ai-factory/workload-map/pull/93) by @yuval-gr)
- Bump the go-minor-patch dependency group. ([#136](https://github.com/dsx-ai-factory/workload-map/pull/136), [#147](https://github.com/dsx-ai-factory/workload-map/pull/147) by @dependabot)
- Add `dependabot.yml` for version updates. ([#120](https://github.com/dsx-ai-factory/workload-map/pull/120) by @yuval-gr)
- Create verified dependabot codegen commits via the GitHub API. ([#153](https://github.com/dsx-ai-factory/workload-map/pull/153) by @Isan-Rivkin)
- CI action bumps: checkout 4->7 ([#123](https://github.com/dsx-ai-factory/workload-map/pull/123)), cache 4->6 ([#122](https://github.com/dsx-ai-factory/workload-map/pull/122)), setup-go 5->6 ([#121](https://github.com/dsx-ai-factory/workload-map/pull/121)), setup-buildx 3->4 ([#146](https://github.com/dsx-ai-factory/workload-map/pull/146)). (by @dependabot)

### Documentation

- Add an authoring tutorial and troubleshooting guide. ([#130](https://github.com/dsx-ai-factory/workload-map/pull/130) by @lavianalon)
- Add ROADMAP and GOVERNANCE. ([#89](https://github.com/dsx-ai-factory/workload-map/pull/89) by @lavianalon)
- Add a maintainer ladder / emeritus policy and RELEASE.md. ([#128](https://github.com/dsx-ai-factory/workload-map/pull/128) by @lavianalon)
- Harden OSS health docs (security policy, code of conduct, contributing). ([#111](https://github.com/dsx-ai-factory/workload-map/pull/111) by @lavianalon)
- Add README badges, community section, and CoC link. ([#125](https://github.com/dsx-ai-factory/workload-map/pull/125) by @lavianalon)
- Add AGENTS.md guide for AI coding agents. ([#73](https://github.com/dsx-ai-factory/workload-map/pull/73) by @lavianalon)
- Add a CLI high-level design document. ([#31](https://github.com/dsx-ai-factory/workload-map/pull/31) by @ronlv10)
- Add KAI Scheduler to the "who uses Karta" section. ([#156](https://github.com/dsx-ai-factory/workload-map/pull/156) by @lavianalon)
- Drop the API group rename from the roadmap and governance. ([#119](https://github.com/dsx-ai-factory/workload-map/pull/119) by @lavianalon)

---

### Dependencies

#### Changed

- Go `1.25.9` -> `1.26.3`
- `k8s.io/api` `v0.35.1` -> `v0.36.2`

---

### Contributors

thanks to everyone who shipped this release:

[@AviadHayumi](https://github.com/AviadHayumi), [@Isan-Rivkin](https://github.com/Isan-Rivkin), [@davidLif](https://github.com/davidLif), [@lavianalon](https://github.com/lavianalon), [@rogirun](https://github.com/rogirun), [@ronlv10](https://github.com/ronlv10), [@shaked-bouktus](https://github.com/shaked-bouktus), [@shmuel-runai](https://github.com/shmuel-runai), [@yuval-gr](https://github.com/yuval-gr)

plus automated dependency updates from [@dependabot](https://github.com/dependabot).

---

Full changelog: https://github.com/dsx-ai-factory/workload-map/compare/v0.1.0...v0.2.0
Helm chart: `oci://ghcr.io/run-ai/karta:0.2.0`
Container images: see the [karta-operator package](https://github.com/orgs/run-ai/packages/container/package/karta%2Fkarta-operator). The v0.2.0 images were published before the organization move and stayed under `run-ai`; later releases publish to the `dsx-ai-factory` package.

## v0.1.0 - 2026-05-12

This release completes the project rename from `ri` / `krt` to karta, decouples the library from `sigs.k8s.io/controller-runtime`, and introduces native suspend / resume support on the CRD. See [Breaking changes](#breaking-changes) for migration steps.

---

### Highlights

| | |
|---|---|
| Project rename to `karta` | API group is now `run.ai/v1alpha1`, the CRD `Kind` is `Karta`, and the Helm chart is named `karta`. ([#42](https://github.com/dsx-ai-factory/workload-map/pull/42), [#50](https://github.com/dsx-ai-factory/workload-map/pull/50)) |
| Zero controller-runtime dependency | karta no longer imports `sigs.k8s.io/controller-runtime`. Consumers that only need the CRD types or the resource helpers no longer inherit it transitively. ([#62](https://github.com/dsx-ai-factory/workload-map/pull/62)) |
| Suspend / Resume support | New `SuspendDefinition` field on the CRD, and new resource statuses `Suspended`, `Suspending`, `Resuming`. ([#56](https://github.com/dsx-ai-factory/workload-map/pull/56)) |

---

### Install

Helm chart (from GHCR):

```bash
helm install karta oci://ghcr.io/run-ai/karta/karta --version 0.1.0
```

Go consumers:

```bash
go get github.com/run-ai/karta@v0.1.0
```

```go
import karta "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
```

---

### Compatibility

| | |
|---|---|
| Kubernetes | tested against v1.30 - v1.34 (`k8s.io/api v0.35.1`) |
| Go | 1.25.9 or later |
| Helm | 3.14 or later |

---

### Breaking changes

#### 1. Project rename: `ri` / `krt` -> `karta`

| was | now |
|---|---|
| CRD `Kind` `ResourceInterface` | `Karta` |
| API group `optimization.nvidia.com/v1alpha1` | `run.ai/v1alpha1` |
| Go package `github.com/dsx-ai-factory/workload-map/pkg/api/optimization/v1alpha1` | `github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1` |
| Field on `ComponentFactory`: `ri *ResourceInterface` | `karta *Karta` |
| Helm chart `krt` | `karta` |

Cluster migration - existing CRDs need to be re-applied under the new GVK:

```bash
kubectl get resourceinterfaces.optimization.nvidia.com -o yaml | \
    sed 's|optimization.nvidia.com/v1alpha1|run.ai/v1alpha1|; s|kind: ResourceInterface|kind: Karta|' | \
    kubectl apply -f -

kubectl delete crd resourceinterfaces.optimization.nvidia.com
```

Go consumer migration:

```diff
- import ri "github.com/dsx-ai-factory/workload-map/pkg/api/optimization/v1alpha1"
+ import karta "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"

- var obj ri.ResourceInterface
+ var obj karta.Karta

- factory := resource.NewComponentFactoryFromObject(myRi, kubeObj)
+ factory := resource.NewComponentFactoryFromObject(myKarta, kubeObj)
```

#### 2. `sigs.k8s.io/controller-runtime` no longer a karta dependency

karta now uses `k8s.io/apimachinery` directly for the two things it previously needed from controller-runtime:

| was | now |
|---|---|
| `&scheme.Builder{GroupVersion:...}` from `controller-runtime/pkg/scheme` | `runtime.NewSchemeBuilder(addKnownTypes)` from `k8s.io/apimachinery/pkg/runtime` |
| `client.Object` from `controller-runtime/pkg/client` (in `NewComponentFactoryFromObject` and `GetResource`) | local `KubernetesObject` interface (`metav1.Object` + `runtime.Object`) |

Consumer impact: no source code changes required. Go matches interfaces structurally (by method set), so:

```go
import "sigs.k8s.io/controller-runtime/pkg/client"

var pod client.Object = &corev1.Pod{...}
factory := resource.NewComponentFactoryFromObject(myKarta, pod)  // still compiles
obj, _ := factory.GetResource()
var asClientObject client.Object = obj                            // still compiles
```

Cleanup opportunity: consumers that only had `sigs.k8s.io/controller-runtime` in their `go.mod` because karta dragged it in can now drop it:

```bash
go get sigs.k8s.io/controller-runtime@none  # if you don't otherwise use it
go mod tidy
```

This is the dominant pattern across major K8s OSS - see the same idiom in [kubevirt/api](https://github.com/kubevirt/api/blob/0a124c6271bf8ad3b2263b3b16b4544bae794ecd/core/v1/register.go#L85), [prometheus-operator](https://github.com/prometheus-operator/prometheus-operator/blob/0bd09d4dae2c319b9b43eb1921afe869f123d3a8/pkg/apis/monitoring/v1/register.go#L35), [tektoncd/pipeline](https://github.com/tektoncd/pipeline/blob/a3305b1d9b31f743fc625485e5c88837a731bbd3/pkg/apis/pipeline/v1/register.go#L40), [cert-manager](https://github.com/cert-manager/cert-manager/blob/7c3e89bf397945fa0143e3836144fa150bcc55c7/pkg/apis/certmanager/v1/register.go#L36), [cluster-api](https://github.com/kubernetes-sigs/cluster-api/blob/93bb18264d28410997c248052da4d7c4dbad7b91/api/core/v1beta2/groupversion_info.go#L30), [knative/pkg `kmeta.Accessor`](https://github.com/knative/pkg/blob/df317a52d1121053934fddcfff0e1081cfae42c2/kmeta/accessor.go#L32), and [crossplane-runtime `resource.Object`](https://github.com/crossplane/crossplane-runtime/blob/a596e1f7563505485b8ab0a06c1c3dc2d21ecf90/pkg/resource/interfaces.go#L193).

---

### New features

- Suspend / Resume definitions and statuses - new `SuspendDefinition` on the CRD, plus new resource statuses `Suspended`, `Suspending`, `Resuming`. ([#56](https://github.com/dsx-ai-factory/workload-map/pull/56) by @TomB30)
- `ValidateParsedJQ` is now exported - external consumers that already hold a compiled `*gojq.Query` can run the read-only / safe-expression check without re-parsing. ([#53](https://github.com/dsx-ai-factory/workload-map/pull/53) by @AviadHayumi)
- Helm chart published to GHCR on every release - pull with `helm install karta oci://ghcr.io/run-ai/karta --version 0.1.0`. ([#27](https://github.com/dsx-ai-factory/workload-map/pull/27) by @AviadHayumi)
- Chart version bump enforcement in CI - `ct lint` blocks PRs that change the chart without bumping `Chart.yaml` version. Matches the prometheus-operator / argo enforcement model. ([#48](https://github.com/dsx-ai-factory/workload-map/pull/48) by @AviadHayumi)
- `docs/examples/*.yaml` validated in CI - example Kartas are exercised by CI so docs cannot silently drift from the schema. ([#61](https://github.com/dsx-ai-factory/workload-map/pull/61) by @Isan-Rivkin)
- Issue templates + code of conduct - bug / feature templates plus a contributor CoC. ([#38](https://github.com/dsx-ai-factory/workload-map/pull/38) by @yuval-gr)
- Helm chart annotated with OCI source - `helm.sh/chart-source` annotation links chart back to the GHCR package page. ([#49](https://github.com/dsx-ai-factory/workload-map/pull/49) by @AviadHayumi)

### Bug fixes

- Repaired `docs/examples/mpijob.yaml` and simplified the pytorch running mapping. ([#58](https://github.com/dsx-ai-factory/workload-map/pull/58) by @lavianalon)
- Removed accidentally pushed `docs/ri-studio/` directory. ([#40](https://github.com/dsx-ai-factory/workload-map/pull/40) by @ronlv10)
- Fixed formatting and validation in issue templates. ([#41](https://github.com/dsx-ai-factory/workload-map/pull/41) by @yuval-gr)

### Maintenance

- Renamed `ri` -> `krt` across the codebase (first half of the rename to karta). ([#42](https://github.com/dsx-ai-factory/workload-map/pull/42) by @AviadHayumi)
- Renamed Helm chart `krt` -> `karta` (finishing the rename). ([#50](https://github.com/dsx-ai-factory/workload-map/pull/50) by @AviadHayumi)
- Bumped `golang.org/x/net` to v0.51.0 (security). ([#45](https://github.com/dsx-ai-factory/workload-map/pull/45) by @yuval-gr)
- Updated Go toolchain to 1.25.9. ([#30](https://github.com/dsx-ai-factory/workload-map/pull/30) by @yuval-gr)

---

### Dependencies

#### Added

_nothing_

#### Changed

- `golang.org/x/net` v0.39.0 -> v0.51.0
- Go toolchain 1.25.7 -> 1.25.9
- `k8s.io/api` v0.34.x -> v0.35.1
- `k8s.io/apimachinery` v0.34.x -> v0.35.1

#### Removed

- `sigs.k8s.io/controller-runtime` (previously v0.23.1) - dropped entirely
- `sigs.k8s.io/controller-runtime/pkg/client` (transitive) - no longer pulled in
- `sigs.k8s.io/controller-runtime/pkg/scheme` (transitive) - no longer pulled in

---

### Contributors

thanks to everyone who shipped this release:

[@AviadHayumi](https://github.com/AviadHayumi), [@Isan-Rivkin](https://github.com/Isan-Rivkin), [@TomB30](https://github.com/TomB30), [@lavianalon](https://github.com/lavianalon), [@ronlv10](https://github.com/ronlv10), [@yuval-gr](https://github.com/yuval-gr)

---

Full changelog: https://github.com/dsx-ai-factory/workload-map/compare/v0.0.12...v0.1.0
Helm chart: `oci://ghcr.io/run-ai/karta:0.1.0`

## v0.0.1 through v0.0.12 - 2025-09-03 to 2026-04-14

Pre-rename development releases under the earlier project name. See the [release list](https://github.com/dsx-ai-factory/workload-map/releases) for individual notes.
