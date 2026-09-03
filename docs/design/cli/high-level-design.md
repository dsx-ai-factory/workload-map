# Karta CLI - High-Level Design

## Background

Karta is a CRD-based Go library that provides a universal abstraction for any Kubernetes workload type. Using JQ-based Karta definitions, it can extract structure (components, hierarchy), status (phase, conditions), scaling (replicas, min/max), and pod specs from any CRD - PyTorchJob, RayCluster, JobSet, KServe, and others.

We believe Karta's abstraction layer can serve as the foundation for a tool that brings visibility to the k8s workload space - a CLI/web/MCP that understands any workload type out of the box for a live cluster.

### What exists today for workload visibility

- [**kubectl**](https://kubernetes.io/docs/reference/kubectl/) - works per resource type. `kubectl get pytorchjob` shows the CRD, `kubectl get pods` shows a flat list. No connection between them. Each CRD type requires different knowledge to inspect.
- [**k9s**](https://k9scli.io/) / [**Lens**](https://k8slens.dev/) - general-purpose cluster browsers. They show resources by kind but have no concept of a "workload" as a composed unit of multiple resource types.
- [**kubectl-tree**](https://github.com/ahmetb/kubectl-tree) - shows the ownership chain (Pod → ReplicaSet → Deployment) but has no semantic understanding. It can't tell you which pod is a master and which is a worker. No phase or status parsing - it doesn't understand the workload's lifecycle state. No scaling awareness - it shows individual objects but doesn't understand replicas, min/max, or autoscaling.

### What's missing

No existing tool provides **workload-aware** visibility across different CRD types. The questions that keep coming up and we think that Karta can answer:

- What workloads are running in my cluster, across all types, and what's their status?
- What does the structure of a workload look like - which pods play which role (master, worker, head)?
- How many GPUs does a workload consume across all its components?
- How are the components of my inference pipeline performing - which are scaling, which are bottlenecked?

Every workload type (PyTorchJob, RayCluster, JobSet, KServe) structures this information differently. Today, answering these questions requires per-CRD knowledge and manual inspection.

Karta already has the abstraction layer to read any workload type uniformly. A CLI (and later web/MCP) can build on this to provide the missing visibility.

### Side-by-side: kubectl-tree vs karta

Consider a `DynamoGraphDeployment` with Frontend, PrefillWorker, and DecodeWorker roles.

**kubectl-tree** - ownership walk:

```shell
$ kubectl tree dynamographdeployment my-pipeline
NAMESPACE  NAME                                               READY
ml-team    DynamoGraphDeployment/my-pipeline                  -
ml-team    ├─DynamoComponentDeployment/my-pipeline-frontend   -
ml-team    │ └─LeaderWorkerSet/my-pipeline-frontend           -
ml-team    │   ├─Pod/my-pipeline-frontend-0-0                 True
ml-team    │   └─Pod/my-pipeline-frontend-0-1                 True
ml-team    ├─DynamoComponentDeployment/my-pipeline-prefill    -
ml-team    │ └─LeaderWorkerSet/my-pipeline-prefill            -
ml-team    │   ├─Pod/my-pipeline-prefill-0-0                  True
...
```

**karta describe** - semantic walk:

```shell
$ karta describe dynamographdeployment/my-pipeline
DynamoGraphDeployment/my-pipeline   namespace: ml-team   definition: nvidia-com-dynamographdeployment-v1alpha1 (catalog)   age: 1h

`-- service
    |-- Frontend        2/2 ready   gpu: 2    node-01,node-02
    |-- PrefillWorker   3/4 ready   gpu: 24   node-03,node-04,node-05
    `-- DecodeWorker    4/4 ready   gpu: 16   node-07,node-08,node-09,node-10

Phase: Running
```

What Karta brings on top, all derived from the Karta definition:

- **Semantic role names** - Frontend / PrefillWorker / DecodeWorker come from the label mapping declared in the Karta YAML.
- **Pods grouped by their role, not by their owner** - pods playing different roles are shown as separate groups even when they share the same owning resource.
- **Normalized phase** - `Running` has the same meaning across PyTorchJob, RayCluster, Dynamo, etc.
- **Desired vs ready replicas** - `3/4 ready` shows how many pods are ready against the scale target, per role.
- **Collapsed plumbing** - intermediate objects (DynamoComponentDeployment, LeaderWorkerSet) are optionally hidden to reduce noise while keeping the semantic hierarchy visible.

**kubectl-tree walks ownership. Karta walks semantics.**

---

## Karta definition resolution

We want the CLI to be useful immediately - without requiring users to install Karta CRDs into their cluster first. If someone has PyTorchJobs running, `karta get pytorchjob` should find them out of the box.

To make this work, the CLI would ship with **community definitions** - the Karta definitions from `docs/examples/` embedded in the binary. These are maintained by the open-source community, cover standard frameworks (PyTorchJob, RayCluster, JobSet, KServe, etc.), and are tested against each framework's stable API. They update automatically when you upgrade the CLI.

For users who need more - custom CRDs, org-specific status mappings, different pod selectors - they can apply Karta definitions to the cluster as CRDs. These **cluster definitions** take precedence over community ones when both target the same workload type.

The `ORIGIN` column in `karta definition list` shows where each definition came from (`community` or `cluster`).

---

## High-level design

### Command structure

Verb-first, following the kubectl grammar (`karta VERB TYPE[/NAME]`):

```shell
karta get <type>              # operational: what workloads of this type are running
karta describe <type>/<name>  # operational: one workload in full
karta definitions             # meta: what workload types does Karta understand
```

### Namespace behavior

- Karta definitions are **cluster-scoped**. Community definitions (embedded in the binary) and cluster definitions (CRDs) both apply across all namespaces.
- Workloads are **namespaced**. All workload commands follow standard kubectl conventions:
  - `-n <namespace>` targets a specific namespace
  - `-A, --all-namespaces` scans all namespaces
  - Default is the namespace from the current kubeconfig context
- `karta describe <type>/<name>` operates on a single workload. The namespace is taken from `-n` or the current context.
- Pod discovery for tree building happens in the workload's namespace - pods outside it are not considered.

### `karta get`

List workloads of a type, resolved through the Karta definition that covers it. The type is a positional token, following the verb-first kubectl grammar (`karta VERB TYPE[/NAME]`).

```shell
$ karta get pytorchjob -n ml-team
NAME              NAMESPACE   PHASE       COMPONENTS                               GPU   AGE
llama-finetune    ml-team     Running     master(1), worker(4)                     33    2h
sweep-7           ml-team     Degraded    master(1), worker(8)                     64    30m
```

`karta get jobset/preprocess` fetches one row. Type matching is lenient: case-insensitive, singular or plural, and kubectl short names all resolve.

Key flags:
- `--phase <Running|Failed|...>` - filter by normalized phase, repeatable
- `-l, --selector <labels>` - filter by labels (same syntax as `kubectl`)
- `--chunk-size <n>` - API list page size
- `-o <table|wide|json|yaml>` - output format. `-o json` emits the typed `WorkloadView` for scripting and MCP consumers, always under an `items` key alongside a `count`, so consumers never branch on shape.

Rows are ordered newest first. Counts and GPU are read from the workload spec, so listing costs one API call per type and no pod reads; `wide` adds the ORIGIN of the resolving definition. A NODES column needs live pod data and follows with `karta describe`, which has to build pod matching anyway.

The cross-type view - every workload Karta covers, in one table, which is the view no native tool provides - is the next step for this command. It needs a rule for which objects are workload roots: the catalog covers Deployment and Pod but not ReplicaSet, so a single-level owner check either floods the table with a Deployment's pods or hides workloads whose controller Karta does not cover.

Clusters can be huge - thousands of workloads across many types is a realistic scenario for our users. `karta get` pages through large result sets so per-request API pressure stays bounded. Note that `--chunk-size` bounds neither total memory nor time-to-first-row: every page is retained, and the default ordering is global, so the whole result is sorted before the first row is rendered.

### `karta describe <type>/<name>`

The full picture for one workload: the semantic tree with live pods, the normalized phase, and the per-component resource breakdown. It supersedes the separate tree, status and resources commands sketched earlier in this design - they answered three parts of one question, and a reader who ran one usually wanted the others - and pairs with `karta get` as the kubectl-style get/describe split.

```shell
$ karta describe pytorchjob/llama-finetune -n ml-team
PyTorchJob/llama-finetune   namespace: ml-team   definition: kubeflow-org-pytorchjob-v1 (catalog)   age: 2h

|-- master                        1/1 ready                 gpu: 1    node-01
|   `-- llama-finetune-master-0   Running                   gpu: 1    node-01
`-- worker                        3/4 ready                 gpu: 32   node-02,node-03,node-04
    |-- llama-finetune-worker-0   Running                   gpu: 8    node-02
    |-- llama-finetune-worker-1   Running                   gpu: 8    node-03
    |-- llama-finetune-worker-2   Running                   gpu: 8    node-04
    `-- llama-finetune-worker-3   Pending (Unschedulable)   gpu: 8    <none>

Phase: Running

Resources:
COMPONENT   REPLICAS   GPU   CPU   MEMORY
master      1          1     4     16Gi
worker      4          32    16    64Gi
TOTAL       5          33    20    80Gi
```

Key flags:
- `-n, --namespace` - target namespace, kubectl semantics
- `-f, --file <manifest>` - describe a manifest that has not been submitted; `-` reads stdin
- `--pod-limit <n>` - maximum pod rows per component; the default renders every pod
- `-o <table|json|yaml>` - output format. `wide` is rejected: one workload has no extra columns to widen into

Every section renders from a single `WorkloadView`, so the human output and the machine output cannot drift. `-o json` emits that view directly, without the `items`/`count` envelope the list commands carry: an envelope says nothing about a single subject and costs a consumer an `items[0]` hop. Values are typed - `replicas` is `{desired, current, ready}` numbers, never a `"3/4"` a consumer re-parses; an unscheduled pod carries a null node, not an empty string. The JSON shape is deliberately unstable until the CLI reaches v1.

By default every pod row renders, the way `kubectl-tree` renders every descendant: the hidden pod is the one a reader most needs. `--pod-limit` opts into truncation, sorting unhealthy pods first so a failing pod is never what gets cut, and reporting what it hid.

**Pod attribution.** A Karta `PodSelector` says which component type a pod plays, never which workload it belongs to: matching on the selector alone would claim every PyTorchJob worker in the namespace. So pods are scoped by ownership first - each candidate pod's controller owner-reference chain is walked, fetching intermediates, until it reaches the workload root - and only then placed by the definition's selectors. Intermediate fetches are cached hit and miss alike, so siblings sharing a ReplicaSet cost one read.

**File mode.** `karta describe -f jobset.yaml` builds the same view from a manifest alone, resolving the definition by the manifest's GVK, with no cluster and no pods. The structure and the desired scale are real; everything live is left empty and the output says so. Useful for checking a desired topology before submitting it.

**Failures.** Each condition a caller can act on differently gets its own exit code: a missing workload, a type no definition covers, a usage error, and a cluster or auth failure are told apart without parsing a message. The no-definition case is emitted as a parseable object on stdout under a machine format, because an agent's fallback there - inspect the object raw, or author a definition - is unlike its fallback for any other failure.

A later phase accepts a bare `<name>` and searches across every known kind, erroring rather than guessing when more than one kind matches.

### `karta definition list` / `karta definition describe <name>` / `karta definition validate <file>`

Inspect the Karta definitions the CLI knows about - both community and cluster-provided.

```shell
$ karta definition list
NAME                              KIND               ORIGIN     COMPONENTS
kubeflow-org-pytorchjob-v1        PyTorchJob         community    pytorchjob, master, worker
ray-io-raycluster-v1              RayCluster         community    raycluster, head, worker
my-custom-workload                CustomJob          cluster    customjob, runner

$ karta definition describe kubeflow-org-pytorchjob-v1
(shows structure tree, status mappings, gang scheduling config)

$ karta definition validate ./my-custom-karta.yaml
✓ Valid Karta definition
```

---

## Distribution

We will ship the CLI first as a **standalone binary**, with a clear path to becoming a kubectl plugin later.

For v0.1, we plan to use [GoReleaser](https://goreleaser.com/) with GitHub Actions to build binaries for `darwin/linux × amd64/arm64` on every tag, distributed via GitHub Releases, a Homebrew tap (`brew install run-ai/tap/karta`), and `go install`. The CLI itself will be built on [Cobra](https://cobra.dev/), the standard Go framework for Kubernetes CLIs.

In a later phase, we plan to add [krew](https://krew.sigs.k8s.io/) distribution so users can run `kubectl karta tree ...`. From day one the binary will support being invoked as either `karta` or `kubectl-karta` - Cobra does this out of the box, as long as help text uses `cmd.Name()` and we avoid hardcoding "karta" in error messages and usage strings. Enabling krew later will be a flag flip in GoReleaser plus a PR to [`kubernetes-sigs/krew-index`](https://github.com/kubernetes-sigs/krew-index).

---

## Data Model

The CLI, web dashboard, and MCP all need the same underlying data - a workload's component hierarchy with pods, scale, status, and resources. Rather than each consumer assembling this independently, the Karta library should produce a single `WorkloadTree` that any consumer can traverse and render in its own way.

Beyond the visibility tool, this tree can serve as a shared language between components. The Run:ai External Workload Integrator (EWI) already builds a similar tree for its complex workload structure feature (tracking hierarchies like Dynamo with multi-instance components and nested children) - it could adopt `WorkloadTree` as its foundation. A user submitting a workload could use the same tree structure to set desired topology or other instructions pre-submission.

The model is split into two layers: `WorkloadTree` is the raw data produced by the Karta library (desired structure, scale, specs, status - no live pods, no computation). `WorkloadView` is the display layer built on top of it by the CLI (resource aggregation, ready counts, pod details).

### `WorkloadTree` - shared utility (`pkg/tree/`)

A generic tree built from a Karta definition + workload object. Contains the raw Karta-extracted data - the desired structure, scale, extracted specs, and status, derived entirely from the workload spec. This lives in `pkg/` so any consumer can use it.

The structure was designed with the Run:ai External Workload Integrator (EWI) as a reference consumer - recently a feature was added to the EWI that builds complex hierarchical trees for workloads like Dynamo (multi-instance components with nested children per instance). The `WorkloadTree` is shaped so the EWI could adopt it as its tree-building foundation and add its own persistence layer on top.

```go
// WorkloadTree is the raw tree produced by the shared builder.
type WorkloadTree struct {
    Status   WorkloadStatus                // from root component GetStatus()
    Children []ComponentNode               // root-level components
}

type WorkloadStatus struct {
    Phases []string                        // matched phases, can be multiple (e.g. ["Running", "Degraded"])
}

type ComponentNode struct {
    Name      string
    Kind      *GroupVersionKind             // nil for logical grouping components
    Instances []InstanceNode               // always at least one (see below)
}

// InstanceNode represents one instance of a component.
// Every component has at least one instance. When InstanceKey is nil,
// it means the component is not multi-instance - there's just one.
// For multi-instance components (e.g., Dynamo services with "Frontend",
// "PrefillWorker"), each instance has its own InstanceKey and potentially
// its own child components underneath.
type InstanceNode struct {
    InstanceKey       *string              // nil = single instance (not multi-instance)
    ReplicaKey        *string              // nil = not replicated
    Scale             *Scale               // from Component.GetScale()
    ExtractedInstance *ExtractedInstance    // from Component.GetExtractedInstances() - pod specs, metadata
    Children          []ComponentNode       // child components under this instance
}

type Scale struct {
    Replicas    *int32
    MinReplicas *int32
    MaxReplicas *int32
}
```

Each pod-bearing component carries its desired scale over the scale envelope via `Scale` (`MinReplicas`, `MaxReplicas`, and `Replicas`), read directly from the workload spec; a consumer that needs a desired pod count computes it from the scale. The tree carries no live pods; pod-level data (resource usage, phase, node, readiness) is gathered separately by the consumer, not from the tree.

### Tree builder (`pkg/tree/builder.go`)

```go
func Build(ctx context.Context, karta *v1alpha1.Karta, factory *resource.ComponentFactory) (*WorkloadTree, error)
```

The builder works top-down, component by component, reading the desired structure entirely from the workload spec. Each component is expanded into instances from its `InstanceIdPath` (for example the JobSet `replicatedJobs[].name` or Dynamo service keys); a component with no instance id path has a single unnamed instance. Each instance then recurses into its child components.


### Pod matching (live consumers)

Mapping live pods onto the tree is a separate step from building it. Building stays spec-only so it has no cluster dependency; a consumer that needs per-pod status (phase, node, readiness) or the per-replica breakdown fetches the pods itself and matches them onto the tree, using the `pkg/resource` pod selectors (`ComponentTypeSelector`, `ComponentInstanceSelector`, `ReplicaSelector.KeyPath`) through `resource.PodQuerier`.

It happens in two stages, because the selectors answer only one of the two questions involved. A selector says which component type a pod plays; it does not say which workload the pod belongs to, so matching on it alone would claim every PyTorchJob worker in the namespace rather than this job's. Scoping to one workload is therefore ownership, not selectors: each candidate pod's controller owner-reference chain is walked, fetching intermediate objects, until it reaches the workload root or runs out of hops.

Only then does placement run, walking top-down, component by component. At each component the matcher claims the pods that belong to it or its descendants, then distributes them across instances; each instance passes only its pods down to child components, narrowing the set at each level until pods reach their leaf component.

Example for a PyTorchJob with 5 pods (master-0, worker-0..3):

```shell
[5 pods] -> master component: matcher claims master-0 -> [4 remaining]
         -> worker component: matcher claims worker-0..3
```

Example for Dynamo with 6 pods (2 frontend, 4 prefill):

```shell
[6 pods] -> service component: matcher claims all 6
           -> "Frontend" instance: 2 pods
             -> lws child: matcher claims them -> placed
           -> "PrefillWorker" instance: 4 pods
             -> lws child: matcher claims them -> placed
```

### `WorkloadView` - CLI/web layer

The CLI walks the `WorkloadTree` and computes display data as it traverses: resource aggregation (CPU, memory, GPU summed up the tree), ready counts, and pod details (phase, node). This `WorkloadView` is what gets rendered as table, tree, JSON, or served via web/MCP. It is what `karta describe` emits under `-o json`, and every section of the human output is a projection of it, so the two cannot drift.

Values are typed rather than pre-rendered. A `ReadyCount string` holding `"3/4"` was considered and rejected: it forces every consumer to re-parse a display string, and it breaks silently the day the rendering changes. The same reasoning gives a pod an explicit null node when it is unscheduled, rather than an empty string standing in for one.

Instances do not appear as a level of their own. A component whose instances carry an instance key or a replica key renders one child component per instance, named by that key, which is the shape a reader sees in the tree and the shape a consumer walks; a single-instance component is just itself. The uniform component -> instance -> component shape stays in `WorkloadTree`, where a consumer that needs it can walk it.

```go
type DescribeView struct {
    Name       string    `json:"name"`
    Namespace  string    `json:"namespace"`
    Kind       string    `json:"kind"`       // "PyTorchJob", "RayCluster", etc.
    APIVersion string    `json:"apiVersion"`
    CreatedAt  time.Time `json:"createdAt"`  // age is derived, never serialized
    Definition string    `json:"definition"` // the Karta that resolved it
    Origin     string    `json:"origin"`     // catalog | cluster
    Phases     []string  `json:"phases"`     // from WorkloadTree.Status.Phases
    FileMode   bool      `json:"fileMode"`   // built from a manifest, no live data

    Resources  Resources       `json:"resources"`  // summed across components
    Components []ComponentView `json:"components"`
}

type ComponentView struct {
    Name      string          `json:"name"`           // instance key when multi-instance
    Kind      string          `json:"kind,omitempty"` // empty for a grouping component
    Replicas  Replicas        `json:"replicas"`
    Resources Resources       `json:"resources"`      // request times desired scale
    Nodes     []string        `json:"nodes,omitempty"`
    Pods      []PodView       `json:"pods,omitempty"`
    Children  []ComponentView `json:"children,omitempty"`
}

type Replicas struct {
    Desired int32 `json:"desired"` // from the spec
    Current int32 `json:"current"` // pods attributed to this component
    Ready   int32 `json:"ready"`
}

type PodView struct {
    Name      string    `json:"name"`
    Phase     string    `json:"phase"`  // Running, Pending, Succeeded, Failed
    Ready     bool      `json:"ready"`
    Node      *string   `json:"node"`             // null when unscheduled
    Reason    string    `json:"reason,omitempty"` // why it is not running
    Resources Resources `json:"resources"`
}

type Resources struct {
    GPUs        int64 `json:"gpus"`
    CPUMillis   int64 `json:"cpuMillis"`
    MemoryBytes int64 `json:"memoryBytes"`
}
```

