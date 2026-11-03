<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Label resolver

The label resolver adds labels from `labelRules` to exporter metrics. Each rule
names a label, the metrics that get it, the objects it reads and a CEL
expression. The options, defaults and limits are described in
[Configured labels](../../../docs/dsx-workload-exporter/configuration.md#configured-labels).

`Resolve` receives full unstructured workload and Pod objects. The Pod is needed
only when a requested rule reads it. Objects are evaluated as supplied, preserving
field presence and fields unknown to the Kubernetes Go types. Callers must not
mutate either object during evaluation.

## Behavior

- `ResolvedMetricLabels.Apply` copies resolved labels over built-ins. Runtime
  does not call this helper. The current collector appends built-ins last, so
  built-in values win in exported metrics. Use nonconflicting custom names.
- Replacing a label that identifies or joins series, such as `workload_uid`,
  `pod_name` or `phase`, changes the series and can break the queries that use
  it. A name that starts with `__` is not permitted.
- A rule reads its sources in the configured order. The first nonempty value
  becomes the label value.
- Several rules can set the same label on the same metric. Each rule can return
  an empty value when it does not apply. If more than one rule gives a value,
  the last rule in `labelRules` wins. The errors of every rule are reported.
- If every source returns an empty value or fails, the rule gives no value. The
  metric does not get the label, or keeps its built-in value.
- If a source fails, the next source is read. The failure is reported, also
  when a later source gives the value. A failed label does not change the other
  labels within this resolver result. Runtime may retain the whole previous
  record as invalid after any label error.
- A rule for `dsx_workload_status` can read only the workload. That metric has
  workload-level phase series, but a workload can have many Pods or none, so a
  Pod value is not defined. Use `dsx_workload_pod_info` to label by Pod values.
- The resolver reads only the objects it receives. It does not call Kubernetes
  and does not change the objects.
- At startup, the exporter compiles each expression. It stops if a rule has an
  invalid name, metric or source, or if the expression has a syntax error,
  cannot return a string or is too long. Errors when the expression runs, such
  as a missing field, do not stop the exporter; they are reported for that label.

The [controller](../controller/README.md) decides when to resolve labels and saves
the results.
