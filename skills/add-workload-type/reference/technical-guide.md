<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Karta technical guide (cheatsheet)

A condensed field reference for authoring a Karta definition. It matches the API
types in `pkg/api/runai/v1alpha1/` and the validator in
`pkg/api/runai/v1alpha1/validation.go`. The prose reference is
`docs/Technical Guide.md`. It also carries the why and the operator examples
behind steps 3 to 7 of `SKILL.md`: Status mapping patterns for step 5 and
karta-verify runs for steps 6 and 7.

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
components, carries `replicasPath: .spec.replicas` and a `replicaSelector` on
`leaderworkerset.sigs.k8s.io/group-index`, and owns both roles. Without it,
`leader` and `worker` have no shared grouping level and per-group identity is
lost. The catalog builder still reads `.spec.leaderWorkerTemplate.size` there,
the wrong level (Two numbers, two levels).

```yaml
- name: group
  ownerRef: leaderworkerset
  scaleDefinition:
    replicasPath: .spec.replicas
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

An optional full template next to the scattered fields is not a reason to use
`podTemplateSpecPath`. Spark offers `spec.driver.template` beside the scattered
driver fields. An absent template reads null, and a write through it creates a
template with no containers. The scattered fields are always there, so the
definition reads those, and the builder comment says the template is not read.

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
(`select(...)`) are assignable. A `//` fallback is the trap: in gojq,
`path(.a // .b)` is the path of whichever branch the read resolved, so a write
lands silently in that branch, possibly the wrong field. A fallback to a
literal (`.a // 0`) fails on write with `invalid path against: number (0)`.
Both pass the schema and the jq safety validator. When a field can be
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
does this for the NIMCache resources path.

An assignable path can still be the wrong one. When the container-like objects
are not `corev1.Container` (a Tekton Step keeps its resources under
`computeResources` and adds `script` and other step-only fields), a plain
`containersPath: .taskSpec.steps` reads without resources and a write-back
drops the step-only fields. Use a deliberate read-only projection instead,
`[.taskSpec.steps[]? | . + {resources: .computeResources}]`, so reads carry the
right fields and a write fails with `invalid path against: array (...)` rather
than corrupting the spec. Say so in a comment.

A field shared by every instance (a workload-level scheduler name or affinity)
returns one value against N instances. Extraction pads the shorter result list,
so only the first instance gets the value and the others read it as unset.
Repeat it per instance with a read-only path that walks the same iterator:
`. as $root | (.spec.a[]?, .spec.b[]?) | $root.spec.podTemplate.schedulerName`.
An absent field yields null per instance and reads as unset. Writes through it
fail by design.

Variable bindings are allowed. `as $name` is not on the rejected-construct list;
a Grove path that excludes cliques belonging to a scaling group needs one.
Bindings keep a path readable when a filter has to reference a
sibling field, at the cost of assignability. The optional iterator `[]?`, the
comma operator, object construction, and `+` are allowed too.

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
  Each entry sets `type` plus at least one of `status` or `reason`.
- `byPhase`: matches a single phase string from `phaseDefinition.path`.
- `byExpression`: a jq `expression` plus an `expectedResult` string. Use it when
  the state lives in status fields (for example replica counts) rather than
  conditions or a phase.
- Rules under one status are OR'd: any matching rule resolves the status.
- Several statuses can match at once, and all of them land in the workload's
  phases list. Keep the rules exclusive so each frame reads one status (step 5
  in `SKILL.md`). Map only what the workload reports.
- `byConditions` can only match a condition that exists. A frame written before
  the controller adds the condition matches neither `status: "True"` nor
  `status: "False"`. For "not yet written or not True" use `byExpression` over
  `.status.conditions // []`:
  `([.status.conditions // [] | .[] | select(.type == "PodRunning" and .status == "True")] | length) == 0`.

Suspend that does not change the phase. Some controllers keep the phase at
Running and leave the conditions alone while `.spec.suspend` is true; the pods
simply stop being created. `Running` and `Suspended` then both match on every
suspended frame, and the recorded flow cannot tell them apart. AND an expression
into the non-suspended matchers so `Suspended` is exclusive:

```yaml
running:
- byPhase: Running
  byExpression:
    expression: (.spec.suspend // false) | not
    expectedResult: "true"
suspended:
- byExpression:
    expression: (.spec.suspend // false) and ((.status.phase // "") | IN("", "Pending", "Running"))
    expectedResult: "true"
```

