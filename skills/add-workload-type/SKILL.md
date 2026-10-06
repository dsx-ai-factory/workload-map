---
name: add-workload-type
description: >-
  Author and validate a Karta definition that teaches Karta a new Kubernetes
  workload type. Use when a user wants to add, register, onboard, or support a
  workload framework or CRD in Karta (for example an Argo Workflow, a Volcano
  Job, a SparkApplication, or any custom operator), or to write, fix, or review
  a Karta YAML that maps a workload's status, pod template, and scale. Covers
  choosing the closest sample, picking the correct specDefinition pattern,
  writing null-safe jq paths, mapping real conditions or phases to Karta
  statuses, and self-checking against the validator. Not for consuming an
  existing definition from Go code or operating a live cluster.
license: Apache-2.0
---
<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Add a workload type to Karta

A Karta definition describes one Kubernetes workload type as a tree of
components. Once written, any controller or platform built on the Karta library
reads status, scale, and pod specs for that workload through one uniform API,
with no per-type code. This skill walks through authoring a correct definition
and validating it before use.

Every path in a Karta is a jq expression. Paths in `specDefinition`,
`scaleDefinition`, and `statusDefinition` run against the workload object. Paths
in `podSelector` and `optimizationInstructions` run against pod manifests.
Mixing these up is the most common mistake, so keep it in mind throughout.

## Bundled references

Load these as needed. Do not guess field names or rules; confirm them here.

- `reference/technical-guide.md` - the full field and schema cheatsheet:
  component model, the three spec patterns, status mapping semantics, jq safety
  rules, scale, suspend, multi-instance, gang scheduling, and the checklist.
- `reference/sample-index.md` - a decision table that maps a workload shape to
  the closest existing definition under `docs/catalog/`. Start here in step 2.
- `reference/troubleshooting.md` - every validator, jq, and runtime error mapped
  to its cause and fix, plus the mistakes that pass validation but behave wrong.
- `karta/hack/karta-verify/` - the offline harness. Validates a definition
  (step 6) and, given a real CR, runs it and checks the extraction against
  predicted values (step 7). Run it from the `karta/` directory, where relative
  file paths resolve.

## Workflow

### 1. Gather the target facts first

Do not write anything until these facts are known. Read the target CRD source or
documentation to get them right.

Ask the user for two inputs up front:

- The CRD schema (`kubectl get crd <name> -o yaml`, or the operator's API types).
  This is what the definition is written from.
- At least one real example CR (`kubectl get <kind> <name> -o yaml`), ideally one
  that is running and one that has finished. This is optional but valuable: it
  unlocks step 7, which is the only way to prove the paths resolve. A jq path can
  be structurally valid and still point at a field no real object carries.

Proceed either way. Without a CR the definition can still be written and
validated; it just cannot be exercised, which step 7 covers.

From those inputs, establish:

- The full GVK: group, version, and kind. All three are required (the core
  `Pod` kind is the only one allowed to omit the group).
- The real statuses the controller reports: the exact condition types and their
  status and reason values, or the phase strings it writes to `.status`. Use the
  names the controller actually sets. Inventing condition types produces a
  definition that validates but never resolves a status.
- Where the pod template lives in the spec, and whether the workload has one
  role or several (for example master and worker, or head and worker groups).
- How replicas are expressed, if at all.

### 2. Start from the closest sample

Open `reference/sample-index.md`, find the row that matches the workload shape,
and copy that definition from `docs/catalog/` as the starting skeleton. Adapting a
working sample is faster and safer than starting from an empty file. Change the
GVK, the paths, and the status mapping to fit the target.

### 3. Pick one specDefinition pattern per component

The three patterns are mutually exclusive. Set exactly one per component:

- `podTemplateSpecPath` when the CRD embeds a full PodTemplateSpec (metadata and
  spec), for example a Job at `.spec.template`.
- `podSpecPath`, with optional `metadataPath`, when the CRD embeds a bare PodSpec
  and optionally a separate metadata object.
- `fragmentedPodSpecDefinition` when pod fields are scattered across the spec.
  List only the field paths that exist (labels, annotations, resources,
  containers, nodeAffinity, and so on). Each fragmented path is used to mutate
  the field, not only read it, so it must be a path jq can assign through: a `//`
  fallback reads fine but breaks on write. When a field has a default plus an
  override, model the varying items as a multi-instance component
  (`instanceIdPath`) rather than reaching for a fallback. See
  `reference/technical-guide.md`.

A component may also have no spec definition when it exists only to model
ownership or scale. See `reference/technical-guide.md` for the full field list.

### 4. Write null-safe jq paths against the correct resource

- Use absolute paths from the resource root, starting with `.`.
- Supply a default for any field that can be absent, so evaluation never fails on
  null. Examples: `(.status.active // 0)`, `.spec.parallelism // 1`.
- Confirm the resource: spec, scale, and status paths read the workload object;
  selector and optimization paths read a pod manifest.
- Karta rejects jq that can mutate or explode. Do not use assignment or update
  operators, `del`, the recursive descent `..`, or `range`, `paths`, `recurse`,
  `walk`, or `repeat`. Read-only navigation and standard builtins only.
- Test a path with the jq CLI against a real manifest before committing it:
  `kubectl get <resource> -o json | jq '<expr>'`.

### 5. Map real conditions or phases to Karta statuses

`statusDefinition` is required on the root component. It translates the
workload's own conditions or phases into Karta's normalized statuses:
`Initializing`, `Running`, `Completed`, `Failed`, `Degraded`, `Suspended`,
`Suspending`, `Resuming`. A workload that matches no rule resolves to
`Undefined`.

- To match conditions, add `conditionsDefinition` (its path plus field names),
  then use `byConditions`. All conditions in one `byConditions` entry must hold
  (AND). Each entry needs at least a `status` or a `reason`.
- To match a phase string, add `phaseDefinition` (its path), then use `byPhase`.
- When the state lives in status fields rather than conditions or a phase, use
  `byExpression` with a jq expression and an expected result string. Some
  controllers report only status fields (for example replica counts) and no
  aggregate phase; match those with `byExpression`. Do not invent a phase value
  or condition type the controller never sets.
- Rules listed under the same status are OR'd; any one matching resolves that
  status. A single matcher may also combine `byPhase`, `byConditions`, and
  `byExpression`, in which case all of them must hold (AND). Map only the
  statuses the workload actually reports.

### 6. Validate the definition

Always run the validator on the definition just written. Do not hand back a
definition that has not passed it. Run it from the `karta/` directory, where
file arguments resolve: a definition under the repository's `docs/catalog/` is
`../docs/catalog/<file>.yaml`, and any other file needs its path relative to
`karta/` or an absolute path.

```bash
go run ./hack/karta-verify --karta <definition.yaml>
```

It exits 0 when the definition is well-formed, and non-zero with the validator's
message otherwise. Look any failure up in `reference/troubleshooting.md` by the
message text, fix it, and run again.

The validator enforces these, so there is no need to check them by eye:

- All kinds use a full GVK (only `Pod` may omit the group).
- The root component has a `statusDefinition` and no `ownerRef`.
- Every child component has an `ownerRef` naming an existing component, with no
  ownership cycles.
- Component names are unique and non-empty.
- No component sets more than one of the three spec patterns.
- `instanceIdPath` and a `componentInstanceSelector` are either both present or
  both absent on a component.
- Every jq expression parses and uses no rejected construct.

The validator cannot check these. Confirm each one:

- Pod selectors reference pod fields, not workload fields. Selectors of the same
  kind must be mutually exclusive across components so a pod maps to one component
  of that kind; different selector kinds may coexist on a component. Verify
  role-label keys against the controller's real pod labels (they are
  operator-specific), and when two roles share a label, disambiguate by matching
  a key only one role carries (key existence).
