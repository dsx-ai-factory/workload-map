<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# KEP-0001: DSX workload exporter

- Status: provisional
- Authors: @AviadHayumi
- Issue: https://github.com/dsx-ai-factory/workload-map/issues/343
- Created: 2026-10-12
- Last updated: 2026-11-03

## Summary

The DSX workload exporter has two main goals:

1. Show the status of selected workloads, such as `Running`, `Failed`, `Degraded`, `Completed` or `Suspended`.
2. Show where each Pod belongs: its workload, component, named instance and repeated group. For example, a Pod in JobSet `train` can belong to component `replicatedjob`, instance `worker` and group `0`.

The exporter provides this identity alongside workload status. Prometheus joins
it with CPU, memory and GPU measurements from other exporters.


## Motivation

A workload average can hide a busy component or group. Karta makes these
relationships queryable without per-operator PromQL.

Examples:

- Dynamo: find quiet prefill GPUs while decode stays busy; check demand before changing capacity.
- LWS: find a quiet worker group hidden by the workload average.
- Workload state: see Running, Failed or Degraded workloads and their observed history.

See [query examples](../../dsx-workload-exporter/queries.md) for CPU, memory and
GPU joins. The [implementation design](../../design/metrics-exporter/README.md)
explains runtime ownership and verification.

## Metrics

The [metric reference](../../dsx-workload-exporter/metrics.md) owns the four
metric families and their labels. This proposal does not define separate runtime
settings. Use the [configuration reference](../../dsx-workload-exporter/configuration.md)
for implemented settings and defaults.

## Configured labels

Label rules evaluate a full workload or Pod through CEL's `object` variable.
Pod info can read either source. Status reads only the workload, since it can
have many Pods or none. Runtime saves evaluated labels; scrapes do not evaluate
expressions. See [configured labels](../../dsx-workload-exporter/configuration.md#configured-labels).

## Who submitted the workload

Submitter capture runs separately in the API server through a
`MutatingAdmissionPolicy`. It records the caller on CREATE only when the
annotation is missing and the object has no controller owner. A supplied value
is preserved, so it is a hint rather than proof of identity. A platform caller
is its service account, not necessarily the human user.

Capture and exporter label extraction are implemented independently. See
[configuration](../../dsx-workload-exporter/configuration.md#submitter-admission)
and the [admission rationale](../../design/metrics-exporter/submitter-capture.md).

## History

- 2026-10-12 to 2026-10-19: Defined workload metrics and selection; separated configuration and query examples into linked guides.
- 2026-10-25: Added generic metric labels and defined watches for Pods, selected workload kinds and discovered roots. Intermediate owners use metadata reads and periodic revalidation.
- 2026-10-25: Made submitter capture a best-effort MAP with a configurable annotation; dropped the VAP and UPDATE protection.

- 2026-11-03: Renamed exporter metrics and component to DSX. Consolidated current metric/configuration references and aligned the implementation design with metadata watches and separate Pod/workload publications.
