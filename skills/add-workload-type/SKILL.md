---
name: add-workload-type
description: >-
  Author, validate, and ship a Karta definition for a new Kubernetes workload
  type. Use when a user wants to add or support a workload framework or CRD in
  Karta (an Argo Workflow, a Volcano Job, a SparkApplication, any custom
  operator), or to write, fix, or review a Karta definition. In the karta
  repository, step 8 ships it as a catalog entry with a recorded e2e flow;
  elsewhere, the definition is applied to a cluster. Not for consuming a
  definition from Go code.
license: Apache-2.0
---
<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Add a workload type to Karta

A Karta definition describes one Kubernetes workload type as a tree of
components.

Every path is a jq expression. `specDefinition`, `scaleDefinition`, and
`statusDefinition` paths run against the workload object. `podSelector` and
`optimizationInstructions` paths run against pod manifests.

Every rule in the steps is a requirement. The reference files hold the why,
the examples, and the operator notes.

## Bundled references

Load these as needed; confirm field names and rules here, never guess.

- `reference/technical-guide.md` - schema, status patterns, karta-verify runs.
- `reference/sample-index.md` - closest sample and what not to copy (step 2).
- `reference/recorded-flow.md` - operator source and step 8 detail.
- `reference/troubleshooting.md` - every error and symptom with its fix.

## Two ways to use this skill

In the karta repository, every step applies: the definition becomes a catalog
entry, so step 8 ships it with an operator install and a recorded flow.

Anywhere else, steps 1 to 7 apply and step 8 is replaced by applying the Karta
to the cluster that runs Karta. Nothing needs a checkout:

- Run karta-verify from the public module instead of `./hack/karta-verify`:

  ```bash
  go run github.com/dsx-ai-factory/workload-map/hack/karta-verify@main \
    --karta <definition.yaml> --workload <real-cr.yaml>
  ```

  Pin `@main` to a commit or a release tag that ships karta-verify. Every
  `go run` below works in this form; for the binary,
  `GOBIN=<scratch> go install` the same argument.
- Fetch a sample from the catalog by URL instead of `docs/catalog/`:
  `https://raw.githubusercontent.com/dsx-ai-factory/workload-map/main/docs/catalog/<name>.yaml`.
- The operator source and controller code read in step 1 come from the
  operator's own repository either way.

## Workflow

### 1. Gather the target facts first

Do not write anything until these facts are known, from the target CRD source
or documentation.

Inputs. Ask the user for them when there is a user and a cluster; otherwise use
the offline sources.

- The CRD schema (`kubectl get crd <name> -o yaml`, or the operator's API
  types).
- At least one real CR (`kubectl get <kind> <name> -o yaml`), ideally one
  running and one finished. Optional, but only step 7 on a real CR proves a
  path resolves.

Sources:

- Offline: the release manifest or chart CRDs, the API types (`pkg/apis/`,
  `api/`), and the controller code that writes status. Grep it for the
  condition type constants and phase setters (`markRunning`, `markCompleted`,
  `SetPhase`). When the phase is computed from several inputs, grep for the
  function that returns it (`func Calculate.*Phase`) and copy its order of
  checks into the mapping comments.
- Confirm condition and label names in a shallow clone at the pinned tag
  (`git clone --depth 1 --branch <tag>`), not from documentation alone. The
  controller code that assigns the phase or conditions is the source of truth
  for step 5, not the CRD enum.
- Pin the newest non-prerelease tag
  (`git ls-remote --tags --refs <repo-url> | sort -V -k2`); it becomes
  `<NAME>_VERSION` in step 8. A Helm-installed operator pins the chart version:
  check that its `appVersion` is the tag the source was read at, and note any
  difference next to the pin.
- An operator already in `hack/e2e/global.env`: read the source at that tag and
  leave the pin alone. A bump re-records every flow of that operator.
- A Kubernetes builtin (apps, batch, core): do not clone kubernetes/kubernetes.
  Read the status type from the `k8s.io/api` module cache, and the controller
  with its `util/` counter helpers at the `KIND_NODE_IMAGE` version in
  `hack/e2e/global.env` (commands: Kubernetes builtins in
  `reference/recorded-flow.md`). Grep it for `Status.Conditions` before mapping
  any condition.

