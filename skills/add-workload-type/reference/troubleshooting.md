<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Troubleshooting catalog

Match the error text to a row and apply the fix. Messages come from the
validator (`karta/pkg/api/runai/v1alpha1/validation.go`), the jq validator
(`karta/pkg/jq/validation.go`), or the Go accessor API at runtime. The prose version
is `docs/Troubleshooting.md`.

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
- A non-assignable jq path in a `fragmentedPodSpecDefinition`. These paths are
  used to mutate the pod spec, not only to read it, so each must be a path jq can
  assign through. A `//` fallback such as
  `.spec.templates[].affinity // .spec.affinity` reads correctly and passes every
  validator, then fails when a consumer writes the field. Use an assignable path
  (navigation, iteration, or `select(...)`); for override semantics, model the
  varying items as a multi-instance component with `instanceIdPath` and target
  one layer.
