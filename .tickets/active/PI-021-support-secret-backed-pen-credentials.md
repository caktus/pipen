---
id: PI-021
github_issue: 21
github_url: https://github.com/caktus/pipen/issues/21
last_synced: "2026-11-24T21:31:01.333996+00:00"
synced_hash: ea513ad490b07d0465118c41450d8a09311fb450115ac7e3c749d492b580d2e2
title: Support Secret-backed pen credentials
assignee: "@me"
labels:
  - phase/in-progress
context:
  relates_to: []
---

As a user, I want pens to take credentials from a Secret, so that a credential in the namespace is not held in a plain ConfigMap.

Today a value a user wants inside a pen, such as a model API key, is delivered through a plain ConfigMap, because the operator's reconcile contract expects that object shape. Any principal that can read ConfigMaps in the namespace can read the credential, and Secrets are the conventional object for credentials even where RBAC adds no per-object difference.

### To Do

- Explore how the operator could consume a Secret in place of or alongside the ConfigMap (for example an annotation that references a Secret)
- Confirm the API surface and RBAC a Secret-backed path needs

## AC

- [ ] A user can deliver a pen credential through a Secret, and the pen receives it
- [ ] The existing ConfigMap path still works for non-sensitive values

<!-- tickets:research -->

The pen receives a ConfigMap `herdr-dev-<ns>` (keys `models.json`, `settings.json`, `litellm_key`) mounted at `/run/herdr/config`, optional. `herdr-boot` copies `/run/herdr` to `/var/lib/herdr`, and `pen-setup` installs the three named files into the home with mode 0600 and sha256 markers (devenv/entrypoint.sh). The pen also mounts the bootstrap Secret `herdr-dev-<ns>` (key `authorized_keys` only) at `/run/herdr/secret`, non-optional, and the pen Pends until both objects exist (operator/internal/pen/reconcile.go).

Operator RBAC: a read-only ClusterRole (no configmaps, no secrets) plus a per-namespace Role whose configmaps/secrets rules are `resourceNames`-scoped to exactly `herdr-dev-<ns>` (charts/dev-namespace/templates/operator-role.yaml). The developer token Role has pens CRUD and pods get/log, and no secret rule (charts/dev-namespace/templates/role.yaml).

The operator does not converge pod template volumes on an existing sandbox; it converges env container resources (the PI-019 resize path) through a suspend cycle. Rotation contract (PI-023): a changed Secret reaches a pen at pod recreation.

Full research with file+line citations: `.superpowers/sdd/pi-021-plan/research.md` in the branch worktree.

<!-- tickets:approaches -->

Three options were evaluated (details and citations in the research section and the SDD research doc).

**Chosen: Option A, a pen annotation that references a user Secret.**
Annotation `pens.pipen.dev/credentials: <secret-name>`, one Secret per pen. The operator adds a Secret volume (all keys, `Optional: true`) mounted at `/run/herdr/credentials`. `pen-setup` installs every file into `~/.herdr/credentials/` (directory 0700, files 0600) with the sha256 marker scheme, so a removed key is deleted from the home only while its hash matches the marker. Controller rulings:

- All keys, no allowlist: no frozen names in operator or image code. The user's tooling reads `~/.herdr/credentials/<key>`.
- Missing Secret: run degraded (no credential files), no Pending, no operator probe. The operator RBAC does not change. The kubelet reads the Secret at mount time.
- Who creates the Secret: the developer, with the developer token. The dev Role gains `create, get, update, delete` on `secrets` (namespace-scoped; the namespace is per developer).
- Existing pens: the annotation is converged with the pod template through the existing PI-019 suspend cycle. Adding or removing the annotation restarts the pen once. A pen pod rebuilt from a stale template is avoided by convergence.
- No CLI command in this ticket. `kubectl` with the dev token is the workflow.
- Rotation: same contract as SSH keys, pod recreation.

Option C (typed `spec.credentialsSecret` field) is the fallback: same mechanics, but it pays a CRD change, a chart bump, and a CLI flag. Option B (split the bootstrap into a second bootstrap Secret) moves the admin-owned litellm key only, gives a developer no per-pen credential, and answers a different ticket. It can follow.

<!-- tickets:plan -->

Task 1: pen image installs a credentials directory. `devenv/entrypoint.sh` installs every file from `/var/lib/herdr/credentials` into `~/.herdr/credentials/` (dir 0700, files 0600), one sha256 marker per file under the dev home at `~/.herdr/projected-credentials/`. A removed file is deleted only while its hash matches its marker. A file the developer edited stays. Zero-byte and dangling entries read as absent. The existing config loop is untouched. The CI image job gains a boot probe that seeds `/run/herdr/credentials` with a file and asserts the install, the modes, and the marker, plus a second boot that removes the key and asserts the placed file goes.

Task 2: operator consumes the annotation. The pen pod template gains a `dev-credentials` Secret volume (all keys, `Optional: true`) mounted read-only at `/run/herdr/credentials` when the pen carries the `pens.pipen.dev/credentials` annotation. The existing PI-019 divergence check converges the volume: an annotation added, removed, or renamed on an existing pen triggers the suspend cycle, and the pod comes back with (or without) the volume. Go table tests: no annotation means no volume. An annotation adds the volume with the right name and optionality. A changed annotation on an existing sandbox patches the template and suspends. A removed annotation converges back and the pen returns to Ready.

Task 3: RBAC and docs. The dev-namespace chart's developer Role gains `create, get, update, delete` on `secrets`, and the chart version bumps. The README documents the workflow (create the Secret with the dev token, set the annotation, where the files land, 0600, rotation at pod recreation, a missing Secret runs the pen without the files), and the e2e comment notes match.

Task 4: kind e2e. After the pen is Ready, the job creates a credentials Secret with the dev token, sets the annotation with `kubectl patch`, waits for the pod uid change and Ready again (the convergence restart), asserts the pod spec carries the `dev-credentials` volume, and asserts the pen is Ready. The existing ConfigMap assertions run unchanged.
