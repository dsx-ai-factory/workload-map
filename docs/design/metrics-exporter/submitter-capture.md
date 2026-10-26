<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Submitter capture

The chart can record who created a workload. See
[KEP-0001](../../../keps/0001-metrics-exporter/README.md#who-submitted-the-workload)
for the attribution contract. A `MutatingAdmissionPolicy` (MAP)
writes the authenticated caller into an annotation on CREATE. The exporter reads
it later through `submittedByExpression`. Capture runs in the API server, so it
needs neither the operator nor the exporter.

## Policy

`charts/karta/templates/admission/submitter-capture.yaml` renders one MAP and
one binding when `admissionPolicies.submitter.enabled` is true.

```yaml
spec:
  failurePolicy: Ignore
  reinvocationPolicy: Never
  matchConstraints:
    namespaceSelector: {} # From namespaceSelector.
    objectSelector: {} # From objectSelector.
    resourceRules: [] # From resources, with operations ["CREATE"] and scope Namespaced.
    excludeResourceRules: [] # From excludeResources, when set.
  matchConditions:
    - name: karta-submitter-unset
      expression: '!has(object.metadata.annotations) || !("<key>" in object.metadata.annotations)'
    - name: karta-submitter-root
      expression: '!has(object.metadata.ownerReferences) || !object.metadata.ownerReferences.exists(r, has(r.controller) && r.controller)'
    - name: karta-submitter-fits
      expression: '(has(object.metadata.annotations) ? object.metadata.annotations.map(k, size(bytes(k)) + size(bytes(object.metadata.annotations[k]))).sum() : 0) + size(bytes("<key>")) + size(bytes(request.userInfo.username)) <= 262144'
    # User matchConditions follow.
  mutations:
    - patchType: ApplyConfiguration
      applyConfiguration:
        expression: 'Object{metadata: Object.metadata{annotations: {"<key>": request.userInfo.username}}}'
```

`<key>` is `admissionPolicies.submitter.annotation`, `karta.run.ai/submitted-by`
by default.

- CREATE only. Users list group, version and resource. The chart sets operations
  and scope, so a user cannot widen capture to UPDATE.
- A supplied value is kept. The `karta-submitter-unset` condition skips requests
  that already carry the key.
- Workload roots only. The `karta-submitter-root` condition skips objects with a
  controller `ownerReference`. A Pod created by a Job controller is not stamped
  with the controller's identity.
- Within the annotation size limit. Kubernetes caps all annotation keys and
  values at 262144 bytes, and checks it after mutation. The
  `karta-submitter-fits` condition skips objects the new annotation would push
  over the limit. It counts bytes, not characters, so multibyte values are
  measured correctly.
- Best effort. `failurePolicy: Ignore` admits the request unchanged when the
  policy is unavailable or a CEL expression fails to evaluate. It does not cover
  an object that fails validation after mutation, which is why the size check
  is a condition.
- Upgrade safe. The template defaults `admissionPolicies` and its `submitter`
  map, so `helm upgrade --reuse-values` from a chart without them renders
  nothing instead of failing.
- One evaluation per request. `reinvocationPolicy: Never`, since no other
  mutation removes the annotation.
- Server-side apply keeps the value. The mutation is not owned by the applying
  field manager, so a later apply that omits the annotation does not remove it.

## Render checks

The chart fails `helm install` and `helm template` instead of shipping a policy
the API server would reject or that would capture too much.

| Check | Error names |
| --- | --- |
| `resources` empty when enabled | `admissionPolicies.submitter.resources` |
| A rule key other than `apiGroups`, `apiVersions`, `resources` | the rule index and key |
| An empty rule field | the rule index and field |
| A subresource such as `jobs/status` | the rule index and resource |
| A `matchConditions` name starting with `karta-` | the condition index and name |
| `annotation` empty or not a valid annotation key | the key |
| `apiVersion` other than `v1` or `v1beta1` | the value |

The annotation key is spliced into CEL string literals, so the key check also
keeps quotes out of the expressions.

## API version

MAP is v1 on Kubernetes 1.36+. On 1.34-1.35 it is v1beta1 behind
`--feature-gates=MutatingAdmissionPolicy=true` and
`--runtime-config=admissionregistration.k8s.io/v1beta1=true`.

With `apiVersion` empty, the chart reads `.Capabilities.APIVersions`. It prefers
v1, falls back to v1beta1, and fails when neither is served. `helm template` and
GitOps renderers see no cluster APIs, so they must set `apiVersion`.

## Platform submissions

The policy records the authenticated Kubernetes caller. When a platform creates
the workload through its own service account, the annotation holds that service
account, for example `system:serviceaccount:<namespace>:<name>`. Objects the
platform's controllers create below the root are skipped.

Platforms usually record the human user on the root object themselves. The
exporter's `submittedByExpression` can prefer that annotation and fall back to
the captured one:

```yaml
attribution:
  submittedByExpression: |
    ("example.com/workload-owner" in annotations)
      ? annotations["example.com/workload-owner"]
      : ("karta.run.ai/submitted-by" in annotations
          ? annotations["karta.run.ai/submitted-by"]
          : "")
```

## Alternatives not taken

- Rejecting a supplied value with a `ValidatingAdmissionPolicy`. It would turn a
  metrics hint into a gate on every submission. A platform that sets the real
  user on objects it creates would be rejected.
- Making the annotation immutable on UPDATE. It needs a policy on every update
  of the listed types, including `status` and other subresources that accept
  metadata changes. On large clusters that cost is paid by every controller.
- Overwriting a supplied value with the caller. A platform's service account
  would replace the user it recorded.
- Writing a label instead of an annotation. Label values reject `:` and `@`, so
  service accounts and most OIDC users could not be recorded. An invalid label
  fails object validation after the policy runs, which rejects the request.
- Defaulting an empty `resources` list to `*`. Every root object created in a
  selected namespace would be annotated, including Events, Secrets and
  ConfigMaps. The API server rejects an empty list, so the chart requires one.

## Testing

`operator/test/admission` renders the chart with the `helm` binary and installs
the policy into an envtest API server at Kubernetes 1.36.2. It covers users and
service accounts, supplied values, selectors, exclusions, CEL conditions and
errors, controller-owned objects, server-side apply, a custom annotation key,
the annotation size limit, render errors, API version detection and rendering
without submitter values. `make check` runs it through `test-operator`, so it
needs `helm` on the PATH.
