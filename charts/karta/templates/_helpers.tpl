
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
{{- if .Values.fullnameOverride -}}
{{ printf "%s-dsx-workload-exporter" (.Values.fullnameOverride | trunc 41 | trimSuffix "-") }}
{{- else -}}
dsx-workload-exporter
{{- end -}}
{{- end -}}

{{/* Exporter labels use a distinct name to avoid matching operator selectors. */}}
{{- define "karta.exporter.selectorLabels" -}}
app.kubernetes.io/name: dsx-workload-exporter
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* Cluster-scoped objects carry the namespace, so exporter releases in different namespaces do not collide. */}}
{{- define "karta.exporter.clusterScopedName" -}}
{{ printf "%s-%s" (include "karta.exporter.fullname" .) .Release.Namespace }}
{{- end -}}

{{/*
GOMEMLIMIT for the exporter container. auto: 90% of resources.limits.memory in bytes,
so the Go GC works harder before the kernel OOM-kills the container; the rest covers
memory outside the Go runtime. Any other non-empty value passes through. Empty, or
auto without a memory limit or with a zero one, renders nothing.
*/}}
{{- define "karta.exporter.goMemLimit" -}}
{{- $value := toString (.Values.exporter.goMemLimit | default "") -}}
{{- if ne $value "auto" -}}
{{- $value -}}
{{- else -}}
{{- $raw := dig "resources" "limits" "memory" "" .Values.exporter -}}
{{- $bytes := 0.0 -}}
{{- if or (kindIs "float64" $raw) (kindIs "int64" $raw) (kindIs "int" $raw) -}}
{{- $bytes = float64 $raw -}}
{{- else if $raw -}}
{{- $limit := toString $raw -}}
{{- /* A Kubernetes quantity: a signed decimal number, then an SI suffix or a decimal exponent. */ -}}
{{- $number := regexFind "^[+-]?([0-9]+(\\.[0-9]*)?|\\.[0-9]+)" $limit -}}
{{- $suffix := trimPrefix $number $limit -}}
{{- $units := dict "" 1 "m" 0.001 "k" 1e3 "M" 1e6 "G" 1e9 "T" 1e12 "P" 1e15 "E" 1e18 "Ki" 1024 "Mi" 1048576 "Gi" 1073741824 "Ti" 1099511627776 "Pi" 1125899906842624 "Ei" 1152921504606846976 -}}
{{- $multiplier := 0.0 -}}
{{- if and $number (hasKey $units $suffix) -}}
{{- $multiplier = float64 (get $units $suffix) -}}
{{- else if and $number (regexMatch "^[eE][+-]?[0-9]{1,2}$" $suffix) -}}
{{- $exponent := atoi (trimPrefix "+" (substr 1 (len $suffix) $suffix)) -}}
{{- if and (ge $exponent -18) (le $exponent 18) -}}
{{- $multiplier = 1.0 -}}
{{- range until (int (max $exponent (sub 0 $exponent))) -}}
{{- if ge $exponent 0 -}}
{{- $multiplier = mulf $multiplier 10 -}}
{{- else -}}
{{- $multiplier = divf $multiplier 10 -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if not $multiplier -}}
{{- fail (printf "exporter.goMemLimit=auto cannot read exporter.resources.limits.memory %q; set exporter.goMemLimit to a Go value such as 3600MiB" $limit) -}}
{{- end -}}
{{- $bytes = mulf (float64 $number) $multiplier -}}
{{- end -}}
{{- /* bytes x 9 / 10, not x 0.9: 4e9 x 0.9 is not exact in floating point. */ -}}
{{- $goMemLimit := floor (divf (mulf $bytes 9) 10) -}}
{{- /* A zero limit means unlimited; GOMEMLIMIT=0 would be a zero-byte Go limit. */ -}}
{{- if ge $goMemLimit 1.0 -}}
{{- printf "%.0f" $goMemLimit -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "karta.exporter.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "karta.exporter.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Renders submitter capture resourceRules from group/version/resource entries,
adding the CREATE operation and Namespaced scope, which users never set.
Takes a dict: rules (nonempty list), field (value path for errors).
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
- apiGroups: {{ toJson $rule.apiGroups }}
  apiVersions: {{ toJson $rule.apiVersions }}
  resources: {{ toJson $rule.resources }}
  operations: ["CREATE"]
  scope: Namespaced
{{- end }}
{{- end -}}
