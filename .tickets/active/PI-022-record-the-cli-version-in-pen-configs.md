---
id: PI-022
github_issue: 22
github_url: https://github.com/caktus/pipen/issues/22
last_synced: "2026-11-24T23:59:39.866388+00:00"
synced_hash: f5341e516aa7fe49e349845e0c7f71e317857d58c539c2508cb308f65603797b
title: Record the CLI version in pen configs
assignee: "@me"
labels:
  - phase/review
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

<!-- tickets:research -->

The pen config is a flat key: value file (api_base, namespace, dev_user, token, plus the optional image key from PI-017) that the admin writes by hand at bootstrap (README step 5); no CLI command writes it today, and no CLI command ever rewrites it afterwards (the only open() in cli/pipen.py is the read). Note at close: this plan adds the `pipen config` command, which is now the writer. The parser skips blank lines and whole-line comments, keeps unknown keys, and requires only the four core keys, so new version fields are safe in both directions (new CLI reads old file, old CLI reads new file). The researcher verified this by running the parser.

The CLI has no version constant; cli/pyproject.toml carries version 0.1.0, but a bare script run (the e2e mode) has no package metadata, so a module constant is the only source that works in both modes. The operator has no code version; its identity is the image tag (a UTC date, possibly latest), readable by the admin from the deployment. Chart versions are not readable in-cluster (no helm.sh/chart labels in any template); the admin knows them from helm or Chart.yaml. There are two charts: pipen-operator and dev-namespace.

Full research with citations: .superpowers/sdd/pi-022-plan/research.md in the branch worktree.

<!-- tickets:approaches -->

Controller rulings (2026-11-24):

- A new pipen config subcommand generates the file. The ticket says the config records the CLI version that wrote it, which requires a machine-writer. The admin runs it once at bootstrap with the four core values as flags. It writes a whole-line comment header, the core keys, the optional image key when the flag is given, and the version fields. It refuses to overwrite an existing file.
- Format: structured fields, not a header alone. The parser tolerates unknown keys in both directions (verified), and fields are testable. A whole-line generated-by comment is added for humans.
- Version fields: cli_version is always written (the module constant). operator_version and chart_version are optional, written only when the admin passes the flags (where applicable, per AC2). operator_version is the operator image tag, possibly latest. chart_version is the dev-namespace chart, the chart installed for the pen namespace. The pipen-operator chart version is out of scope: the operator image tag identifies the binary, and the chart is its packaging.
- cli_version source: a module constant at the top of cli/pipen.py, kept in sync with cli/pyproject.toml by a unit test that reads both and compares. The constant starts at 0.1.0. Bumps stay manual until the repo has a CLI release process.
- AC3 holds by construction: the file is written once by the generator, and no CLI command ever rewrites it. The README says so.

<!-- tickets:plan -->

Task 1: the pipen config command. A **version** constant at the top of cli/pipen.py (0.1.0, matching cli/pyproject.toml) and a new config subcommand: flags for out (file path), api-base, namespace, dev-user, token (all required) and image, operator-version, chart-version (all optional). It writes the file with a whole-line comment header naming the generator and version, the four core keys, and any given optional keys, with cli_version always present. It refuses to overwrite an existing file with a clear error. Python unit tests: a generated file parses back with the core keys and cli_version; optional keys appear only when flagged; an existing file is refused; the module constant equals the pyproject version.

Task 2: docs and e2e. README bootstrap step 5 uses the command instead of the hand-written heredoc, and documents the version fields (which are always present, which are optional, what chart_version means, that the CLI never rewrites the file so an older CLI keeps showing its own version). The e2e job replaces its hand-written config heredoc with the config command run as a bare script, and asserts the generated file carries cli_version.
