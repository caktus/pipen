---
id: PI-013
github_issue: 13
github_url: https://github.com/caktus/pipen/issues/13
last_synced: "2026-11-21T01:44:01.942107+00:00"
synced_hash: 9f158605df60378fc406ddb545f138ed6fe08ad46f08e6d95f30563fa059405f
title: Enforce the namespace lockdown and optional pen config
type: Bug
assignee: "@me"
labels:
  - priority/p1
  - phase/in-progress
context:
  relates_to:
    - PI-010
    - PI-012
---

As a cluster admin, I want the dev namespace lockdown and the optional pen config to work as the README says, so that pens are isolated and a pen boots without a model provider config.

A code review found that both dev namespace NetworkPolicies contain an empty rule. An empty rule allows all traffic, so no dev namespace is isolated today, on any cluster. The review also found that the operator mounts the three config keys as required items, so a pen whose ConfigMap lacks a key never starts. The entrypoint keeps old config files on the home volume when the ConfigMap drops them, so a mode switch or a key removal does not take effect. The fix turns on isolation for the first time, so each cluster must check its egress allows before it upgrades.

### To Do

- Confirm which Kubernetes version made `kubernetes.io/metadata.name` automatic, and fix the floor in the README
- Confirm that the kind e2e CNI enforces NetworkPolicy

### AC

- [ ] `helm template ci charts/dev-namespace --set dev=ci | grep -cE '^\s*- \{\}\s*$'` prints 0
- [ ] In the kind e2e, a pod in another namespace cannot connect to a pod in `env-ci`. A pod in `env-ci` cannot reach a Service in another namespace. DNS lookups from `env-ci` still work.
- [ ] The `pipen-operator` Role in the dev namespace reads only the `herdr-dev-<namespace>` ConfigMap and Secret (by `resourceNames`), and the e2e pen still reaches phase Running
- [ ] An operator unit test shows that the `dev-config` volume is optional and the pen pod does not mount a ServiceAccount token
- [ ] In the kind e2e, a pen whose ConfigMap has no `litellm_key` and no `settings.json` reaches phase Running
- [ ] In the `image` CI job, the dev image boots with an empty config dir: sshd runs, the dev user exists, and `~/.pi/agent` and `~/.litellm_key` do not exist
- [ ] In the `image` CI job, on a reused `/home` volume, a second boot without `litellm_key` and `models.json` removes the files that the first boot placed. A file that the developer created or changed stays.
- [ ] A zero-byte ConfigMap entry counts as absent
- [ ] On push to main, the `verify.yml` chart-bump check runs and fails when `charts/` changed with no version bump
- [ ] The dev-namespace chart is 0.6.0. Its README has 0.5.1 and 0.6.0 sections, and the 0.6.0 section lists the upgrade checks: model endpoint `egress.podAllows`, the Mayastor gateway allow, and kube-proxy `masqueradeAll`.
- [ ] The README Prerequisites section lists the Mayastor gateway check and states the namespace-label floor in one place (the chart README points to it). It marks `dev` as required, and installs with `--version 0.6.0`.
- [ ] The README describes both model modes in its first sentence about pi.dev. Step 4 says that the ConfigMap must exist, even when empty. No text mentions `PIPEN_TEMPLATES` or `devenv/templates`.
- [ ] Lines that this ticket changes contain no "may", "would", "might", "should", "could", and no em-dashes
- [ ] `pre-commit run --all-files` and all `verify.yml` jobs pass on the PR
- [ ] On the reference cluster, one dev namespace upgraded to 0.6.0 keeps ssh, the home mount, and model access working

### Out of scope

- Pin or check the image's install scripts, the CLI install ref, and the `:latest` defaults (follow-up ticket for the image supply chain)
- Move the model key from the ConfigMap into the dev Secret (follow-up ticket, needs the private deployment repo)
- CLI TLS CA option, token file mode check, name-length message, non-JSON responses, and crash reasons in pen status (follow-up ticket for CLI hardening)
- Share the GHCR publish steps between the two workflows (follow-up ticket)
- Explain or rename the `herdr-*` names (follow-up ticket, needs a naming decision)

<!-- tickets:plan -->

1. Chart lockdown (WI1)
   - Files: charts/dev-namespace/templates/networkpolicy-{ingress,egress}.yaml (delete `- {}`, fix the egress header comment), templates/operator-role.yaml (resourceNames for configmaps and secrets), values.yaml:13-14, Chart.yaml (0.6.0), charts/dev-namespace/README.md (0.5.1 and 0.6.0 sections, Objects text), .github/workflows/verify.yml (e2e network probes)
   - e2e probes: start a listener pod in env-ci and a client pod in default. Assert connect fails both ways. Assert `nslookup kubernetes.default` works from env-ci. If kindnet does not enforce policy, install Calico in the e2e.
   - Verify: the helm template grep, `helm lint`, pre-commit, e2e

2. Operator optional config (WI2)
   - Files: operator/internal/pen/reconcile.go (ConfigMapVolumeSource Optional: ptr.To(true), pod AutomountServiceAccountToken: ptr.To(false)), reconcile_test.go
   - e2e: create the bootstrap ConfigMap with models.json only
   - Keep the ConfigMap existence gate (reconcile.go:78)
   - Verify: `go test ./...`, e2e

3. Entrypoint placement (WI3)
   - Files: devenv/entrypoint.sh, .github/workflows/verify.yml (image job)
   - Design: for each of models.json, settings.json, litellm_key: when the source is non-empty (`-s`), install it and write its sha256 to /home/.herdr/projected/<name>.sha256. When the source is absent or empty and a marker exists: remove the target only if its hash still matches the marker, then remove the marker. Never touch a target with no marker.
   - Probes: `docker run` with an empty /run/herdr/config and a test authorized_keys, check `pgrep sshd`, `id <user>`, no ~/.pi/agent. Then two runs on one named /home volume (with keys, then without) and check removal, with a developer-written settings.json that stays.
   - Verify: shellcheck through pre-commit, image job

4. Chart-bump guard on push (WI4)
   - File: .github/workflows/verify.yml (drop the PR-only `if`, base = pull_request.base.sha or github.event.before, skip when before is all zeros)
   - Verify: a throwaway PR that touches charts/ without a bump fails

5. Docs (WI5)
   - Files: README.md (Prerequisites, values table, model modes, step 4, Working on pipen, install pin), charts/dev-namespace/README.md (0.4.0 section points to the README)
   - Verify: pre-commit (prettier), a grep for modals and em-dashes in the changed hunks
