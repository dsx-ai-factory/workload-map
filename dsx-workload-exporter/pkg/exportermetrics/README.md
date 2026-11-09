<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Exporter metrics

`Register` adds the exporter's own metrics to the `/metrics` registry:

| Family | Source |
| --- | --- |
| `go_*`, `process_*` | Go runtime and process collectors. |
| `workqueue_*{name}` | Each controller queue (`exporter-pods`, `exporter-workloads`, `exporter-sources`). |
| `rest_client_requests_total{code,method,host}` | Every Kubernetes API request. |
| `rest_client_request_duration_seconds{verb,host}` | API request latency. |
| `rest_client_rate_limiter_duration_seconds{verb,host}` | Wait in the client-side `apiQPS`/`apiBurst` limiter. |

Queues report through the `Workqueue` provider, set on each queue's config. The
client-go hooks are process-wide and install on the first `Register` call; requests
made before it are not counted.

The names match controller-runtime and client-go defaults, so existing dashboards
for those metrics work unchanged.
