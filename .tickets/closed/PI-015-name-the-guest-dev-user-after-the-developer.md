---
id: PI-015
github_issue: 15
github_url: https://github.com/caktus/pipen/issues/15
last_synced: "2026-11-24T04:55:02.083560+00:00"
synced_hash: e3a98aff8e1e79f0d7aa49f33d83926df96533bad3f830df6413eebb8c8c7c8a
title: Name the guest dev user after the developer
type: Bug
assignee: "@me"
labels:
  - phase/done
context:
  relates_to: []
---

As a developer, I want to ssh into my env as my dev name (for example `tobias`), not as the namespace name, so that the username stays stable and human across namespace renames.

### Background

The operator sets `DEV_USER` to the Pen's namespace (`operator/internal/pen/reconcile.go`), and the dev image entrypoint creates the unix user from that name. Since the move to `env-<dev>` namespaces, the ssh user is `env-tobias` instead of `tobias`. The dev name is already on the namespace as the `herdr.dev/owner` label (set by the dev-namespace chart from its `dev` value); the namespace itself stays `env-<dev>`.

### To Do

- Decide where the operator reads the dev name from: the `herdr.dev/owner` namespace label vs an operator config value
- No data migration: the renamed user starts from an empty home, and the old `/home/env-<dev>/` directory stays untouched on the volume

## AC

- [ ] The operator sets `DEV_USER` to the developer name, and a unit test asserts `DEV_USER` is the `herdr.dev/owner` value, not the namespace name
- [ ] In the kind e2e, the pen pod's unix user is `<dev>` and key ssh lands in `/home/<dev>`
- [ ] An env whose home volume has data under `/home/env-<dev>/` recreates its pod with the new operator: ssh lands in an empty `/home/<dev>`, the old directory stays untouched (no migration)
- [ ] The dev-namespace chart README (its `DEV_USER` note) and the hosted-llm docs (`README-herdr.md`, the bootstrap play's next-steps message) say the guest user is the dev name, and the operator ships a new version pin

## Related tickets

- caktus/hosted-llm: `README-herdr.md` and `herdr/devenvs.yml` document `ssh env-<you>@<hostname>` and must be updated in the same rollout
