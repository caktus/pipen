---
id: PI-015
github_issue: 15
github_url: https://github.com/caktus/pipen/issues/15
last_synced: "2026-11-21T03:56:05.586464+00:00"
synced_hash: e3a98aff8e1e79f0d7aa49f33d83926df96533bad3f830df6413eebb8c8c7c8a
title: Name the guest dev user after the developer
type: Bug
assignee: "@me"
labels:
  - phase/plan-ready
context:
  relates_to: []
---

As a developer, I want to ssh into my env as my dev name (for example `tobias`), not as the namespace name, so that the username stays stable and human across namespace renames.

### Background

The operator sets `DEV_USER` to the Pen's namespace (`operator/internal/pen/reconcile.go:253`: `{Name: "DEV_USER", Value: pen.Namespace}`; the pinned expectations are the two `want the namespace name` asserts in `reconcile_test.go:271/443`), and the dev image entrypoint (`devenv/entrypoint.sh`) creates the unix user from that name. Before the `env-<dev>` namespace move the namespace WAS the bare dev name, so the user was bare — the namespace-prefix change silently renamed every guest user. Live repro 2026-11-22 on the hosted-llm cluster: `ssh tobias@tobias-roots` → Permission denied (publickey), `ssh env-tobias@tobias-roots` → ok. The dev name is already on the namespace as the `herdr.dev/owner` label (set by the dev-namespace chart from its `dev` value); the namespace itself stays `env-<dev>`.

### To Do

- Decide where the operator reads the dev name from: the `herdr.dev/owner` namespace label vs an operator config value
- No data migration: the renamed user starts from an empty home, and the old `/home/env-<dev>/` directory stays untouched on the volume

## AC

- [ ] The operator sets `DEV_USER` to the developer name, and a unit test asserts `DEV_USER` is the `herdr.dev/owner` value, not the namespace name
- [ ] In the kind e2e, the pen pod's unix user is `<dev>` and key ssh lands in `/home/<dev>`
- [ ] An env whose home volume has data under `/home/env-<dev>/` recreates its pod with the new operator: ssh lands in an empty `/home/<dev>`, the old directory stays untouched (no migration)
- [ ] The dev-namespace chart README (its `DEV_USER` note) and the hosted-llm docs (the `README.md` Kata Dev Environments section — `README-herdr.md` was folded into `README.md` and deleted 2026-11-22 — and the bootstrap play's report message, `herdr/devenvs.yml`) say the guest user is the dev name, and the operator ships a new version pin

## Related tickets

- caktus/hosted-llm: `README.md` (Kata Dev Environments) and `herdr/devenvs.yml` document `ssh env-<you>@<hostname>` and must be updated in the same rollout (the old `README-herdr.md` no longer exists — folded into `README.md` on 2026-11-22)
