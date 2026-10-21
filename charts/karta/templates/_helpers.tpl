
{{/*
Fully qualified resource name: "<chart-name>-operator" (e.g. "karta-operator").
Override wholesale via fullnameOverride.
*/}}
{{- define "karta.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-operator" .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{/*
Common labels stamped onto every resource.
*/}}
{{- define "karta.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "karta.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Selector labels. These must be stable across upgrades, so they intentionally
exclude version/chart labels (which change between releases).
*/}}
{{- define "karta.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Name of the ServiceAccount to use. When create=true the chart owns the name
(karta.fullname). When create=false the caller must supply serviceAccount.name.
*/}}
{{- define "karta.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- include "karta.fullname" . -}}
{{- else -}}
{{- required "serviceAccount.name is required when serviceAccount.create is false" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Name for the CRD upgrader hook resources. Includes the release namespace and
name so separate releases never share the cluster-scoped ClusterRole/Binding.
*/}}
{{- define "karta.crdUpgrader.name" -}}
{{- printf "%s-crd-upgrader-%s-%s" (include "karta.fullname" .) .Release.Namespace .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Webhook resource names. The service name doubles as the serving cert SAN, so it
must match the --webhook-service-name flag passed to the operator.
*/}}
{{- define "karta.webhook.serviceName" -}}
{{- printf "%s-webhook" (include "karta.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "karta.webhook.secretName" -}}
{{- printf "%s-webhook-cert" (include "karta.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "karta.webhook.configName" -}}
{{- printf "%s-mutating" (include "karta.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "karta.webhook.validatingConfigName" -}}
{{- printf "%s-validating" (include "karta.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Resolves and validates global.fipsMode, returning "off", "on", or "only". The
value lives under global rather than scoped to this chart, so an umbrella
chart embedding karta as a dependency can drive it with one flag alongside
its other components. An unquoted on/off/yes/no/true/false in values.yaml
parses as a YAML 1.1 boolean, so that case is coerced before validating.
Included from both deployment.yaml and crd-upgrader-job.yaml, since
deployment.yaml renders nothing when operator.enabled=false (CRD-only
install) and crd-upgrader-job.yaml reads fipsMode independently of that flag.
*/}}
{{- define "karta.fipsMode" -}}
{{- $fipsMode := (.Values.global).fipsMode | default "off" -}}
{{- if kindIs "bool" $fipsMode -}}
  {{- $fipsMode = ternary "on" "off" $fipsMode -}}
{{- end -}}
{{- if not (has $fipsMode (list "off" "on" "only")) -}}
  {{- fail (printf "global.fipsMode must be one of: off, on, only (got %v)" $fipsMode) -}}
{{- end -}}
{{- $fipsMode -}}
{{- end -}}

{{- define "karta.exporter.fullname" -}}
{{ printf "%s-metrics-exporter" (.Values.fullnameOverride | default .Chart.Name | trunc 46 | trimSuffix "-") }}
{{- end -}}

{{/* Exporter labels use a distinct name to avoid matching operator selectors. */}}
{{- define "karta.exporter.selectorLabels" -}}
app.kubernetes.io/name: {{ printf "%s-metrics-exporter" .Chart.Name | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Submitter admission policy names. The policies are cluster-scoped, so like the
webhook configurations they follow karta.fullname.
*/}}
{{- define "karta.submitter.captureName" -}}
{{- printf "%s-submitter-capture" (include "karta.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "karta.submitter.createName" -}}
{{- printf "%s-submitter-create" (include "karta.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "karta.submitter.immutableName" -}}
{{- printf "%s-submitter-immutable" (include "karta.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fails unless the cluster serves ValidatingAdmissionPolicy v1. An explicit
admissionPolicies.submitter.apiVersion skips detection, since helm template and
GitOps renderers see no cluster APIs.
*/}}
{{- define "karta.submitter.requireVAP" -}}
{{- if not (or .Values.admissionPolicies.submitter.apiVersion (.Capabilities.APIVersions.Has "admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy")) -}}
  {{- fail "admissionPolicies.submitter requires ValidatingAdmissionPolicy v1 (Kubernetes 1.30+); set admissionPolicies.submitter.apiVersion when rendering without a cluster" -}}
{{- end -}}
{{- end -}}

{{/*
Resolves the MutatingAdmissionPolicy API version, detecting it from the cluster
unless admissionPolicies.submitter.apiVersion is set.
*/}}
{{- define "karta.submitter.mapAPIVersion" -}}
{{- include "karta.submitter.requireVAP" . -}}
{{- $v := .Values.admissionPolicies.submitter.apiVersion -}}
{{- $caps := .Capabilities.APIVersions -}}
{{- if not $v -}}
  {{- if $caps.Has "admissionregistration.k8s.io/v1/MutatingAdmissionPolicy" -}}
    {{- $v = "v1" -}}
  {{- else if $caps.Has "admissionregistration.k8s.io/v1beta1/MutatingAdmissionPolicy" -}}
    {{- $v = "v1beta1" -}}
  {{- else -}}
    {{- fail "admissionPolicies.submitter.enabled requires MutatingAdmissionPolicy: v1 on Kubernetes 1.36+, or v1beta1 enabled on 1.34-1.35; set admissionPolicies.submitter.apiVersion when rendering without a cluster" -}}
  {{- end -}}
{{- end -}}
{{- if not (has $v (list "v1" "v1beta1")) -}}
  {{- fail (printf "admissionPolicies.submitter.apiVersion must be v1 or v1beta1 (got %v)" $v) -}}
{{- end -}}
{{- printf "admissionregistration.k8s.io/%s" $v -}}
{{- end -}}

{{/*
Renders admission resourceRules from group/version/resource entries, adding the
operation and Namespaced scope. Users never set operations or scope: capture is
CREATE only and immutability is UPDATE only. Users list main resources only;
with subresources set, every subresource is matched as well, since some (such
as jobs/status) accept metadata changes.
Takes a dict: rules (nonempty list), operation (string), subresources (bool),
field (value path for errors).
*/}}
{{- define "karta.submitter.resourceRules" -}}
{{- if not .rules }}
{{- fail (printf "%s must list at least one resource" .field) }}
{{- end }}
{{- range $i, $rule := .rules }}
{{- range $key, $_ := $rule }}
{{- if not (has $key (list "apiGroups" "apiVersions" "resources")) }}
{{- fail (printf "%s[%d]: unsupported key %q; set only apiGroups, apiVersions and resources (the chart sets operations and scope)" $.field $i $key) }}
{{- end }}
{{- end }}
{{- range $key := list "apiGroups" "apiVersions" "resources" }}
{{- if not (index $rule $key) }}
{{- fail (printf "%s[%d].%s must be a nonempty list" $.field $i $key) }}
{{- end }}
{{- end }}
{{- range $rule.resources }}
{{- if contains "/" . }}
{{- fail (printf "%s[%d]: subresource %q is not supported; list the main resource" $.field $i .) }}
{{- end }}
{{- end }}
{{- $resources := $rule.resources }}
{{- if $.subresources }}
{{- range $rule.resources }}
{{- $resources = append $resources (printf "%s/*" .) }}
{{- end }}
{{- end }}
- apiGroups: {{ toJson $rule.apiGroups }}
  apiVersions: {{ toJson $rule.apiVersions }}
  resources: {{ toJson $resources }}
  operations: [{{ $.operation | quote }}]
  scope: Namespaced
{{- end }}
{{- end -}}

{{/*
matchConstraints shared by the capture MAP and the CREATE VAP, so both always
select the same requests.
*/}}
{{- define "karta.submitter.captureMatchConstraints" -}}
{{- $s := .Values.admissionPolicies.submitter -}}
namespaceSelector:
  {{- toYaml ($s.namespaceSelector | default dict) | nindent 2 }}
objectSelector:
  {{- toYaml ($s.objectSelector | default dict) | nindent 2 }}
resourceRules:
  {{- include "karta.submitter.resourceRules" (dict "rules" $s.resources "operation" "CREATE" "field" "admissionPolicies.submitter.resources") | trim | nindent 2 }}
{{- with $s.excludeResources }}
excludeResourceRules:
  {{- include "karta.submitter.resourceRules" (dict "rules" . "operation" "CREATE" "field" "admissionPolicies.submitter.excludeResources") | trim | nindent 2 }}
{{- end }}
{{- end -}}
