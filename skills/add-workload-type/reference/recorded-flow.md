<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Operator source and recorded flow reference

The why, the commands, and the operator-specific examples behind step 1 (where
the facts come from) and step 8 (the recorded flow) of `SKILL.md`. The rules
themselves are in `SKILL.md`; this file explains and illustrates them.

Why a recording: the run in step 7 checks one object once, while the recording
checks every status frame the controller writes, and the replay suite checks it
again on every CI run.

## Reading the operator source

Where the facts come from when no cluster or user is at hand:

- The operator's release manifest (the `install.yaml` or Helm chart CRDs). It
  carries the CRD schemas, often in full.
- The API types in the operator repository, usually under `pkg/apis/` or
  `api/`. They name every status field and the condition type constants.
- The controller code that writes status. This is where the real condition
  types, reason strings, and pod labels come from. When the phase is computed
  from several inputs rather than set in one place, the function that returns
  the phase type (`func Calculate.*Phase` in Argo Rollouts) holds the order of
  checks. That order is what keeps the mapped statuses apart, which is why it
  goes into the mapping comments.

A real CR matters because a jq path can be structurally valid and still point
at a field no real object carries. Only step 7 shows that.

Pinning examples:

- Newest non-prerelease tag: `git ls-remote --tags --refs <repo-url> | sort -V -k2`.
- Helm-installed operators pin the chart version: kuberay `1.6.2`, milvus
  `1.3.7`.
- An operator already in `hack/e2e/global.env`, for example a new kind for
  Kubeflow, keeps its pin. A bump re-records every flow of that operator.
- A checkout already on another branch:
  `git fetch --depth 1 origin tag <tag>`, then `git show <tag>:<path>`.

Roles. The controller loop that walks the roles is the role list, for example
TFJob's `allTypes` in `UpdateJobStatus`. It includes deprecated aliases the API
still accepts (TFJob `Master`), which the documentation often omits.

### Kubernetes builtins

A builtin (apps, batch, core) has no operator repository. Read the status type
from the module cache:

```bash
cat "$(go list -m -f '{{.Dir}}' k8s.io/api)/<group>/<version>/types.go"
```

Fetch the controller at the Kubernetes version of `KIND_NODE_IMAGE` in
`hack/e2e/global.env`, for example:

```bash
curl -fsSL https://raw.githubusercontent.com/kubernetes/kubernetes/v1.34.0/pkg/controller/daemon/daemon_controller.go
```

The DaemonSet and StatefulSet controllers write counters and
`observedGeneration` only, which is why the grep for `Status.Conditions` comes
before any condition mapping. The helpers a counter comes from live under
`pkg/controller/<name>/util/`: the DaemonSet updated count comes from
`IsPodUpdated` in `daemon/util/daemonset_util.go`.

### Getting a CR from the test cluster

For a catalog definition, step 8 needs a cluster with the operator anyway, so
the step 1 CR comes from it. The hand-applied object lives in `default`, while
the recorder uses its own namespace, and the hand-applied pods compete with the
recording. That is why it is deleted before `make record-e2e`.

## Catalog entry and builder comment

The builder is the source; `make generate-samples` writes the YAML under
`docs/catalog/`. Either port the YAML validated in steps 6 and 7 into the
builder and then rerun those steps on the generated file, or write the builder
first and run steps 6 and 7 on the generated file only.

The builder comment holds what the code cannot show. An example of what an
action does not do: a pause that stops new work only and leaves running pods
alive, so a consumer that suspends to free capacity gets none back. Two
examples of what a write through an exposed path does not do. With
spark-operator's `PartialRestart` gate on, a write to the executor
`schedulerName`, affinity, or `priorityClassName` skips the rerun. Only
executors created later get the change. `spec.batchScheduler` overrides both
`schedulerName` paths at pod creation. A write through them does nothing while
it is set.

The 3 to 26 line bound counts every `//` line of the doc comment, the summary
sentence and the blank `//` line included. The recorded phase sequence is the
first thing to cut when the comment runs long, since the fixtures and flow
comments already show it.

The Pre-built Karta Definitions table in `README.md` lists operator-backed kinds
only. A builtin's `docs/catalog/` link covers it.

## Operator install under hack/e2e/

The operator directory takes the upstream project's short name as its chart or
release spells it (`kuberay`, `spark-operator`). The same string is
`Fixture.Operator`, the first `Label`, and the `version_of` case; a hyphen is
fine in all three.

### Builtins

