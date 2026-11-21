---
id: PI-001
github_issue: 1
github_url: https://github.com/caktus/pipen/issues/1
last_synced: "2026-11-20T22:19:08.735616+00:00"
synced_hash: 7c5c22b65739ce6cebd9da832c73dd9283cef8640eca85b492959081550639ec
title: Add herdr kata dev environments and CLI
type: Feature
assignee: "@me"
labels:
  - phase/done
context:
  relates_to: []
---

As a developer, I want to bring up an isolated dev environment on the cluster with one command, so that I can ssh into a fresh machine per project instead of waiting for a dev VM.

This repo carries the generic half of that: the dev toolchain image inputs, the per-developer CLI, and the `dev-namespace` chart. Cluster-specific provisioning (storage nodes, registry, litellm keys, tailnet domain) stays in the private caktus deployment repo (see Related tickets).

### To Do

- Add `devenv/` (dev toolchain image: Dockerfile + entrypoint, and the pod/Service templates) + `cli/` (per-developer stdlib-only CLI)
- Add `charts/dev-namespace`: Namespace + ServiceAccount + ns-scoped Role/RoleBinding + SA token + ssh-key secret, so namespace provisioning needs no ansible
- Envs schedule on the Mayastor storage nodes (`openebs.io/engine: mayastor`); `/home` is a per-env `mayastor-2` PVC
- Verify tailnet exposure of the env Service via the Tailscale operator (`tailscale.com/expose` + `tailscale.com/hostname`) in a smoke test

## AC

- [ ] `pipen up <name>` yields a kata environment reachable from the tailnet as `ssh <dev>-<name>.<tailnet-domain>` (env names are force-prefixed with the developer's namespace — added if not present in the request), with docker, pi.dev, and the per-dev litellm key working inside
- [ ] Each developer works in their own namespace with a ServiceAccount token whose Role is scoped to that namespace only (no pvc delete — that is the admin-only data wipe)
- [ ] `pipen down <name>` removes the Pod/Service, and the persistent home directory survives and is wiped only by the documented admin action

## Related tickets

- caktus/hosted-llm#37 (cluster-side implementation and verification)
