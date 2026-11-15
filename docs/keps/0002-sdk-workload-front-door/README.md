<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# KEP-0002: Workload front door for SDK consumers

- Status: provisional
- Authors: @rogirun
- Issue: https://github.com/dsx-ai-factory/workload-map/issues/341
- Created: 2026-11-15
- Last updated: 2026-11-15

## Summary

This KEP adds a Go package, `karta/sdk/pkg/karta`, that gives SDK consumers one `Workload` value. A consumer uses it to read a workload and to change its pod templates. The same consumer code works for every definition style, with no per-type code.

The main write call is `MutatePodTemplates`. The caller edits a plain `corev1.PodTemplateSpec` in a callback, and Karta writes each changed field to the CR path that the definition declares for it.

## Motivation

Today a consumer must know how each definition describes its pods, and must call a different API for each style. That breaks the promise of no per-type code.

- To read, a consumer builds a `resource.ComponentFactory`, then a tree, and walks it with its own helper.
- To write, a consumer picks one of four methods on `resource.Component`: `UpdatePodTemplateSpec`, `UpdatePodSpec`, `UpdatePodMetadata` or `UpdateFragmentedPodSpec`. The right one depends on the definition: `podTemplateSpecPath`, `podSpecPath` plus `metadataPath`, or `fragmentedPodSpecDefinition`.
- Each update method replaces the whole value at its path, for all instances.
- A fragmented definition maps only some pod fields. Nothing tells the caller which fields it can change, for example tolerations.
- The public API exposes internals: `pkg/jq`, `pkg/resource` and `pkg/tree`.

### Goals

- One read call that returns every component and instance, with one pod shape for every definition style.
- One write call that changes pod templates for every definition style, on all instances or on selected ones.
- Writes that apply all or nothing, and an error that names a field the definition cannot write.
- A list of writable fields per component, so a caller can check before it writes.
- Suspend and resume through the same value.
- Remove `pkg/jq` from the public API.

### Non-goals

- Changing running pods. Karta changes the pod template in the CR, and the controller of the workload creates the pods.
- Mapping a running pod back to its component and instance (`LocatePod`). This is a follow-up.
- Moving the CLI, karta-wasm, the Headlamp plugin or the exporter to the new API in the same change.
- Changes to the Karta CRD.

## Proposal

```go
definition, err := karta.Compile(kartaDef) // once per kind
w, err := definition.Load(obj)             // works on a deep copy of obj
snapshot, err := w.Read(ctx)               // components, instances, status, scale, pod templates

changed, err := w.MutatePodTemplates(ctx, "worker", func(id string, pod *corev1.PodTemplateSpec) error {
    pod.Spec.SchedulerName = "kai-scheduler"
    return nil
}, karta.OnInstances("workers"))
if changed {
    err = c.Update(ctx, w.Object())
}
```

### API

The package has 2 functions, 4 interfaces, 5 structs, 4 type aliases and 12 sentinel errors. A consumer goes from a `Definition` to a `Workload` to a `Snapshot`. A generated mock in `karta/kartamock` lets consumers test their error paths.

#### Entry points

```go
// Compile validates and indexes a definition. Reuse the result for every
// object of that kind.
func Compile(def *v1alpha1.Karta) (Definition, error)

// Load is Compile plus Definition.Load in one call, for a single object.
func Load(def *v1alpha1.Karta, obj runtime.Object) (Workload, error)
```

#### Definition

A validated and indexed definition. It is immutable and safe to share across goroutines.

```go
type Definition interface {
    // Karta returns a deep copy of the source definition.
    Karta() *v1alpha1.Karta
    // Components lists every component, the root first.
    Components() []ComponentInfo
    // Load deep-copies the object and makes sure that its apiVersion, kind,
    // name and root kind match the definition.
    Load(obj runtime.Object) (Workload, error)
}
```

#### Workload, Reader and Mutator

A `Workload` holds one object in memory. It is not safe for concurrent use. Every `Mutator` call is all or nothing: on error the object is unchanged.

```go
type Workload interface {
    Reader
    Mutator
    Definition() Definition
}

type Reader interface {
    // Status returns the root status, or nil when the definition has none.
    Status(ctx context.Context) (*Status, error)
    // Read returns every component, the root included, with its instances.
    Read(ctx context.Context) (*Snapshot, error)
    // Component reads one component.
    Component(ctx context.Context, name string) (*Component, error)
}

type Mutator interface {
    // MutatePodTemplates runs fn on the pod template of every targeted
    // instance and writes back what fn changed. It reports whether the
    // object changed.
    MutatePodTemplates(ctx context.Context, component string, fn PodTemplateMutateFunc, opts ...MutateOption) (bool, error)
    // CanSuspend reports whether the definition declares suspend actions.
    CanSuspend() bool
    // Suspend and Resume apply the suspend or resume actions, root first.
    Suspend(ctx context.Context) error
    Resume(ctx context.Context) error
    // Object returns a deep copy of the current object, ready for Update.
    Object() *unstructured.Unstructured
    // Modified reports whether any call changed the object since Load.
    Modified() bool
}
```

