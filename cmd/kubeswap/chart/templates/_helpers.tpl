{{/*
Chart name (truncated to 63 chars per the k8s name limit).
*/}}
{{- define "llama-swap.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified app name: <release>-<chart> unless the release name already
contains the chart name (helm convention).
*/}}
{{- define "llama-swap.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Chart label value.
*/}}
{{- define "llama-swap.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Standard labels for every object the chart renders.
*/}}
{{- define "llama-swap.labels" -}}
helm.sh/chart: {{ include "llama-swap.chart" . }}
{{ include "llama-swap.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Labels that select the head-end pod. Must be immutable after install.
*/}}
{{- define "llama-swap.selectorLabels" -}}
app.kubernetes.io/name: {{ include "llama-swap.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
ServiceAccount the head-end (and its kubeswap calls) runs as.
*/}}
{{- define "llama-swap.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "llama-swap.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
ConfigMap holding config.yaml: the chart's own, or an existing one.
*/}}
{{- define "llama-swap.configMapName" -}}
{{- if .Values.config.existing -}}
{{- .Values.config.existing -}}
{{- else -}}
{{- include "llama-swap.fullname" . -}}
{{- end -}}
{{- end -}}

{{/*
Head-end image reference.
*/}}
{{- define "llama-swap.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}
