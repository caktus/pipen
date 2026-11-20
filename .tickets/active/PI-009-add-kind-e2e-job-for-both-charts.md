---
id: PI-009
github_issue: 9
github_url: https://github.com/caktus/pipen/issues/9
last_synced: '2026-11-20T18:33:25.765909+00:00'
synced_hash: 0bd19a7a14185d908137e1ace63c9b82e8fddca212a18a00908a4ac7b027ea85
title: Add kind e2e job for both charts
type: Task
assignee: '@me'
labels:
- phase/draft
context:
  relates_to:
  - PI-004
---

As a maintainer, I want CI to install both charts into a kind cluster and drive a pen through its lifecycle, so that RBAC and operator wiring regressions are caught before merge.

The unit tests run against a fake client (no RBAC, no informers) and the chart job only lints and templates. The local kind run for #4 showed the operator start path (leader election, namespace-scoped config cache, ConfigMap watch) only executes against a real API server, and nothing in CI exercises `kubectl auth can-i` against the rendered Roles.

### To Do

- Add an `e2e` job to `.github/workflows/verify.yml`: kind cluster, build the operator image, `kind load docker-image`, install `charts/pipen-operator` with kind-shaped values (long-running test image, `standard` storage class, no node selector) and `charts/dev-namespace` for one test dev
- Drive the lifecycle: operator Deployment ready; as the dev token assert `can-i create pods` is no and `can-i create pens.pipen.dev/pens` is yes; create a Pen and assert PVC + Service + Pod appear; patch the Service `status.loadBalancer.ingress` hostname (kind runs no Tailscale operator) and assert Pen status turns Ready with that hostname; delete the Pen and assert Pod/Service are gone and the PVC remains
- Make the pod runtime class configurable in the operator config (`runtimeClass`, default `kata`, empty in kind) — the reconciler currently hardcodes `kata`, which kind nodes cannot run

### AC

- [ ] The `e2e` job installs both charts into kind and passes on a clean main
- [ ] The job asserts, via `kubectl auth can-i` as the dev token, that the token cannot create pods and can create Pens
- [ ] The pen lifecycle (create, objects appear, Ready via patched ingress, delete leaves the PVC) is asserted with kubectl

## Related tickets

- Extends #4 (pipen operator); the `runtimeClass` config key is a small operator change in the same spirit as the other cluster-shape config values
