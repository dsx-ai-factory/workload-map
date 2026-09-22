<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Troubleshooting catalog

Match the error text to a row and apply the fix. Messages come from the
validator (`pkg/api/runai/v1alpha1/validation.go`), the jq validator
(`pkg/jq/validation.go`), or the Go accessor API at runtime. The same validator
backs `karta validate` and `hack/karta-verify`, so a message reads identically
whichever you ran. The prose version is `docs/Troubleshooting.md` in the Karta
repository.

`karta validate` exits 1 for an invalid definition and 2 when the file will not
parse as YAML at all. An exit 2 is a syntax problem in the file, so none of the
rows below apply; fix the YAML first.

## Structure validation errors

The validator joins several errors at once, so fix every named component.

| Message | Cause | Fix |
|---|---|---|
| `root component must have full kind (group, version, kind)` | Root is missing group, version, or kind. | Provide all three under `kind`. Only the core `Pod` kind may omit the group. |
| `root component must have status definition` | Root has no `statusDefinition`. | Add a `statusDefinition` to the root. It is required. |
| `root component cannot have owner ref` | An `ownerRef` is set on the root. | Remove it. Only child components have owners. |
| `child component '<name>' has no owner ref` | A child is missing `ownerRef`, or it is empty. | Set `ownerRef` to the parent component `name`. |
| `child component '<name>' has owner ref to non-existing component '<owner>'` | `ownerRef` names a component that does not exist. | Correct it to an existing `name`. Watch for typos and renames. |
| `component name <name> is not unique` | Two components share a `name`. | Make every `name` unique. |
| `component name is empty` | A component has no `name`. | Add a non-empty `name`. |
| `component '<name>' has multiple pod spec definitions` | More than one of `podTemplateSpecPath`, `podSpecPath`, `fragmentedPodSpecDefinition` is set. | Keep exactly one. They are mutually exclusive. |
| `component '<name>' has instance id path but no pod component instance selector` | `instanceIdPath` is set without a `componentInstanceSelector`. | Add a `componentInstanceSelector`, or remove `instanceIdPath`. |
| `component '<name>' has pod component instance selector but no instance id path` | A `componentInstanceSelector` is set without `instanceIdPath`. | Add `instanceIdPath`, or remove the instance selector. |
| `ownership cycle detected involving component <name>` | Owner refs form a loop instead of reaching the root. | Break the cycle. Every owner chain must terminate at the root. |
| `pod-group member component '<name>' is not defined (should be a root or child component)` | A gang-scheduling member names a missing component. | Make each `componentName` match a defined component. |
| `karta is nil` | The validator got no definition. | Ensure the file parsed and loaded before validation. |

## jq path errors

Each message names the exact expression, so search the definition for it.

| Message | Cause | Fix |
|---|---|---|
| `failed to parse JQ expression '<expr>' at '<path>': ...` | The expression is not valid jq. | Fix syntax: unbalanced brackets or quotes, a missing leading `.`, or wrong quote style in `["key"]`. |
| `failed to compile JQ expression '<expr>': ...` | Parses but will not compile, for example an unknown function. | Use only standard jq builtins; check names and arity. |
| `JQ execution error for expression '<expr>': ...` | Compiled but failed at runtime, often a null traversal. | Make it null-safe with `//`, for example `(.status.active // 0)`. |
| `JQ expression '<expr>' at '<path>' failed validation: modifying operator '<op>' is not allowed` | Uses an assignment or update operator. | Paths read state only. Rewrite to read, not write. |
| `... failed validation: del function is not allowed` | Uses `del`. | Read, do not modify. |
| `... failed validation: recursive descent operator '..' is not allowed` | Uses `..`. | Spell out the absolute path instead. |
| `... failed validation: function '<name>' may produce excessive output and is not allowed` | Uses `range`, `paths`, `recurse`, `walk`, or `repeat`. | Address the fields directly with a bounded expression. |

Tip: reproduce what Karta evaluates with
`kubectl get <resource> -o json | jq '<expr>'`, or use the jq playground at
play.jqlang.org.

## Accessor errors at runtime

Raised by the Go Component API when reading a definition.

| Error type | Example | Cause | Fix |
|---|---|---|---|
| `DefinitionNotFoundError` | `component <name> does not have suspendDefinition` | Code asked for a part the component does not define. | Add the missing definition, or guard the call with `errors.As` against `DefinitionNotFoundError`. |
| `InstanceNotFoundError` | `could not match instance id "<id>". existing instance ids [...]` | A pod's extracted instance id matches no instance from `instanceIdPath`. | Confirm the `componentInstanceSelector` reads the same id the `instanceIdPath` produces. |

## Silent mistakes (valid but wrong)

These pass validation but behave incorrectly. Check them first when a definition
"works" but reports the wrong thing.

- Using `ownerName` instead of `ownerRef`. The field is `ownerRef`. A child with
  `ownerName` decodes with no owner and is only caught where the validator runs.
- Expecting a `referencedComponents` field. It does not exist. Model owned
  resources as child components; list other managed kinds under
  `additionalChildKinds`.
- Status conditions that do not match the workload's real API. The definition
  validates but status never resolves because the controller never sets those
  types. Verify against the CRD source or docs.
- A path evaluated against the wrong resource. Spec, scale, and status paths run
  against the workload object; selector and optimization paths run against pod
  manifests. A selector pointing at a workload field matches nothing.