#### Snapshot

The result of `Read`. All the read structs are plain data, so tests can build them as literals.

```go
type Snapshot struct {
    // Status is the root status, nil without a status definition.
    Status *Status
    // Components holds every component, the root first.
    Components []Component
}

// Get returns the named component.
func (s *Snapshot) Get(name string) (*Component, bool)

// Children returns the direct children of the named component.
func (s *Snapshot) Children(name string) []Component
```

#### Component and ComponentInfo

`ComponentInfo` is the static description of a component, from the definition. `Component` adds the read state.

```go
type Component struct {
    ComponentInfo
    Status *Status
    // Instances are sorted by ID. A single-instance component has one
    // instance with an empty ID.
    Instances []Instance
}

type ComponentInfo struct {
    Name string
    // Parent is empty for the root component.
    Parent string
    // Kind is nil for logical grouping components.
    Kind          *schema.GroupVersionKind
    PodSource     PodSource
    MultiInstance bool
    Suspendable   bool
    // Writable lists the pod template fields MutatePodTemplates can write,
    // as dotted paths. "metadata" or "spec" means every field below it.
    Writable []string
    // Selector finds the running pods of the component.
    Selector *v1alpha1.PodSelector
}

// PodSource names how the definition locates the pods of a component.
type PodSource string

const (
    PodSourceNone      PodSource = ""
    PodSourceTemplate  PodSource = "PodTemplateSpec"
    PodSourceSpec      PodSource = "PodSpec"
    PodSourceFragments PodSource = "Fragments"
)
```

#### Instance

```go
type Instance struct {
    // ID is empty for single-instance components.
    ID string
    // Present is false when the definition describes the instance but the
    // object does not hold it, for example an optional component.
    Present bool
    Scale   *Scale
    // Pod is the pod template, whichever way the definition locates it.
    // Nil without a pod definition or when not present.
    Pod *corev1.PodTemplateSpec
    // Fragments holds the raw fragment values for fragmented definitions.
    Fragments *PodFragments
}
```

`Pod` has one shape for every definition style: `podTemplateSpecPath`, `podSpecPath` plus `metadataPath`, or `fragmentedPodSpecDefinition`.

#### Callback and options

```go
// PodTemplateMutateFunc edits the pod template of one instance in place.
// Returning an error aborts the call.
type PodTemplateMutateFunc func(instanceID string, pod *corev1.PodTemplateSpec) error

// MutateOption configures one MutatePodTemplates call.
type MutateOption func(*mutateOptions)

// OnInstances limits the call to the given instance IDs. The default is every
// instance. An unknown ID fails before anything is written.
func OnInstances(ids ...string) MutateOption

// SkipAbsent skips targeted instances that are absent from the object,
// instead of failing with ErrComponentAbsent.
func SkipAbsent() MutateOption
```

#### Aliases

These are aliases of the existing `pkg/resource` types, so current code that uses them keeps working.

```go
type Status = resource.Status
type Condition = resource.Condition
type Scale = resource.Scale
type PodFragments = resource.FragmentedPodSpec
```

#### Errors

Every error from the package wraps one sentinel, so callers use `errors.Is`.

| Sentinel | Returned when |
|---|---|
| `ErrInvalidDefinition` | `Compile` rejects the definition. |
| `ErrInvalidObject` | `Load` gets an object of the wrong kind, or without apiVersion, kind, or name or generateName. |
| `ErrComponentNotFound` | The component name is not in the definition. |
| `ErrInstanceNotFound` | `OnInstances` names an unknown instance. |
| `ErrComponentAbsent` | A targeted instance is not in the object, without `SkipAbsent`. |
| `ErrNoPodDefinition` | The component has no pod definition. |
| `ErrFieldNotRoutable` | A changed field has no CR path. Wrapped in a `FieldError`. |
| `ErrPathNotAssignable` | A definition path cannot be written in this object: it crosses a non-object, an index is out of range, or it matches nothing. Wrapped in a `FieldError`. |
| `ErrInstanceMismatch` | A path returns a different number of results than the component has instances. |
| `ErrVerificationFailed` | The read-back does not match what the callback wrote. |
| `ErrSuspendNotSupported` | `Suspend` or `Resume` on a definition with no suspend actions. |
| `ErrEvaluation` | A jq expression fails to evaluate. |

`FieldError` names where a write failed:

```go
type FieldError struct {
    Component string
    Instance  string
    // Field is a dotted path, for example "spec.tolerations".
    Field string
    // Err is the sentinel. Error() and Unwrap() make errors.Is work.
    Err error
}
```

### How MutatePodTemplates works

1. It builds a `corev1.PodTemplateSpec` for each instance from the paths in the definition.
2. It calls the callback, then compares the pod template before and after.
3. It writes each changed field to the CR path that the definition declares for it. Write paths come from gojq (`[path(expr)]`), so any path that jq can address is writable. A pod spec and its metadata can share one CR path. Then a write at the root of that path merges field by field, so CR fields outside the pod stay.
4. It reads every instance back and compares it with what the callback left. On a mismatch it returns `ErrVerificationFailed`.

Every call works on a copy of the object. On any error the object is unchanged. If the callback changes nothing, the call returns false and writes nothing.

The template that the callback leaves is the template written. The caller owns these semantics:

- A field that the callback removes or clears is deleted from the CR. Replacing `pod.Spec` wholesale deletes every field that is not set again.
- An empty string, nil or empty map means unset. A label or annotation with an empty value cannot be written.
- If the names in a list of named objects (containers, volumes, env) match in order, the list changes entry by entry. Any other changed list is replaced whole, and fields that the CRD adds inside its entries, outside corev1, are lost.
- A changed field that has no CR path fails the whole call with `ErrFieldNotRoutable`. `ComponentInfo.Writable` lists the writable fields up front.

### Changes outside the new package

- Move `pkg/jq` to `internal/jq`. This removes `jq` from the public API.
- Fix `pkg/resource`: a single-instance component whose path uses `select(...)` and matches nothing is absent, not an error. Today every LLMInferenceService recording with workers fails with "instance ids count (1) does not match results count (0)". `tree.Build` on `main` fails the same way.
- Update the root README, the quickstart and the controller-runtime example to use the new API. The root README shows `factory.GetObject()`, which does not exist.

### Delivery

Four PRs, in this order. Only the third adds real logic.

| # | Change | Estimated size |
|---|---|---|
| 1 | `refactor(karta)`: move `pkg/jq` to `internal/jq` | 19 files, +10 / -10 |
| 2 | `fix(resource)`: the `select` fix and a regression test | 2 files, +19 |
| 3 | `feat(karta)`: the new package, unit tests, mock, and a replay test over all recordings | 10 files, about +2,160 |
| 4 | `docs(examples)`: README and examples | 5 files, +196 / -255 |

The new package has about 1,170 lines of code without tests. A prototype of all four changes exists on the branch `rogirun:feat/karta-front-door-proposal`.

## Compatibility and migration

The change is additive, with one exception.

- `pkg/jq` moves to `internal/jq`. An external Go import of `pkg/jq` stops compiling. No module in this repository imports it.
- `pkg/resource` and `pkg/tree` keep their API. `Status`, `Condition`, `Scale` and `PodFragments` in the new package are aliases of the `pkg/resource` types, so code that uses them keeps working.
- The CLI, the operator, the exporter and karta-wasm need no changes.
- The `select` fix changes behavior that users see: LLMInferenceService workloads with workers failed to read before, and now show in `kli`, the Headlamp plugin and the exporter.
- The Karta CRD does not change, so existing definitions work as they are.

A follow-up replaces `tree.WorkloadTree` with `Snapshot`. That one is a breaking change to `pkg/tree` and to the JSON that karta-wasm returns, so it gets its own issue. See Risks and alternatives.

## Test plan

- Unit tests in `pkg/karta` cover instance targeting, deletes, and all or nothing on a callback error. They also cover unroutable fields, absent components, CR fields outside the pod, a shared spec and metadata path, and suspend.
- A replay test runs on every recorded state of every catalog definition. On each component with pods, it sets the scheduler name, a label and a toleration. Writable fields must read back. Other fields must fail with `ErrFieldNotRoutable` and leave the object unchanged. The CR status and labels must not change.
- A regression test in `pkg/resource` covers a single-instance component that selects nothing.
- `make check` runs for the library, the CLI, the operator, the exporter and karta-wasm.
- The quickstart example runs on JobSet and LeaderWorkerSet.

## Risks and alternatives

### Risks

| Risk | Effect | Mitigation |
|---|---|---|
| A callback clears a field, or replaces `pod.Spec` wholesale | Every field it drops is deleted from the CR | Caller's responsibility (decision 2). The godoc and README say that the template the callback leaves is the template written |
| An empty string, nil or empty map means unset | A label or annotation with an empty value cannot be written | Known limitation, stated in the godoc |
| A changed list whose entries have no names (tolerations, topology spread) is replaced whole | Fields that a CRD adds inside those entries, outside corev1, are lost | Known limitation, stated in the godoc. No catalog definition hits it today |
| A definition maps a pod spec but no metadata (LLMInferenceService main) | A label change cannot be written | `ErrFieldNotRoutable`, and `Writable` shows it up front |
| Two definition paths alias each other, or a CR stores a field in a lossy form | A write lands in the wrong place | The read-back check returns `ErrVerificationFailed` and leaves the object unchanged |