On the test side the recorder's `AddState` order decides which of several
matching states is the strongest (last match wins), so the flow mirrors this by
declaring states least to most advanced.

One matcher may combine kinds. A single `StatusMatcher` can set more than one of
`byPhase`, `byConditions`, and `byExpression` at once, and then all of them must
hold (AND). Use this when a status needs both a phase and an extra field check.
This is distinct from listing separate rules under a status, which are OR'd.

Not every controller has a phase or conditions. Some report only replica counts
or other status fields (for example Grove PodCliqueSet has no aggregate phase).
Do not invent a phase or a condition type the controller never sets: that
produces a definition that validates but never resolves. Match such states with
`byExpression` over the real status fields, for example
`(.status.observedGeneration // 0) > 0 and (.status.availableReplicas // 0) >= (.spec.replicas // 1)`
for running.

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

## Status mapping patterns

The why and the operator examples behind the step 5 rules in `SKILL.md`.

### One status per frame

Every status that matches lands in the workload's phases list, and a consumer
cannot tell which one is current. The recorded flow cannot tell them apart
either. Keeping each pair exclusive means finding what the controller leaves
unchanged in the other state and guarding on a field it does change.

Phase derived from a spec field. A controller that reports Paused whenever
`.spec.paused` is set leaves that field on the frames before its first phase
write. Mirroring the controller:

- `Suspended` also matches `(.spec.paused // false) and (.status.phase // "") == ""`.
- `Initializing` ANDs `(.spec.paused // false) | not`.

Flag read before the phase switch. Spark reads `spec.suspend` before its phase
switch and writes its own Suspending and Resuming phases. A frame with the flag
set and a phase it will suspend from reads `Suspending`, and the paused phase
with the flag cleared reads `Resuming`: the status of the phase it writes next.
The flow mirrors both with an `AnyOf` helper over the phase and the flag, added
to `predicates.go` when missing.

A rule that negates a phase list (`IN(...) | not`) also matches the empty
phase, so it maps the object with no status too.

Hibernation by annotation. A controller that hibernates by annotation can keep
its healthy phase and `Ready=True` while it clears the ready count, so
`Running` needs `(.status.readyInstances // 0) >= (.spec.instances // 1)` as
well.

A condition set once and never cleared. Kubeflow `Created` holds on every later
frame, so a rule on it alone overlaps every other status. The guarded rule:

```text
[.status.conditions // [] | .[] | select((.type == "Running" or .type == "Succeeded" or .type == "Failed" or .type == "Suspended") and .status == "True")] | length == 0
```

The rule still requires `Created` itself. An object with no status then reads
`Undefined`, which step 7 allows and the recorder drops from the walk. The
PyTorchJob and MPIJob samples still carry the overlap.

Conditions written in one reconcile. The training-operator loops over all roles
in one status pass, so a succeeded Chief and a failed PS set `Succeeded=True`
and `Failed=True` together.

Polling loops beside the reconcile (KEDA, other autoscalers). When the
reconcile copies the conditions, starts the loop, and writes its copy
afterwards, its late write can undo the loop's first poll for one frame. When
the loop also writes its own cached copy, it can write a stale pause condition
back after a resume, and no reconcile clears it when status-only changes do not
trigger one. Matching `Suspended` on the intent field (a spec flag, an
annotation the controller only reads) together with the condition makes the
stale frame read what the workload does. Parse the field as the controller
does: KEDA's `strconv.ParseBool` treats any unparsable value as paused.

### Failed and Degraded

A consumer that reads `Failed` as final may delete the workload. An error a poll
loop sets and clears on its next good poll leaves the started work running, so
it is `Degraded`. KEDA shows both: `ScaledJobCheckFailed` (the scale loop never
started) is `Failed`, and `TriggerError` is `Degraded`.

A progress deadline, `Progressing=False/ProgressDeadlineExceeded`, goes back to
True by itself once the pods become available, so it is never `Failed`; the
Deployment sample maps it to `Failed`. It marks a fault only when the
controller restarts the clock. The Deployment controller moves
`lastUpdateTime` on every progress write and writes a new reason for a new
rollout. The OpenKruise CloneSet does neither: it checks the deadline against
the condition copied from the old status and keeps the last update time of an
unchanged condition. A CloneSet settled longer than the deadline therefore
trips it on the first reconcile of a healthy scale-up or rollout, and so does a
slow rollout that keeps progressing. Left unmapped, such a frame reads by the
counters: `Initializing` while pods are missing, `Running` once they are
available, which is where the controller clears the condition.

