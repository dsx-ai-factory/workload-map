<!--
SPDX-License-Identifier: Apache-2.0
Copyright (c) 2026 NVIDIA Corporation
-->
# Karta controller-runtime example

This example installs a real controller into a Kubernetes cluster (a Kind
cluster works out of the box) and reacts to live changes. It watches a
`leaderworkerset.x-k8s.io` LeaderWorkerSet and, on every change, uses Karta to
inspect and mutate it through a generic reconcile loop, with no CRD-specific
branching.

LeaderWorkerSet is a good showcase because its shape is non-trivial: it has no
single pod template on the root. Pods live in two child components, a leader and
a worker, each with its own template under `spec.leaderWorkerTemplate`, and the
worker replica count is a computed expression. The reconciler does not know or
care, because Karta resolves where everything lives from the LeaderWorkerSet's
Karta definition.

It complements the [quickstart](../quickstart/), which runs offline against
embedded YAML. Here the workload structure is not embedded in the binary: it
lives in the cluster as a Karta custom resource, and the controller reads it at
runtime. The controller is not hard-wired to LeaderWorkerSet either: the watched
types come from the `--watch-gvk` flag, so managing another workload type needs
no code change (see
[Manage another workload type](#manage-another-workload-type-no-code-change)).

## What it demonstrates

The reconcile loop in [controller.go](controller.go) does the same work for any
workload Karta can describe:

1. Discover the workload structure from the cluster by GVK: the controller lists
   Karta objects and selects the one whose root component matches the watched
   workload's GVK. It never hard-codes a Karta name, so it resolves a definition
   the same way a real consumer does for an arbitrary workload type.
2. Read a unified status (`Initializing`, `Running`, `Failed`, and so on) without
   parsing CRD-specific conditions.
3. Aggregate replica counts and container resource requests across every
   component (root and children), so the totals are right even though the leader
   and worker pods live in separate child components.
4. Write the results back as `karta/*` annotations, and emit a `StatusChanged`
   Kubernetes Event on each real status transition (a single canonical phase, so
   no churn through transient or simultaneous states).
5. Inject a pod-template label into every pod-bearing component (leader and
   worker) through Karta `UpdatePodTemplateSpec`.

There is no `switch` on workload kind anywhere in the reconciler.

### About the pod-template mutation

A LeaderWorkerSet's pod templates are mutable while it runs (an update triggers a
normal rolling update), and it has no suspend field. So unlike a Job or JobSet,
there is no immutable window to work around: the controller injects the managed-by
label directly into the leader and worker templates, and Karta routes each update
to the right path in the workload.

The bundled [lws Karta definition](../../catalog/leaderworkerset-x-k8s-io-leaderworkerset-v1.yaml) maps the leader and
worker templates and the computed worker replica count, so the controller reads
and writes them with plain `GetPodTemplateSpec` / `UpdatePodTemplateSpec` calls.

## Prerequisites

- [kind](https://kind.sigs.k8s.io/), `kubectl`, `docker`
- Go (only to build the image)

This example is a standalone module. Its local `replace` directive selects the
repository library in `karta/`, so the image is built from the repository root.

## Run it on Kind

From the repository root.

Create a cluster:

```bash
kind create cluster
```

Install the LeaderWorkerSet operator (its CRD and controller are not built into
Kubernetes):

```bash
VERSION=v0.8.0
kubectl apply --server-side -f https://github.com/kubernetes-sigs/lws/releases/download/$VERSION/manifests.yaml
kubectl -n lws-system rollout status deploy/lws-controller-manager
```

Install the Karta CRD, then apply the LeaderWorkerSet Karta definition. Order
matters: the CRD must exist before the object that uses it.

```bash
kubectl apply -f charts/karta/crds/run.ai_kartas.yaml
kubectl apply -f docs/catalog/leaderworkerset-x-k8s-io-leaderworkerset-v1.yaml
```

Build the controller image and side-load it into the cluster:

```bash
# Derive the Go version from go.mod so the build image stays aligned with it.
GO_VERSION=$(awk '/^go /{print $2}' docs/examples/controller-runtime/go.mod)
docker build --build-arg GO_VERSION=$GO_VERSION \
  -f docs/examples/controller-runtime/Dockerfile -t generic-controller-example:latest .
kind load docker-image generic-controller-example:latest
```

Deploy the controller:

```bash
kubectl apply -f docs/examples/controller-runtime/manifests/
kubectl -n karta-system rollout status deploy/generic-controller
```

## See it work

Apply the LeaderWorkerSet and inspect what the controller wrote:

```bash
kubectl apply -f docs/examples/controller-runtime/samples/leaderworkerset.yaml

# Karta-derived annotations on the LeaderWorkerSet metadata
kubectl get leaderworkerset karta-demo-lws -o jsonpath='{.metadata.annotations}' | tr ',' '\n'

# The label is injected into both the leader and worker templates
kubectl get leaderworkerset karta-demo-lws \
  -o jsonpath='leader={.spec.leaderWorkerTemplate.leaderTemplate.metadata.labels}{"\n"}worker={.spec.leaderWorkerTemplate.workerTemplate.metadata.labels}{"\n"}'

# The injected label also reaches the running pods
kubectl get pods -l app.kubernetes.io/managed-by=karta

# Events recorded by the controller
kubectl describe leaderworkerset karta-demo-lws | sed -n '/Events:/,$p'
```

Expected annotations once the group is ready (replicas sums the group, leader and
worker components; cpu/memory sum the leader and worker requests):

```text
karta/status: Running
karta/replicas: 4
karta/cpu-request: 200m
karta/memory-request: 128Mi
karta/gpu-request: 0
```

The label injection is recorded as a single event:

```text
Normal  PodTemplateLabeled  Injected pod-template label app.kubernetes.io/managed-by=karta via Karta
```

The first observed status is recorded silently, so a brand-new workload does not
emit a `StatusChanged` event. To see one, drive a transition, for example a
rolling update that the controller observes going back through `Initializing` and
into `Running`:

```bash
kubectl patch leaderworkerset karta-demo-lws --type=json \
  -p '[{"op":"replace","path":"/spec/leaderWorkerTemplate/workerTemplate/spec/containers/0/image","value":"busybox:1.38"}]'
kubectl describe leaderworkerset karta-demo-lws | sed -n '/Events:/,$p'
```

```text
Normal  StatusChanged  Workload status changed: Running -> Initializing (replicas=4 cpu=200m memory=128Mi gpu=0)
```

## Change the definition live

The controller also watches Karta objects. Editing the one whose root component
is `leaderworkerset.x-k8s.io/v1 LeaderWorkerSet` re-reconciles every governed
LeaderWorkerSet with the new structure, with no redeploy:

```bash
kubectl edit karta leaderworkerset-x-k8s-io-leaderworkerset-v1
# change something the controller reports, then save
```

This is the deeper layer of the example: the workload structure is cluster data,
not code. See [docs/catalog/](../../catalog/) for ready-made definitions covering
PyTorchJob, RayCluster, JobSet, and more.

## Manage another workload type (no code change)

The watched types are configured at runtime through the `--watch-gvk` flag, so
adding one never touches the Go code. To also manage, say, JobSet:

1. Install the JobSet operator (the controller can only watch types the API
   server knows):
   ```bash
   VERSION=v0.12.0
   kubectl apply --server-side -f https://github.com/kubernetes-sigs/jobset/releases/download/$VERSION/manifests.yaml
   ```
2. Apply its Karta definition:
   ```bash
   kubectl apply -f docs/catalog/jobset-x-k8s-io-jobset-v1alpha2.yaml
   ```
3. Add the GVK to the flag in `manifests/01-deployment.yaml` (format
   `group/version/kind`, core group empty):
   ```yaml
   args:
     - --watch-gvk=leaderworkerset.x-k8s.io/v1/LeaderWorkerSet,jobset.x-k8s.io/v1alpha2/JobSet
   ```
4. Grant RBAC for the new resource in `manifests/00-rbac.yaml`:
   ```yaml
   - apiGroups: ["jobset.x-k8s.io"]
     resources: ["jobsets"]
     verbs: ["get", "list", "watch", "update", "patch"]
   ```
5. Re-apply the manifests:
   ```bash
   kubectl apply -f docs/examples/controller-runtime/manifests/
   ```
6. Resume the Job and watch the injected label reach the pods:
   ```bash
   kubectl patch jobset karta-demo-jobset-suspended --type=merge -p '{"spec":{"suspend":false}}'
   kubectl get pods -l app.kubernetes.io/managed-by=karta
   ```

The controller creates one watch/reconciler per configured GVK at startup, so a
flag change takes effect on the next rollout. The reconcile code is untouched;
Karta absorbs the new workload's structure.

## File layout

| File | Purpose |
|------|---------|
| `main.go` | Manager setup: scheme, one reconciler per watched GVK, start |
| `controller.go` | `WorkloadReconciler` inspect + mutate loop (no per-CRD branching) |
| `Dockerfile` | Builds the controller image from the repository root |
| `manifests/00-rbac.yaml` | Namespace, ServiceAccount, ClusterRole, ClusterRoleBinding (applied first) |
| `manifests/01-deployment.yaml` | Controller Deployment in the `karta-system` namespace |
| `samples/leaderworkerset.yaml` | LeaderWorkerSet (leader + worker child templates, computed worker count) |

## Clean up

```bash
kubectl delete -f docs/examples/controller-runtime/samples/ --ignore-not-found
kubectl delete -f docs/examples/controller-runtime/manifests/ --ignore-not-found
kind delete cluster
```
