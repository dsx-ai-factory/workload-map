<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Output adapters

Adapters will translate published workload observations into an output format.
They will not discover workloads, evaluate Karta definitions or run submitter CEL.

The first adapter will use [OpenTelemetry](otel/). Implementation follows in a
separate change.
