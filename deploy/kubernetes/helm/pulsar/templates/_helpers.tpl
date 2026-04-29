{{/*
Expand the name of the chart.
*/}}
{{- define "pulsar.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
Truncated at 63 chars because some Kubernetes name fields are limited to this
(by the DNS naming spec).
*/}}
{{- define "pulsar.fullname" -}}
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

{{/*
Create chart label.
*/}}
{{- define "pulsar.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "pulsar.labels" -}}
helm.sh/chart: {{ include "pulsar.chart" . }}
{{ include "pulsar.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "pulsar.selectorLabels" -}}
app.kubernetes.io/name: {{ include "pulsar.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service account name.
*/}}
{{- define "pulsar.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "pulsar.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Controller gRPC Service address — used by all agents to connect.
Format: <release>-pulsar-controller:<grpcPort>
*/}}
{{- define "pulsar.controllerGRPC" -}}
{{- printf "%s-controller:%d" (include "pulsar.fullname" .) (int .Values.controller.service.grpcPort) }}
{{- end }}

{{/*
Etcd endpoint(s) for the config file.
Returns the first endpoint only (single-string format used by pulsar.yaml).
Agents/controller use a list; only one value is written here; extend for HA etcd.
*/}}
{{- define "pulsar.etcdEndpoint" -}}
{{- if .Values.etcd.enabled }}
{{- printf "%s-etcd:2379" (include "pulsar.fullname" .) }}
{{- else }}
{{- first .Values.etcd.external.endpoints }}
{{- end }}
{{- end }}

{{/*
Full etcd endpoints list — for multi-endpoint external clusters.
*/}}
{{- define "pulsar.etcdEndpoints" -}}
{{- if .Values.etcd.enabled }}
- {{ printf "%s-etcd:2379" (include "pulsar.fullname" .) | quote }}
{{- else }}
{{- range .Values.etcd.external.endpoints }}
- {{ . | quote }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Container image (repository:tag).
*/}}
{{- define "pulsar.image" -}}
{{- printf "%s:%s" .Values.image.repository .Values.image.tag }}
{{- end }}

{{/*
JWT Secret name.
*/}}
{{- define "pulsar.jwtSecretName" -}}
{{- if .Values.jwt.existingSecret }}
{{- .Values.jwt.existingSecret }}
{{- else }}
{{- printf "%s-jwt" (include "pulsar.fullname" .) }}
{{- end }}
{{- end }}

{{/*
JWT Secret key.
*/}}
{{- define "pulsar.jwtSecretKey" -}}
{{- if .Values.jwt.existingSecret }}
{{- .Values.jwt.existingSecretKey }}
{{- else }}
{{- "jwt-secret" }}
{{- end }}
{{- end }}

{{/*
Postgres Secret name (password / DSN).
*/}}
{{- define "pulsar.postgresSecretName" -}}
{{- if .Values.postgres.auth.existingSecret }}
{{- .Values.postgres.auth.existingSecret }}
{{- else if (not .Values.postgres.enabled) }}
{{- if .Values.postgres.external.existingSecret }}
{{- .Values.postgres.external.existingSecret }}
{{- else }}
{{- printf "%s-postgres" (include "pulsar.fullname" .) }}
{{- end }}
{{- else }}
{{- printf "%s-postgres" (include "pulsar.fullname" .) }}
{{- end }}
{{- end }}

{{/*
Image pull secrets block.
*/}}
{{- define "pulsar.imagePullSecrets" -}}
{{- with .Values.image.pullSecrets }}
imagePullSecrets:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end }}