### Design A: UpdatePodTemplate with PodPatch

Issue #341 and the closed PR #345 proposed design A. `UpdatePodTemplate` takes a typed `PodPatch` or a raw `Patch`, `WithInstances` selects instances, and a custom parser decides which jq paths are writable. This KEP proposes design B.

| Topic | Design A: `PodPatch` | Design B: `MutatePodTemplates` |
|---|---|---|
| Caller writes | A patch type with a subset of pod fields | Plain Go on `corev1.PodTemplateSpec` |
| New pod fields | Need a new `PodPatch` field each time | Work with no API change |
| Write paths | Own parser for writable jq expressions | `[path(expr)]` from gojq |
| Deleting a field | Not possible: null never deletes | Possible: clearing a field deletes it in the CR |
| Partial failure | Works on a copy of the object | Works on a copy, reads every instance back, then commits |
| Unmapped field | Ignored or rejected per patch field | `ErrFieldNotRoutable` names the field, and `Writable` lists fields up front |
| Code without tests | About 1,530 lines | About 1,170 lines |

### Return tree.WorkloadTree from Read

`Read` returns a `Snapshot` and not a `tree.WorkloadTree`. The tree lacks what the front door needs: one pod shape for every definition style, and the fields that `MutatePodTemplates` relies on.

| | `tree.WorkloadTree` | `karta.Snapshot` |
|---|---|---|
| Shape | Nested: component, instance, child components | Flat list. Each component names its parent, and `Children(name)` walks down |
| Child components | Copied under every parent instance, and every copy is identical | Listed once |
| Root component | Hidden: only its children and its matched phases appear | Included like any other component |
| Status | Root only, and only `Phases` | Every component: phase, conditions and matched statuses |
| Pod | `ExtractedInstance` holds a `PodTemplateSpec`, or a `PodSpec` plus `Metadata`, or fragments | One `Pod *corev1.PodTemplateSpec` for every style, plus `Fragments` |
| What the caller can do | Missing | `Writable`, `PodSource`, `Suspendable`, `Selector`, `Present` |
| `ReplicaKey` | A field that the builder never fills | Not present |
| Instance ID | `*string`, nil for a single instance | `string`, empty for a single instance |

A `Snapshot` holds everything the tree holds. A follow-up replaces the tree with it, so there is one read model:

| Consumer | Uses from the tree | Work to move |
|---|---|---|
| Headlamp plugin, through karta-wasm | Only `Status.Phases` | Small. karta-wasm returns the snapshot or the root status, and the plugin reads `matchedStatuses` |
| CLI `view.go` | `Status.Phases` | Small |
| CLI `describe.go` | Walks the tree. Reads `InstanceKey`, `ReplicaKey` and `ExtractedInstance`, and rebuilds the root instances that the tree hides | Medium. Walk with `Snapshot.Children`, and read `Instance.Pod`. The output must not change |
| `hack/karta-verify` | Walks the tree to print it | Small |

The follow-up has three costs. External Go code that imports `pkg/tree` breaks, so `pkg/tree` stays deprecated for one release before it is deleted. The JSON from karta-wasm changes shape, so the snapshot types need deliberate JSON tags, and karta-wasm and the plugin change in one PR. A replay test compares the old and new `kli describe` output on every recording.

### Open decisions

| # | Decision | Options | Proposed |
|---|---|---|---|
| 1 | Design | A: `UpdatePodTemplate` with `PodPatch`. B: `MutatePodTemplates` with a callback | B |
| 2 | Deleting fields | Clearing a field in the callback deletes it in the CR, or null never deletes | Allow deletes. The caller owns the result, and the godoc and README say so |
| 3 | List merge | Match entries by name in order, and replace any other changed list. Or always replace | Match by name, so an image change keeps CR-only fields in the container |
| 4 | Delivery | One PR with four commits, or four PRs | Four PRs. The `jq` move and the `select` fix can merge first |

### Follow-ups

- `LocatePod`: map a running pod back to its component and instance.
- Move the DSX workload exporter to the new API.
- Replace `tree.WorkloadTree` with `Snapshot`, as described above.
- Move `pkg/resource` and `pkg/instructions` under `internal`, after the CLI and karta-wasm use the new API.

## History

- 2026-11-15: Proposal opened.
