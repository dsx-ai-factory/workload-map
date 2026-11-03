<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# DSX workload exporter metrics

The exporter maps Pods to workload identities and reports workload state.
It does not measure CPU, memory or GPU activity. Use the [query examples](queries.md)
to join those measurements.

## Metrics

| Metric                                 | Prometheus type | Value                                           | Why it is needed                                                                 |
| -------------------------------------- | --------------- | ----------------------------------------------- | -------------------------------------------------------------------------------- |
| `dsx_workload_pod_info`            | Gauge           | Always `1`                                    | Attach workload, component and group identity to measurements from a Pod.        |
| `dsx_workload_status`              | Gauge           | `1` for a matching phase, otherwise `0`     | Distinguish running, completed or failed work, including workloads with no Pods. |
| `dsx_workload_created_timestamp_seconds` | Gauge | Root `metadata.creationTimestamp` in Unix seconds. | Calculate workload age. |
| `dsx_exporter_observation_valid` | Gauge | Resource-type collection: `1` or `0`. Workload/discovery failures: `0` only. | Report collection health and failure reasons. Healthy workloads have no separate health row. |

Without a selected Karta definition, a verified root still supplies Pod identity,
creation time and all nine status phase gauges with value `0`. The status labels
`karta` and `karta_source` are empty. Zero means no phase matched; it does not
assert an evaluated application state. Workload label rules still apply.
Component and instance are `<unknown>`; group is empty. Missing a definition
alone is not a verification failure.

Creation time comes from the root object. Missing timestamps or failed
root verification omit the sample. Status evaluation alone does not invalidate
verified creation metadata.

### Metric examples

These examples use one Dynamo workload. Values are illustrative.

```prometheus
dsx_workload_pod_info{namespace="team-a",workload_name="dynamo",workload_group="nvidia.com",workload_version="v1alpha1",workload_kind="DynamoGraphDeployment",workload_uid="cea29a7c-8a8b-4dad-aa48-ef0f7c888a73",owner_resolution="resolved",pod_name="prefill-0",pod_uid="f6610f41-62d8-4111-92a7-05aa776ff926",component="service",component_instance="prefill",replica_group_id="",observation_valid="true"} 1
dsx_workload_status{namespace="team-a",workload_name="dynamo",workload_group="nvidia.com",workload_version="v1alpha1",workload_kind="DynamoGraphDeployment",workload_uid="cea29a7c-8a8b-4dad-aa48-ef0f7c888a73",phase="Running",owner_resolution="resolved",karta="nvidia-com-dynamographdeployment-v1alpha1",karta_source="catalog",observation_valid="true"} 1
dsx_workload_created_timestamp_seconds{namespace="team-a",workload_name="dynamo",workload_uid="cea29a7c-8a8b-4dad-aa48-ef0f7c888a73"} 1792324800
dsx_exporter_observation_valid{scope="resource_type",namespace="team-a",resource_group="nvidia.com",resource_version="v1alpha1",resource_kind="DynamoGraphDeployment",workload_name="",workload_uid="",reason="none"} 1
```

### Labels

| Metric | Built-in labels |
| --- | --- |
| `dsx_workload_pod_info` | `namespace`, `workload_name`, `workload_group`, `workload_version`, `workload_kind`, `workload_uid`, `owner_resolution`, `pod_name`, `pod_uid`, `component`, `component_instance`, `replica_group_id`, `observation_valid` |
| `dsx_workload_status` | `namespace`, `workload_name`, `workload_group`, `workload_version`, `workload_kind`, `workload_uid`, `phase`, `owner_resolution`, `karta`, `karta_source`, `observation_valid` |
| `dsx_workload_created_timestamp_seconds` | `namespace`, `workload_name`, `workload_uid` |
| `dsx_exporter_observation_valid` | `scope`, `namespace`, `resource_group`, `resource_version`, `resource_kind`, `workload_name`, `workload_uid`, `reason` |

