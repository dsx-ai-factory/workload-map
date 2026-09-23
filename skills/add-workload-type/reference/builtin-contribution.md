<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Contributing a workload type as a built-in

Read this only when the definition is meant to ship with the repository, so that
every consumer of the Karta library resolves the type with no CRD applied to the
cluster. For a definition the user applies to their own cluster, stay on the
standalone path in `SKILL.md` and ignore this file.

This whole branch needs a checkout of `dsx-ai-factory/workload-map` and a working
Go toolchain. It edits Go source, regenerates committed files, and runs the
repository's own suites, so there is no partial version of it that works from
fetched files. Confirm the clone before starting:

```bash
git clone https://github.com/dsx-ai-factory/workload-map
cd workload-map && ls pkg/catalog/kartas
```

Work on a branch, sign off commits with `-s` for the DCO, use Conventional
Commits, and link an open issue in the PR. `AGENTS.md` and `CONTRIBUTING.md` in
the repository root are authoritative on all of that; this file covers only what
is specific to adding a workload type.

## The invariant that shapes everything

`docs/catalog/*.yaml` is generated. The typed Go definitions in
`pkg/catalog/kartas/` are the source of truth, `hack/gen-samples` renders them,
and `make validate` fails CI on drift. Hand-editing a catalog YAML produces a
change that is silently reverted by the next generation run and fails the build
in the meantime.

`validate` tests `git status --porcelain`, not just a diff, so it fails on
untracked files as readily as on stale generated ones. A scratch predictions file
or a copied CR left in the tree fails `make check` with "generated files are
stale or untracked", which reads like a codegen problem and is not one. Keep
scratch files outside the repository, and check `git status` before blaming the
generators.

So on this branch the YAML is an output, not the deliverable. Author in Go.

## 1. Write the definition in Go

One file per workload type, `pkg/catalog/kartas/<name>.go`, exporting one
function that returns `*v1alpha1.Karta`. Copy the nearest existing file as the
skeleton, the same way `reference/sample-index.md` directs on the standalone
path; the struct fields map one-to-one onto the YAML fields the technical guide
documents.

Two conventions the surrounding files hold to:

- Optional string fields are `*string`, built with `ptr.To("...")`. A field left
  nil is a field the definition does not extract, which has consequences for
  status matching - see the `reasonFieldName` trap in
  `reference/troubleshooting.md`.
- A comment above a status matcher explains why that matcher exists, in terms of
  what the controller actually does. The matchers that needed fixing were the
  ones nobody could justify later. `deployment.go`, `lws.go`, and `jobset.go`
  carry good examples.

## 2. Register it

Add the function to the `definitions` slice in `pkg/catalog/catalog.go`. That
slice is the single wiring point; nothing else needs touching. The catalog keys
by the root component's GVK and panics at init on a duplicate or an incomplete
GVK, so a registration mistake fails loudly on the first test.

## 3. Generate the YAML

```bash
make generate-samples
```

The filename is derived, not chosen: `catalog.Slug` renders
`{group-with-dots-as-dashes}-{kind-lowercased}-{version}.yaml`. The core (empty)
group becomes `core`, so `v1 Pod` lands at `docs/catalog/core-pod-v1.yaml` and
`ray.io/v1 RayCluster` at `docs/catalog/ray-io-raycluster-v1.yaml`. Commit the
generated file alongside the Go source; CI compares them.

## 4. Prove it offline before involving a cluster

The generated YAML is a normal definition, so steps 6 and 7 of `SKILL.md` apply
unchanged and are worth running first - they are seconds, and a cluster recording
is minutes:

```bash
go run ./hack/karta-verify --karta docs/catalog/<slug>.yaml \
  --workload <real-cr.yaml> --predict <predictions.yaml> --strict
```

If a related type is already recorded, `test/e2e/recorded_data/` holds real CRs
to run against. See the fixtures section in `SKILL.md` step 1 for how to extract
one.

## 5. Declare the journey as an e2e flow

`test/e2e/flows/<name>_test.go`, one Ginkgo file per workload type. Each flow
declares the states the workload passes through, drives it on a live cluster, and
saves every CR it observes. `statefulset_test.go` is a compact model:

- `BeforeAll` installs the generated catalog YAML with `installKarta`, builds a
  `recorder.Fixture` naming the operator, its version, and the Karta file, and
  constructs a `recorder.New(cfg)` with one `AddState` per status the definition
  claims to report.
- `AddState` pairs a Karta status with an independent check on the CR's own
  fields, drawn from `predicates.go` (`FullyAvailable`, `ReplicasDegraded`,
  `CondTrue`, `PhaseEq`, and so on). This is the part that makes the suite mean
  something: the predicate must read the raw CR, never the Karta's own answer,
  or the test proves only that the definition agrees with itself.
- Each `It` runs a `recorder.NewFlow(...).Through(...)` sequence over a manifest
  in `testdata/<name>/<flow>.yaml`, with `recorder.Reaches(<status>)` gates and
  `.Do(...)` actions from `actions.go` (`ScaleReplicas`, `Suspend`, `Resume`).
  Mark genuinely transient dips `.Optional()` so the order check tolerates them.

Add the input manifests under `test/e2e/flows/testdata/<name>/`. Use
`example.com`-style placeholder images and registries, never an internal one.

## 6. Record, then replay

Recording needs a cluster with the operator installed:

```bash
make e2e-up                                     # provision kind plus operators
make record-e2e WORKLOADS="<name>"              # record just this type
make record-e2e WORKLOADS="<name>" FLOW="running"
```

Recordings land in `test/e2e/recorded_data/<operator>/<version>/<kartaName>/<flow>.yaml`
and are committed. Replay is offline and is the real regression gate:

```bash
make test-replay        # every recorded CR must resolve to its recorded state
make verify-recordings  # fail if any recording ended with succeeded: false
```

`test-replay` walks each recording step by step and asserts the Karta's matched
statuses contain the state the recorder observed. A definition that maps only the
happy path fails here on the first scale or suspend step, which is exactly the
point.

## 7. Finish the presubmit

```bash
make check
```

`check-lib` runs `validate` (generated-file drift), `verify-recordings`,
`test-lib`, and `test-replay`. CI additionally runs `helm-lint`, `helm-validate`,
`image-lock-verify`, `image-lock-test`, and `lint-shell`, so a green `check` is
necessary rather than sufficient.

Commit the Go source, the generated YAML, the flow test, the testdata manifests,
and the recordings together: they are one change, and a recording without its
definition cannot replay.

## When the cluster is not available

Step 5 and the recording half of step 6 need a live operator, which the user may
not have. `make test-replay` and `make verify-recordings` are offline and still
run, so once any recording exists the regression gate applies with no cluster.
Do not fake a
recording by hand - the replay suite treats recordings as ground truth, and an
invented one turns the regression gate into a rubber stamp.

Deliver the Go definition, the registration, and the generated YAML, proven with
`hack/karta-verify` against whatever real CR is available, and say plainly that
the flow test and recording remain to be done on a cluster with the operator.
That is an honest partial contribution. A hand-written recording is not.