Proceed without a CR if there is none. For a catalog definition, take the CR
from the step 8 cluster: do the operator install, export its kubeconfig
(Record, step 8), run `make e2e-up`, `kubectl apply` a running manifest, and
save the CR with `-o yaml` and one pod with `-o json`. Delete the object before
`make record-e2e`.

Establish:

- The full GVK. All three parts are required; only core `Pod` may omit the
  group.
- The real statuses: exact condition types with their status and reason
  values, or the phase strings, as the controller sets them. Never invent one.
  Drop a condition named elsewhere that the controller never sets (a `Running`
  condition on a CRD that only writes `Complete` and `Failed`). Derive that
  state from fields the controller writes; say so in the builder comment and
  the summary.
- The spec values a rule branches on, from the validating webhook as well as
  the types. A value the webhook rejects (CloneSet `OnDelete`) or the API lacks
  gets no rule and no flow; name it in the summary.
- Where the pod template lives, and one role or several. Take the role list
  from the controller loop that walks the roles, not the documentation. It
  includes deprecated aliases, whose pods need a component too.
- How replicas are expressed, if at all.

Detail: Reading the operator source in `reference/recorded-flow.md`.

### 2. Start from the closest sample

Find the row for the workload shape in `reference/sample-index.md` and copy
that definition from `docs/catalog/` as the skeleton. Change the GVK, the
paths, and the status mapping. Skip what the Do not copy table lists for it.

### 3. Pick one specDefinition pattern per component

Set exactly one per component:

- `podTemplateSpecPath` for an embedded PodTemplateSpec (a Job's
  `.spec.template`).
- `podSpecPath`, with optional `metadataPath`, for a bare PodSpec.
- `fragmentedPodSpecDefinition` for scattered pod fields. List only paths that
  exist. Each must be assignable, since it is written too: a `//` fallback
  writes into whichever branch the read resolved. Model a default plus an
  override as a multi-instance component. With no assignable path, use a
  read-only projection and say in a comment that writes through it fail. When
  an optional full template sits next to the scattered fields, use the
  scattered fields and say in the builder comment that the template is not
  read.

A component may have no spec definition when it only models ownership or scale.

Multi-instance:

- A component whose spec or scale paths can return more than one value, or
  zero, needs `instanceIdPath` plus `componentInstanceSelector`.
- Roles that are optional map keys (`.spec.tfReplicaSpecs`): one
  multi-instance child keyed by the map, never a fixed child per role.
- `keys_unsorted` passes the validator but fails at runtime. Check an
  `ascii_downcase` id against the real pod label. Karta walks map keys sorted:
  test with `jq -S . cr.json | jq '<expr>'` or karta-verify.

Detail: Spec definitions, Read-only projections, Multi-instance components in
`reference/technical-guide.md`.

### 4. Write null-safe jq paths against the correct resource

- Absolute paths, starting with `.`.
- In status expressions, default every field that can be absent to what its
  absence means to the controller: an `omitempty` counter is 0
  (`(.status.active // 0)`), never the desired count.
- Spec and scale paths stay plain (`.spec.parallelism`,
  `.spec.tasks[].replicas`), even for `omitempty` fields, so they can be
  written. Never put a default after an iterator. An absent field reads null
  (`replicas=<none>`, accepted by `--strict`): leave `replicas` out of that
  prediction and name the controller default in the builder comment. A count
  derived from several fields stays a read-only formula.
- No assignment or update operators, `del`, `..`, `range`, `paths`, `recurse`,
  `walk`, or `repeat`. Read-only navigation and standard builtins only.
- Test each path with jq against a real manifest before committing it:
  `kubectl get <resource> -o json | jq '<expr>'`.

Detail: Scale definition and jq safety rules in `reference/technical-guide.md`.

### 5. Map real conditions or phases to Karta statuses

