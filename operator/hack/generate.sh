#!/usr/bin/env bash
# Regenerate operator code: deepcopy methods (api/v1alpha1) and the CRD
# manifest (charts/pipen-operator/crds/). Run from the repo root.
set -euo pipefail
cd "$(dirname "$0")/.."
CONTROLLER_GEN="${CONTROLLER_GEN:-v0.19.0}"
go run "sigs.k8s.io/controller-tools/cmd/controller-gen@${CONTROLLER_GEN}" object paths=./api/...
rm -f ../charts/pipen-operator/crds/pens.pipen.dev_pens.yaml
go run "sigs.k8s.io/controller-tools/cmd/controller-gen@${CONTROLLER_GEN}" \
  crd paths=./api/... output:crd:dir=../charts/pipen-operator/crds
mv ../charts/pipen-operator/crds/pens.pipen.dev_pens.yaml \
  ../charts/pipen-operator/crds/pens.pipen.dev.yaml
