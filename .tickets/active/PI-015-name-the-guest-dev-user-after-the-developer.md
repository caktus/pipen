---
id: PI-015
github_issue: 15
github_url: https://github.com/caktus/pipen/issues/15
last_synced: "2026-11-21T02:05:16.658440+00:00"
synced_hash: 19bef777a04a60df9bb5767413c4fe7c9211a1eb4f18b0e09c627687b9ea47ee
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

The operator sets `DEV_USER` to the Pen's namespace (`operator/internal/pen/reconcile.go`), and the dev image entrypoint creates the unix user from that name. Since the move to `env-<dev>` namespaces, the ssh user is `env-tobias` instead of `tobias`. The dev name is already on the namespace as the `herdr.dev/owner` label (set by the dev-namespace chart from its `dev` value); the namespace itself stays `env-<dev>`.

### To Do

- Decide where the operator reads the dev name from: the `herdr.dev/owner` namespace label vs an operator config value
- Plan the migration for existing envs: their persistent `/home` volume holds data under `/home/env-<dev>/`, which must land in `/home/<dev>/` when the user is renamed

## AC

- [ ] The operator sets `DEV_USER` to the developer name, and a unit test asserts `DEV_USER` is the `herdr.dev/owner` value, not the namespace name
- [ ] In the kind e2e, the pen pod's unix user is `<dev>` and key ssh lands in `/home/<dev>`
- [ ] An env with existing data under `/home/env-<dev>/` has that data available under `/home/<dev>/` after pod recreation on the new operator, with no data loss
- [ ] The dev-namespace chart README (its `DEV_USER` note) and the hosted-llm docs (`README-herdr.md`, the bootstrap play's next-steps message) say the guest user is the dev name, and the operator ships a new version pin

## Related tickets

- caktus/hosted-llm: `README-herdr.md` and `herdr/devenvs.yml` document `ssh env-<you>@<hostname>` and must be updated in the same rollout