`statusDefinition` is required on the root. It maps the workload's conditions
or phases to `Initializing`, `Running`, `Completed`, `Failed`, `Degraded`,
`Suspended`, `Suspending`, or `Resuming`. A frame no rule matches reads
`Undefined`.

Matchers:

- `byConditions` with a `conditionsDefinition`: all conditions in one entry
  must hold, and each needs a `status` or a `reason`. It matches only a
  condition that exists; for "not yet written or not True", use `byExpression`
  over `.status.conditions // []`.
- `byPhase` with a `phaseDefinition`.
- `byExpression` (jq plus an expected result string) for state in status
  fields, such as a controller that reports only counters.
- Rules under one status are OR'd; one matcher setting several kinds ANDs
  them. Map only the statuses the workload reports.

One status per frame:

- Every settled rule requires a field only the controller writes, such as
  `(.status.observedGeneration // 0) > 0`.
- Check every pair that should be exclusive (Running and Suspended, Running and
  Degraded): guard on a field the controller changes between them. When
  suspending changes neither phase nor conditions, AND
  `(.spec.suspend // false) | not` into `Running` and `Initializing`.
- A phase derived from a spec field: let `Suspended` also match the field with
  an empty phase, and AND the field's negation into `Initializing`.
- A flag read before the phase switch, with the controller's own Suspending and
  Resuming phases: a frame with the flag set and a phase it will suspend from
  maps to `Suspending`, the paused phase with the flag cleared to `Resuming`.
  Mirror both in the flow with an `AnyOf` helper (add it when missing).
- A rule that negates a phase list (`IN(...) | not`) also matches the empty
  phase; say so in the builder comment.
- A condition set once and never cleared: AND the absence of every later
  condition into its rule, and keep the condition itself required. Do not add
  a no-status rule only to fill the no-status frame. Do not copy the PyTorchJob
  or MPIJob `Initializing` rule.
- Trace every condition one reconcile writes together. When two terminal
  conditions can be True at once, guard `Completed` with the absence of
  `Failed=True` (or the reverse, per what the controller treats as final) and
  mirror it in the flow.
- A polling loop next to the reconcile: when the reconcile writes its copy of
  the conditions after starting the loop, that late write can undo the first
  poll for one frame. Declare that dip `Optional()` in the flow between the
  advanced state and the next one, with a comment.
- A loop that writes a cached copy can write a stale pause condition back after
  a resume. For pause intent in a field the controller only reads, match
  `Suspended` on that field and the condition together, parsed as the
  controller does, and AND its negation into every other rule. Drop a flow
  only when no field tells the states apart; keep the action and name it
  unproven in the builder comment.

Failed and Degraded:

- Reserve `Failed` for a state the controller does not leave without a spec
  change. An error the next good poll clears is `Degraded`. When one condition
  carries both, split it by reason and mirror the split in the flow with a
  status-plus-reason predicate.
- Map `Degraded` only on a field that a fault sets, normal progress does not,
  and the controller clears on recovery (a failure condition, an error reason,
  a restart count). Leave a never-cleared condition unmapped and say in the
  builder comment that a consumer wanting the fault reads it directly. A
  `Degraded` flow fires the fix and ends `Running` when the kind cluster can
  reach it.
- A progress deadline (`ProgressDeadlineExceeded`) is `Degraded` only when the
  controller restarts its clock on progress and on each new rollout or scale
  (Deployment). Otherwise healthy progress trips it (CloneSet): leave it
  unmapped. Never map it to `Failed`.
- A counters-only controller maps some-but-not-all ready to `Initializing`.
  Do not copy the StatefulSet sample's `Degraded` rule or its `Optional()` dip.
- Every builder comment says whether `Degraded` is mapped. If not, name the
  fault signals the controller writes and the status each reads; if it writes
  none to status, say so and name any counter a reader could take for one.
  Read the controller branch before saying why it is not one.
- A condition written while pods are recreated needs no rule when a guarded
  rule already maps the frame. Say in the builder comment which rule carries it
  and that it is unrecorded.

Spec-driven branches:

