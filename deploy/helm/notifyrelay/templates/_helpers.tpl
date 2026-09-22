{{/* Chart name, overridable. */}}
{{- define "notifyrelay.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Fully qualified name. */}}
{{- define "notifyrelay.fullname" -}}
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

{{- define "notifyrelay.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Labels applied to everything. */}}
{{- define "notifyrelay.labels" -}}
helm.sh/chart: {{ include "notifyrelay.chart" . }}
{{ include "notifyrelay.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "notifyrelay.selectorLabels" -}}
app.kubernetes.io/name: {{ include "notifyrelay.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "notifyrelay.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "notifyrelay.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/* The image reference, with the tag defaulting to the chart's appVersion. */}}
{{- define "notifyrelay.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}

{{/* The Secret holding the environment. */}}
{{- define "notifyrelay.secretName" -}}
{{- if .Values.existingSecret -}}
{{- .Values.existingSecret -}}
{{- else -}}
{{- printf "%s-env" (include "notifyrelay.fullname" .) -}}
{{- end -}}
{{- end -}}
