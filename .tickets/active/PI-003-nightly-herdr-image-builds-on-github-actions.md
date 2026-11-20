---
id: PI-003
github_issue: 3
github_url: https://github.com/caktus/pipen/issues/3
last_synced: "2026-11-20T14:51:25.380055+00:00"
synced_hash: 7178874f72192cedb1255f29ea85ec7a8b1465c6d8906fbfdfbd0425b040da9c
title: Nightly herdr image builds on GitHub Actions
type: Task
assignee: "@me"
labels:
  - phase/draft
context:
  relates_to: []
---

As an operator, I want the herdr dev image built in this public repo by a scheduled GitHub Action and pushed to GHCR, so that envs always start from the latest toolchain (pull-always) without anyone rebuilding images on a cluster node.

The dev toolchain image is currently built on a cluster node by the private deployment repo's ansible playbook and pushed to an in-cluster registry. This migrates the build to a nightly GHA workflow here — the source of truth for the image inputs is already this repo (`devenv/Dockerfile`, `devenv/entrypoint.sh`, `devenv/docker-daemon.json`) — and retires the local registry: pods pull `ghcr.io/caktus/pipen:<tag>` directly, anonymously.

### To Do

- Add a GHA workflow: nightly timer + manual dispatch, build (single-platform, no layer cache), layer-digest-compare against current `latest`, push `YYYYMMDD` + move `latest` only on content change, prune date tags beyond a rolling window
- Consumer side (private deployment repo): point the pod template at `ghcr.io/caktus/pipen` (`imagePullPolicy: Always` + `latest` default, `--tag` pin), drop the local registry and the playbook's image build tag

## AC

- [ ] The nightly workflow pushes a new `YYYYMMDD` tag and moves `latest` only when the built image's content changed; on a quiet night it pushes nothing; manual dispatch works; old date tags are pruned (rolling 14)
- [ ] `pipen up` with no flag starts the env on the latest image as of pod start (pull-always); `pipen up --tag <date-tag>` pins that tag; both verified live
- [ ] The in-cluster registry is gone and no playbook path builds images locally; README.md documents the new flow and pinning

## Related tickets

- caktus/hosted-llm#40
