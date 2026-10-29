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
- A rule cannot use a label that identifies or joins series: `namespace`,
  `pod_name`, `pod_uid`, `workload_uid`, `phase`, `observation_valid`, `job`
  and `instance`, or a name that starts with `__`.
- A rule reads its sources in the configured order. The first nonempty value
  becomes the label value.
- If every source returns an empty value or fails, the rule gives no value. The
  metric does not get the label, or keeps its built-in value.
- If a source fails, the next source is read. The failure is reported, also
  when a later source gives the value. A failed label does not change the other
  labels.
- A workload metric cannot read the Pod. A workload has zero or more Pods, so
  there is no single value to use.
- The resolver reads only the objects it receives. It does not call Kubernetes
  and does not change the objects.
- An invalid rule or expression stops the exporter at startup.

Runtime decides when to resolve labels and saves the results.
