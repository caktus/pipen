---
id: PI-017
github_issue: 20
github_url: https://github.com/caktus/pipen/issues/20
last_synced: "2026-11-24T04:39:22.301471+00:00"
synced_hash: a9a322a33d3f52c9735b4994031e2f3d8b584310787afa62ad01efc44b142b51
title: Default pens to pinned image tags
assignee: "@me"
labels:
  - phase/draft
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
