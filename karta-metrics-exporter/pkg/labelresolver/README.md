<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Label resolver

The label resolver adds labels from `labelRules` to exporter metrics. Each rule
names a label, the metrics that get it, the objects it reads and a CEL
expression. The options, defaults and limits are described in
[Configured labels](../../../docs/karta-metrics-exporter/configuration.md#configured-labels)
and in the [evaluator configuration](../labelevaluator/README.md#configuration).

```yaml
labelRules:
  - name: team
    metrics: [karta_workload_status, karta_pod_workload_info]
    sources: [workload]
    expression: |
      has(object.metadata.labels) && "example.com/team" in object.metadata.labels
        ? object.metadata.labels["example.com/team"] : ""
  - name: owner
    metrics: [karta_pod_workload_info]
    sources: [workload, pod]
    expression: |
      has(object.metadata.labels) && "example.com/owner" in object.metadata.labels
        ? object.metadata.labels["example.com/owner"] : ""
```

For a workload labeled `example.com/team=research`, both metrics get
`team="research"`. The Pod metric also gets `owner` from the workload. If the
workload has no owner label, it gets `owner` from the Pod.

## Behavior

- A rule adds a label. If a built-in label has the same name, the rule value
  replaces the built-in value. If the rule gives no value, the built-in value
  stays.
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
  labels.
- A rule for `karta_workload_status` can read only the workload. That metric has
  one series for each workload, but a workload can have many Pods or none, so a
  Pod value is not defined. Use `karta_pod_workload_info` to label by Pod values.
- The resolver reads only the objects it receives. It does not call Kubernetes
  and does not change the objects.
- At startup, the exporter compiles each expression. It stops if a rule has an
  invalid name, metric or source, or if the expression has a syntax error,
  cannot return a string or is too long. Errors when the expression runs, such
  as a missing field, do not stop the exporter; they are reported for that label.

Runtime decides when to resolve labels and saves the results.