A condition the controller never clears does not mark a fault. CloneSet
`FailedScale` and `FailedUpdate` are set once; a pod update conflict right
after creation raised `FailedUpdate` in most CloneSet recordings.

A `Degraded` flow proves the clear: `Reaches(Degraded).Do(<fix>)`, then a
terminal `Running` gated on `observedGeneration`. A `nodeSelector` no node
carries is a generic fault, and a merge patch that sets it to `null` is the
generic fix, since a container list cannot be merge-patched.

A controller that reports only counters cannot tell one stuck pod from one that
is starting: some-but-not-all ready also holds on every create, the last pod of
every rollout, and every node join. That is why it maps to `Initializing`, as
`kubectl rollout status` does. The StatefulSet sample still maps that partial
to `Degraded` and declares the dip `Optional()` in its flows.

Fault signals that are not `Degraded`. Kubeflow `Restarting` evicts `Running`,
and the guarded `Initializing` rule maps the frame, so it reads
`Initializing`. DaemonSet pod failures are events only. DaemonSet
`numberMisscheduled` also counts pods that a NoSchedule taint lets keep
running, so the controller does not always clear it.

### Spec-driven branches

A settled state that owes no pods (replicas 0, a node selector no node
matches) reads `Running`, as the StatefulSet and Grove rules do. A desired > 0
guard leaves it `Undefined`. A `nodeSelector` no node carries reaches it on the
kind cluster.

Spec fields that choose how the controller progresses include an update
strategy such as `OnDelete`, a rollout `partition`, `paused`, and a restart
policy. The default for an absent field goes into the rule,
`(.spec.updateStrategy.type // "RollingUpdate")`, and comes from API defaulting
(`SetDefaults_DaemonSet`), a webhook, or the controller. Under a StatefulSet's
or DaemonSet's `OnDelete` the controller never replaces old pods by itself, so
a rule that waits for the updated count leaves a healthy workload in
`Initializing` forever. A feature gate that changes how the controller moves
pods is a branch too.

Reaching failure branches. A branch the controller enters when it cannot
create a pod is often reachable on kind through admission. A driver
`serviceAccount` naming a ServiceAccount that does not exist makes the
ServiceAccount admission plugin reject the Spark driver pod, and the operator
writes SUBMISSION_FAILED. A namespace ResourceQuota the pod exceeds does the
same for most controllers. The workload object itself is admitted, so try such
a manifest on the cluster before naming the branch unreachable.

The validating webhook can reject a value the Go types declare. The CloneSet
API still declares `OnDelete`, while its webhook
(`pkg/webhook/cloneset/validating`) accepts only `ReCreate`,
`InPlaceIfPossible`, and `InPlaceOnly`. A rule or flow for such a value is
dead code, and the flow fails at create. Stand-in CRs in step 7 cannot catch
it, which is why the webhook is read in step 1 and the manifests are applied
with `--dry-run=server` before recording.

An `omitempty` status counter is absent at 0, so its default is 0, never the
desired count. CloneSet `status.expectedUpdatedReplicas` (replicas minus the
partition) is absent when the partition covers every replica, and
`(.status.updatedReplicas // 0) >= (.status.expectedUpdatedReplicas // 0)` then
holds, as the controller means. A desired-count default would wait for a
rollout the partition forbids.

### In-flight phases

Karta has no in-flight status except `Suspending` and `Resuming`. A phase the
controller writes while it finishes a transition it always completes (draining
pods before Completed, Aborted, or Terminated) maps to the status it ends in.
Left unmapped, the workload reads `Undefined` mid-transition.

Two exits. Volcano's Restarting goes to Failed when
`status.retryCount >= spec.maxRetry`, else back to Pending. Both inputs are on
the object, so the phase splits into one matcher per exit, `byPhase` plus a
`byExpression`, each under the status it ends in. A retry policy with one retry
and a short interval reaches the retry exit on the kind cluster.

