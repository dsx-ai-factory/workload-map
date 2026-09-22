---
name: add-workload-type
description: >-
  Author and validate a Karta definition that teaches Karta a new Kubernetes
  workload type. Use when a user wants to add, register, onboard, or support a
  workload framework or CRD in Karta (for example an Argo Workflow, a Volcano
  Job, a SparkApplication, or any custom operator), to write, fix, or review a
  Karta YAML that maps a workload's status, pod template, and scale, or to
  contribute a new built-in type to this repository's catalog. Covers choosing
  the closest sample, picking the correct specDefinition pattern, writing
  null-safe jq paths, mapping real conditions or phases to Karta statuses across
  every state the workload reaches, and checking the result with the karta CLI or
  the repository harness. Works whether or not the Karta repository is checked
  out. Not for consuming an existing definition from Go code or operating a live
  cluster.
license: Apache-2.0
---
<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Add a workload type to Karta

A Karta definition describes one Kubernetes workload type as a tree of
components. Once written, any controller or platform built on the Karta library
reads status, scale, and pod specs for that workload through one uniform API,
with no per-type code. This skill walks through authoring a correct definition
and proving it before use.

(The project was renamed Karta to Workload-Map and the Go module path is now
`github.com/dsx-ai-factory/workload-map`. The CRD Kind, the Helm chart, and the
package paths are unchanged, so a definition is still a Karta and this skill is
unaffected beyond import paths.)

## This skill ships separately from Karta

Nothing here assumes the working directory is the Karta repository. What can be
done depends on what is to hand, so establish that first, in this order, and say
which tier is in play when reporting back.

```bash
command -v karta kli                     # tier 1: the CLI
ls hack/karta-verify pkg/catalog/kartas  # tier 2: a clone (run from its root)
```

Tier 1, the `karta` CLI on PATH. It embeds the whole built-in catalog, so it
covers sample selection (step 2) and validation (step 6) with no cluster and no
checkout. It cannot run a definition against a manifest, so step 7 is out.

Tier 2, a clone of `dsx-ai-factory/workload-map`. Everything works, including
`hack/karta-verify` for the predict-and-prove loop in step 7, the recorded
fixtures, and the built-in contribution branch. If the user wants step 7 and has
no clone, this is the thing to ask for:

```bash
git clone https://github.com/dsx-ai-factory/workload-map
make -C workload-map build-cli   # also yields the CLI at bin/karta
```

That `make build-cli` is the only way to get the CLI: `cli/go.mod` carries a
`replace` directive pointing at the parent module, which makes
`go install github.com/dsx-ai-factory/workload-map/cli@latest` fail outright. Do
not suggest it.

Tier 3, neither. Individual files can still be fetched, so the catalog and the
fixtures remain available for reading and copying:

```bash
KARTA_REF=889b50ed674e780d7a1654f2ec7d6bf0d18455dc
KARTA_RAW=https://raw.githubusercontent.com/dsx-ai-factory/workload-map/$KARTA_REF
curl -sL "$KARTA_RAW/docs/catalog/batch-job-v1.yaml"
```

The pin is a commit, not a release tag, and deliberately so: at every tag up to
and including v0.2.7 the directory is still `docs/samples/` with different
filenames, and `test/e2e/recorded_data/` and `hack/karta-verify/` do not exist.
Everything this skill draws on lives on `main`. Substituting a tag will 404.

A tier-3 definition cannot be validated at all. That is a real limitation, not a
formality: say so in the answer rather than implying the result was checked.

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
- `reference/builtin-contribution.md` - the repository-contributor path only:
  authoring in Go under `pkg/catalog/kartas/`, generating the catalog YAML, and
  the e2e flow and replay suite. Read it only if step 0 selects that branch.

In the Karta repository, so tier 2 only:

- `hack/karta-verify/` - the offline harness. Validates a definition (step 6)
  and, given a real CR, runs it and checks the extraction against predicted
  values (step 7). Nothing else does the latter.
- `test/e2e/recorded_data/` - real CRs recorded from live clusters for 17 of the
  20 catalog types, across the states each reaches. Readable at tier 3 too, by
  fetching the file. Step 1 explains how to use them.

## Workflow

### 0. Decide what the deliverable is

Two different jobs share this skill, and they end in different artifacts. Settle
which one before writing, asking the user if it is not obvious:

- A standalone definition. The user has their own CRD and wants a Karta YAML
  to apply to their cluster. This is the default, and the rest of this file
  covers it end to end.
- A new built-in in this repository. The type should ship with Karta so every
  consumer of the library resolves it with nothing applied. Here the deliverable
  is Go under `pkg/catalog/kartas/` plus generated YAML, a flow test, and a
  recording. Steps 1 and 3 through 7 still apply to the definition's content;
  `reference/builtin-contribution.md` covers everything around it.