- A settled state that owes no pods reads `Running`; do not add a desired > 0
  guard. Record it when the kind cluster can reach it, else name it unrecorded
  in the builder comment.
- Read the spec fields that choose how the controller progresses (update
  strategy, partition, pause, restart policy) and check every rule under each
  value, with the default for an absent field. Say where the default comes
  from: API defaulting, a webhook, or the controller. Under a StatefulSet's
  `OnDelete`, updated below desired is settled. Record one flow per value a
  rule branches on that the kind cluster can reach, and name unrecorded
  branches in the builder comment. Before calling a failure branch
  unreachable, try an admission rejection (Spec-driven branches in the
  technical guide). Check feature gates the same way and name any no rule
  covers.

In-flight phases:

- Only `Suspending` and `Resuming` are in-flight. A phase of a transition the
  controller always completes maps to the status it ends in; say so in a
  comment. Never leave it unmapped.
- An in-flight phase whose exit is picked from fields on the object: one
  matcher per exit, `byPhase` plus a `byExpression` on those fields (with
  defaults), each under its end status. Mirror the split in the flow predicates
  and record each exit the kind cluster can reach. Do not absorb the frame with
  an extra `Optional()` step.
- A pause through a separate object, not a spec field: map the resumable
  paused phase to `Suspended` and its draining phase to `Suspending` without a
  `suspendDefinition`, and say why in a comment. Leave `Resuming` unmapped when
  its phase is one another transition also writes. The recorder only patches
  the workload, so reach the paused state through an in-CR trigger.

Scale and suspend:

- No count in the spec: no `scaleDefinition`. Never write `replicasPath: 1`;
  `replicas=<none>` is not a warning. Leave out an implied count and a
  status-only count the same way.
- Bounds on how many child objects run go on that child with no
  `replicasPath`. Never put bounds in one unit next to a `replicasPath` in
  another.
- Search the CRD for min and max bounds (an autoscaling or elastic policy); a
  per-role gang minimum goes in `minReplicasPath`.
- A suspend action `value` is JSON-encoded (`'"on"'`, `"null"`). Say in the
  builder comment what the controller stops. A hold honored only before start
  is still a suspend, recorded with a manifest created held.

Detail: Status mapping patterns, and the Status, Scale, and Suspend
definitions in `reference/technical-guide.md`.

### 6. Validate the definition

Always run the validator. Do not hand back a definition that has not passed.

```bash
go run ./hack/karta-verify --karta <definition.yaml>
```

On a non-zero exit, look the message up in `reference/troubleshooting.md`, fix,
and rerun. Confirm by hand what it cannot check:

- Pod selectors reference pod fields. Selectors of the same kind are mutually
  exclusive across components; different kinds may coexist. Verify role-label
  keys against the controller's real pod labels. Two roles sharing a label are
  told apart by a key only one carries (key existence). Prefer a label set at
  pod creation over one written later by status reconciliation. Selectors stay
  unproven until the step 7 pod check.
- Conditions and phases match the real API.
- Every gang `componentName` names a defined component; the validator checks
  only the deprecated `podGroups`, so check `podGroup.subGroups` by hand. Never
  gang a role with the role whose running pod creates it. A creator with no
  `scaleDefinition` takes its children's count (`CalculateSubtreeScale`); name
  that in the builder comment.
- An `idPath` that reads the pod spec gets a comment saying when it returns
  null.
- Replica counts describe the right tree level, and siblings agree.

Detail: Pod selectors, Scale definition, Optimization instructions,
Validation checklist in `reference/technical-guide.md`.

### 7. Run the definition against a real CR

Required for a catalog definition. Skip it only when no CR exists; say so in
the summary and state what is unverified. A CR written from the controller
source (one per mapped phase plus one with no status) and a pod built from its
pod label code are stand-ins; every run on them counts as unverified.

- The CR with no status, and each step 8 testdata manifest as written, must
  read `Initializing`, `Undefined`, or, when created with the suspend flag set,
  the status step 5 gives that frame; never `Running`. Predict a no-match frame
  as `status: [Undefined]` and run it without `--strict`.
