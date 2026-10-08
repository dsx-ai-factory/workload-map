# Karta Anatomy
## Root Component
Every Karta must define a root component:
- Must use full Kubernetes GVK (Group, Version, Kind)
- Must include a `statusDefinition`

```YAML
rootComponent:
  name: "pytorchjob"
  kind:
    group: "kubeflow.org"
    version: "v1"
    kind: "PyTorchJob"
  statusDefinition:
    statusMappings:
      running:
      - byConditions:
        - type: "Running"
          status: "True"
```

## Child Components
For resources owned by the root component:
- Must include `ownerRef` (points to parent component)
- Usually include a `specDefinition`
- All paths must be absolute from the CRD root

```YAML
childComponents:
- name: "worker"
  ownerRef: "pytorchjob"
  kind:
    group: "apps"
    version: "v1"
    kind: "StatefulSet"
  specDefinition:
    podTemplateSpecPath: ".spec.pytorchReplicaSpecs.Worker.template"
```

## Paths
All paths provided in a Karta are written in [jq](https://jqlang.org/) syntax. The jq query language provides both path navigation and various query capabilities, and is widely used in the k8s ecosystem. 
Make sure to provide a matching jq query type for every property (path/query), and to provide default values where necessary.


## Spec Definitions

There are multiple, mutually exclusive,  types of specDefinitions:

| Pattern | Use Case | Example |
| ------- | -------- | ------- |
| PodTemplateSpecPath | CRD embeds a full pod template | .spec.pytorchReplicaSpecs.Master.template |
| PodSpecPath & MetadataPath | CRD directly embeds a podSpec and/or an objectMeta | .spec.jobTemplate.spec .spec.jobTemplate.metadata |
| FragmentedPodSpecDefinition | Pod fields scattered across CRD | See below |

```YAML
specDefinition:
  fragmentedPodDefinition:
    labelsPath: ".spec.labels"
    annotationsPath: ".spec.annotations"
    resourcesPath: ".spec.resources"
    schedulerNamePath: ".spec.schedulerName"
```

## Component Instances

Component’s spec definition might point to multiple instances specs (in map/array format). In those cases it’s crucial to be able to distinguish between each instance of that component.
In order to do so, users must define an instanceIdPath - the location in the spec where we can find the name of each instance.
For example:

1. Array of specs:

```YAML
instanceIdPath: ".spec.jobs[].name"
```

2. Map of specs: (extract the map keys that represent the instance id)

```YAML
instanceIdPath: ".spec.jobs | to_entries[] | .key"
```


## Pod Selectors
PodSelector defines how to identify pods belonging to a component / an instance of a component.
A component can define both type and instance selectors.
All selectors of each kind (component, instance) must be mutually exclusive within themselves.

**Paths in pod selectors are referring to paths on the pod’s manifest**

1. Component type selector - key (and value) selector that associates pod to the current component.
```YAML
podSelector:
  componentTypeSelector:
    keyPath: '.metadata.labels["training.kubeflow.org/replica-type"]'
    value: "master"
```
If value is not provided, only key existence is checked.

When one label is not enough to identify the pods, use `matchLabels`. Each entry is an equality requirement on a pod label, and all entries must match.
```YAML
podSelector:
  componentTypeSelector:
    matchLabels:
      app: "pulsar"
      component: "proxy"
```
`keyPath` and `matchLabels` can be combined, and the pod must then satisfy both. At least one of them must be set, and `value` requires `keyPath`.

2. Component instance selector - a path on the pod that holds its matching instance id.

```YAML
podSelector:
  componentInstanceSelector:
    idPath: '.metadata.labels["jobset.sigs.k8s.io/replicatedjob-name"]'
```

## Status Definitions
- Mapping the described CRD’s conditions or phases to the Karta generic statuses. Must be based on the actual conditions/phases used by the described CRD.
- For each generic status, the user can provide a definition based on conditions or phases. If both are provided, both are validated when evaluating the status.
- If using definition by conditions/phase, you first must include `conditionsDefinition` / `phaseDefinition`
- Multiple, separate, definitions can be provided for each generic status.
- When providing a definition `byConditions`, all must exist (AND logic)
- Required for the root component

```YAML
statusDefinition:
        conditionsDefinition:
          path: ".status.conditions"
          typeFieldName: "type"
          statusFieldName: "status"
        statusMappings:
          initializing:
          - byConditions:
            - type: "Created"
              status: "True"
            - type: "Running"
              status: "False"
          running:
          - byConditions:
            - type: "Running"
              status: "True"
            - type: "Succeeded"
              status: "False"
            - type: "Failed"
              status: "False"
          completed:
          - byConditions:
            - type: "Succeeded"
              status: "True"
          failed:
          - byConditions:
            - type: "Failed"
              status: "True"
```

## Additional child kinds
List any GVK of objects created or managed by the CRD that are not mentioned explicitly by any child component.
This is essential for permission management so that your CRD can be managed correctly.

```YAML
  additionalChildKinds:
   - group: apps
     version: v1
     kind: Deployment
   - group: leaderworkerset.x-k8s.io
     version: v1
     kind: LeaderWorkerSet
```

## Optimization Instructions
Used for scheduling.

**Paths in the instruction are referring to paths on the pod’s yaml/json**

Currently supported instructions:

- `gangScheduling`: instruct the scheduler how to group pods. Each pod-group definition contains a list of the included members.
Each defined member can provide a list of distinct keys to group pods by and a list of filters to determine which pods should be included.

```YAML
optimizationInstructions:
  gangScheduling:
    podGroups:
    - name: "job"
        members:
        - componentName: "master"
          groupByKeyPaths: 
          - '.metadata.labels["training.kubeflow.org/job-name"]'
        - componentName: "worker"
          groupByKeyPaths:
          - '.metadata.labels["training.kubeflow.org/job-name"]'
```

Grouping examples:

1. Different hierarchy: The following are equivalent (given that master and worker has the same value for that label)

```YAML
optimizationInstructions:
  gangScheduling:
    podGroups:
    - name: "job"
        members:
        - componentName: "master"
          groupByKeyPaths: 
          - '.metadata.labels["training.kubeflow.org/job-name"]'
        - componentName: "worker"
          groupByKeyPaths:
          - '.metadata.labels["training.kubeflow.org/job-name"]'

optimizationInstructions:
  gangScheduling:
    podGroups:
    - name: "job"
        members:
        - componentName: "job"
          groupByKeyPaths: 
          - '.metadata.labels["training.kubeflow.org/job-name"]'
```

2. Using default values: when multiple groups are possible, use default value to cover cases where single group is used (the used pattern: <prefix>-{name}-{index})

```YAML
optimizationInstructions:
   gangScheduling:
     podGroups:
     - name: "group"
       members:
       - componentName: "group"
         groupByKeyPaths:
         - '.metadata.labels["leaderworkerset.sigs.k8s.io/name"]'
         - '.metadata.labels["leaderworkerset.sigs.k8s.io/group-index"] // "0"'


3. Filters: when different components are not named components (array/map of specs in the same component) you can use filters to form different pod groups.
In this example, for a CRD that defines multiple jobs but all under the “job” component, we use jq query as filter to identify pods that use Nvidia GPUs, and queries with hard-coded values as grouping keys.

optimizationInstructions:
  gangScheduling:
    podGroups:
    - name: "gpu-jobs"
        members:
        - componentName: "job"
          filters:
          - 'any(.spec.jobs[].spec.containers[]; (.resources.limits["nvidia.com/gpu"] // 0) > 0)'
          groupByKeyPaths: 
          - 'gpu'
    - name: "no-gpu-jobs"
        members:
        - componentName: "job"
          filters:
          - 'any(.spec.jobs[].spec.containers[]; (.resources.limits["nvidia.com/gpu"] // 0) == 0)'
          groupByKeyPaths: 
          - 'no-gpu'
```

## Best Practices
- Always use full GVK for kinds (group, version, kind)
- Absolute paths only in spec definitions
- Avoid duplication: don’t list explicitly defined components in `additionalChildKinds`
- Mutually exclusive pod selectors in multi-component workloads
- Null-safe JQ expressions (e.g. `// 0` defaults)
- Target actual components in optimization instructions, not just the root CRD

## Examples
Example 1: Kserve inference service
```YAML
{
  "spec": {
    "structureDefinition": {
      "rootComponent": {
        "name": "inferenceservice",
        "kind": {
          "group": "serving.kserve.io",
          "version": "v1beta1",
          "kind": "InferenceService"
        },
        "specDefinition": {
          "fragmentedPodSpecDefinition": {
            "resourcesPath": ".spec.domain.resources",
            "priorityClassNamePath": ".spec.priorityClassName",
            "nodeAffinityPath": ".spec.affinity.nodeAffinity"
          }
        },
        "statusDefinition": {
          "conditionsDefinition": {
            "path": ".status.conditions",
            "typeFieldName": "type",
            "statusFieldName": "status"
          },
          "statusMappings": {
            "running": [
              {
                "byConditions": [
                  {
                    "type": "PredictorReady",
                    "status": "True"
                  },
                  {
                    "type": "RoutesReady",
                    "status": "True"
                  },
                  {
                    "type": "LatestDeploymentReady",
                    "status": "True"
                  }
                ]
              }
            ],
            "failed": [
              {
                "byConditions": [
                  {
                    "type": "PredictorReady",
                    "status": "False"
                  },
                  {
                    "type": "PredictorConfigurationReady",
                    "status": "False"
                  },
                  {
                    "type": "RoutesReady",
                    "status": "False"
                  }
                ]
              }
            ]
          }
        }
      },
      "childComponents": [
        {
          "name": "predictor",
          "kind": {
            "group": "apps",
            "version": "v1",
            "kind": "Deployment"
          },
          "ownerRef": "inferenceservice",
          "specDefinition": {
            "podSpecPath": ".spec.predictor",
            "metadataPath": ".spec.predictor",
            "fragmentedPodSpecDefinition": {
              "containerPath": ".spec.predictor.model"
            }
          },
          "scaleDefinition": {
            "minReplicasPath": ".spec.predictor.minReplicas",
            "maxReplicasPath": ".spec.predictor.maxReplicas"
          },
          "podSelector": {
            "componentTypeSelector": {
              "keyPath": ".metadata.labels[\"component\"]",
              "value": "predictor"
            }
          }
        },
        {
          "name": "transformer",
          "kind": {
            "group": "apps",
            "version": "v1",
            "kind": "Deployment"
          },
          "ownerRef": "inferenceservice",
          "specDefinition": {
            "podSpecPath": ".spec.transformer",
            "metadataPath": ".spec.transformer"
          },
          "scaleDefinition": {
            "minReplicasPath": ".spec.transformer.minReplicas",
            "maxReplicasPath": ".spec.transformer.maxReplicas"
          },
          "podSelector": {
            "componentTypeSelector": {
              "keyPath": ".metadata.labels[\"component\"]",
              "value": "transformer"
            }
          }
        }
      ]
    },
    "optimizationInstructions": {
      "gangScheduling": {
        "podGroups": [
          {
            "name": "service",
            "members": [
              {
                "componentName": "predictor",
                "groupByKeyPaths": [
                  ".metadata.labels[\"serving.kserve.io/inferenceservice\"]"
                ]
              },
              {
                "componentName": "transformer",
                "groupByKeyPaths": [
                  ".metadata.labels[\"serving.kserve.io/inferenceservice\"]"
                ]
              }
            ]
          }
        ]
      }
    }
  }
}
```

## Validation Checklist
Before submitting, confirm:

- All kinds use full GVK
- root component has statusDefinition
- All JQ paths are absolute and null-safe (and are paths in the correct resource - root CRD/pod)
- No duplicated child kinds
- Pod selectors are mutually exclusive
- Status conditions match real framework APIs
- All child component has ownerRef directed to existing components and there are no ownership cycles


## Quick-Start Templates
Use these as starting points when creating new workload type definitions.
👉 With these templates, you can register new workload types by simply filling in the blanks instead of starting from scratch.

### Template: Generic Job
A single-component workload with pods defined directly in its spec.

```YAML
rootComponent:
  name: "job"
  kind:
    group: "batch"
    version: "v1"
    kind: "Job"
  statusDefinition:
    statusMappings:
      running:
      - byConditions:
        - type: "Running"
          status: "True"
      completed:
      - byConditions:
        - type: "Complete"
          status: "True"
      failed:
      - byConditions:
        - type: "Failed"
          status: "True"

  specDefinition:
    podTemplateSpecPath: ".spec.template"
```

### Template: Deployment
A controlling resource with generated ReplicaSets.

```YAML
rootComponent:
  name: "deployment"
  kind:
    group: "apps"
    version: "v1"
    kind: "Deployment"
  statusDefinition:
    statusMappings:
      running:
      - byConditions:
        - type: "Available"
          status: "True"

childComponents:
- name: "replicaset"
  ownerRef: "deployment"
  kind:
    group: "apps"
    version: "v1"
    kind: "ReplicaSet"
  specDefinition:
    podTemplateSpecPath: ".spec.template"
```

### Template: Distributed Training (PyTorchJob)
Multi-component workload with role-based pods.
```YAML
rootComponent:
  name: "pytorchjob"
  kind:
    group: "kubeflow.org"
    version: "v1"
    kind: "PyTorchJob"
  statusDefinition:
    statusMappings:
      running:
      - byConditions:
        - type: "Running"
          status: "True"
      completed:
      - byConditions:
        - type: "Succeeded"
          status: "True"
      failed:
      - byConditions:
        - type: "Failed"
          status: "True"

childComponents:
- name: "master"
  ownerRef: "pytorchjob"
  kind:
    group: "apps"
    version: "v1"
    kind: "ReplicaSet"
  specDefinition:
    podTemplateSpecPath: ".spec.pytorchReplicaSpecs.Master.template"
  podSelector:
    componentTypeSelector:
      keyPath: '.metadata.labels["training.kubeflow.org/replica-type"]'
      value: "master"

- name: "worker"
  ownerRef: "pytorchjob"
  kind:
    group: "apps"
    version: "v1"
    kind: "ReplicaSet"
  specDefinition:
    podTemplateSpecPath: ".spec.pytorchReplicaSpecs.Worker.template"
  podSelector:
    componentTypeSelector:
      keyPath: '.metadata.labels["training.kubeflow.org/replica-type"]'
      value: "worker"
```

### Template: Inference Service (Knative)
Workload that owns a secondary component (Revision).

```YAML
rootComponent:
  name: "service"
  kind:
    group: "serving.knative.dev"
    version: "v1"
    kind: "Service"
  statusDefinition:
    statusMappings:
      running:
      - byConditions:
        - type: "Ready"
          status: "True"

childComponents:
- name: "revision"
  ownerRef: "service"
  kind:
    group: "serving.knative.dev"
    version: "v1"
    kind: "Revision"
  specDefinition:
    podTemplateSpecPath: ".spec.template"
```

## Minimum requirements for defining a Karta

The minimal Karta must contain a rootComponent with name, full GVK (group, version, kind) and statusDefinition. 

For example:
```YAML
rootComponent:
  name: "minimal"
  kind:
    group: "minimal.org"
    version: "v1"
    kind: "Minimal"
  statusDefinition:
    statusMappings:
      running:
      - byConditions:
        - type: "Running"
          status: "True"
```

## Pro Tips
- Start from the closest template to your workload type.

- Replace the GVK (group, version, kind) with your CRD’s.

- Verify status conditions in the CRD source code or documentation.

- Add child/referenced components only if they matter for scheduling or optimization.
