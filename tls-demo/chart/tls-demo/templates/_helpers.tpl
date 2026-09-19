{{/*
Expand the name of the chart.
*/}}
{{- define "tls-demo.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}