A builtin needs no install scripts, pin, or `up.sh` entry. `up.sh` rejects any
name outside `ALL_WORKLOADS`, so `e2e-up` takes `WORKLOADS=none`. In the flow,
`Fixture.Operator` and the first `Label` are the same name (`deployment`,
`batch-job`) and the second label is `builtin`. `operatorVersion()` falls back
to the cluster's Kubernetes version, so the fixtures land under `v1.34.0` like
`deployment` and `statefulset`.

### A new kind for an installed operator

The training-operator runs with one `--enable-scheme=<kind>` per kind and
silently ignores the kinds not listed, so a new kind without the flag never
reconciles. The second smoke manifest sits next to `smoke.yaml`, as
`mpi-smoke.yaml` does.

### verify.sh and run_smoke

`run_smoke` drives `smoke.yaml` to a terminal or stable state. That proves the
controller, its RBAC, and the pod path end to end. It waits on one target, so a
workload that never settles and proves the pod path on an object it creates (a
KEDA ScaledJob, any Job generator) does not fit it. The hand-written sequence
mirrors `run_smoke`:

1. `apply_with_retry` the manifest.
2. `kubectl wait` for the controller condition on the workload.
3. `retry` until the child exists. `kubectl wait` with a label selector fails
   while nothing matches, which is why the retry comes first.
4. `kubectl wait` on the child by its owner label.
5. `kubectl delete`.

Each step's status goes into `rc` so the delete always runs.

Fully qualified resources for `run_smoke`: `<plural>.<group>/<name>-smoke`, for
example `clonesets.apps.kruise.io/kruise-smoke`. `kubectl wait` always resolves
that form. A bare `job/` resolves to `batch/v1`, so a Volcano Job needs
`jobs.batch.volcano.sh/<name>-smoke`, and a builtin Job uses
`job.batch/<name>`.

### Namespaces the controller watches

The recorder creates the flow object in a namespace it generates. A controller
or webhook limited to some namespaces (a `--namespaces` flag, a `jobNamespaces`
chart value, a webhook `namespaceSelector`) never reconciles it, and the run
times out with no frames. For a chart, the setting goes in a co-located
`values.yaml`, the way `grove/values.yaml` carries its chart overrides.

A chart can render per-namespace RBAC or a service account from the same list.
spark-operator's `spark.jobNamespaces` does: `""` alone widens the watch to
every namespace but renders the driver's service account and Role nowhere, so
the smoke run in `default` fails. Listing `["", "default"]` keeps both, and the
flows reach the recorder's namespace through the ClusterRole and `BeforeAll`
helper under RBAC and shared objects.

### RBAC and shared objects

When the upstream release manifest grants the workload's pods no permissions,
every pod ends in error until a role exists. Other cluster-scoped or shared
objects the flows need include a runtime, a class, or a template the CR
references by name.

Namespaced objects the pods need (a ServiceAccount, its RoleBinding, a Secret)
must live in the generated namespace. A chart that creates them per configured
namespace cannot reach it, which is why they are created in the flow's
`BeforeAll` through a helper in `test/e2e/flows/setup_test.go`, as
`ensureSecret` does.

### Pinning the install manifest

Preference order with examples:

1. GitHub release asset:
   `https://github.com/<org>/<repo>/releases/download/<tag>/<asset>` (jobset,
   lws, kserve).
2. Raw manifest at the tag:
   `https://raw.githubusercontent.com/<org>/<repo>/<tag>/<path>` (mpi-operator).
3. Kustomize base at the tag:
   `kubectl apply --server-side -k "github.com/<org>/<repo>/<path>?ref=<tag>"`
   (kubeflow).
4. Helm chart:
   `helm upgrade -i <release> <repo>/<chart> --version "${<NAME>_VERSION}"`
   (kuberay, milvus).

Tags are pinned as upstream spells them (`v2.21.0`); a Helm chart version has
no `v`. When the asset name drops the `v` (`keda-2.21.0.yaml`), `install.sh`
derives it with `${<NAME>_VERSION#v}`.

Project storage buckets often lag the newest tag, and a guessed URL that
returns 404 does not tell a missing asset from a wrong name. List the assets:

```bash
gh release view <tag> -R <org>/<repo> --json assets -q '.assets[].name'
```

then `curl -fsSIL` the listed asset.

A remote kustomize resource must be a directory, so a single upstream file (a
Namespace) is copied locally. When the upstream overlay or chart bundles an
operator the suite installs on its own (a JobSet), a co-located kustomization
lists the same upstream bases minus the bundled one, with the version pin put
in by `sed`.