Pause through another object. Volcano suspends through a separate Command
object (AbortJob, ResumeJob) and reports Aborting, then Aborted. No
`suspendDefinition` can be written, yet Aborted is the resumable paused phase
(`Suspended`) and Aborting its draining phase (`Suspending`). A resume that
writes Restarting, as a restart policy does, leaves `Resuming` unmapped. A
lifecycle policy in the CR reaches the paused state, since the recorder only
patches the workload.

## Scale definition

```yaml
scaleDefinition:
  replicasPath: .spec.parallelism
  minReplicasPath: .spec.minReplicas
  maxReplicasPath: .spec.maxReplicas
```

All three paths are optional. Write them as plain assignable paths, even when
the field is `omitempty`: an absent field reads as null, which is the honest
value, and `//` defaults belong only in status expressions. Older catalog
entries such as `.spec.replicas // 1` predate this rule; do not copy the
fallback. A count derived from several fields (see Two numbers, two levels) is
the exception: it has no plain path, so it stays a read-only formula.

Omit the whole `scaleDefinition` when the spec carries no count for the
component. A workflow template or pipeline step runs any number of pods, and
nothing in the spec says how many. Do not write `replicasPath: 1` to fill the
gap: it is a number the CRD never declared. karta-verify prints
`replicas=<none>` for such a component, and that is not a warning. The same
holds for a count the API only implies (one TaskRun per Tekton task unless a
matrix fans it out): the catalog models counts a spec field states, so leave it
out and say so in a comment. A count only the status carries (DaemonSet
`desiredNumberScheduled`) is left out the same way.

A component's replica count is the number of units at that component's level of
the tree, counted across the whole workload. It is not the number of API objects
of the component's `kind`. The distinction matters because a component's `kind`
often names the controller object that produces the pods rather than the pods
themselves. In LeaderWorkerSet the `leader` component has kind `StatefulSet` and
`replicasPath: .spec.replicas`, which for three groups resolves to 3, even
though the operator creates a single leader StatefulSet. The count describes the
level, not the object.

Two numbers, two levels. A grouped or replicated workload usually holds both a
group count and a members-per-group count, and picking the wrong one is a valid
jq path that returns the wrong number, so the validator cannot catch it.
LeaderWorkerSet is the trap: `.spec.replicas` is the number of groups and
`.spec.leaderWorkerTemplate.size` is pods per group. The `group` component scales
on `.spec.replicas`, `leader` on `.spec.replicas` (one leader per group), and
`worker` on the derived read-only formula
`(.spec.replicas // 1) * ((.spec.leaderWorkerTemplate.size // 1) - 1)`. The
catalog's `group` still reads `.spec.leaderWorkerTemplate.size` and its
`leader` the older `.spec.replicas // 1`, which is why karta-verify reports
`group` 4 next to `leader` 3 for 3 groups of 4. A nested
level multiplies by its parent's count the same way: JobSet's `replicatedjob`
uses `.spec.replicatedJobs[] | .replicas * .template.spec.parallelism`.

Two self-checks. Sibling components that model the same level should resolve to
the same count (`group` and `leader` above both give 3). And the numbers should
add up against a real manifest: if the CR declares 3 groups of 4, the components
should report 3, 3, and 9, not 4.

Bounds without a count. Some specs declare only bounds on how many child
objects run (KEDA ScaledJob `minReplicaCount` and `maxReplicaCount` count Jobs,
while `.spec.jobTargetRef.parallelism` counts pods per Job). Put the bounds on
the child for that level with no `replicasPath`, and the per-Job count on the
component that carries the template. karta-verify prints
`replicas=<none> min=N max=M` for the child, and that is not a warning.

Look for autoscaling bounds explicitly. `minReplicasPath` and `maxReplicasPath`
are easy to miss because they usually live somewhere other than the replica field
itself, for example PyTorchJob's `.spec.elasticPolicy.minReplicas` or Grove's
`.spec.template.cliques[].spec.autoScalingConfig.minReplicas`. Search the CRD for
an autoscaling or elastic policy block before deciding the workload has none.
When a flag turns the bounds on (Spark `spec.dynamicAllocation.enabled`), Karta
still reads them with the flag off, and the gang minimum below uses them. Say
so in the builder comment.