- Predict before running: the status, and per component instance the replicas
  and container names, from the CR's own numbers, never from an existing
  definition. Keys are `name`, `name[instanceId]`, `owner/child`. Never list
  the root; a root-only definition predicts `status:` alone, and root read
  paths are checked with jq. Predict the one status the frame should read; an
  extra status is an overlap to fix in step 5, never a prediction to widen.
- Run from the repository root with absolute paths:

  ```bash
  go run ./hack/karta-verify --karta <definition.yaml> \
    --workload <real-cr.yaml> --predict <predictions.yaml> --strict
  ```

- Reconcile every mismatch and warning. Decide whether the path or the
  understanding of the CRD is wrong first. Never edit the prediction just to
  pass, and never adjust the checklist; look the symptom up in
  `reference/troubleshooting.md`, fix the path, and rerun.
- Done means exit 0, with `--strict` unless a case in this step runs without
  it.
- Use a CR that defines its items inline for `--strict`. A CR that only
  references them reports zero instances: run it without `--strict` and state
  the expected zero in the summary.
- A `fragmentedPodSpecDefinition` with no container path goes on the
  component whose pods it describes, even when the root is not extracted. Check
  its paths with jq, run without `--strict`, and predict
  `podSpec: true` with no `containers`. Expect one `no containers` warning per
  such child and no other; never point `containerPath` at the role spec to
  silence it.
- Run against a CR in another state (completed or failed) when one exists.
- Multi-instance: run against a CR whose array has two entries (a hand-written
  scratch CR is fine).
- Run against a CR that omits each optional role, with `--strict` unless a
  no-container child exists.
- Pod selectors: evaluate every `podSelector` path and `groupByKeyPaths` entry
  with jq on a real pod
  (`kubectl get pod -l <owner label> -o json | jq '.items[] | <path>'`, or the
  pod saved in step 1). The `componentTypeSelector` must match, and the
  `idPath` must return an instance id karta-verify printed. With no selectors,
  check the owner chain; the component `ownerRef` names the `controller=true`
  owner.

TODO, the write round trip. karta-verify proves the read side only. Nothing
yet proves that a consumer can write through the definition without touching
anything else: writing a pod spec back unchanged, writing one probe field,
applying the suspend and resume actions, and checking that only the expected
leaves changed. Until that check exists, keep every spec, scale and suspend
path a plain assignable path (step 4), keep the CR from this step for the day
the check lands, and say in the summary that the writes are unproven. What the
check must do, where it belongs, and how to read it: The write round trip
(planned) in `reference/technical-guide.md`.

Output and scratch:

- Report the run output with the definition.
- Keep predictions and scratch copies in `mktemp -d`, else in a git-ignored
  directory that is not a checkout (`git check-ignore -v <dir>`): `<scratch>`.
- When the exit code matters (2 mismatch, 3 warnings), build
  `go build -o <scratch>/verify ./hack/karta-verify`, run `<scratch>/verify`
  unpiped, and read `$?`.

Detail: karta-verify runs in `reference/technical-guide.md`.

### 8. Ship the recorded flow

The definition is not done until a recorded flow proves it. Catalog
Definitions in `CONTRIBUTING.md` lists the deliverables. Why, commands, and
examples: `reference/recorded-flow.md`.

Catalog entry:

- The source is a Go builder, `pkg/catalog/kartas/<name>.go`, registered in
  `pkg/catalog/catalog.go`. `make generate-samples` writes `docs/catalog/`;
  never hand-edit it. Rerun steps 6 and 7 on the generated file.
- The builder comment holds what code cannot show: the controller's order of
  checks, why each guard exists, what is unproven, the unmapped fault signals.
  It names what an action or a write through an exposed path does not do (a
  feature gate, a spec field that overrides the path at pod creation). Do not
  restate paths. Stay within 3 to 26 `//` lines, summary included, by cutting
  what the fixtures and flow comments show, never those items.
- Add a Pre-built Karta Definitions row in `README.md` for an operator-backed
  kind; none for a builtin.

Operator install under `hack/e2e/`:

- A builtin needs no install scripts, pin, or `up.sh` entry: `make e2e-up`
  with `WORKLOADS=none`, `make record-e2e` with `WORKLOADS=<label>`.
  `Fixture.Operator` equals the first `Label`; the second label is `builtin`.
  Its fixtures land under the Kubernetes version, `v1.34.0`.
- A new operator follows Adding an operator in `hack/e2e/README.md`:
  `hack/e2e/operators/<name>/{install.sh,verify.sh,smoke.yaml}`, `<name>` the
  upstream project's short name (`kuberay`, `spark-operator`), a
  `<NAME>_VERSION` pin in `global.env`, a `version_of` case and an
  `ALL_WORKLOADS` entry (install order) in `up.sh`, and `deps_of` only when it
  needs another operator first. With no `deps_of` and no dependents, it goes
  last in `ALL_WORKLOADS`.
- A new kind for an installed operator reuses its directory and keeps the pin,
  `version_of`, and `ALL_WORKLOADS`. Extend any `install.sh` flag that gates
  which kinds the controller serves. Add `<kind>-smoke.yaml` and a second
  `run_smoke` line in `verify.sh`.
- `verify.sh` drives the smoke manifest to a terminal or stable state with
  `run_smoke` on the fully qualified resource, `<plural>.<group>/<name>-smoke`;
  a bare `job/` resolves to `batch/v1`. A workload that never settles and
  proves pods on an object it creates gets the hand-written sequence (verify.sh
  in `reference/recorded-flow.md`), each step captured in `rc` so the delete
  always runs, and a comment saying why `run_smoke` is not used.
- Check which namespaces the controller and its webhooks watch (a
  `--namespaces` flag, a `jobNamespaces` chart value, a webhook
  `namespaceSelector`) and widen each to cover the recorder's generated
  namespace (chart: a co-located `values.yaml`). When that value also renders
  per-namespace RBAC or a service account, keep `default` listed for the smoke
  test.
- Pods with no permissions upstream: a co-located RBAC manifest applied from
  `install.sh`, with a comment that it is scoped to the test cluster. Other
  cluster-scoped or shared objects the flows need: same way, with
  `apply_with_retry` and a comment naming their users.
- Namespaced objects the pods need: a ClusterRole in the RBAC manifest, the
  objects created in the flow's `BeforeAll` through a helper in
  `test/e2e/flows/setup_test.go`, and any new API group registered in
  `suite_test.go`. A CR with no pod template takes the Manifests pod
  conventions in that object.
- Pin the manifest by preference: release asset, raw manifest at the tag,
  kustomize base at the tag, Helm chart. Tags as upstream spells them; chart
  versions without `v`. An asset name without the `v` is derived with
  `${<NAME>_VERSION#v}`, noted next to the pin. List assets with
  `gh release view` before writing a URL, then `curl -fsSIL` the listed one.
  Apply manifests with `--server-side`.
- A remote kustomize resource must be a directory; copy a single file locally.
  An operator the suite installs separately is dropped from an upstream bundle
  (a co-located kustomization or the chart values) and added to `deps_of`.
- A one-shot webhook cert Job: `kubectl wait` for it to complete before
  `rollout_wait`. A pod webhook with `failurePolicy: Fail` admits every later
  pod: `rollout_wait` on a readiness check that covers it, with a comment.
- Grep the manifest (chart: `helm template`) for `kind: Namespace` and
  `namespace:`. With neither, create the namespace and apply with `-n <ns>`;
  after a failed install on a reused cluster, delete what landed in the wrong
  namespace first. A chart that renders its own Namespace owns it: install as
  upstream documents, usually without `-n` or `--create-namespace`, and do not
  turn that Namespace off.
- Preload a large image with the pinned upstream reference as both arguments,
  `preload_image "${img}" "${img}" || warn "..."`, and keep testdata on it.
- A temp dir in `install.sh` must not be `local`; assign it before
  `trap 'rm -rf "${tmp}"' EXIT`.
- While fixing, run `install.sh` and `verify.sh` directly with `KUBECONFIG`
  exported. Run `make e2e-up` once at the end; it writes
  `.installed-versions-<cluster>`.