A manifest that ships a one-shot Job generating webhook certs needs
`kubectl wait --for=condition=Complete job.batch/<init-job>` before
`rollout_wait` on the webhook. Otherwise the rollout times out on a pod
waiting for the secret.

### Namespace in the manifest

Some release manifests (Argo Rollouts) carry neither `kind: Namespace` nor
`namespace:`. A plain `kubectl apply -f` then lands the controller in `default`
while its ClusterRoleBinding names a ServiceAccount in the intended namespace.
Upstream documents creating the namespace and applying with `-n <ns>`.

A chart that renders its own Namespace owns it. `--create-namespace` would make
two owners. Turning the chart's Namespace off loses its labels, and a webhook
`namespaceSelector` can key on them: OpenKruise exempts
`control-plane: openkruise` from its fail-closed pod webhook, so without the
label its own manager pods must pass a webhook that is not up yet. Without
`-n`, Helm stores the release record in the kubeconfig's current namespace
(`default` on the kind cluster). That is fine, and `helm upgrade -i` stays
idempotent on a reused cluster.

A pod webhook with `failurePolicy: Fail` (grep the rendered webhooks) admits
every pod created after it in every namespace it does not exempt, the Karta
operator's included. The position in `ALL_WORKLOADS` does not make that safe,
since `up.sh` installs the Karta operator after every workload operator. What
does is `rollout_wait` on a deployment whose readiness probe covers the
webhook (the OpenKruise manager's `readyz` includes its webhook check). The
`install.sh` comment says so, and that the webhook stays in the pod admission
path of every later flow on that cluster.

### Images and install.sh details

`kind load` can fail on a multi-arch image under Docker Desktop's containerd
store (Shared helpers in `hack/e2e/README.md`). Passing the pinned upstream
reference as both arguments of `preload_image` lets the nodes pull the same
reference when the load fails. A local tag (`ray-e2e:local`) works only when
the load does. A smoke run does not stand in for the preload: its pods may all
land on one of the two workers, and a flow pod on the other then pulls the
image inside the 3 minute deadline.

A temp dir in `install.sh` is not `local` because the EXIT trap fires after
`main` returns, and under `set -u` it fails on an unbound variable.
`dynamo/install.sh` and `nim/install.sh` show the pattern.

### Iterating on an install

`make e2e-up` on a reused cluster re-runs every selected install and smoke,
dependencies included. `install.sh` and `verify.sh` are standalone, so they run
directly while fixing one operator:

```bash
bash hack/e2e/operators/<name>/install.sh
bash hack/e2e/operators/<name>/verify.sh
```

The final `make e2e-up` writes the `.installed-versions-<cluster>` entry the
recorder needs.

## Flow under test/e2e/flows/

### Fixture, labels, and where recordings land

`operatorVersion()` reads `hack/e2e/operators/.installed-versions-<cluster>`
keyed by `Fixture.Operator`. A name that does not match the operator directory
finds no entry and files the recording under the Kubernetes version.

Recordings land under
`test/e2e/recorded_data/<operator>/<version>/<kartaName>/<flow>.yaml`. The
fixtures already in the repository all sit under the Kubernetes version
(`v1.34.0`), operators included. New ones go under the operator's `version_of`
string from `up.sh`, which can be composite (`kubeflow` is `v1.9.0+mpiv0.8.2`).
A new kind for an operator with older fixtures therefore lands in a second
version directory next to `v1.34.0` until the older fixtures are re-recorded.

Naming examples: `Label("kubeflow", "mpijob")`; testdata directories `mpijob`,
`rayjob` (`pytorch` is a legacy name); single-kind operators `nim`, `milvus`.
A Volcano `Job` collides with the builtin, so it uses `vcjob` for the second
label, the testdata directory, the object names, and the root component name
(otherwise the lowercase kind, as in every catalog builder).

### Predicates

Helpers in `test/e2e/flows/predicates.go`: `CondTrue`, `CondFalse`,
`CondsFalse`, `CondNotTrue`, `CondStatus`, `CondReason`, `PhaseEq`, `PhaseAny`,
`IntAtLeast`, `IntEq`, `BoolTrue`, `Absent`, `AllOf`. `CondReason` requires
status True and `CondNotTrue` also matches Unknown, so a controller that
reports one condition and tells states apart by reason while it is Unknown
needs generic helpers (status plus reason, condition absent, any-of). The same
goes for a state reported two ways (any-of), a negation, or a count that
`omitempty` drops at zero (at-most, absent read as 0). `predicates.go` has none
of these; add each next to the existing helpers when a flow first needs it.

The flow files dot-import ginkgo and gomega, so a helper named `Not`, `And`,
`Or`, or `Equal` fails vet. Name them `Negate` and `AnyOf` when adding them.

Reusing a named predicate under another path: a CR that copies the JobSet
counters reads the same fields `JobsetRunning` reads. Give `JobsetRunning` the
path as a parameter, keep its callers on the old path, and add the
not-suspended guard with `AllOf`.

Counter comparisons (desired, ready, updated, observedGeneration) of one
workload get one named predicate per state, as the StatefulSet's
`FullyAvailable`, `ReplicasDegraded`, and `ReplicasInitializing` do.

### Proving predicates offline

A scratch test in `test/e2e/flows` checks every step 7 CR against the
predicates before the cluster run:

```go
func TestX(t *testing.T) {
	raw, _ := os.ReadFile("<scratch>/running.yaml")
	js, _ := yaml.YAMLToJSON(raw) // sigs.k8s.io/yaml
	var u unstructured.Unstructured // k8s.io/apimachinery/pkg/apis/meta/v1/unstructured
	_ = u.UnmarshalJSON(js)
	// assert exactly one predicate holds and that it names the status
	// karta-verify printed for this CR
}
```

A plain `yaml.Unmarshal` into a map yields float64, so `NestedInt64` reads 0
and every counter predicate passes or fails silently. Run it from `test/e2e`
with `GOWORK=off go test -run '^TestX$' ./flows`; the Ginkgo suite that needs a
cluster does not run. Delete the file afterwards.

### State order, Optional steps, revisits

Order only decides a frame where two predicates hold, so with exclusive
mappings it never changes the walk. The batch Job and JobSet flows put
`Suspended` first in case the Suspended condition lingers after a resume; the
Kubeflow flows keep it last because the training-operator flips it to False on
resume.

`Optional()` covers a short frame a watch can miss, or a fast pod that goes
from Initializing straight to Completed. The create response is recorded only
when it already reaches the terminal state. Otherwise the first frame is the
controller's first write, often a label or finalizer patch with no status. It
reads `Undefined` unless a no-status rule maps it, and the walk drops
`Undefined` frames. When a no-status rule exists and the first frame already
carries a phase or condition, no recording proves that rule.

When a dip is certain. A controller that writes status in the same reconcile
from the pod list it read before it created or deleted pods, and writes
whenever `observedGeneration` rises, always lands a frame with the new
generation and the old counts (CloneSet). That dip is required, not
`Optional()`. The stale frame right after the `ACTION` does not count, since
stale frames stay out of the walk; the certain frame is the one after it. Mark
a dip `Optional()` when the controller can skip the status write or the frame
can be shorter than a watch event. The StatefulSet flows' `Optional()` dips
predate this test.

A step whose state was declared earlier in the journey may be absent from the
walk, like an `Optional()` step (`order.go`). Consecutive frames of one state
collapse into one visit, and an `ACTION` is not a frame, so
`Running -> ACTION -> Running` and `Degraded -> ACTION -> Degraded -> Running`
both pass. A revisit copied from a sibling
flow documents a frame the target controller may never write. The PyTorchJob
flow declares an `Initializing` dip before the terminal state, yet the
training-operator sets `Running` to False in the same status write that sets
`Succeeded` or `Failed` (`filterOutCondition` in `pkg/util/status.go`), so a
training-operator kind with the guarded `Initializing` rule declares none.

Each flow run has a 3 minute deadline (`defaultTimeout` in
`test/e2e/recorder/recorder.go`). Slow image pulls or many pods on the two
workers are the usual reasons to raise it with `SetTimeout`.

### Actions and gates

Actions are merge patches in `test/e2e/flows/actions.go`. A generic helper
takes its inputs (an annotation patch takes the key and value), so suspend and
resume share it. A missing helper is added when a flow first needs it, next to
the existing ones: a `SuspendRunPolicy` with
`{"spec":{"runPolicy":{"suspend":true}}}` and `ActionSuspend` goes next to
`ResumeRunPolicy`. A new `ActionType` constant in `test/e2e/recorder/flow.go`
is only the recorded action name.

A pod annotation patch drives a rollout on any kind that rolls its template.
The first rollout flow adds `Annotate(key, value, path...)`, which builds the
nested merge patch, and `ActionRollout` (`"Rollout"`). The usual path is
`"spec", "template", "metadata", "annotations"`; a SparkApplication takes
`"spec", "driver", "annotations"`.

spark-operator resubmits on any spec change other than suspend and TTL. That is
the case for a flow that patches an exposed path from Running and walks the
rerun phases back to Running.

`Do()` fires on the first frame judged to be its state. When the predicate
reads a spec field (a `Suspended` that matches `spec.paused`), that can be the
pre-status frame. A gate on a field only the controller writes fixes it:

```go
recorder.Reaches(kartav1alpha1.SuspendedStatus).With(PhaseEq("Paused", "status", "phase")).Do(...)
```

The run ends only after every `With()` or `Do()` step was reached
(`actionSteps` in `recorder.go`). A gated step whose frame the watch misses
stalls the run until the timeout.

A CR created suspended never shows the controller turning Running off, and
that is the transition a consumer drives. Hence the suspend flow:
`Reaches(Running).Do(<suspend action>)` then `Reaches(Suspended)`.

The run ends on the first frame that matches the terminal state. When an
in-flight phase maps to the same status as the final one, the terminal gate
keeps both frames, for example:

```go
recorder.Reaches(kartav1alpha1.FailedStatus).With(PhaseEq("Aborted", "status", "state", "phase"))
```

When the state that fires a `Do()` is also the terminal state (Running, an
action, Running again), the run would end on the frame that fired the action,
and the order check still passes because a revisit may be absent. Gate the
terminal step on what the action changes and the controller echoes:
`observedGeneration` at least 2 after a template patch, the new ready count
after a scale.

Some controllers echo nothing. spark-operator's INVALIDATING resets the status,
so `executionAttempts` is 1 again and no field tells the rerun's RUNNING from
the first; a gate on `IntAtLeast(2, ...)` stalls until the deadline. The flow
then runs a short workload on to a state the controller writes only after the
action, `Completed` gated on `PhaseEq("COMPLETED", ...)`. A fixture whose last
event is the `ACTION` still reports `succeeded: true`; the walk check after
recording catches it.

### Stale frames

The recorder keeps frames written before the controller observed the current
spec, marked `staleObservedGeneration: true`, out of the order-checked walk and
the step actions. The replay still asserts them, so the stale frame after an
action proves a rule on `observedGeneration != generation`. This works only
when `status.observedGeneration` is an integer. A controller that stores it as
a string (Argo Rollouts writes `"1"`) or not at all (the training-operator
kinds) disables that guard, and a late write computed from the old spec lands
in the checked walk after a `Do()`. The `With()` gate is then the only
protection: it makes the action land after the controller's first real status,
so a late write of the same state stays in order. The API server echo of a
resume patch shows as a revisit,
`Suspended -> ACTION -> Suspended -> Initializing -> Running`, which is normal.

## Testdata manifests

`karta-e2e-tfjob-running` is the name shape. Older manifests that drop the flow
suffix or shorten the workload (`karta-e2e-pytorch`, `karta-e2e-mpi`,
`karta-e2e-sts`) predate the convention. The recorder overrides
`namespace: default` with its own generated one; `default` keeps the manifest
usable with a plain `kubectl apply` while debugging.

The pod stays alive well past the Running check so the state is stable when the
watch sees it. A workload that cannot sleep runs as long as its work: SparkPi
with 100000 slices on one 512m executor stayed RUNNING for about 100 seconds,
not the minutes assumed. That held only because each action fired on the first
RUNNING frame. The fixture timestamps or the controller log give the real
time.

The kind cluster has a tainted control-plane and two workers
(`hack/e2e/kind-config.yaml`). A readiness probe that fails keeps a pod unready
without a restart; an exec probe sees the container env. A crash loop instead
flips Ready on every restart and the counters never settle. The settled partial
frame can carry the same counters as a transient frame of a normal start, and
the controller writes nothing after it.

The pod, batch-job, deployment, and statefulset manifests show
`automountServiceAccountToken: false`.

## Recording

`hack/e2e/up.sh` writes the kubeconfig (`kind create cluster`, or
`kind export kubeconfig` on a reused cluster) and runs
`kubectl config use-context` against whatever `KUBECONFIG` resolves to. An
explicit `KUBECONFIG=<file>` wins. Otherwise a non-default `CLUSTER_NAME`
resolves to `~/.kube/kind-<name>.kubeconfig`, and the default (`karta-e2e`)
resolves to the shared `~/.kube/config`, which switches the shell's context
away from whatever was selected. Exporting the per-cluster file once also makes
hand-run `kubectl` commands hit the test cluster.

A webhook Service can refuse connections for a few seconds after
`rollout_wait`, until its endpoints propagate. `run_smoke` absorbs that through
`apply_with_retry`; a hand-run `kubectl apply --dry-run=server` fails with
`connection refused` and passes on a retry.

`WORKLOADS` on `record-e2e` selects flows by label, so the operator name on a
multi-kind operator re-records every sibling flow. `E2E_LABELS` takes a raw
Ginkgo label expression. `FLOW` is a Ginkgo focus regex: `FLOW=<name>` narrows
to one flow, and `FLOW="aborted|terminated"` re-records just those two and
leaves the other fixtures untouched. See Record in `test/e2e/README.md`.
`FLOW` fits a failure in one flow or its manifest. The `phases` and states in
every fixture of the kind come from the flow's predicates at record time, and
the predicates mirror the status rules, so a change to either re-records them
all.

Every flow file is an `Ordered` container: the first failing `It` skips every
later one in the file.

## Reading fixtures

Extract the controller-written CR for the step 7 rerun (the last frame of the
flow's terminal state; use `Initializing` for a flow that ends there):

```bash
yq '[.events[] | select(.state == "Running")] | .[-1].object' <fixture> > <scratch>/cr.yaml
<scratch>/verify --karta <definition.yaml> --workload <scratch>/cr.yaml --strict > <scratch>/out.txt; echo $?
```

Use the strictness step 7 settled on. A fragmented spec with no container path
runs without `--strict` and exits 0; with it, exit 3 with only the
`no containers` warnings is the expected result.

Outcome, first frame, settled conditions, and the walk:

```bash
yq '.result.succeeded' <fixture>
yq '.events[0].object.status' <fixture>
yq '.events[-1].object.status.conditions' <fixture>
yq '[.events[] | (.state // "ACTION") + "=" + ((.phases // []) | join(",")) + ((.staleObservedGeneration // false) | tostring | sub("true", "(stale)") | sub("false", ""))] | join(" -> ")' <fixture>
```

Frame dump, to see when each condition appears and drops:

```bash
yq -o json -I0 '.events[] | {"state": .state, "conds": [.object.status.conditions // [] | .[] | .type + "=" + .status + "/" + (.reason // "-")]}' <fixture>
```

yq v4 needs the quoted keys. Slice with `| head -N`, since yq rejects
`.events[0:9] | {...}`.

In the walk, `Running=Initializing,Running` means two predicates matched the
frame (`judge` in `test/e2e/recorder/flow.go`); Karta is not involved. Since
the predicates mirror the rules, run karta-verify on that frame to confirm the
definition overlaps too (step 5). Older fixtures carry such frames; a new one
must not. When
the controller stores `observedGeneration` as an integer, the first frame after
an `ACTION` usually carries `(stale)`; that frame proves the
`observedGeneration != generation` rule.

Claims in the builder comment are checked against these frames: which
conditions are set, which are never cleared, which defaults the webhook
stores. A fixture that starts from zero proves nothing about a reset. The
fixtures hold only the CR, which is why claims about pods (owner references,
labels) are traced to the code and checked on the hand-applied pod.

The recorder deletes each flow object after its run, so no recorded pod is
left. That is why one testdata manifest is applied by hand before
`make e2e-down`. A pod that another pod creates appears later, so loop on
`kubectl get` until it exists before `kubectl wait`.

## Before make check

`make lint-shell`, `make test-replay`, and `make verify-recordings` take
seconds. `make check` does not vet or lint the `test/e2e` module, hence the
separate `GOWORK=off go vet ./...` and `gofmt -l .` there.

`make check` first downloads the pinned tools missing from `bin/`
(golangci-lint, goreleaser, controller-gen, go-licence-detector, setup-envtest
and its control-plane binaries) and prints nothing while it does, often for
minutes.

The `validate` target requires a clean tree. It reports untracked files as
`generated files or module manifests are stale or untracked`, which reads like
a broken generator but is not.

`make test-replay` prints only `ok`. To see the new fixtures replayed:

```bash
cd test/e2e && GOWORK=off go test -count=1 -v ./replay_tests/... -args -ginkgo.v
```

and grep the output for the `kartaName`.
