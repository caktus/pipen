---
id: PI-005
github_issue: null
github_url: null
last_synced: null
synced_hash: null
title: Publish dev-namespace chart via GitHub Pages
type: Feature
assignee: '@me'
labels:
- phase/draft
context:
  relates_to: []
---

As an admin, I want the `dev-namespace` chart published as a Helm repository on GitHub Pages, so that I can `helm pull` or `helm install` it by name and tag instead of keeping a repo checkout and pointing at `charts/dev-namespace` by path.

Today the chart is installed by path from a checkout (`helm install <dev> charts/dev-namespace`). A CI workflow builds and tags the dev image on every merge to main; the chart has no equivalent.

### To Do
- Add a chart-releaser publish workflow, enable Pages on the `gh-pages` branch, and enforce version bumps in CI

## AC
- [ ] `helm repo add pipen https://caktus.github.io/pipen` lists `dev-namespace` and its published versions
- [ ] A merge to main that bumps the `Chart.yaml` version publishes a release and the chart to Pages; a chart change without a bump publishes nothing and fails `verify.yml`
- [ ] README documents the chart repo URL and the `helm install` flow by repo

<!-- tickets:research -->
Gathered 2026-07: codebase scout (inline files, this repo) and web research. Each finding cites its source.

## Chart and CI state (scout)

- `charts/dev-namespace/Chart.yaml` is static at `version: 0.1.0`; the repo has no changelog and no other versioning. The chart takes `dev` (namespace name, RFC 1123) and `authorizedKeys` (ssh public keys).
- House CI style (`.github/workflows/publish.yml`): push-to-main + schedule + `workflow_dispatch` only, no `pull_request` trigger (never publish unmerged code), minimal `permissions:` block, `actions/checkout@v4`, self-contained bash, `if: env.X == 'true'` conditionals.
- The chart is consumed only by path from a checkout: `helm install <dev> charts/dev-namespace ...` (README Admins step 2). No repo reference anywhere else.
- No existing `gh-pages` branch or Pages config in the repo. Pre-commit `check-yaml` and `prettier` exclude `^charts/.*/templates/`, so a new workflow file is linted normally.
- The dev image tag (`YYYYMMDD-pi<pi-version>`) is derived from the built image content; there is no equivalent derived version signal for the chart. A chart version must come from `Chart.yaml`.

## How a chart gets to GitHub Pages (web)