- `make lint-shell` must pass on the new scripts.

Flow under `test/e2e/flows/` (read `test/e2e/recorder/README.md` first):

- One Ginkgo file whose `recorder.Fixture` `Operator` equals the directory name
  under `hack/e2e/operators/` (a builtin has none; see above); a mismatch
  silently files the recording under the Kubernetes version. Check the path the
  recorder prints on save: the operator's `version_of` string, so a second
  version directory beside `v1.34.0` is expected.
- The operator name is the first `Label`; a multi-kind operator adds the kind
  as the second. The testdata directory and object names use the lowercase kind
  (a single-kind operator may use its name). When that collides with a builtin
  or another catalog entry, use the upstream short name for the second label,
  testdata directory, object names, and root component name (else the
  lowercase kind) alike.
- Predicates read the CR's fields, never Karta. Each `AddState` predicate holds
  on exactly the frames its `statusMappings` rule matches, step 5 guards
  included.
- Reuse the helpers in `test/e2e/flows/predicates.go`; add a named predicate
  only when none fits. `CondReason` requires True; `CondNotTrue` also matches
  Unknown. A missing shape (status plus reason, condition absent, any-of,
  negation, at-most with absent as 0) is added as a generic helper next to the
  existing ones, not a workload-specific one. Never name a helper `Not`, `And`,
  `Or`, or `Equal`. To reuse a named predicate on another path, add a path
  parameter, keep existing callers on the old path, and compose extra guards
  with `AllOf`. A state judged by comparing several counters gets one named
  predicate per state.
- Prove the predicates offline first: a scratch `TestX` in `test/e2e/flows`
  decodes each step 7 CR through `yaml.YAMLToJSON` (never plain
  `yaml.Unmarshal`) and asserts exactly one predicate holds, naming the status
  karta-verify printed. Delete it afterwards (Proving predicates offline in
  `reference/recorded-flow.md`).
- A reason no recording can show stays mapped, stays out of the predicate, and
  is named unproven in the builder comment.
- `AddState` order decides only a frame two predicates match: least to most
  advanced, last match strongest. Declare `Suspended` first when the
  controller leaves the condition after a resume, last when it flips it to
  False.
- Mark a step `Optional()`, with a comment saying why, when the controller may
  skip it; a dip it always writes is required (State order in
  `reference/recorded-flow.md`). Check each fixture's first frame after the
  first run. If a no-status rule exists and the first frame already carries
  status, keep the rule and say in the builder comment no recording proves it;
  with no such rule, say an object with no status reads `Undefined`.
- Declare a revisit only when the controller source can produce it, described
  as allowed, not predicted. Do not copy a sibling flow's revisit.
- Raise the 3 minute deadline with `SetTimeout` only when a run hits it, with a
  comment saying why.
- Actions are generic merge-patch helpers in `test/e2e/flows/actions.go`, not
  named after the workload; suspend and resume share them, side by side. An
  action other than suspend, resume, or scale needs an `ActionType` constant in
  `test/e2e/recorder/flow.go`. A pod template rollout or rerun uses
  `Annotate(key, value, path...)`, a merge patch at any annotations map, with
  `ActionRollout` (`"Rollout"`); add them when missing.
- A `suspendDefinition` that can pause a running workload needs a flow
  `Reaches(Running).Do(<suspend action>)` then `Reaches(Suspended)`; the resume
  is optional.
- A write through an exposed path that makes the controller rerun the
  workload needs a flow that patches it from Running and walks back to Running.
- Gates. A `With()` or `Do()` step must be reached, in order: gate only the
  terminal step or a step whose frame always appears, and never pair `With()`
  with `Optional()`. A `Do()` whose predicate reads a spec field is gated on a
  field only the controller writes. A terminal status shared with an in-flight
  phase is gated on the CR field.
- A `Do()` state that is also terminal gets a terminal `With()` on a field the
  action changes and the controller echoes, or, when it echoes none, ends on a
  later state only the action leads to; confirm `STATE` frames follow the
  `ACTION`. With a string or absent `observedGeneration`, the `With()` gate is
  the only protection against a late write.