Signals for the built-in branch: the request names a type the repository would
plausibly ship (a widely used operator rather than an in-house CRD), mentions
the catalog, a PR, or CI, or the user is working inside this repository on a
contribution. When the signals are mixed, ask rather than guess. Producing Go
and a catalog registration for someone who wanted one YAML file is a large,
unwanted change, and the reverse leaves a contribution that CI rejects.

### 1. Gather the target facts first

Do not write anything until these facts are known. Read the target CRD source or
documentation to get them right.

Ask the user for two inputs up front:

- The CRD schema (`kubectl get crd <name> -o yaml`, or the operator's API types).
  This is what the definition is written from.
- Real example CRs (`kubectl get <kind> <name> -o yaml`), ideally one per state
  the workload reaches. These unlock step 7, which is the only way to prove the
  paths resolve. A jq path can be structurally valid and still point at a field
  no real object carries.

Proceed either way. Without a CR the definition can still be written and
validated; it just cannot be exercised, which step 7 covers.

The repository already ships real CRs, so "no example available" is rarer than it
looks. `test/e2e/recorded_data/<operator>/<k8s-version>/<karta-name>/<flow>.yaml`
holds recordings captured from live clusters for 17 of the 20 catalog types.
Only `ray-io-rayservice-v1`, `apps-nvidia-com-nimcache-v1alpha1`, and
`nvidia-com-dynamographdeployment-v1beta1` have none.

Nothing about that path is guessable, so do not construct it. The `<operator>`
segment is the e2e suite's name for the operator and often differs from the
definition (`ray-io-raycluster-v1` sits under `kuberay/`, the two Kubeflow types
under `kubeflow/`, LeaderWorkerSet under `lws/`, NIMService under `nim/`), and
`<k8s-version>` is a cluster version nobody can predict. The flows present vary
per type too: across the suite they are `running`, `scaled`, `degraded`,
`suspended`, `resumed`, `completed`, `failed`, `initializing`, and
`born-suspended`, but no type has all of them and some lack the obvious one
(`apps-deployment-v1` has no `running`). List, then pick:

```bash
find test/e2e/recorded_data -name '*.yaml' | grep <karta-name>      # in a clone
# or, without one:
curl -sL "https://api.github.com/repos/dsx-ai-factory/workload-map/git/trees/$KARTA_REF?recursive=1" \
  | grep -o 'test/e2e/recorded_data/[^"]*\.yaml'
```

A recording is the whole flow, not one moment: it holds a `kind: STATE` event per
observed step, each carrying the `object` and the `state` the recorder read from
that object's own fields. The flow name is the journey's destination, not the
state of every event in it. `batch-job/.../resumed.yaml` runs Suspended,
Suspended, Initializing, Running, Initializing, Completed.

Taking the first event is therefore wrong, and quietly so: it hands back a
Suspended CR from a file named `resumed`. Select the state you want by name, and
read the sequence first if unsure:

```bash
F=test/e2e/recorded_data/batch-job/v1.34.0/batch-job-v1/resumed.yaml
yq '[.events[] | select(.kind == "STATE") | .state]' "$F"   # what this flow passes through
yq '[.events[] | select(.kind == "STATE" and .state == "Running")][-1].object' "$F" > /tmp/cr.yaml
```

The same expression works on a fetched file, with `curl -sL "$KARTA_RAW/<path>" |`
in front of the `yq`. Keep the state you selected: it is the prediction step 7
checks against, and it is trustworthy only because it was read from the CR rather
than from any definition.

Two uses, both valuable even when the target type is not recorded:

- Adapting a near neighbour: read a real RayCluster before writing a definition
  for another group-based operator, rather than trusting the CRD schema's account
  of what the controller writes.
- Calibration: the `state` of the event you selected is ground truth established
  without reference to any definition, which makes it a sound prediction to write
  down in step 7.

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
and copy that definition as the starting skeleton. Adapting a working sample is
faster and safer than starting from an empty file. Change the GVK, the paths, and
the status mapping to fit the target.

The CLI carries the catalog, so at tier 1 or 2 there is nothing to download:

```bash
karta definitions                                  # every definition, as a table
karta definitions --group ray.io --kind RayCluster # which one covers a type
karta definitions ray-io-raycluster-v1 -o yaml     # the definition itself
```

The table's COMPONENTS column names each definition's component tree, which is a
fast way to confirm a row's shape matches the target before copying it. With no
reachable cluster it lists the built-ins regardless, usually after a warning that
it could not read definitions from one. That warning is the expected path here,
not a failure.

At tier 3, fetch the file instead:

```bash
curl -sL "$KARTA_RAW/docs/catalog/ray-io-raycluster-v1.yaml"
```

Either way the YAML is generated output. `docs/catalog/` is rendered from
`pkg/catalog/kartas/*.go` by `make generate-samples`, and `make validate` fails CI
on drift, so never edit a catalog file in place. On the standalone branch the copy
belongs outside the repository anyway. On the built-in branch the edit belongs in
the Go source; see `reference/builtin-contribution.md`.

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

### 5. Map every state the workload reaches, not just the happy path

`statusDefinition` is required on the root component. It translates the
workload's own conditions or phases into Karta's normalized statuses:
`Initializing`, `Running`, `Completed`, `Failed`, `Degraded`, `Suspended`,
`Suspending`, `Resuming`. A workload that matches no rule resolves to
`Undefined`.

The mechanics:

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

The hard part is coverage, not syntax. Nearly every built-in in this repository
has needed a follow-up fix, and each one was the same shape: a first draft that
described the workload at rest and fell to `Undefined` the moment it moved.
Before settling, walk the workload's life and ask what the CR looks like at each
turn. These are the transitions that have actually bitten, each drawn from a real
correction to a shipped definition:

- Birth. The very first reconcile often reports differently from steady
  state. A Deployment's `Progressing=True` carries reason `NewReplicaSetCreated`
  once, before any `Available` condition exists, so a matcher that requires
  `Available=False` misses the window entirely.
- Scale up and scale down. Scale-down is the commonly missed half: a
  StatefulSet that has acknowledged the new spec while extra pods drain reports
  `.status.replicas > .spec.replicas` with every other counter looking settled.
- Transient flapping. Controllers briefly zero a counter mid-run. A JobSet
  matcher reading `ready > 0 and active > 0` dropped to `Undefined` whenever
  `ready` dipped; `active > 0 or ready > 0` holds through it. Prefer a matcher
  that stays true across the controller's own churn.
- Suspend and resume. Many controllers leave `.status.phase` at `Running`
  while `.spec.suspend` is true, so a bare `byPhase: Running` misreports a
  suspended workload. Guard the running rule on the suspend field.
- Tri-state conditions. A condition reporting `Unknown` means something
  distinct from `False`. Knative signals the whole deploy through
  `Ready=Unknown` and a broken Service through `Ready=False`, which map to
  Initializing and Failed respectively.
- Version-dependent condition types. Newer API versions add types beside the
  old ones. A `batch/v1` Job now reports `SuccessCriteriaMet` and `FailureTarget`
  alongside `Complete` and `Failed`; map both so the definition works across
  cluster versions.

One trap deserves naming here because it fails silently and the validator cannot
see it: a matcher may only constrain `reason` if `conditionsDefinition` declares
`reasonFieldName`. Without it the accessor never populates the field, the
comparison never matches, and the status simply never resolves. This shipped in
the LeaderWorkerSet definition and went unnoticed until the recorded fixtures
were replayed. `messageFieldName` is a different thing and not a second version
of this trap: a matcher can constrain only `type`, `status` and `reason`, so
declaring it affects what gets extracted, never what matches.

When adding a matcher, write down in a comment what the controller does that
makes it fire. The matchers that later needed fixing were the ones nobody could
justify afterwards.

### 6. Validate the definition

Always run the validator on the definition just written. Do not hand back a
definition that has not passed it. The CLI is the way to do this, and needs no
cluster and no checkout:

```bash
karta validate <definition.yaml>
karta definitions ray-io-raycluster-v1 -o yaml | karta validate -   # also reads stdin
```

It prints `OK: <file> is a valid Karta definition (maps <gvk>)` and exits 0 when
the definition is well-formed. Exit 1 means invalid, with the findings listed;
exit 2 means the file could not be read as YAML at all, which is a parse problem
before any Karta rule applies. Look a failure up in
`reference/troubleshooting.md` by its message text, fix it, and run again.

In a clone, `go run ./hack/karta-verify --karta <definition.yaml>` runs the same
validator and is worth preferring there, because it is also step 7 and saves
switching tools mid-loop.

(A naming wrinkle worth knowing so you quote the right command: the binary builds
to `bin/karta` and is installed as `karta`, but its own `--help` output still
calls itself `kli`. Follow the installed name, not the usage string.)

At tier 3 there is no validator to run, since the CLI cannot be fetched as a
single file and building it means cloning anyway. Do not describe the definition
as validated, checked, or verified. Name the gap, and close it for the user by
handing them the exact command to run once they have either tier:

```bash
karta validate <definition.yaml>
```

If they would rather you did it, the clone in the tier section gets there in a
minute and also unlocks step 7.

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
- Status conditions and phases match the workload's real API, and any matcher
  constraining `reason` has `reasonFieldName` declared.
- Every gang-scheduling `componentName` names a defined component. The validator
  checks this only for the deprecated `podGroups` format; references under
  `podGroup.subGroups` are not checked, so verify those by hand.
- Replica counts describe the right level of the tree, and siblings at the same
  level agree. See the scale section of `reference/technical-guide.md`.

A valid definition is still an unproven one: validation says nothing about
whether a path resolves against a real object. Step 7 is what proves that.

### 7. Run the definition against real CRs

This step needs a clone (tier 2): `hack/karta-verify` is the only thing that runs
a definition against a manifest offline, and the CLI has no equivalent. `karta
get` resolves status through a definition, but only against a live cluster and
only once the definition is applied to it, which is a slower and more invasive
loop than this one.

Do it whenever there is both a clone and a real CR, which after step 1 is most of
the time. It is the only step that proves a path resolves: a definition can pass
step 6 in full, resolve to null against the real object, and report nothing.

When there is no clone or no CR, skip the step and say so in the final answer,
naming which. The definition is structurally valid and never exercised, and which
parts are unverified should be stated plainly rather than left for someone to
discover. If the user wants this coverage, the clone command in the tier section
is the ask; it is a minute of their time and it is what turns a plausible
definition into a proven one.

The same command does it, with `--workload` added. It builds the workload tree
from the manifest and prints the extracted status, replica counts, and containers
per component instance, with no cluster involved. Its flags and the predictions
format are documented in `hack/karta-verify/README.md`.

Predict before running. Writing down the expected values first is the point of
this step: reading the output afterwards invites accepting whatever appears,
while a prediction that disagrees with the extraction is a defect that cannot be
talked away.

1. From the CR, write the values the definition should produce into a predictions
   file: the status, and per component instance the replica count and container
   names. Derive them from the CR's own numbers, never by reading them back out
   of an existing definition. When the CR came from `recorded_data`, its `state`
   field is exactly this: a status read from the CR's own fields with no
   definition involved. Use it.
2. Run it, from the repository root:

   ```bash
   go run ./hack/karta-verify --karta <definition.yaml> \
     --workload <real-cr.yaml> --predict <predictions.yaml> --strict
   ```

3. Reconcile every mismatch and warning. A mismatch means either the path is
   wrong or the understanding of the CRD is wrong. Decide which before changing
   anything, and never edit the prediction just to make the run pass.

The definition is done when the command exits 0 with `--strict`: the status
resolved, every child component declaring a spec pattern extracted a pod spec
with containers, every `instanceIdPath` produced the instance keys the CR
contains, and every predicted number matched.

Child is the load-bearing word. `WorkloadTree` documents that it excludes the
root component, and the harness walks `wt.Children`, so none of its component
checks see the root: not the extraction, not the replica count, and not the
"declares a specDefinition but extracted no pod spec" warning. Point a root
`podTemplateSpecPath` at a field that does not exist and `--strict` still exits 0
with the status resolved and nothing reported. For a single-component definition
that is the entire pod-spec check silently doing nothing.

So prove the root's own paths by hand. jq against the CR is enough and needs no
build:

```bash
yq '.spec.template.spec.containers[].name' /tmp/cr.yaml   # whatever the root's spec path is
yq '.spec.replicas' /tmp/cr.yaml                          # and its replicasPath
```

A predictions file cannot cover this either: predictions are keyed by component
and the root has no key, so predicting a root row reports `extracted keys are
<none>` rather than checking anything.

Run it once per state, not once. A single `running` CR exercises one branch of
the status mapping and says nothing about the other five, which is precisely
where step 5's failures hide.

The unit is a state, not a file. `--workload` takes one extracted CR, while a
recording holds a `kind: STATE` event per observed step, so one flow file is
several runs. Loop the events, predicting each one's own `state`:

```bash
F=test/e2e/recorded_data/batch-job/v1.34.0/batch-job-v1/resumed.yaml
n=$(yq '[.events[] | select(.kind == "STATE")] | length' "$F")
for i in $(seq 0 $((n-1))); do
  yq "[.events[] | select(.kind == \"STATE\")][$i].object" "$F" > /tmp/cr.yaml
  yq "[.events[] | select(.kind == \"STATE\")][$i].state"  "$F"   # the prediction
  # then run the harness against /tmp/cr.yaml with that status predicted
done
```

Each state that goes unexercised is a state the definition is only guessing at,
and worth naming as such in the final answer.

Show the user the run output alongside the definition. Keep the predictions file
and any scratch copies out of the repository. When something comes back empty or
wrong, do not adjust the checklist; look the symptom up in
`reference/troubleshooting.md`, fix the path, and run again.

### 8. On the built-in branch, finish the contribution

A proven definition is the middle of that job, not the end. This branch is tier 2
by definition: it edits Go, regenerates the catalog, and runs the repository's
test suites, none of which is possible without a clone. Return to
`reference/builtin-contribution.md` for registration, generation, the flow test,
the recording, and `make check`.
