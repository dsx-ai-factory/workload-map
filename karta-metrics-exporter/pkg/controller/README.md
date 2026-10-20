<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Exporter controller

The controller will connect Kubernetes watches to the exporter's work queue.
Workers will call discovery and submitter extraction, then publish observations
for the output adapters to read.

It will handle Pod, workload and definition changes, retry unavailable inputs,
and remove observations after deletion. It runs inside the exporter process and
does not reconcile Karta CRs or mutate workloads.

Implementation follows in a separate change.