Manifests under `test/e2e/flows/testdata/<workload>/`:

- Name objects `karta-e2e-<workload>-<flow>`; set `namespace: default`.
- Pin image tags, declare requests and limits, add the SPDX header, and keep
  the pod alive past the Running check: `sleep infinity` when it never
  completes, `sleep 300` for a Job that must hold Running. A workload that
  cannot sleep holds Running by its work size: measure how long in the first
  recording and say so in the manifest comment.
- Do not tolerate the control-plane taint. To make one pod of a per-node
  workload differ, branch on `spec.nodeName` from the downward API
  (`*-worker2`); keep it unready by failing its readiness probe, not by exit.
  Say in the flow comment that the recording cannot tell that settled partial
  frame from a transient one.
- Set `automountServiceAccountToken: false` unless the pods call the API
  server.

Record on an isolated cluster:

```bash
export CLUSTER_NAME=<name> KUBECONFIG=~/.kube/kind-<name>.kubeconfig
make e2e-up CLUSTER_NAME=<name> WORKLOADS=<operator>
make record-e2e CLUSTER_NAME=<name> WORKLOADS=<operator>
```

- Export both once per session and use the same `CLUSTER_NAME` on every
  `make` call. The cluster stays up until the checks after recording are
  done; teardown is the last step below.
- Before recording, `kubectl apply --dry-run=server -f` every testdata
  manifest. Retry a connection refused.
- A new kind on a multi-kind operator passes the kind label to `record-e2e`
  (`WORKLOADS=tfjob`); `e2e-up` takes the operator name, or `none`.
  `FLOW="<a>|<b>"` re-records only those flows; after a change to a rule or a
  predicate, re-record every flow of the kind. `E2E_LABELS` takes a raw Ginkgo
  label expression.
- On `required state ... missing or out of order`, an action step that never
  advances, or `Ran N of M Specs` below the `It` count, apply its row in
  Recorder and e2e in `reference/troubleshooting.md`; never drop a flow before
  checking for a stale writer.

After recording, with the cluster still up (yq commands for each check:
Reading fixtures in `reference/recorded-flow.md`):

- Rerun step 7 on the last frame of the flow's terminal state.
- Every fixture ends with `succeeded: true`. `phases` lists the predicates that
  matched a frame; in a new fixture every `STATE` frame after the controller's
  first status write lists one, and the frames before it read Undefined and
  match none. Two mean the predicates overlap: run karta-verify on that frame,
  fix the overlap (step 5), and re-record. Check the `phase:` values; a flow
  that stops one frame early still succeeds.
- Check every builder comment claim against the frames; rewrite it as observed
  or mark it unproven. A reset or clear claim needs a frame where the field was
  set before the action. Trace pod claims to the code and check them on the
  hand-applied pod.
- `kubectl apply -f` one testdata manifest and wait until it settles. Check
  selectors and `groupByKeyPaths` (or the owner chain) with jq on its pod.
  Save its CR as a second step 7 input, then delete it.
- Last, tear down: `make e2e-down CLUSTER_NAME=<name>`. With `KUBECONFIG`
  exported it leaves the kubeconfig file and
  `hack/e2e/operators/.installed-versions-<cluster>`; remove both.

Before `make check`:

- `make lint-shell`, `make test-replay`, and `make verify-recordings` must be
  green, and in `test/e2e` `GOWORK=off go vet ./...` and `gofmt -l .` clean,
  which `make check` does not cover. Then run `make check`; do not skip it.
  It first downloads the pinned tools missing from `bin/` and prints nothing
  for minutes; run it in the background with its output in a log file.
- `make test-replay` prints only `ok`. To see the new fixtures replayed, run
  `GOWORK=off go test -count=1 -v ./replay_tests/... -args -ginkgo.v` in
  `test/e2e` and grep the output for the `kartaName`.
- Commit the new files, fixtures included, before `make check`; `validate`
  needs a clean tree.
- Fixtures carry no SPDX header; do not add one.

