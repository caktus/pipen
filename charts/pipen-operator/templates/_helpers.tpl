{{- define "pipen-operator.requireImage" -}}
{{- if not .Values.image }}{{ fail "pipen-operator: .Values.image is required (full operator image reference)" }}{{- end }}
{{- end }}
