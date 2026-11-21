---
id: PI-011
github_issue: 11
github_url: https://github.com/caktus/pipen/issues/11
last_synced: "2026-11-20T22:53:48.106875+00:00"
synced_hash: 78023a49cbb0f5708b5518bf99873854061a98a90caea85a3584c50015376f59
title: Make dev namespace prefix configurable
type: Feature
assignee: "@me"
labels:
  - phase/done
context:
  relates_to: []
---

As an admin, I want the dev-namespace chart to name each developer namespace from a configurable prefix plus the dev name (default `env-<dev>`, e.g. `env-tobias`), so that dev namespaces live in a reserved range that the cluster can pick, and do not collide with unix users, machine names, or other clusters' namespaces.

Today `dev` is both the developer identity and the namespace name, so namespaces are bare person names (tobias, copelco, ...).

### To Do

- Add a `namespacePrefix` value (default `env-`) to the dev-namespace chart and use `<prefix><dev>` for the Namespace and all namespaced objects
- Settle and document the bootstrap object naming (herdr-dev-<namespace> vs the herdr.dev/owner label) so operator, chart, and bootstrap all agree

## AC

- [ ] The Namespace is named `<namespacePrefix><dev>` (default `env-tobias` for dev `tobias`), all namespaced objects live in it, and `dev` keeps meaning the developer identity (herdr.dev/owner label)
- [ ] Rendering fails when the composed namespace name is not RFC 1123 or exceeds 63 characters
- [ ] The chart version is bumped, and the README documents `namespacePrefix`, the bootstrap naming convention (ssh-key secret `herdr-dev-<namespace>` matches the operator's `herdr-dev-<pen namespace>` lookup), and the per-dev cutover (retain the home PVC and recreate it in the new namespace)
