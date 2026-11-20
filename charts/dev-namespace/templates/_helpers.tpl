{{/*
The developer namespace name: namespacePrefix plus dev (default
env-tobias for dev tobias). The Namespace and every namespaced object
in this chart use it. The render fails when the composed name is not
RFC 1123 (lowercase alphanumerics and '-') or exceeds 63 characters.
*/}}
{{- define "dev-namespace.name" -}}
{{- $ns := printf "%s%s" (.Values.namespacePrefix | toString) (.Values.dev | toString) -}}
{{- if or (gt (len $ns) 63) (not (regexMatch "^[a-z0-9]([a-z0-9-]*[a-z0-9])?$" $ns)) -}}
{{- fail "namespace name (namespacePrefix + dev) must be an RFC 1123 name (lowercase alphanumerics and '-', max 63 chars)" -}}
{{- end -}}
{{- $ns -}}
{{- end -}}

{{/*
Common labels for all chart objects. Every template also includes
dev-namespace.name in its metadata, so the namespace name is validated
in every rendered object.
*/}}
{{- define "dev-namespace.labels" -}}
herdr.dev/managed: chart
{{- end -}}