`minReplicasPath` is also the gang minimum. Gang scheduling sizes a component
from its min replicas when set and above zero, else from its replicas
(`getEffectiveMinReplicas` in `pkg/instructions/gang_scheduling.go`). A
per-role gang minimum the CRD declares (Volcano `.spec.tasks[].minAvailable`)
therefore belongs in `minReplicasPath`. A workload-wide minimum (Volcano
`.spec.minAvailable`) belongs to no single component; leave it out and say so
in a comment.

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

`value` is a JSON-encoded string (`"true"`, `"0"`, `"paused"`, `"null"`). A
string field is quoted inside the YAML string, and `"null"` clears a field on
resume. Tekton holds a run through `.spec.status`:

```yaml
suspendActions:
- path: .spec.status
  value: '"PipelineRunPending"'
resumeActions:
- path: .spec.status
  value: "null"
```

The path may also target an annotation when the controller suspends through
one: `.metadata.annotations["example.io/hibernation"]` with `'"on"'` to suspend
and `'"off"'` to resume.

Check what the controller stops. A Job or JobSet suspend deletes the pods. An
autoscaler's pause (KEDA `autoscaling.keda.sh/paused`) only stops new work, and
running Jobs and their pods run to completion. Say so in the builder comment,
since a consumer that suspends to free capacity gets none back.

A hold the controller honors only before the run starts is still modeled as
suspend. Say in a comment that the controller rejects it on a started run, and
record it with a manifest created in the held state.

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

When one label is not enough (two operators reuse the same label value), match
several labels with `matchLabels`. All entries must match, and `matchLabels`
can be combined with `keyPath`. At least one of the two must be set, and
`value` requires `keyPath`.

```yaml
componentTypeSelector:
  matchLabels:
    app: pulsar
    component: proxy
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

Labels set at creation. A label the controller writes when it creates the pod
is there from the first frame. A label written later by status reconciliation
(a primary or replica role) leaves new pods unmapped until it appears, and
moves pods between components on failover.

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

Instance ids carried outside labels. Some controllers label pods only with the
workload name and never with the instance (template, step) they belong to. Argo
Workflows is the example: pods carry `workflows.argoproj.io/workflow` and
`workflows.argoproj.io/completed`, and the template name exists only inside the
`ARGO_TEMPLATE` env value of the executor container, as JSON. When no label
carries the instance id, an `idPath` may read the pod spec itself: env values,
annotations, or container names. `fromjson` is allowed, and the path must
return exactly one value. Say in a comment under which conditions it returns
null (for example a pod created by a different executor), because a null id maps
the pod to no instance.

```yaml
componentInstanceSelector:
  idPath: '[.spec.initContainers[]?, .spec.containers[]? | .env[]? | select(.name == "ARGO_TEMPLATE") | .value | fromjson | .name] | first'
```

Verify such a path against a real pod with `kubectl get pod <name> -o json | jq`
before shipping it. karta-verify never sees a pod.

## Multi-instance components

When one component holds several specs (an array or a map), give it an
`instanceIdPath` so each instance is distinguishable, and a matching
`componentInstanceSelector` on the pod side.

```yaml
# array of specs
instanceIdPath: .spec.workerGroupSpecs[].groupName
# map of specs
instanceIdPath: .spec.services | to_entries[] | .key
# two sibling lists of the same shape feeding one component
instanceIdPath: (.spec.pipelineSpec.tasks[]?, .spec.pipelineSpec.finally[]?) | .name
```

For the union form, every fragmented and scale path on the component starts
with the same `(... , ...)` iterator so the counts align. `[]?` on a missing list
yields nothing rather than an error.

This is not only a mutation concern. Reading breaks too: a component without
`instanceIdPath` has one implicit instance, and a fragmented or scale path that
iterates an array yields one result per element. The tree build then fails with
`instance ids count (1) does not match results count (N)`
(`zipWithInstanceIds` in `pkg/resource/component.go`). The rule: any
fragmented or scale path that can return more than one value, or zero, requires
`instanceIdPath` plus a `componentInstanceSelector`.

The counts must also line up. `instanceIdPath` and every fragmented path must
agree on which elements they visit, so when one uses a filter
(`select(.container != null)`) all of them use the same filter. A one-element CR
passes either way. Test with a CR whose array has two entries, and with one that
has zero if the CRD allows it. A workflow built from a template reference has no
inline templates, so the component reports zero instances and does not fail.

Keep a per-element field a plain path, `.spec.tasks[].replicas`. It yields one
value per element, null where the field is absent, so the count stays aligned.
Never put a default after the iterator: `.spec.tasks[].replicas // 0` yields
only the elements that carry the field (or a single `0` when none do), so one
task that omits `replicas` breaks the count. Integer fields marked `omitempty`
make this common. A default inside the pipeline,
`.spec.tasks[] | .replicas // 0`, keeps the count but is not assignable.

