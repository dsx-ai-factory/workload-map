<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Label resolver

The resolver turns the `labelRules` configuration into extra label values for
`karta_pod_workload_info` and `karta_workload_status`. `New` rejects invalid
rules and expressions.

`Resolve` receives the requested metrics, the root workload and, for the Pod
metric, its Pod. It returns the labels for each requested metric, with errors
reported per label.

```go
results, err := resolver.Resolve(ctx, labelresolver.Input{
    Metrics:  []string{labelresolver.MetricWorkloadStatus, labelresolver.MetricPodWorkloadInfo},
    Workload: root, // Labeled example.com/team=research.
    Pod:      pod,
})
// [
//   {Metric: "karta_workload_status", Labels: {"team": "research"}},
//   {Metric: "karta_pod_workload_info", Labels: {"team": "research"}},
// ]
```

A rule reads its sources in the configured order and uses the first nonempty
value. A label with no value is absent. Source errors are reported even when a
later source supplies the value. Workload status rules cannot read the Pod.
The returned error reports invalid input or cancellation.

The resolver reads only the objects it receives. Runtime decides when to call it
and saves the results; that integration is not implemented yet.