- Canonical tooling: `helm/chart-releaser` (CLI `cr`) via `helm/chart-releaser-action`. On push to main it creates a GitHub Release per new chart version (skips already-published versions) and `cr index` pushes `index.yaml` + the `.tgz` packages into the `gh-pages` branch. `GITHUB_TOKEN` with job-level `permissions: contents: write` is sufficient (documented in the official how-to). Sources: https://github.com/helm/chart-releaser-action , https://helm.sh/docs/howto/chart_releaser_action/
- `helm push` is NOT this flow: it uploads to an OCI registry, which has no `index.yaml`. For a file-based Pages repo the index comes from `cr index` or hand-rolled `helm package` + `helm repo index --merge` + git push (example: https://github.com/shvbsle/k10s/blob/main/.github/workflows/release-helm.yml). Source: https://helm.sh/docs/helm/helm_push/
- Alternative mechanism: build the chart repo in CI and publish it with `actions/upload-pages-artifact` + `actions/deploy-pages` (`pages: write` + `id-token: write`), e.g. https://github.com/SAP/pod-reloader-helm/blob/main/.github/workflows/publish.yaml (`.tgz` as release assets, index.yaml at the Pages root). More moving parts than the branch-based flow.
- Helm client rules: a repo is a server of `index.yaml` + `.tgz` files; entries carry `urls` + sha256 `digest`; the version must be SemVer 2. Stale entries (tgz deleted or moved while the index still lists it) make `helm pull` 404 while `helm search` still lists the version. Sources: https://helm.sh/docs/topics/chart_repository/ , https://github.com/helm/helm/issues/12548
- Versioning convention in the wild: the `Chart.yaml` bump IS the release trigger; chart-releaser skips existing versions, so merging chart changes without a bump publishes nothing. Auto-bumping per commit conflicts with chart-releaser's immutable per-version releases. Sources: https://github.com/helm/chart-releaser , https://github.com/ephico2real2/group-sync-operator-helm-chart/blob/fc6c73c7/docs/RELEASING.md
- `https://caktus.github.io` 404s today (no org site, no name collision); the project site would be `https://caktus.github.io/pipen`. Verified by live fetch.
- Public exposure check: a chart repo publishes `Chart.yaml`, `templates/`, default `values.yaml`, `NOTES.txt` — never install-time values. Scout audit of `charts/dev-namespace/**`: no secret material or cluster-specific values (keys and tokens are user-supplied or controller-filled at install time). Safe to publish publicly.

## Open items

- One-time setup needed in the GitHub UI (or API): enable Pages with the `gh-pages` branch as source.
- `cr index` and `cr` need `fetch-depth: 0` on checkout (tags), per the action README.

<!-- tickets:approaches -->
Three ways to get the chart onto a Pages-served repo:

**A. `helm/chart-releaser-action` + `gh-pages` branch (recommended)**
One workflow: on push to main, the action runs `cr` (creates a GitHub Release per new `Chart.yaml` version, skips existing versions) and `cr index --push` (writes `index.yaml` + `.tgz` into `gh-pages`). Enable Pages on `gh-pages` once.
- Pro: maintained by the Helm org; does release + index + Pages branch in one action; `GITHUB_TOKEN` + `contents: write` is enough; version-bump-as-release is the standard, enforced pattern.
- Pro: a GitHub Release per chart version is a useful artifact, not spam (one release per bump).
- Con: one more external tool in the pipeline (`cr`), unlike the hand-rolled `publish.yml`.

**B. Hand-rolled: `helm package` + `helm repo index --merge` + git push to `gh-pages`**
Copy the `publish.yml` style: detect chart change, package, check out `gh-pages`, rebuild the index with `--merge`, commit, push.
- Pro: no third-party action; matches house style.
- Con: we own the index and the push-conflict handling; no releases; version bump is purely convention, easy to get wrong (re-packaging the same version silently overwrites the `.tgz` and leaves the old index entry).

**C. Pages-artifact deploy (`upload-pages-artifact` + `deploy-pages`), tgz as release assets**
Build the repo in CI, publish as a Pages artifact, index at the site root, `.tgz` referenced by release-asset URL.
- Pro: no `gh-pages` branch; the modern Pages flow.
- Con: needs releases anyway (for the tgz assets) plus the `github-pages` environment and OIDC (`id-token: write`); index at site root only; the most moving parts of the three.

**Recommendation: A.** It is the official tooling for exactly this job, it needs the fewest lines of our code, and its "bump `Chart.yaml` to release" model is enforced by the tool rather than by discipline. We add a small check to `verify.yml` so a PR that touches `charts/` must bump the version, keeping the pattern honest.

<!-- tickets:plan -->
Approach A (chart-releaser). Conventions: Conventional Commits, Simple English, `pre-commit run --all-files` before each commit, no `pull_request` trigger on the publish workflow.

### Task 1: Publish workflow + first version bump

Files: create `.github/workflows/publish-chart.yml`; modify `charts/dev-namespace/Chart.yaml`.

- Bump `charts/dev-namespace/Chart.yaml` `version` to `0.2.0` (the first published version).
- Create the workflow, mirroring `publish.yml` style (header comment, `defaults: run: shell: bash`, timeout):

  ```yaml
  name: publish-chart
  on:
    push:
      branches: [main]
    workflow_dispatch:
  permissions:
    contents: write
  jobs:
    release-chart:
      runs-on: ubuntu-latest
      timeout-minutes: 15
      steps:
        - uses: actions/checkout@v4
          with:
            fetch-depth: 0   # cr needs tags
        - uses: helm/chart-releaser-action@v1
          env:
            CR_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          with:
            charts: ./charts
            packages_with_index: true
            index_documentation: false
            fail_on_empty: false
  ```

  Behavior: no new `Chart.yaml` version → no release, run is green. New version → GitHub Release `dev-namespace-<version>` plus `dev-namespace-<version>.tgz` and `index.yaml` committed to the `gh-pages` branch.
- Verify: merge the bump to main, then `gh release view dev-namespace-0.2.0` and `git ls-remote origin gh-pages` (or the branch contents) show the tgz + `index.yaml`.
- Commit: `ci: publish the dev-namespace chart on version bumps`

### Task 2: Enable GitHub Pages (one-time, manual)

- GitHub UI: Settings → Pages → Source "Deploy from a branch" → `gh-pages` / `/ (root)`.
- Verify: `curl -fs https://caktus.github.io/pipen/index.yaml` returns the index with the `dev-namespace` entry and a `https://caktus.github.io/pipen/dev-namespace-0.2.0.tgz` url.
- No commit; note the setup step in Task 3's README text.

### Task 3: Enforce the bump in `verify.yml`

Files: modify `.github/workflows/verify.yml`.

- Add a step that runs on `pull_request`: if the PR diff touches anything under `charts/`, it must also change the `version:` line of `charts/dev-namespace/Chart.yaml`, else fail with the message `charts/ changed but the chart version was not bumped`.
- Verify: a test PR touching a chart template without a bump fails; with a bump it passes.
- Commit: `ci: require a chart version bump with chart changes`

### Task 4: README

Files: modify `README.md`.

- Admins step 2: lead with the repo flow, keep path install as the fallback:

  ```sh
  helm repo add pipen https://caktus.github.io/pipen
  helm repo update pipen
  helm install <dev> pipen/dev-namespace --version <version> \
    --set dev=<dev> --set 'authorizedKeys[0]=<ssh public key>'
  ```

- One paragraph: publishing works by bumping `Chart.yaml` `version`; the CI workflow publishes on the merge to main; Pages is enabled once from the repo settings.
- Commit: `docs: document the chart repository in the README`

### Task 5: End-to-end verification (no commit)

- On a dev cluster: `helm repo add pipen https://caktus.github.io/pipen && helm repo update`, `helm search repo pipen/dev-namespace` lists `0.2.0`, `helm install e2e-test pipen/dev-namespace --version 0.2.0 --set dev=e2e-test --set authorizedKeys[0]=<key>`, and `kubectl -n e2e-test get` shows the same objects the path install creates (Namespace, ServiceAccount `deploy`, Role, RoleBinding, `deploy-token`, `herdr-dev-e2e-test`).
- `helm uninstall e2e-test && kubectl delete ns e2e-test` to clean up.

## Review focus

- A merge that changes a chart template without a version bump must not publish and must fail `verify.yml`.
- A re-merge that does not change the version must not create a duplicate release (cr skips existing versions).
- The `gh-pages` branch must contain only the published artifacts; the workflow must never push source code to it.
- The published `index.yaml` urls must point at the Pages root (`https://caktus.github.io/pipen/...`), or every `helm pull` 404s.
