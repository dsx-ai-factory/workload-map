<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Karta technical guide (cheatsheet)

A condensed field reference for authoring a Karta definition. It matches the API
types in `pkg/api/runai/v1alpha1/` and the validator in
`pkg/api/runai/v1alpha1/validation.go`. The prose reference is
`docs/Technical Guide.md`.

## Top-level shape

```yaml
apiVersion: run.ai/v1alpha1
kind: Karta
metadata:
  name: <lower-case-name>
spec:
  structureDefinition:
    rootComponent: {}          # required
    childComponents: []        # optional
    additionalChildKinds: []   # optional
  optimizationInstructions: {} # optional
```

## Component model

A component is one node in the workload tree.

- Root component: exactly one. Requires a full GVK and a `statusDefinition`. Must
  not have an `ownerRef`.
- Child component: requires an `ownerRef` naming another component (root or
  another child). Owner chains must reach the root with no cycles.
- Component `name` is a free-form identifier, unique within the Karta.
- `kind` is the full GVK. All of group, version, and kind are required. The core
  `Pod` kind is the only kind allowed to omit the group (use `group: ""`).

Virtual components. A component may omit `kind` and `specDefinition` entirely and
exist only to model a level of the tree. Use one when the workload has a grouping
level that owns other components but is not itself a Kubernetes object, and give
it a `scaleDefinition` for the level's count and a `replicaSelector` for the label
that identifies which group a pod belongs to. LeaderWorkerSet is the canonical
case: a `group` component sits between the root and the `leader` and `worker`
components, carries `replicasPath: .spec.replicas // 1` and a `replicaSelector` on
`leaderworkerset.sigs.k8s.io/group-index`, and owns both roles. Without it,
`leader` and `worker` have no shared grouping level and per-group identity is
lost.

```yaml
- name: group
  ownerRef: leaderworkerset
  scaleDefinition:
    replicasPath: .spec.replicas // 1
  podSelector:
    replicaSelector:
      keyPath: .metadata.labels["leaderworkerset.sigs.k8s.io/group-index"]
```

Fields available on a component (`ComponentDefinition`):

| Field | Purpose |
|---|---|
| `name` | Unique identifier. Required. |
| `kind` | Full GVK. Required on root; recommended on children. |
| `ownerRef` | Parent component name. Required on children, forbidden on root. |
| `specDefinition` | Where the pod template lives. |
| `scaleDefinition` | Where replica counts live. |
| `statusDefinition` | Status mapping. Required on root. |
| `suspendDefinition` | Native suspend/resume field assignments. |
| `instanceIdPath` | jq path to instance names for multi-instance components. |
| `podSelector` | How pods map to this component and its instances. |

## Spec definitions (mutually exclusive)

Set exactly one of these three per component. Setting more than one fails
validation with `has multiple pod spec definitions`.

| Pattern | Use when | Example |
|---|---|---|
| `podTemplateSpecPath` | CRD embeds a full PodTemplateSpec | `.spec.template` |
| `podSpecPath` (+ `metadataPath`) | CRD embeds a bare PodSpec, metadata separate | `.spec.jobTemplate.spec`, `.spec.jobTemplate.metadata` |
| `fragmentedPodSpecDefinition` | Pod fields scattered across the spec | see below |

Choosing `fragmentedPodSpecDefinition` is not only about a missing pod template.
A CRD can embed a real `podSpec` and still need fragmented paths, because the
fields Karta treats as part of the pod live at different levels. Grove
PodCliqueSet is the example: containers and scheduler name are inside
`.spec.template.cliques[].spec.podSpec`, but labels and annotations sit one level
up on the clique itself. `podSpecPath` would read the spec and silently drop the
labels and annotations. Check where every field lives, not just the containers.

`fragmentedPodSpecDefinition` fields (all optional; set only those that exist):
`schedulerNamePath`, `labelsPath`, `annotationsPath`, `resourcesPath`,
`resourceClaimsPath`, `podAffinityPath`, `nodeAffinityPath`, `containersPath`,
`containerPath` (single container), `priorityClassNamePath`, `imagePath`.

```yaml
specDefinition:
  fragmentedPodSpecDefinition:
    labelsPath: .spec.labels
    resourcesPath: .spec.resources
    containerPath: .spec.components[] | .podTemplate.spec.containers[] | select(.name == "main")
```

