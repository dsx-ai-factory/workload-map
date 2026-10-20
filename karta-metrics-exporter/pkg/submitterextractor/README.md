<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Submitter extractor

This package reads a submitter identity from one object's metadata with a CEL expression.
`Evaluate` receives one object's metadata and explicit group, version and kind.
GVK lets CEL select metadata by resource type, such as different labels for Jobs
and Deployments. It is passed separately because `metav1.Object` does not expose it.
It reads no Kubernetes resources, receives no workload spec or status, and does not mutate the object.

CEL exposes `labels`, `annotations` and `resource`.
The `resource` map contains `namespace`, `name`, `group`, `version` and `kind`.
`New` compiles the expression once and requires a string result.
An empty expression disables extraction. An invalid expression is a `New` error, and the exporter fails startup.
`Submitter.Identity` holds the extracted identity. An empty identity means unknown. On error, callers ignore the result.
Context cancellation remains `context.Canceled` or `context.DeadlineExceeded`.

Each evaluation is bounded by a CEL cost limit, and the returned identity by a length limit.
The expression itself is bounded in size.

Helm sets the expression through `exporter.attribution.submittedByExpression`.
The chart passes it unchanged to the exporter in `KARTA_SUBMITTED_BY_EXPRESSION`.

```yaml
exporter:
  attribution:
    submittedByExpression: |
      ("karta.run.ai/submitted-by" in annotations)
        ? annotations["karta.run.ai/submitted-by"]
        : ("example.com/submitted-by" in labels
            ? labels["example.com/submitted-by"] : "")
```

The extractor does not authenticate metadata or combine identities across Pods.
