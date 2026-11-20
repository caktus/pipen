{{/*
Common labels for all chart objects. Validates dev first: it is the
namespace name, and every other object in this chart is named relative
to it (RFC 1123: lowercase alphanumerics and '-').
*/}}
{{- define "dev-namespace.labels" -}}
{{- if not (regexMatch "^[a-z0-9]([a-z0-9-]*[a-z0-9])?$" (.Values.dev | toString)) -}}
{{- fail "dev must be an RFC 1123 name (lowercase alphanumerics and '-', max 63 chars)" -}}
{{- end -}}
herdr.dev/managed: chart
{{- end -}}
