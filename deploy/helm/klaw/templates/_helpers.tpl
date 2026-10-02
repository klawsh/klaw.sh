{{- define "klaw.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "klaw.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "klaw.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "klaw.labels" -}}
helm.sh/chart: {{ include "klaw.chart" . }}
app.kubernetes.io/name: {{ include "klaw.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "klaw.selectorLabels" -}}
app.kubernetes.io/name: {{ include "klaw.name" .root }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{- define "klaw.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "klaw.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "klaw.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) }}
{{- end }}

{{- define "klaw.controllerAddress" -}}
{{- printf "%s-controller:%d" (include "klaw.fullname" .) (int .Values.controller.service.port) }}
{{- end }}

{{- define "klaw.authSecretName" -}}
{{- default (printf "%s-auth" (include "klaw.fullname" .)) .Values.auth.existingSecret }}
{{- end }}

{{- define "klaw.anthropicSecretName" -}}
{{- default (printf "%s-anthropic" (include "klaw.fullname" .)) .Values.anthropic.existingSecret }}
{{- end }}

{{/* Pod-level scheduling and registry fields shared by controller and node. */}}
{{- define "klaw.podCommon" -}}
{{- with .Values.imagePullSecrets }}
imagePullSecrets:
  {{- toYaml . | nindent 2 }}
{{- end }}
serviceAccountName: {{ include "klaw.serviceAccountName" . }}
automountServiceAccountToken: {{ .Values.serviceAccount.automount }}
{{- with .Values.podSecurityContext }}
securityContext:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end }}

{{- define "klaw.scheduling" -}}
{{- with .nodeSelector }}
nodeSelector:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .affinity }}
affinity:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .tolerations }}
tolerations:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .topologySpreadConstraints }}
topologySpreadConstraints:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .priorityClassName }}
priorityClassName: {{ . }}
{{- end }}
{{- end }}