- Listing a defined component's kind under `additionalChildKinds`. The list is
  for managed kinds, and duplicating a kind already modeled as a component is
  usually redundant. The validator does not reject it, though, and it is
  legitimate when a kind must also be declared for RBAC or owner traversal (for
  example a scaling-group kind that is both a component and an ancestor to walk).
  Duplicate only with that intent, not by accident.
- A role selector key copied from the nearest sample without checking the target
  controller's real pod labels. Role-label keys are operator-specific (PyTorchJob
  `training.kubeflow.org/replica-type` vs MPIJob `training.kubeflow.org/job-role`),
  so a copied key silently matches nothing. Read the controller's actual pod
  labels.
- Two role components matching the same pod label. A plain value match on a
  shared label is not mutually exclusive. Disambiguate by matching a key only one
  role carries, using key existence (omit `value`), as LeaderWorkerSet does for
  leader versus worker.
- Inventing a phase or condition for a controller that reports neither. Some
  controllers expose only status fields such as replica counts. A `byPhase` or
  `byConditions` rule then never matches. Map those states with `byExpression`
  over the real fields instead.
- Mapping to `Undefined`. It is the implicit no-match result, not a target to
  map. Map only the statuses the workload reports.
- A scale or spec path that can yield zero results. Every path must produce
  exactly one value per instance. A `// empty` fallback produces none when the
  field is absent, which fails the whole extraction with `instance ids count (1)
  does not match results count (0)` rather than reporting a missing number. Emit
  null instead, for example
  `(.metadata.annotations["x"]) | if . == null then null else tonumber end`.
- Trusting `--strict` to have checked a root component. `hack/karta-verify` walks
  `WorkloadTree.Children`, which by documented design excludes the root, so every
  component-level check it runs skips the root silently. A root
  `podTemplateSpecPath` aimed at a nonexistent field passes `--strict` with exit 0
  and no warning, and a predictions row for the root reports `extracted keys are
  <none>` instead of failing. Single-component definitions get no pod-spec
  verification at all from the harness. Check the root's paths with jq against the
  CR directly.
- A matcher constraining `reason` when `conditionsDefinition` declares no
  `reasonFieldName`. The accessor only populates a condition's reason when that
  field name is set, so the comparison is against nil and the matcher can never
  fire. The status silently resolves to `Undefined` instead. The same holds for
  `message`. Declare `reasonFieldName` alongside `typeFieldName` and
  `statusFieldName`, or drop the reason constraint. This shipped in the
  LeaderWorkerSet definition and survived until the recordings were replayed.
- A status mapping that only describes the workload at rest. Validation and a
  single `running` CR both pass, and the definition falls to `Undefined` the
  first time the workload moves. The states that have actually broken shipped
  definitions: first reconcile before any condition exists (Deployment needed a
  `NewReplicaSetCreated` matcher), scale-down while extra pods drain
  (StatefulSet needed `.status.replicas > .spec.replicas`), suspend while the
  phase still reads `Running`, and `Unknown` on a tri-state condition (Knative's
  `Ready=Unknown` is Initializing, `Ready=False` is Failed). Walk the workload's
  life, and exercise each state separately in step 7.
- A matcher that breaks on the controller's own churn. Controllers briefly zero a
  counter mid-run, so a conjunction over two counters drops out for an instant.
  JobSet's `any(.ready > 0 and .active > 0)` went `Undefined` whenever `ready`
  dipped; `any(.active > 0 or .ready > 0)` holds across it. Prefer the matcher
  that stays true through a transient, not the one that is most precise at a
  single instant.
- Condition types that exist only on some cluster versions. Newer API versions
  add types beside the old ones rather than replacing them: a `batch/v1` Job
  reports `SuccessCriteriaMet` and `FailureTarget` alongside `Complete` and
  `Failed`. Map both as separate OR'd matchers so the definition works across the
  versions it will meet.
- A non-assignable jq path in a `fragmentedPodSpecDefinition`. These paths are
  used to mutate the pod spec, not only to read it, so each must be a path jq can
  assign through. A `//` fallback such as
  `.spec.templates[].affinity // .spec.affinity` reads correctly and passes every
  validator, then fails when a consumer writes the field. Use an assignable path
  (navigation, iteration, or `select(...)`); for override semantics, model the
  varying items as a multi-instance component with `instanceIdPath` and target
  one layer.

## Replay failures (built-in contributions, clone required)

`make test-replay` walks every recording under `test/e2e/recorded_data/` and
asserts the Karta's matched statuses contain the state the recorder read from the
CR's own fields.

| Symptom | Cause | Fix |
|---|---|---|
| `Karta read [] , recorded state was "<state>"` | No matcher fired for that state. Almost always a state-coverage gap, not a broken path. | Find the state in the silent-mistakes list above. Extract that CR from the recording and iterate with `hack/karta-verify` until it resolves. |
| `Karta read [<other>], recorded state was "<state>"` | Two statuses overlap and the wrong one matched first, or a rule is under-constrained. | Narrow the rule that should not have fired, usually by adding the field that distinguishes the two states, rather than by reordering. |
| `Karta could not parse the "<state>" CR` | The definition does not load against a real object, for example an `instanceIdPath` producing a different count from its selector. | Run that CR through `hack/karta-verify --workload` for the fuller message. |
| `no recordings under test/e2e/recorded_data` | The type was never recorded. | `make record-e2e WORKLOADS="<name>"` against a cluster with the operator installed. |
| A recording exists but references a missing catalog file | The recording's `kartaFile` names a `docs/catalog/` file that was renamed or never generated. | Run `make generate-samples` and confirm the slug matches; see `builtin-contribution.md`. |