| Label | Metrics | Meaning | Example |
| --- | --- | --- | --- |
| `namespace` | Pod info, Status, Created, Health | Namespace associated with the observation. | `team-a` |
| `workload_name` | Pod info, Status, Created, Health | Selected root workload object name. Pod info, Status and Created name the same root. Empty for resource-type health. | `train` |
| `workload_group` | Pod info, Status | API group of the observed workload; empty for the core API. | `jobset.x-k8s.io` |
| `workload_version` | Pod info, Status | API version of the attributed root: the selected definition's GVK, or the verified owner GVK when no definition exists. Not `metadata.resourceVersion`. | `v1alpha2` |
| `workload_kind` | Pod info, Status | Kind in the observed workload GVK. | `JobSet` |
| `workload_uid` | Pod info, Status, Created, Health | Kubernetes UID identifying this workload's lifetime. Empty for resource-type health. | `628cbe4e-f726-4279-a174-0d18e9cd0a28` |
| `pod_name` | Pod info | Attributed Pod's name. | `train-worker-0` |
| `pod_uid` | Pod info | Kubernetes UID identifying this Pod's lifetime. | `596e77f2-50af-40d9-96d5-28ab7c01b4dd` |
| `component` | Pod info | Component name from the selected definition; `<unknown>` if unresolved. | `replicatedjob` |
| `component_instance` | Pod info | Named entry within the component; empty when there are no named instances, or `<unknown>` if unresolved. | `worker` |
| `replica_group_id` | Pod info | Definition-selected key for a repeated group of Pods within the workload/component/instance. Empty when no group applies. It is neither a Pod index nor a replica count. | `0` |
| `phase` | Status | Evaluated workload phase; its series value is `1` when matched and `0` otherwise. | `Running` |
| `owner_resolution` | Pod info, Status | `resolved` after verifying the chain; `ignored_owner` at an intentional boundary; retained records keep their previous resolution on failure. Failure reasons are reported by Health. | `resolved` |
| `observation_valid` | Pod info, Status | `"true"` while the verification required for this observation is valid; `"false"` for a retained cached observation after failure. | `true` |
| `karta` | Status | Name of the selected Karta definition, or empty when none is selected. | `jobset-x-k8s-io-jobset-v1alpha2` |
| `karta_source` | Status | Origin of that definition: `catalog`, `cluster`, or empty when none is selected. | `catalog` |
| `scope` | Health | `resource_type` reports collection health per namespace + GVK, including before object names are known; it does not certify each workload evaluation. `workload` reports failures affecting a known workload. `discovery` groups unresolved-input failures with empty workload identity. No healthy workload or discovery row is emitted. | `workload` |
| `resource_group` | Health | API group being verified. For workload scope, identifies the affected root workload, even when a dependency failed. Empty for the core API. | `jobset.x-k8s.io` |
| `resource_version` | Health | API version being verified, not Kubernetes `metadata.resourceVersion`, using the same scope as `resource_group`. | `v1alpha2` |
| `resource_kind` | Health | API kind being verified, using the same scope as `resource_group`. | `JobSet` |
| `reason` | Health | Bounded failure category. `none` is used only for healthy resource-type collection. Workload rows always carry a failure reason. Detailed errors belong in logs. | `none`, `permission_denied` |

### Reading observation validity

`observation_valid="true"` means saved errors and captured source health report
the observation as valid, even if the workload failed. `false` marks retained
Pod info or status with failed evaluation or dependencies. A scrape alone does
not refresh inputs. See [validity limits](#validity-limits). Each
observation has only one validity value in a snapshot. Deletion or scope exit
removes current series; previously collected history remains.

| `reason` | Meaning |
| --- | --- |
| `permission_denied` | A required Kubernetes read was denied. |
| `api_unavailable` | The API could not be reached or a required source is unavailable. |
| `invalid_definition` | The selected Karta definition is invalid. |
| `evaluation_failed` | Karta attribution/status or configured label evaluation failed. |
| `unresolved_owner` | A required owner is missing or its UID does not match. |
| `none` | Resource-type collection is verified. This is never a healthy workload row. |

Prefer `workload_uid` and `pod_uid` over names when matching metrics, where both
inputs provide them. Names can be reused after deletion.

Illustrative component attribution (not a claim of tested operator coverage):

| Example                           | `component`     | `component_instance` | `replica_group_id` |
| --------------------------------- | ----------------- | ---------------------- | -------------------- |
| Dynamo prefill, Deployment-backed | `service`       | `prefill`            | empty                |
| JobSet, first worker Job          | `replicatedjob` | `worker`             | `0`                |
| JobSet, second worker Job         | `replicatedjob` | `worker`             | `1`                |
| LWS, leader in group 0            | `leader`        | empty                  | `0`                |
| LWS, worker in group 0            | `worker`        | empty                  | `0`                |
| LWS, worker in group 1            | `worker`        | empty                  | `1`                |

## Validity limits

Validity uses saved operation errors and captured source health. The current Store
has no clock or expiry. Source recovery can restore source-only validity before
reconciliation completes. A successful scrape does not prove retained values were
recalculated against recovered inputs. See the
[Store contract](../../dsx-workload-exporter/pkg/store/README.md).