Roles keyed by a map. When the roles are optional keys of a map
(`.spec.tfReplicaSpecs`, `.spec.pytorchReplicaSpecs`), one multi-instance child
keyed by the map covers every role, deprecated aliases included:

```yaml
- name: replica
  instanceIdPath: .spec.tfReplicaSpecs | keys[] | ascii_downcase
  specDefinition:
    podTemplateSpecPath: .spec.tfReplicaSpecs[].template
  scaleDefinition:
    replicasPath: .spec.tfReplicaSpecs[].replicas
  podSelector:
    componentInstanceSelector:
      idPath: .metadata.labels["training.kubeflow.org/replica-type"]
```

A fixed child per role breaks on a CR that omits the key: it extracts an empty
pod spec, and a write through it creates the key with no containers, which the
operator's webhook rejects.

In Karta (gojq), `keys` and `.[]` both walk the map in sorted key order, so ids
and templates line up. The jq CLI walks `.[]` in insertion order instead, so a
CR whose keys are not sorted looks misaligned there when it is not.
`jq -S . cr.json | jq '<expr>'` sorts the input first. `keys_unsorted` passes
the validator but fails at runtime with `function not defined`.
`ascii_downcase` matches a controller that lowercases the key into the pod
label; the real label decides.

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

How a pod reaches a component. Which pods belong to the workload is decided by
the owner-reference chain from the pod up to the root. Which component a pod
maps to is decided among the components that declare a spec pattern
(`InferPodComponent` in `pkg/instructions/pod.go`): with exactly one, every pod
maps to it and no selector is needed; with several, `componentTypeSelector`
decides. An ownership-only child has no spec pattern and receives no pods. In
the CronJob shape the pods map to the root, which carries the template, and the
`Job` child only names the kind the owner chain passes through. Offline nothing
proves the chain; check it on a recorded pod with
`kubectl get pod <pod> -o json | jq '.metadata.ownerReferences'`.

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

Gang scheduling and creator pods. When one role's pods are created by another
role's running pod (a Spark driver creates its executors through the API), the
creator must schedule alone first. A gang over both with a minimum above 1
holds the creator until pods that cannot exist yet appear, and the workload
never starts. Read the minimum of the operator's own PodGroup integration
before writing a gang: spark-operator sets `MinMember` 1 so the driver
schedules first. Gang only the roles the controller creates together, or leave the
gang out and say why in the builder comment. The tree has the same trap: a
creator with no `scaleDefinition` and the created role as its child gets its
children's count from `CalculateSubtreeScale`
(`pkg/instructions/gang_scheduling.go`), not 1. Name that in the comment too.

## karta-verify runs

The why and the detail behind steps 6 and 7 in `SKILL.md`. Flags and the
predictions format are documented in `hack/karta-verify/README.md`.

Validation says nothing about whether a path resolves against a real object. A
definition can pass step 6 in full, resolve to null against the real object,
and report nothing. A run against a real CR is the only proof. With
`--workload`, karta-verify builds the workload tree from the manifest and
prints the extracted status, replica counts, and containers per component
instance, with no cluster involved.

Stand-in CRs. When no real CR exists, a CR written by hand from the controller
source (the status its first sync writes, the fields the admission webhook
defaults) exercises the paths, one per mapped phase plus one with no status. A
pod built from the controller's pod label code does the same for selectors.

The no-status frame. A `// 0` default on both sides of a comparison
(`(.status.observedGeneration // 0) == (.metadata.generation // 0)`, updated
equals desired) holds on an object no controller has seen. That is why every
settled rule requires a field only the controller writes. `--strict` counts an
unresolved status as a warning, which is why an `Undefined` prediction runs
without it.

Predictions. Writing the expected values first is the point: reading the output
afterwards invites accepting whatever appears, while a prediction that
disagrees with the extraction is a defect that cannot be talked away.

```yaml
status: [Running]
components:
- key: task[worker]
  replicas: 2
  containers: [worker]
  podSpec: true
```

