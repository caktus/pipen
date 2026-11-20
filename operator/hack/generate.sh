#!/usr/bin/env bash
# Regenerate operator code: deepcopy methods (api/v1alpha1) and the CRD
# manifest (charts/pipen-operator/templates/crd.yaml). Run from the repo root.
set -euo pipefail
cd "$(dirname "$0")/.."
CONTROLLER_GEN="${CONTROLLER_GEN:-v0.19.0}"
go run "sigs.k8s.io/controller-tools/cmd/controller-gen@${CONTROLLER_GEN}" object paths=./api/...
rm -f ../charts/pipen-operator/templates/pens.pipen.dev_pens.yaml
go run "sigs.k8s.io/controller-tools/cmd/controller-gen@${CONTROLLER_GEN}" \
  crd paths=./api/... output:crd:dir=../charts/pipen-operator/templates
mv ../charts/pipen-operator/templates/pens.pipen.dev_pens.yaml \
  ../charts/pipen-operator/templates/crd.yaml
# Format the generated manifest with prettier here: pre-commit skips helm templates
if command -v prettier >/dev/null 2>&1; then
  prettier -w ../charts/pipen-operator/templates/crd.yaml >/dev/null
fi
