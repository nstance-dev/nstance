# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

{{/*
Expand the name of the chart.
*/}}
{{- define "nstance-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "nstance-operator.fullname" -}}
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
Create chart name and version as used by the chart label.
*/}}
{{- define "nstance-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "nstance-operator.labels" -}}
helm.sh/chart: {{ include "nstance-operator.chart" . }}
{{ include "nstance-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "nstance-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "nstance-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "nstance-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.enabled }}
{{- default (include "nstance-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/* Name of the operator configuration ConfigMap. */}}
{{- define "nstance-operator.configMapName" -}}
{{- default (printf "%s-config" (include "nstance-operator.fullname" .)) .Values.operatorConfigMap.name }}
{{- end }}

{{/* Name of the CAPI workload ServiceAccount. */}}
{{- define "nstance-operator.capiServiceAccountName" -}}
{{- default (printf "%s-capi-workload" (include "nstance-operator.fullname" .)) .Values.capi.serviceAccount.name }}
{{- end }}

{{/* Name of the webhook Service. */}}
{{- define "nstance-operator.webhookServiceName" -}}
{{- printf "%s-webhook" (include "nstance-operator.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Name of the webhook serving certificate Secret. */}}
{{- define "nstance-operator.webhookSecretName" -}}
{{- printf "%s-webhook-cert" (include "nstance-operator.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