Fragmented paths must be assignable. Every `fragmentedPodSpecDefinition` path is
used both to read the field and to write it back when a consumer mutates the pod
spec, so each path must be a jq path expression that jq can assign through.
Navigation (`.a.b`), array iteration (`.items[]`), and path-preserving filters
(`select(...)`) are assignable. A `//` fallback is not: `.a // .b` produces
values, not a path, so it reads fine but fails on mutation. This passes both the
schema and the jq safety validator, so it is a silent trap. When a field can be
overridden (for example a workflow-level default plus a per-template override),
target one layer with an assignable path rather than a fallback expression, and
model per-item variation with a multi-instance component (`instanceIdPath`) so
each item's field stays index-aligned and assignable.

Read-only projections. Some shapes have no assignable path at all. A path that
needs a variable binding to filter one array by another (`. as $t | $t.items[] |
select(...)`) or that constructs an object rather than navigating to one reads
correctly but cannot be assigned through. Prefer an assignable path whenever one
exists. When none does, the fragmented path is still usable for reading, and the
consequence is explicit: mutating that component's pod spec fails. Say so in a
comment next to the path so the limitation is not rediscovered later. The catalog
does this for the Grove standalone-clique paths and the NIMCache resources path.

Variable bindings are allowed. `as $name` is not on the rejected-construct list,
and the corrected Grove definition relies on it to exclude cliques that belong to
a scaling group. Bindings keep a path readable when a filter has to reference a
sibling field, at the cost of assignability.

## Status definition

Required on the root component. Optional on children. Structure:

```yaml
statusDefinition:
  conditionsDefinition:      # needed only if any rule uses byConditions
    path: .status.conditions
    typeFieldName: type      # defaults: type / status / message / reason
    statusFieldName: status
    reasonFieldName: reason
    messageFieldName: message
  phaseDefinition:           # needed only if any rule uses byPhase
    path: .status.phase
  statusMappings:            # required
    running:
    - byConditions:
      - type: Ready
        status: "True"
```

Normalized statuses (the `ResourceStatus` enum): `Initializing`, `Running`,
`Completed`, `Failed`, `Degraded`, `Suspended`, `Suspending`, `Resuming`.
`Undefined` is the implicit result when no rule matches; do not map to it.

Matcher semantics (`StatusMatcher`):

- `byConditions`: a list of expected conditions, all of which must hold (AND).
  Each entry sets `type` plus at least one of `status` or `reason`. Constraining
  `reason` requires `conditionsDefinition.reasonFieldName` to be declared: the
  accessor populates a condition's reason only when that field name is set, so
  without it the comparison runs against nil and the matcher can never fire. The
  validator does not catch this; the status just resolves to `Undefined`. Same
  for `message` and `messageFieldName`.
- `byPhase`: matches a single phase string from `phaseDefinition.path`.
- `byExpression`: a jq `expression` plus an `expectedResult` string. Use it when
  the state lives in status fields (for example replica counts) rather than
  conditions or a phase.
- Rules under one status are OR'd: any matching rule resolves the status.
- Several statuses can match at once. Map only what the workload reports.

One matcher may combine kinds. A single `StatusMatcher` can set more than one of
`byPhase`, `byConditions`, and `byExpression` at once, and then all of them must
hold (AND). Use this when a status needs both a phase and an extra field check.
This is distinct from listing separate rules under a status, which are OR'd.

A condition's `status` is tri-state. `Unknown` is a deliberate signal and means
something other than `False`: Knative reports an in-progress deploy as
`Ready=Unknown` and a broken Service as `Ready=False`, which map to Initializing
and Failed. Treat a missing `Unknown` rule as a gap, not a detail.

Newer API versions add condition types beside the old ones rather than replacing
them, so a definition that must work across cluster versions maps both. A
`batch/v1` Job reports `SuccessCriteriaMet` and `FailureTarget` alongside
`Complete` and `Failed`; each goes in its own OR'd rule under the same status.

Not every controller has a phase or conditions. Some report only replica counts
or other status fields (for example Grove PodCliqueSet has no aggregate phase).
Do not invent a phase or a condition type the controller never sets: that
produces a definition that validates but never resolves. Match such states with
`byExpression` over the real status fields, for example
`(.status.availableReplicas // 0) >= (.spec.replicas // 0)` for running.

Example combining expression and condition rules:

```yaml
statusMappings:
  running:
  - byExpression:
      expression: (.status.active // 0) > 0 and (.status.ready // 0) > 0
      expectedResult: "true"
  completed:
  - byConditions:
    - type: Complete
      status: "True"
```

## Scale definition