The read side does not extract the root's scale and spec paths, so a prediction
keyed on the root fails as `predicted but not extracted`. For a
Deployment-shaped root, check `.spec.replicas` and
`.spec.template.spec.containers[].name` with jq instead. `status` is compared
as a set against every status that matched.

Done. With `--strict` the run exits 0 when the status resolved, every child
component declaring a spec pattern extracted a pod spec with containers, every
`instanceIdPath` produced the instance keys the CR contains, and every predicted
number matched. A child declared only for ownership (no spec or scale
definition, like the Deployment's `replicaset`) prints
`replicas=<none> podSpec=n/a containers=<none>`, which `--strict` accepts.

Zero instances. A CR that only references its items (a PipelineRun by
`pipelineRef`, a Workflow by template reference) correctly reports zero
instances, which `--strict` counts as a warning.

No container field. A CRD that only exposes image and resources overrides gets
a `fragmentedPodSpecDefinition` with no `containersPath` or `containerPath`. On
a child it always warns `extracted a pod spec with no containers`; Milvus warns
the same way. Pointing `containerPath` at the
role spec stores a whole container there on write and drops the role's other
fields.

Optional roles. A fixed child for a role the CR omits warns
`extracted a pod spec with no containers` next to `replicas=<none>`, and a
write through it would create the role. That is the map-keyed shape problem
(Roles keyed by a map above), not a CR to skip.

Exit codes. karta-verify exits 0 on success, 2 on a mismatch, and 3 on
warnings. `go run` turns every non-zero exit into 1, hence the built binary when
the code matters. A pipeline reports the exit code of its last command, and zsh
has no `PIPESTATUS`, so redirect instead:
`<scratch>/verify --karta ... > <scratch>/out.txt; echo $?`.

Scratch directory. A sibling of a linked worktree can be another checkout, so
`mktemp -d` is the safe default. A git-ignored directory inside the worktree
works too: neither `git status` nor `make validate` sees it.

### The write round trip (planned)

TODO. This check does not exist yet. This section records what it must do, so
that it is built the same way it was designed and so that step 7 can point at
it the day it lands.

Why. karta-verify proves the read side: a path resolves and the extraction
matches the prediction. A definition is also written through. A consumer sets
a node selector, bumps a resource request, or suspends the workload, and the
library writes the changed pod spec or the suspend value back through the same
paths. Two things can go wrong there, and neither is visible from reading:

- The write engine can change more than the one field. The pod spec travels
  through typed Go structs, so fields the type does not model are dropped and
  zero values the object never carried appear, for example
  `template.metadata: {}` or `containers[].resources: {}`. On the current
  engine six catalog definitions change on an identity write (pod, mpijob,
  pytorchjob, raycluster, knative service, kserve inferenceservice), all in
  that way. The fix is a write engine that produces a minimal merge patch
  instead of replacing the struct; it is a separate change to the library.
- The definition path can be wrong for writing while reading fine. A `//`
  fallback writes into whichever branch the read resolved, which can be the
  wrong field; a fallback to a literal, arithmetic, or a value-producing
  projection has no path to write to and fails with
  `invalid path against: ...`; a fragmented `containerPath` pointed at a
  role spec stores a whole container there and drops the role's other fields.

What it must do. Per component, the root included, each on its own copy of
the object:

1. Identity write: extract the pod spec (template, bare spec, or fragmented)
   through the definition and write it back unchanged. Nothing may change.
2. Change probe: set one field to a marker value and write again. The probe
   is a `nodeSelector` entry for a pod spec or template, and for a fragmented
   spec the scheduler name when `schedulerNamePath` is set, else a label, else
   the image. Exactly one leaf per instance may change, with the marker value.
3. Suspend, then resume: apply the suspend actions, then the resume actions.
   Only the action paths may change, and after resume the object is back to
   the original except where an action set a field the object did not carry.

It prints every changed path with its before and after value. Every
unexpected change is a warning, so `--strict` exits 3 on it. A component that
extracts no instances from the CR is reported as skipped, never as clean.

How to read it, once it exists:

- A changed path on the identity write that is `{}` or `null` for a field the
  CR did not carry: the engine is at fault, not the definition. Say so in the
  summary.
- A changed path on the identity write for a field the CR did carry: the read
  path and the write path do not address the same location, or the field sits
  in an object that is not the type the path expects. Fix the path, or use a
  read-only projection (An assignable path can still be the wrong one, above).
- The probe landed in fewer places than there are instances, or in another
  path: the spec path is a formula. Rewrite it as a plain path.
- A suspend or resume changed a path outside its actions: the action path is a
  formula. Rewrite it as a plain path.
- `invalid path against: ...` on a read-only projection that has no assignable
  alternative (step 3): expected; run without `--strict` and say so.

Where it belongs. In the CLI, as flags of `kli validate`:
`kli validate <definition> --workload <cr> --write --strict`, next to a
`--predict` flag for the read-side prediction, with the same exit codes as
karta-verify (0 success, 2 mismatch, 3 warnings). `kli` is the released binary,
so a definition author outside this repository gets the check without a
checkout, and the skill then names one command for steps 6 and 7. The
`hack/karta-verify` harness becomes a thin wrapper or goes away.

Order of work. The engine fix lands first; until then the identity write
reports the engine's own defects on every definition that carries a pod
template, and the check cannot separate a definition defect from an engine one
by itself. Then the CLI flags, with the probe and diff code as a library
package shared by `kli validate` and the harness. Then step 7 of `SKILL.md`
replaces its TODO with the command, and the Recorder and e2e table of
`troubleshooting.md` gains one row per warning above.

Until then. Write every spec, scale and suspend path as a plain path (step 4),
keep the controller-written CR from step 7 and the live CR from step 8 so the
check can run on them later, and state in the summary that the writes are
unproven. Scale paths are read today and would not be probed by this check
either, but they stay plain for the same reason. A count derived from several
fields (LeaderWorkerSet workers, JobSet replicas times parallelism) has no
plain path and is read-only.

The pod check. karta-verify never sees a pod. A selector that returns null on a
real pod maps the pod to nothing. A definition with no selectors (the CronJob
shape) relies on the owner chain (How a pod reaches a component above). To
list each pod's owners and which one is the controller:

```bash
kubectl get pod -l <label> -o json | jq -r '.items[] | .metadata.name + " " + ([.metadata.ownerReferences[]? | .kind + "/" + .name + "(controller=" + ((.controller // false) | tostring) + ")"] | join(","))'
```

## jq safety rules

Every path is validated statically. These constructs are rejected:

- Assignment and update operators (`=`, `|=`, `+=`, `-=`, and the rest).
- The `del` function.
- The recursive descent operator `..`.
- Unbounded builtins: `range`, `paths`, `recurse`, `walk`, `repeat`.

Rules for correct paths:

- Absolute, starting with `.`.
- Null-safe with `//` defaults for any field that may be absent, in status
  expressions only. Spec and scale paths stay plain and assignable.
- Evaluated against the correct resource (workload object vs pod manifest).

## Validation checklist

The validator (`go run ./hack/karta-verify --karta <definition.yaml>`) enforces
the first six items and that every jq expression parses and uses no rejected
construct. Check the rest by hand.

- All kinds use a full GVK (only `Pod` may omit the group).
- Root has a `statusDefinition` and no `ownerRef`.
- Every child has an `ownerRef` to an existing component; no ownership cycles.
- Component names are unique and non-empty.
- No component sets more than one spec pattern.
- `instanceIdPath` and `componentInstanceSelector` are both present or both absent.
- Pod selectors reference pod fields; selectors of the same kind are mutually exclusive across components.
- Status conditions and phases match the workload's real API.
- Every declared `conditionsDefinition` or `phaseDefinition` is referenced by at least one matcher, and every matcher has the definition it needs.
- Replica counts describe the component's level, siblings at the same level agree, and nested levels multiply by the parent count. A component whose spec carries no count has no `scaleDefinition`.
- Every component whose paths can return several values has `instanceIdPath` plus `componentInstanceSelector`, and all its paths share the same `select(...)` filter.
- When suspend does not change the phase, the `Running` and `Initializing` matchers AND a not-suspended expression so `Suspended` is exclusive.
- Autoscaling bounds were looked for, not assumed absent.
- Every `fragmentedPodSpecDefinition` path is assignable, or is documented as read-only.
- No redundant duplicate kinds in `additionalChildKinds` (duplicates are allowed only when needed for RBAC or owner traversal).
- Every gang-scheduling member names a defined component, and `groupByKeyPaths` and `filters` reference pod fields.
