<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Label evaluator

The compiler turns one CEL expression into an `Evaluator`. `Evaluate` receives
a full Pod or workload as `*unstructured.Unstructured` and returns a string.
It reads the supplied object without changing it or calling Kubernetes.

```go
compiler := labelevaluator.NewCompiler()
evaluator, err := compiler.Compile(labelevaluator.Config{
    Expression: `object.spec.schedulerName`,
})
// Handle err before calling Evaluate.
value, err := evaluator.Evaluate(ctx, pod)
// A Pod with spec.schedulerName: example-scheduler returns "example-scheduler".
```

CEL sees `object`, including apiVersion, kind, metadata, spec and status. Guard
optional fields with `has(...)`. Known non-string expressions fail compilation.
Dynamic results, such as `object.spec.schedulerName`, are checked during evaluation.
Empty results mean no label value. An empty expression compiles to an evaluator
that returns an empty string; configured label rules require an expression.

The compiled evaluator is safe for concurrent calls. Callers must not change the
input object during evaluation.

## Configuration

```yaml
exporter:
  labelRules:
    - name: team
      metrics: [karta_pod_workload_info, karta_workload_status]
      sources: [workload]
      expression: |
        has(object.metadata.labels) && "example.com/team" in object.metadata.labels
          ? object.metadata.labels["example.com/team"] : ""
  genericLabelEvaluationLimits:
    costLimit: 10000
    maxExpressionBytes: 4096
    maxResultBytes: 1024
```

`labelRules` defaults to `[]`. Rules target Pod info or workload status. A Pod
source is allowed only for Pod info. Sources run in listed order until a nonempty
value is found. The resolver retains per-source evaluation errors, including
when a fallback succeeds. A rule can replace a built-in label of the same name.

The evaluator, startup rule validation and the [label resolver](../labelresolver/README.md)
work today. Runtime evaluation and metric delivery are not implemented yet.

Helm mounts these two sections as a config file. Outside Helm, use the same file
without the `exporter:` wrapper and pass `--config path.yaml`.

| Config key | Flag | Environment variable |
| --- | --- | --- |
| Config path | `--config` | `KARTA_EXPORTER_CONFIG` |
| `genericLabelEvaluationLimits.costLimit` | `--generic-label-eval-cost-limit` | `KARTA_EXPORTER_GENERIC_LABEL_EVAL_COST_LIMIT` |
| `genericLabelEvaluationLimits.maxExpressionBytes` | `--generic-label-eval-max-expression-bytes` | `KARTA_EXPORTER_GENERIC_LABEL_EVAL_MAX_EXPRESSION_BYTES` |
| `genericLabelEvaluationLimits.maxResultBytes` | `--generic-label-eval-max-result-bytes` | `KARTA_EXPORTER_GENERIC_LABEL_EVAL_MAX_RESULT_BYTES` |

Explicit flags override environment variables, then YAML, then defaults. An empty
numeric override is an error. `--config=""` skips a path set through the environment.
Zero limits use compiler defaults. Negative limits are rejected. Rules stay in YAML.
