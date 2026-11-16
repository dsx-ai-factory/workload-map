<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# KEP-0002: CEL expressions and resource references

- Status: provisional
- Authors: @AviadHayumi
- Issue: https://github.com/dsx-ai-factory/workload-map/issues/330
- Created: 2026-11-15
- Last updated: 2026-11-16

## Summary

Replace jq with [CEL](https://kubernetes.io/docs/reference/using-api/cel/) in Karta definitions.  
Separate read expressions from write locations, with an optional match expression on each.  
Read named references through a Kubernetes reader or objects supplied by the caller.  
The SDK changes the workload locally; the caller saves it to Kubernetes.

![KServe read and write flow, with the caller updating the Kubernetes API server.](cel-overview-api-server.png)

## Motivation

The KServe example below shows how CEL can make a complex mapping easier to follow.  
KServe can keep a model under `spec.predictor.model`, `spec.predictor.sklearn`, or another predictor key.  
Today one jq path finds that model for both reads and writes.  
Separate CEL rules show what is read and where a change goes.  
CEL does not support assignment, so the SDK applies the change.

This KEP also covers references to other Kubernetes objects.  
Trainer v2 needs values from two custom resources (CRs): the [TrainJob](https://trainer.kubeflow.org/en/latest/user-guides/trainjob-lifecycle.html) and the [ClusterTrainingRuntime](https://trainer.kubeflow.org/en/latest/operator-guides/runtime.html) named by `spec.runtimeRef`.  
The SDK makes both objects available to CEL.  
A read expression uses the TrainJob's image override when set, or the runtime's image otherwise.  
CEL lets Karta authors express this choice directly.  
They can change the fallback or add conditions without changing SDK code.

### Goals

- Make Karta easier to use for cluster admins who already use [CEL in Kubernetes](https://kubernetes.io/docs/reference/using-api/cel/).
- Use CEL for reads, match expressions and computed write paths.
- Keep new values and patch application in the SDK.
- Read references through the caller's client or supplied objects.

### Non-goals

Writes to references and cross-namespace references.  
The SDK does not start watches or retry loops, even when given a Kubernetes reader.  
The supplied reader can provide caching and retries.  
The [implementation design](../../design/cel-expressions/README.md) explains the SDK interfaces and execution order.

## Proposal

### KServe: the same operation in jq and CEL

These are the relevant fields of an InferenceService passed to the SDK:

```yaml
apiVersion: serving.kserve.io/v1beta1
kind: InferenceService
metadata: {name: model, namespace: team-a}
spec:
  predictor:
    model:
      image: ghcr.io/example/inference:v1
      storageUri: s3://example-models/model
```

The [KServe Karta from v0.3.0](https://github.com/dsx-ai-factory/workload-map/blob/bb13880e96e77087f074d6a04e5b6696762f6d3f/docs/catalog/serving-kserve-io-inferenceservice-v1beta1.yaml) uses this jq expression:

```jq
.spec.predictor | ( (.[]? | select(type == "object" and .storageUri)))
```

Read it from left to right:

- `.spec.predictor` reads the predictor from the InferenceService.
- `.[]?` visits its values.  
  `?` suppresses an error if the predictor is not an object or array.
- `select(...)` keeps objects whose `storageUri` is neither null nor false.  
  An empty string still matches.

For the input above, the result is the model object:

```json
{
  "image": "ghcr.io/example/inference:v1",
  "storageUri": "s3://example-models/model"
}
```

For writing, the [jq runner from v0.3.0](https://github.com/dsx-ai-factory/workload-map/blob/bb13880e96e77087f074d6a04e5b6696762f6d3f/karta/sdk/pkg/jq/execution/runner.go) uses the same selection on the left of an assignment:

```jq
(.spec.predictor | ( (.[]? | select(type == "object" and .storageUri)))) = $val
```

`Assign` replaces the selected object.  
`$val` must therefore include the entire model, including `storageUri`, even when only the image changes.

CEL solves the same case by separating the jobs.  
`spec.variables` gives shared expressions a name so reads and writes can reuse them.  
Here, `containerKeys` finds the matching predictor keys.  
Both sides use `variables.containerKeys`: `read` returns the model, and `write` builds its location.  
The SDK applies the new value.  
This excerpt shows the proposed CRD fields:

```yaml
apiVersion: run.ai/v1alpha2
kind: Karta
metadata: {name: serving-kserve-io-inferenceservice-v1beta1}
spec:
  variables:
    - name: containerKeys
      expression: >-
        ([dyn(object[?"spec"][?"predictor"].orValue(null))]
          .filter(v, type(v) == map) + [{}])[0]
          .map(k, string(k)).sort().filter(k,
            type(object.spec.predictor[k]) == map &&
            "storageUri" in object.spec.predictor[k] &&
            object.spec.predictor[k]["storageUri"] != null &&
            object.spec.predictor[k]["storageUri"] != false)
  structureDefinition:
    rootComponent:
      name: inferenceservice
      kind: {group: serving.kserve.io, version: v1beta1, kind: InferenceService}
    childComponents:
      - name: predictor
        kind: {group: apps, version: v1, kind: Deployment}
        ownerRef: inferenceservice
        specDefinition:
          fragmentedPodSpecDefinition:
            container:
              read:
                matchExpression: variables.containerKeys.size() > 0
                expression: >-
                  variables.containerKeys.size() == 1 ?
                  object.spec.predictor[variables.containerKeys[0]] :
                  variables.containerKeys.map(k, object.spec.predictor[k])
              write:
                matchExpression: variables.containerKeys.size() == 1
                pathExpression: >-
                  "/spec/predictor/" +
                  variables.containerKeys[0].replace("~", "~0").replace("/", "~1")
```

`object` is the InferenceService, not its Deployment or Pod.  
`containerKeys` is `["model"]`.  
The read returns the model object; the write returns `/spec/predictor/model`.  
For a `sklearn` entry, the same definition returns `/spec/predictor/sklearn`.  
The two replacements escape a key as a JSON Pointer.

The caller supplies `{image: ghcr.io/example/inference:v2}` as a merge patch at that location.  
The result has the new image and the original `storageUri`.  
With no model, the field read returns `ErrValueNotFound` and writing fails.  
This `container` mapping requires exactly one matching model.  
If both `model` and `sklearn` match, the SDK returns an error for both reads and writes.

<details>
<summary>Full proposed CEL KServe Karta</summary>

This is the complete KServe definition using the proposed `run.ai/v1alpha2` API.  
Workload fields read the InferenceService.  
Selectors and grouping expressions read each Pod.

```yaml
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
apiVersion: run.ai/v1alpha2
kind: Karta
metadata:
  name: serving-kserve-io-inferenceservice-v1beta1
spec:
  variables:
    - name: containerKeys
      expression: >-
        ([dyn(object[?"spec"][?"predictor"].orValue(null))]
          .filter(v, type(v) == map) + [{}])[0]
          .map(k, string(k)).sort().filter(k,
            type(object.spec.predictor[k]) == map &&
            "storageUri" in object.spec.predictor[k] &&
            object.spec.predictor[k]["storageUri"] != null &&
            object.spec.predictor[k]["storageUri"] != false)
  structureDefinition:
    rootComponent:
      name: inferenceservice
      kind:
        group: serving.kserve.io
        version: v1beta1
        kind: InferenceService
      statusDefinition:
        conditionsDefinition:
          read:
            expression: object.?status.?conditions.orValue(null)
          typeFieldName: type
          statusFieldName: status
          messageFieldName: message
        statusMappings:
          running:
            - byConditions:
                - type: PredictorReady
                  status: "True"
                - type: RoutesReady
                  status: "True"
                - type: LatestDeploymentReady
                  status: "True"
          failed:
            - byConditions:
                - type: PredictorReady
                  status: "False"
                - type: PredictorConfigurationReady
                  status: "False"
                - type: RoutesReady
                  status: "False"
    childComponents:
      - name: predictor
        kind:
          group: apps
          version: v1
          kind: Deployment
        ownerRef: inferenceservice
        specDefinition:
          fragmentedPodSpecDefinition:
            schedulerName:
              read:
                expression: object.?spec.?predictor.?schedulerName.orValue(null)
              write:
                path: /spec/predictor/schedulerName
            labels:
              read:
                expression: object.?spec.?predictor.?labels.orValue(null)
              write:
                path: /spec/predictor/labels
            annotations:
              read:
                expression: object.?spec.?predictor.?annotations.orValue(null)
              write:
                path: /spec/predictor/annotations
            podAffinity:
              read:
                expression: object.?spec.?predictor.?affinity.?podAffinity.orValue(null)
              write:
                path: /spec/predictor/affinity/podAffinity
            nodeAffinity:
              read:
                expression: object.?spec.?predictor.?affinity.?nodeAffinity.orValue(null)
              write:
                path: /spec/predictor/affinity/nodeAffinity
            containers:
              read:
                expression: object.?spec.?predictor.?containers.orValue(null)
              write:
                path: /spec/predictor/containers
            container:
              read:
                matchExpression: variables.containerKeys.size() > 0
                expression: >-
                  variables.containerKeys.size() == 1 ?
                  object.spec.predictor[variables.containerKeys[0]] :
                  variables.containerKeys.map(k, object.spec.predictor[k])
              write:
                matchExpression: variables.containerKeys.size() == 1
                pathExpression: >-
                  "/spec/predictor/" +
                  variables.containerKeys[0].replace("~", "~0").replace("/", "~1")
            priorityClassName:
              read:
                expression: object.?spec.?predictor.?priorityClassName.orValue(null)
              write:
                path: /spec/predictor/priorityClassName
        scaleDefinition:
          minReplicas:
            read:
              expression: object.?spec.?predictor.?minReplicas.orValue(null)
            write:
              path: /spec/predictor/minReplicas
          maxReplicas:
            read:
              expression: object.?spec.?predictor.?maxReplicas.orValue(null)
            write:
              path: /spec/predictor/maxReplicas
        podSelector:
          componentTypeSelector:
            read:
              expression: object.?metadata.?labels[?"component"].orValue(null)
            value: predictor
      - name: transformer
        kind:
          group: apps
          version: v1
          kind: Deployment
        ownerRef: inferenceservice
        specDefinition:
          podSpec:
            read:
              expression: object.?spec.?transformer.orValue(null)
            write:
              path: /spec/transformer
          metadata:
            read:
              expression: object.?spec.?transformer.orValue(null)
            write:
              path: /spec/transformer
        scaleDefinition:
          minReplicas:
            read:
              expression: object.?spec.?transformer.?minReplicas.orValue(null)
            write:
              path: /spec/transformer/minReplicas
          maxReplicas:
            read:
              expression: object.?spec.?transformer.?maxReplicas.orValue(null)
            write:
              path: /spec/transformer/maxReplicas
        podSelector:
          componentTypeSelector:
            read:
              expression: object.?metadata.?labels[?"component"].orValue(null)
            value: transformer
  optimizationInstructions:
    gangScheduling:
      podGroups:
        - name: service
          members:
            - componentName: predictor
              groupByExpressions:
                - object.?metadata.?labels[?"serving.kserve.io/inferenceservice"].orValue(null)
            - componentName: transformer
              groupByExpressions:
                - object.?metadata.?labels[?"serving.kserve.io/inferenceservice"].orValue(null)
```

</details>

### CRD rules

A field has `read`, `write`, or both.  
`read.expression` returns its value.  
`write` has exactly one of `path` or `pathExpression`.  
A fixed path is a JSON Pointer ([RFC 6901](https://www.rfc-editor.org/rfc/rfc6901)) such as `/spec/predictor/schedulerName`; a path expression returns one.  
`path: ""` selects the workload root, as needed for a standalone Pod's `podTemplateSpec`.  
Without `write`, the SDK rejects mutation requests for that field.  
Omit it for fields that are immutable or should only be read.

Each side evaluates its own `matchExpression` first.  
Omission means true.  
A false read match skips the read expression and returns `ErrValueNotFound` for that field.  
Whole-workload reads skip fields with this error.  
A false write match returns an error before changing anything.  
A match must return a boolean; errors never mean false.  
CEL conditional expressions handle alternative reads or locations.

| Current API | Proposed API |
| --- | --- |
| `containerPath`, `podSpecPath`, `replicasPath`, other value paths | `container`, `podSpec`, `replicas`, each with `read`, `write`, or both |
| `instanceIdPath` | `instanceIds.read.expression`; read-only |
| Condition `path` and selector `keyPath`/`idPath` | `read.expression` inside the same definition or selector |
| `groupByKeyPaths` | `groupByExpressions`; evaluated separately for each Pod |
| Status matcher `byExpression.expression` | CEL boolean: `true` matches the status; remove `expectedResult` |
| Suspend/resume action `path` and JSON `value` | Action `write` location and the same JSON `value` |
| No shared expressions | `spec.variables[{name, expression}]` |
| No external inputs | `spec.structureDefinition.references` |

Component kinds, ownership and scheduling structure retain their current shape.  
Workload reads and shared variables use the root workload as `object`.  
Pod selectors and grouping expressions run separately for each Pod, with that Pod as `object`.  
They do not receive shared variables or references.  
Reference recipes see only the root workload.

<details>
<summary>Proposed CRD schema changes</summary>

This excerpt shows the proposed `run.ai/v1alpha2` CRD schema.  
It is not an installable CRD: unchanged fields and the full reference-list selector schema are omitted.  
`podTemplateSpec` shows the `read`/`write` shape used by the other value accessors.

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: kartas.run.ai
spec:
  group: run.ai
  names:
    kind: Karta
    plural: kartas
    singular: karta
  scope: Cluster
  versions:
    - name: v1alpha2
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              required: [structureDefinition]
              properties:
                variables:
                  type: array
                  x-kubernetes-list-type: map
                  x-kubernetes-list-map-keys: [name]
                  items:
                    type: object
                    required: [name, expression]
                    properties:
                      name: {type: string, minLength: 1}
                      expression: {type: string, minLength: 1}
                structureDefinition:
                  type: object
                  required: [rootComponent]
                  properties:
                    references:
                      type: array
                      x-kubernetes-list-type: map
                      x-kubernetes-list-map-keys: [name]
                      items:
                        type: object
                        required: [name, apiVersion, kind]
                        x-kubernetes-validations:
                          - rule: has(self.lookup) != has(self.list)
                            message: Set exactly one of lookup or list.
                        properties:
                          name: {type: string, minLength: 1}
                          apiVersion: {type: string, minLength: 1}
                          kind: {type: string, minLength: 1}
                          lookup:
                            type: object
                            required: [nameExpression]
                            properties:
                              nameExpression: {type: string, minLength: 1}
                          list:
                            type: object
                            # Selector properties omitted; see note below.
                    rootComponent:
                      type: object
                      required: [name]
                      properties:
                        name: {type: string, minLength: 1}
                        specDefinition:
                          type: object
                          properties:
                            podTemplateSpec:
                              type: object
                              x-kubernetes-validations:
                                - rule: has(self.read) || has(self.write)
                                  message: Set read, write, or both.
                              properties:
                                read:
                                  type: object
                                  required: [expression]
                                  properties:
                                    matchExpression: {type: string, minLength: 1}
                                    expression: {type: string, minLength: 1}
                                write:
                                  type: object
                                  x-kubernetes-validations:
                                    - rule: has(self.path) != has(self.pathExpression)
                                      message: Set exactly one of path or pathExpression.
                                  properties:
                                    matchExpression: {type: string, minLength: 1}
                                    path:
                                      type: string
                                      description: JSON Pointer; an empty string selects the root.
                                    pathExpression: {type: string, minLength: 1}
```

Read and write have separate optional match expressions.  
An omitted match means true; `path: ""` remains a valid write location.  
Reference lists use `matchLabels` and `matchExpressions`, with literal `value` or CEL `expression` entries.  
Their full schema is not shown here.  
The webhook and local loader compile expressions, check their available inputs, and reject variable dependency cycles.  
They also check fixed JSON Pointer syntax; the SDK checks computed pointers after evaluation.

</details>

### Pod grouping and scheduling

Grouping chooses which Pods belong together.  
Gang scheduling asks a consuming scheduler to admit a whole group together.

The [LeaderWorkerSet Karta from v0.3.0](https://github.com/dsx-ai-factory/workload-map/blob/bb13880e96e77087f074d6a04e5b6696762f6d3f/docs/catalog/leaderworkerset-x-k8s-io-leaderworkerset-v1.yaml#L76-L84) groups Pods by workload name and group index.  
The scheduling structure stays the same.  
The grouping expressions change from jq to CEL:

```diff
 spec:
   optimizationInstructions:
     gangScheduling:
       podGroups:
         - name: group
           members:
             - componentName: group
-              groupByKeyPaths:
-                - .metadata.labels["leaderworkerset.sigs.k8s.io/name"]
-                - .metadata.labels["leaderworkerset.sigs.k8s.io/group-index"] // "0"
+              groupByExpressions:
+                - object.metadata.labels["leaderworkerset.sigs.k8s.io/name"]
+                - object.metadata.labels[?"leaderworkerset.sigs.k8s.io/group-index"].orValue("0")
```

The SDK evaluates both expressions for each Pod, with that Pod as `object`.  
For four Pods, the two label values produce these keys:

| Pod | Workload name label | Group index label | Grouping key |
| --- | --- | --- | --- |
| `leader-0` | `demo` | `"0"` | `["demo", "0"]` |
| `worker-0` | `demo` | `"0"` | `["demo", "0"]` |
| `leader-1` | `demo` | `"1"` | `["demo", "1"]` |
| `worker-1` | `demo` | `"1"` | `["demo", "1"]` |

Matching keys form two groups: `leader-0` with `worker-0`, and `leader-1` with `worker-1`.  
The consuming scheduler admits each group as a gang.

### Status mapping

A status read maps a workload's status to a Karta status.  
Here, `object` is this RayJob:

```yaml
apiVersion: ray.io/v1
kind: RayJob
metadata: {name: train, namespace: team-a}
status:
  jobDeploymentStatus: Suspended
```

The [RayJob Karta from v0.3.0](https://github.com/dsx-ai-factory/workload-map/blob/bb13880e96e77087f074d6a04e5b6696762f6d3f/docs/catalog/ray-io-rayjob-v1.yaml#L35-L38) changes as follows:

```diff
 spec:
   structureDefinition:
     rootComponent:
       statusDefinition:
         statusMappings:
           suspended:
             - byExpression:
-                expression: .status.jobDeploymentStatus == "Suspended"
-                expectedResult: "true"
+                expression: object.?status.?jobDeploymentStatus.orValue("") == "Suspended"
```

The CEL expression returns `true`, so the SDK matches Karta's `suspended` status.  
A different or missing deployment status returns `false`, so this rule does not match.  
`expectedResult` is no longer needed because the expression returns a boolean.

### Trainer v2: read through a reference

These trimmed objects show why a read can need more than the workload:

```yaml
apiVersion: trainer.kubeflow.org/v1alpha1
kind: TrainJob
metadata: {name: train, namespace: team-a}
spec:
  runtimeRef: {name: torch}
---
apiVersion: trainer.kubeflow.org/v1alpha1
kind: ClusterTrainingRuntime
metadata: {name: torch}
spec:
  template:
    spec:
      replicatedJobs:
        - name: node
          template:
            metadata:
              labels: {trainer.kubeflow.org/trainjob-ancestor-step: trainer}
            spec:
              template:
                spec:
                  containers:
                    - {name: node, image: 'ghcr.io/example/trainer:v1'}
```

The Karta declares the lookup and reads the effective image:

```yaml
# Under spec.structureDefinition.
references:
  - name: trainingRuntime
    apiVersion: trainer.kubeflow.org/v1alpha1
    kind: ClusterTrainingRuntime
    lookup:
      nameExpression: object.spec.runtimeRef.name
rootComponent:
  name: trainjob
  kind: {group: trainer.kubeflow.org, version: v1alpha1, kind: TrainJob}
  specDefinition:
    fragmentedPodSpecDefinition:
      image:
        read:
          expression: >-
            object.?spec.?trainer.?image.orValue(
              references.trainingRuntime.spec.template.spec.replicatedJobs
                .filter(j, j.?template.?metadata.?labels
                  ["trainer.kubeflow.org/trainjob-ancestor-step"]
                  .orValue("") == "trainer")[0]
                .template.spec.template.spec.containers
                .filter(c, c[?"name"].orValue("") == "node")[0].image)
```

The read returns `ghcr.io/example/trainer:v1`.  
Adding `spec.trainer.image: ghcr.io/example/trainer:v2` makes it return that override.  
The runtime lookup uses the trainer label and container name, so an initializer or sidecar does not become the trainer.  
This field has no write location.

A reference declares exactly one lookup or label-selector list.  
A lookup names one object; a list returns matching objects.  
Namespaced targets use the workload's namespace.  
Cluster-scoped targets have no namespace.  
Lookup misses stay unbound; empty lists bind `[]`.  
Read failures are errors, not missing objects.  
References are read-only, whether a reader fetches them or the caller supplies them.

## Compatibility and migration

Karta definitions move from `run.ai/v1alpha1` (jq) to `run.ai/v1alpha2` (CEL).  
This is a breaking change for definitions and Go consumers.  
Existing jq definitions are not converted automatically.

The CEL release will include CEL versions of every catalog definition.  
To install the full catalog in the cluster, enable this chart option:

```yaml
catalogInstaller:
  enabled: true
```

The option defaults to `false`.  
The installer applies the definitions bundled with that chart release after the new CRD and webhook are ready.  
It rejects conflicts with custom definitions.

The [add-workload-type skill](../../../skills/add-workload-type/SKILL.md) will also support CEL.  
Use it to create and validate CEL replacements for custom Karta definitions.

Back up existing definitions before the coordinated CRD and SDK upgrade.  
Helm rollback alone cannot restore the old definitions.

## Test plan

The CEL release will include:

- Replay tests for every converted catalog definition, confirming that existing recordings map to the same Karta statuses as jq.
- Mutation parity tests for every supported write mapping.  
  Equivalent jq and CEL updates must produce the same workload object, including unchanged fields.
- New recordings and replay tests for grouping keys, Pod group membership, and scheduling requirements.

Release tests will also cover independent read/write matches, live and manual references, schema validation, and admission.  
Catalog installation, partial failures, retries, and migration in both directions will be covered before release.

## Risks and alternatives

Separate expressions can read and write different fields; catalog fixtures must check both.  
References can become stale between Kubernetes reads; the consumer creates a new SDK snapshot on its next observation.  
Karta SDK options configure the CEL cost limit and evaluation timeout.  
Evaluation also respects cancellation from the caller's context.

Keep one SDK format initially: [JSON Merge Patch](https://www.rfc-editor.org/rfc/rfc7396).  
It covers the model image, labels, resources and scalar values in these examples.  
Null inside a map patch deletes a key; arrays replace in full.  
JSON Patch can be added for a demonstrated need to store literal null or edit a list by position.  
Keeping jq alongside CEL would require maintaining and testing both expression contracts.

## History

- 2026-11-15: Proposal opened.  
  CEL writes and Trainer references have separate prototypes.  
  This combined CRD and SDK contract is proposed; see the [implementation evidence](../../design/cel-expressions/README.md#implementation-evidence).