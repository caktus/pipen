---
id: PI-017
github_issue: 20
github_url: https://github.com/caktus/pipen/issues/20
last_synced: "2026-11-24T23:29:11.489737+00:00"
synced_hash: a9a322a33d3f52c9735b4994031e2f3d8b584310787afa62ad01efc44b142b51
title: Default pens to pinned image tags
assignee: "@me"
labels:
  - phase/done
context:
  relates_to: []
---

As a cluster operator, I want pens to start from a pinned image by default, so that a pen I create today behaves the same as one I created last month.

Today `pipen up` defaults the per-pen container image to a floating tag such as `latest`. An operator who pins the operator and chart versions still gets a data plane that changes under them, and nothing records which image a pen actually ran.

### To Do

- Confirm which release tags the pen image publishes and that every release is tagged
- Explore recording the resolved image tag or digest on the Pen and its pod

## AC

- [ ] A pen created with no image override starts from the latest pinned release tag, not a floating tag
- [ ] When a floating tag is in effect, `pipen up` warns before it creates the pen
- [ ] Every pen records the resolved image tag or digest in its metadata or labels, so the environment is reproducible

<!-- tickets:research -->

The pen image default: `pipen up --image` defaults to empty, the CRD `PenSpec.Image` is optional, and the operator resolves an empty spec image to its ConfigMap key `image` at pen creation only (operator/internal/pen/reconcile.go). The chart ships that key as the floating tag `ghcr.io/caktus/pipen:latest` (charts/pipen-operator/values.yaml).

The pen image publishes no semver release tags. The publish workflow pushes date tags `YYYYMMDD-pi<pi-version>` plus `latest` on every content change to main, nightly, and on dispatch, and prunes to the 14 newest date tags (never reusing a name, so a date tag is immutable). Live GHCR check on 2026-11-24: the newest tag is `20261124-pi1.6.0`.

Nothing records the resolved image on the Pen today: no labels, no status field. The operator rewrites the whole Pen status every reconcile and already holds `pens/status` update, so a status field needs no RBAC change. The e2e pins its own `config.image` (pause 3.10), so a chart default change needs no e2e image change. The CLI cannot read the operator ConfigMap (no RBAC, wrong namespace), so it cannot verify the cluster default on its own.

Full research with citations: `.superpowers/sdd/pi-017-plan/research.md` in the branch worktree.

<!-- tickets:approaches -->

Controller rulings (2026-11-24):

- AC1, the pinned default: the chart default `config.image` becomes a date tag pinned at chart-release time, `ghcr.io/caktus/pipen:20261124-pi1.6.0` for this release. The operator has no registry access, so "latest pinned tag" cannot be resolved at runtime. The pin moves when a new pen image lands on main: the operator chart bumps its default and releases. Existing installs re-pin on a chart upgrade. Existing pens keep their image (the create-only resolution is pinned by tests and documented).
- AC2, the warning: the CLI warns in `up` before the POST. With `--image`, warn when the tag is `latest` or absent (implicit latest); a digest counts as pinned. The tag parse ports the Go rule. Warning only, no opt-in flag. Without `--image`, the CLI applies the same rule to an optional `image` key in its flat config, which the admin bootstrap writes with the same value as the chart default. Without the key, the warning says the image is the operator default and the CLI cannot verify it is pinned.
- AC3, the record: a new `PenStatus.image` field holding the resolved image reference (the same string in the pod spec). Not a digest: date tags are immutable, and the reference costs no extra read. Not a label: status is operator-owned and needs no RBAC change. Existing pens get the field on their next reconcile after the upgrade.
- The `--image` help text and the README values table move from `latest` to the pinned tag. The README note that the CRD manifest never changes while v1alpha1 is frozen gains the exception for this additive status field.

Full option analysis (label vs status vs CLI label, tag vs digest, warning channels): `.superpowers/sdd/pi-017-plan/research.md` sections 4-6.

<!-- tickets:plan -->

Task 1: pin the chart default. charts/pipen-operator/values.yaml sets config.image to ghcr.io/caktus/pipen:20261124-pi1.6.0, the chart version bumps, the chart README gains a release note (the default is a date tag, re-pinned per chart release, existing pens keep their image), and the README values table plus the pinning paragraph match.

Task 2: record the resolved image. PenStatus gains an Image string field (json image, optional). The operator sets it to the resolved image on every status write, so every pen, new and existing, carries it. The CRD manifest in the operator chart gains the status property and the chart version bumps. Go tests: a default-image pen records the config image, an overridden pen records its own image, and an existing pen gains the field on its next reconcile. The e2e asserts status.image equals the pinned pause reference after the pen is Ready.

Task 3: the CLI warning. cli/pipen.py gains a tag parse (tag after the last colon that follows the last slash; a sha256 digest is pinned) and a warning in the up path before the POST: an explicit latest or missing tag warns, an absent flag falls back to the optional image key in the CLI config with the same rule, and a missing key warns that the operator default applies and the CLI cannot verify it. Python unit tests cover the parse table, the warning decision, and the warning-to-stderr-before-POST behavior with the existing FakeApi harness. The README documents the optional image key in the CLI config and the warning.
