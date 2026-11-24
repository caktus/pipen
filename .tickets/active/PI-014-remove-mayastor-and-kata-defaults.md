---
id: PI-014
github_issue: 14
github_url: https://github.com/caktus/pipen/issues/14
last_synced: "2026-11-24T04:55:32.531100+00:00"
synced_hash: 5022b6a00747aef1bf8c3d58f0aded44a028ae501fa4746d5e1540d6b2f0096f
title: Remove Mayastor and Kata defaults
assignee: "@me"
labels:
  - phase/in-progress
context:
  relates_to: []
---

As a cluster admin, I want pipen to install with no kata or Mayastor assumption, so that a cluster without them is not turned away.

Today the operator chart defaults to `runtimeClass: kata`, `storageClass: mayastor-2`, and a Mayastor node selector, and the README presents a pen as a kata pod on a Mayastor node. A cluster that has neither must know which values to override, and the operator sets `storageClassName: ""` on the home PVC when the storage class is empty, which no storage class satisfies.

### To Do

- Make an empty `config.storageClass` mean no storage class (leave `storageClassName` unset), matching the existing `runtimeClass` handling
- Rewrite the README intro and "What a pen gives you" in runtime-neutral terms, and move kata and Mayastor to a reference-configuration note under Admins

## AC

- [ ] `helm template` of `pipen-operator` with no values renders an empty `runtimeClass`, an empty `storageClass`, and no pen node selector
- [ ] An operator unit test shows a pen with an empty `storageClass` creates a home PVC with no `storageClassName`
- [ ] The kind e2e installs the operator with the chart defaults only (no `config.storageClass`, `config.nodeSelector`, or `config.runtimeClass` override) and the pen reaches phase Running on a plain kind cluster
- [ ] The README names no kata or Mayastor requirement outside the Admins reference-configuration text, and the values table lists the empty defaults
- [ ] The pipen-operator chart is version-bumped with an upgrade note: an existing cluster re-sets `config.storageClass`, `config.nodeSelector`, and `config.runtimeClass` or its pens change behavior
- [ ] `pre-commit run --all-files` and all `verify.yml` jobs pass on the PR

<!-- tickets:research -->

- The three runtime-specific defaults all live in `charts/pipen-operator/values.yaml` under `config:` (nodeSelector lines 24-26, storageClass lines 27-28, runtimeClass lines 39-41). The Deployment-level `nodeSelector` (line 11) is already empty.
- `charts/pipen-operator/templates/configmap.yaml:17-23` renders all three into the `pipen-operator-config` ConfigMap.
- `operator/internal/config/config.go` `FromData`: Go fallback `RuntimeClass: "kata"` (line 51). `StorageClass` has no Go default (empty string), so the chart always ships the key.
- `operator/internal/pen/reconcile.go:158-167`: `ensurePVC` sets `StorageClassName: &sclass` unconditionally. An empty string is not the same as unset: Kubernetes does not apply a default StorageClass to an explicitly empty `storageClassName`, so an empty value must map to a nil pointer.
- `operator/internal/pen/reconcile.go:237`: `runtimeClassPtr` already maps an empty string to nil, so runtimeClass is optional today; only its Go default is still kata.
- The kind e2e job (`.github/workflows/verify.yml:188-200`) currently overrides `config.storageClass=standard`, `config.runtimeClass=`, and patches `nodeSelector` to `{}`. With neutral chart defaults the e2e drops those overrides and tests the defaults on a plain kind cluster, where the default StorageClass is `standard`.
- README.md kata/Mayastor mentions: intro (lines 6-7), repo layout (line 22), "What a pen gives you" (lines 35-48), "What stays, and what does not" (line 108), "Updating pi inside a pen" (line 135), Admins Prerequisites "Default-install requirements" (lines 204-211), the values table, and the Capacity section (mayastor-2 example).
- `charts/pipen-operator/README.md:38-44` documents the same three values and needs matching wording.

<!-- tickets:plan -->

1. Neutral chart defaults
   - Files: charts/pipen-operator/values.yaml (nodeSelector {}, storageClass "", runtimeClass ""; flip comments to reference-configuration phrasing), Chart.yaml (version bump), charts/pipen-operator/README.md (value table wording + upgrade note)
   - Verify: `helm template charts/pipen-operator` with no values renders the three empty defaults; helm lint

2. Operator: empty storage class means unset
   - Files: operator/internal/pen/reconcile.go (set StorageClassName pointer only when non-empty, mirroring runtimeClassPtr), operator/internal/config/config.go (Go default RuntimeClass "kata" to empty), reconcile_test.go and config_test.go (empty storageClass yields nil StorageClassName; default config has no runtime class)
   - Verify: `go test ./...`

3. README rewrite
   - Files: README.md (intro, repo layout, "What a pen gives you", "What stays, and what does not", "Updating pi", Prerequisites, values table, Capacity)
   - A pen is described as a pod on the cluster; kata, Mayastor, and `openebs.io/engine=mayastor` appear only in a reference-configuration note under Admins Prerequisites, with the matching values to set
   - Verify: pre-commit (prettier), grep for kata and mayastor outside the reference note

4. CI: e2e on chart defaults
   - Files: .github/workflows/verify.yml (delete the three `config.*` overrides and the nodeSelector/serviceAnnotations patch at lines 188-200; keep the serviceAnnotations patch only if the chart default stops matching the e2e expectation)
   - The e2e installs the operator with the chart defaults only. A plain kind cluster has StorageClass `standard`, no runtime class, and no Mayastor label, so this proves the defaults work out of the box
   - Verify: `pre-commit run --all-files`, all verify.yml jobs
