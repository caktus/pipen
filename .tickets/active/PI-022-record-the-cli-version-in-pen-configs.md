---
id: PI-022
github_issue: 22
github_url: https://github.com/caktus/pipen/issues/22
last_synced: "2026-11-24T04:39:27.322653+00:00"
synced_hash: f5341e516aa7fe49e349845e0c7f71e317857d58c539c2508cb308f65603797b
title: Record the CLI version in pen configs
assignee: "@me"
labels:
  - phase/draft
context:
  relates_to: []
---

As a maintainer, I want the generated pen config to record the CLI version that wrote it, so that I can tell which toolchain created a pen when I debug it weeks later.

The generated pen config file carries no record of which pipen CLI version wrote it, nor of the chart or operator versions in effect. A maintainer cannot tell which CLI created a pen or reproduce the exact toolchain.

### To Do

- Decide between a header comment and a structured field in the generated config
- Confirm where the CLI, chart, and operator versions are readable at generation time

## AC

- [ ] A generated pen config records the pipen CLI version that wrote it
- [ ] The generated config records the chart and operator versions where applicable
- [ ] A pen created with an older CLI still shows that older version in its config