- Status conditions and phases match the workload's real API.
- Every gang-scheduling `componentName` names a defined component. The validator
  checks this only for the deprecated `podGroups` format; references under
  `podGroup.subGroups` are not checked, so verify those by hand.
- Replica counts describe the right level of the tree, and siblings at the same
  level agree. See the scale section of `reference/technical-guide.md`.

A valid definition is still an unproven one: validation says nothing about
whether a path resolves against a real object. Step 7 is what proves that.

### 7. Run the definition against a real CR (optional)

Do this whenever the user supplied a real CR. It is the only step that proves a
path resolves: a definition can pass step 6 in full, resolve to null against the
real object, and report nothing.

When no CR is available, skip the step and say so in the final answer. The
definition is structurally valid and never exercised, and which parts are
unverified should be stated plainly rather than left for someone to discover.

The same command does it, with `--workload` added. It builds the workload tree
from the manifest and prints the extracted status, replica counts, and containers
per component instance, with no cluster involved. Its flags and the predictions
format are documented in `karta/hack/karta-verify/README.md`.

Predict before running. Writing down the expected values first is the point of
this step: reading the output afterwards invites accepting whatever appears,
while a prediction that disagrees with the extraction is a defect that cannot be
talked away.

1. From the CR, write the values the definition should produce into a predictions
   file: the status, and per component instance the replica count and container
   names. Derive them from the CR's own numbers, never by reading them back out
   of an existing definition.
2. Run it from the `karta/` directory. Give each of the three files its path
   relative to `karta/` (`../` for a file under the repository root) or an
   absolute path:

   ```bash
   go run ./hack/karta-verify --karta <definition.yaml> \
     --workload <real-cr.yaml> --predict <predictions.yaml> --strict
   ```

3. Reconcile every mismatch and warning. A mismatch means either the path is
   wrong or the understanding of the CRD is wrong. Decide which before changing
   anything, and never edit the prediction just to make the run pass.

The definition is done when the command exits 0 with `--strict`: the status
resolved, every component declaring a spec pattern extracted a pod spec with
containers, every `instanceIdPath` produced the instance keys the CR contains,
and every predicted number matched.

Show the user the run output alongside the definition. Keep the predictions file
and any scratch copies out of the repository. When something comes back empty or
wrong, do not adjust the checklist; look the symptom up in
`reference/troubleshooting.md`, fix the path, and run again. If a second example
CR in a different state is available (completed or failed), run against it too to
confirm the other status rules fire.
