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

The [configuration reference](../../../docs/dsx-workload-exporter/configuration.md#configured-labels)
owns the Helm schema, examples, limits and flag/environment precedence.
Runtime uses the [label resolver](../labelresolver/README.md) to apply rules and
saves results for collection. The evaluator itself has no publication policy.