```yaml
scaleDefinition:
  replicasPath: .spec.parallelism // 1
  minReplicasPath: .spec.minReplicas
  maxReplicasPath: .spec.maxReplicas
```

All three paths are optional. Keep them null-safe.

A component's replica count is the number of units at that component's level of
the tree, counted across the whole workload. It is not the number of API objects
of the component's `kind`. The distinction matters because a component's `kind`
often names the controller object that produces the pods rather than the pods
themselves. In LeaderWorkerSet the `leader` component has kind `StatefulSet` and
`replicasPath: .spec.replicas // 1`, which for three groups resolves to 3, even
though the operator creates a single leader StatefulSet. The count describes the
level, not the object.

Two numbers, two levels. A grouped or replicated workload usually holds both a
group count and a members-per-group count, and picking the wrong one is a valid
jq path that returns the wrong number, so the validator cannot catch it.
LeaderWorkerSet is the trap: `.spec.replicas` is the number of groups and
`.spec.leaderWorkerTemplate.size` is pods per group. The `group` component scales
on `.spec.replicas // 1`, `leader` on `.spec.replicas // 1` (one leader per
group), and `worker` on the derived
`(.spec.replicas // 1) * ((.spec.leaderWorkerTemplate.size // 1) - 1)`. A nested
level multiplies by its parent's count the same way: JobSet's `replicatedjob`
uses `.spec.replicatedJobs[] | .replicas * .template.spec.parallelism`.

Two self-checks. Sibling components that model the same level should resolve to
the same count (`group` and `leader` above both give 3). And the numbers should
add up against a real manifest: if the CR declares 3 groups of 4, the components
should report 3, 3, and 9, not 4.

Look for autoscaling bounds explicitly. `minReplicasPath` and `maxReplicasPath`
are easy to miss because they usually live somewhere other than the replica field
itself, for example PyTorchJob's `.spec.elasticPolicy.minReplicas` or Grove's
`.spec.template.cliques[].spec.autoScalingConfig.minReplicas`. Search the CRD for
an autoscaling or elastic policy block before deciding the workload has none.

## Suspend definition

For workloads with native suspend support (for example `.spec.suspend` on a
Job). Both action lists require at least one entry.

```yaml
suspendDefinition:
  suspendActions:
  - path: .spec.suspend
    value: "true"
  resumeActions:
  - path: .spec.suspend
    value: "false"
```

`value` is a JSON-encoded string (`"true"`, `"0"`, `"paused"`, `"null"`).

## Pod selectors (paths run against pod manifests)

```yaml
podSelector:
  componentTypeSelector:        # maps a pod to this component type
    keyPath: .metadata.labels["training.kubeflow.org/replica-type"]
    value: worker               # optional; if omitted, only key existence is checked
  componentInstanceSelector:    # splits one component into named instances
    idPath: .metadata.labels["ray.io/group"]
  replicaSelector:              # distinguishes replicas of the same sub-structure
    keyPath: .metadata.labels["leaderworkerset.sigs.k8s.io/group-index"]
```

`componentInstanceSelector` must pair with a component-level `instanceIdPath`,
and vice versa. Selectors of the same kind must be mutually exclusive across
components.

Role-label keys are framework-specific, and differ even between operators from
the same project. Do not copy a selector key from the nearest sample without
checking the target controller's real pod labels. For example the Kubeflow
training-operator (PyTorchJob, TFJob) labels role with
`training.kubeflow.org/replica-type` (values `master`, `worker`), while the
Kubeflow mpi-operator (MPIJob v2beta1) labels role with
`training.kubeflow.org/job-role` (values `launcher`, `worker`). Read the actual
pod labels the controller sets before writing `keyPath`.

Disambiguating roles that share a label. When two components would match the
same pod label, a plain value match is not mutually exclusive. Separate them by
matching on a key that only one role carries, using key existence (omit `value`).
LeaderWorkerSet is the canonical case: both leader and worker pods carry
`leaderworkerset.sigs.k8s.io/worker-index`, so the leader is matched by that
label with `value: "0"`, and the worker is matched by the existence of the
`leaderworkerset.sigs.k8s.io/leader-name` annotation, which only worker pods
have.

```yaml
# leader: value match on the shared label
componentTypeSelector:
  keyPath: .metadata.labels["leaderworkerset.sigs.k8s.io/worker-index"]
  value: "0"
# worker: key existence of a role-specific annotation (no value)
componentTypeSelector:
  keyPath: .metadata.annotations["leaderworkerset.sigs.k8s.io/leader-name"]
```

