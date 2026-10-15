<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Changelog

Notable changes per release. This file is the in-repo source of record: a release's entry is added here before the tag is pushed (see [RELEASE.md](RELEASE.md)), and the [GitHub Release](https://github.com/dsx-ai-factory/workload-map/releases) body carries the same content, normalized to the repository Markdown rules in [AGENTS.md](AGENTS.md).

Scope: this file covers releases tagged from `main`. Patch releases on a minor line are tagged from that line's release branch (`v0.1`, `v0.2`) and are documented in the changelog there. The v0.0.x releases predate that scheme and are summarized at the end.

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
Container images: see [packages page](https://github.com/dsx-ai-factory/workload-map/pkgs/container/karta)

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
