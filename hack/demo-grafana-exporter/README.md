<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Local Grafana demo

From the repository root, run:

```sh
./hack/demo-grafana-exporter/up.sh
```

Requires Docker, Kind, kubectl, Helm, Make, Go, Git, curl, tar and shasum on PATH.
Allow about 8 GiB of Docker memory and internet access for images and Go modules.
The script reuses the e2e [exporter installer](../e2e/dsx-workload-exporter/install.sh)
and [fake GPU installer](../e2e/infra/fake-gpu-operator/install.sh).
Both the demo and exporter e2e use `make exporter-image` to build the exporter.
Demo manifests, values and the simulator patch live in this directory.
The script creates a fresh two-node Kind cluster with unique local image tags and keeps
the current Kubernetes context unchanged. Run `--help` for cluster and local-port
options. An existing cluster with the same name is refused.

The script prints dashboard links and a dedicated kubeconfig path. Keep it
running while viewing Grafana. Ctrl-C stops its port-forwards and leaves the
cluster for inspection. The printed cleanup command removes only that demo
cluster. Monitoring storage is temporary and retains two hours of history.
Grafana allows anonymous viewing inside this local demo. There is no ingress.

The stack contains DSX, Prometheus, Grafana and fake GPU operator. CPU and RAM
come from kubelet's cAdvisor endpoint. The fake GPU operator supplies simulated
DCGM metrics; a real DCGM Exporter needs GPU hardware and is not installed here.
There are no recording rules, GPU observer or kube-state-metrics.

## Workloads and controls

| Workload | Expected result |
| --- | --- |
| `gpu-active` | Deployment with two Pods, one fake GPU each at 80% activity. |
| `gpu-quiet` | Root Pod with one fake GPU at 2%; Warming, then Low activity after three sampled minutes. |
| `cpu-only` | Deployment with CPU/RAM measurements and unknown GPU activity. |
| `zero-pods` | Deployment root with no Pods. |
| `completed` | Successful Job. |
| `failed` | Intentionally failed Job, without retries. |

The two GPU annotations are `run.ai/simulated-gpu-utilization` (percent) and
`gpu-fraction` (fraction of the simulated GPU memory). They control generated
GPU measurements, not actual computation. CPU/RAM values are real, usually small.

Use the kubeconfig path printed by the script to make `gpu-active` quiet:

```sh
kubectl --kubeconfig <printed-kubeconfig> -n dashboard-lab annotate pods \
  -l app=gpu-active run.ai/simulated-gpu-utilization=2 --overwrite
```

The next scrapes show Warming, then Low activity after the sampled window.
Change `2` back to `80` to return to Active. Change the Deployment's template
annotations as well if replacement Pods should keep the new value.
Select a workload name in Overview to open its component and Pod structure.

## Simulator compatibility

The pinned fake GPU operator 0.0.72 does not emit `pod_uid`. The demo applies the
included patch to its checksum-verified source and builds two local images.
The patch copies the actual allocating Pod UID from the operator's cache into
its topology and DCGM labels. It also refreshes that UID after updater restarts.
It does not change dashboard queries or invent a workload UID.

The upstream source and its MIT license remain in the temporary build directory.
The local images include that license. No images are published. The patch's
focused tests cover UID export and allocation updates.

Prometheus discovers each simulated DCGM Pod and excludes the unrelated KWOK
exporter. It scrapes kubelet through the API server with its service account and
verified TLS. The node-proxy permission is confined to this disposable demo.
Each dashboard has its own generated ConfigMap because the pair exceeds 1 MiB.

For a real cluster, use the [collector setup](../../dsx-workload-exporter/examples/grafana/setup.md).