## Multi-instance components

When one component holds several specs (an array or a map), give it an
`instanceIdPath` so each instance is distinguishable, and a matching
`componentInstanceSelector` on the pod side.

```yaml
# array of specs
instanceIdPath: .spec.workerGroupSpecs[].groupName
# map of specs
instanceIdPath: .spec.services | to_entries[] | .key
```

## Additional child kinds

List GVKs the workload creates or manages that are not modeled as components.
Used for RBAC. Avoid duplicating a kind already declared as a component, unless
the kind must also be listed here for RBAC or owner traversal (the validator does
not reject it).

Component or additional kind. Model a kind as a component when something needs to
be read from it or written to it: a pod template, a replica count, a status, or a
selector that maps pods to it. Otherwise list it here. A component is also the
right choice when it is only a placeholder in the ownership chain that another
component must hang off, in which case it carries a `kind` and an `ownerRef` and
nothing else. The CronJob definition does exactly that for the `batch/v1` Job it
creates. Do not list a kind here merely because the workload creates it, if a
component already covers it.

```yaml
additionalChildKinds:
- group: apps
  version: v1
  kind: Deployment
```

## Optimization instructions (paths run against pod manifests)

Optional, used by schedulers. Two formats exist. `podGroup` is current;
`podGroups` is marked deprecated in the API but is what every catalog definition
still uses, so expect to read it. Every member or subgroup `componentName` must
name a defined component.

```yaml
# current format
optimizationInstructions:
  gangScheduling:
    podGroup:
      name: job
      subGroups:
      - componentName: worker
```

The deprecated `podGroups` format carries two fields the current one has no
equivalent for, which is why the catalog still uses it:

```yaml
optimizationInstructions:
  gangScheduling:
    podGroups:
    - name: job
      members:
      - componentName: worker
        groupByKeyPaths:
        - .metadata.labels["training.kubeflow.org/job-name"]
        filters:
        - (.spec.containers[0].resources.limits["nvidia.com/gpu"] // 0) > 0
```

- `groupByKeyPaths`: jq paths evaluated against individual pod manifests, whose
  values decide which pods share a gang. Use them when pods of one component must
  be split into several gangs, typically by owner name plus a replica index. When
  omitted, grouping falls back to owner reference traversal. Each path must return
  a single non-empty value for every pod, or grouping fails at runtime, so keep
  them null-safe (the LeaderWorkerSet definition uses
  `.metadata.labels["leaderworkerset.sigs.k8s.io/group-index"] // "0"`).
- `filters`: jq expressions, ANDed, also evaluated against pod manifests, to
  restrict a member to a subset of its pods.

Both are pod-level paths, not workload paths. When copying a catalog definition
as a skeleton, copy the format it uses rather than converting it, and check the
`groupByKeyPaths` label keys against the target controller's real pod labels the
same way as `podSelector` keys.

## jq safety rules

Every path is validated statically. These constructs are rejected:

- Assignment and update operators (`=`, `|=`, `+=`, `-=`, and the rest).
- The `del` function.
- The recursive descent operator `..`.
- Unbounded builtins: `range`, `paths`, `recurse`, `walk`, `repeat`.

Rules for correct paths:

- Absolute, starting with `.`.
- Null-safe with `//` defaults for any field that may be absent.
- Evaluated against the correct resource (workload object vs pod manifest).

## Validation checklist

- All kinds use a full GVK (only `Pod` may omit the group).
- Root has a `statusDefinition` and no `ownerRef`.
- Every child has an `ownerRef` to an existing component; no ownership cycles.
- Component names are unique and non-empty.
- No component sets more than one spec pattern.
- `instanceIdPath` and `componentInstanceSelector` are both present or both absent.
- Pod selectors reference pod fields; selectors of the same kind are mutually exclusive across components.
- Status conditions and phases match the workload's real API.
- Every declared `conditionsDefinition` or `phaseDefinition` is referenced by at least one matcher, and every matcher has the definition it needs.
- Replica counts describe the component's level, siblings at the same level agree, and nested levels multiply by the parent count.
- Autoscaling bounds were looked for, not assumed absent.
- Every `fragmentedPodSpecDefinition` path is assignable, or is documented as read-only.
- No redundant duplicate kinds in `additionalChildKinds` (duplicates are allowed only when needed for RBAC or owner traversal).
- Every gang-scheduling member names a defined component, and `groupByKeyPaths` and `filters` reference pod fields.
