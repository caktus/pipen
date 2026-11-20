---
id: PI-004
github_issue: 4
github_url: https://github.com/caktus/pipen/issues/4
last_synced: '2026-11-20T22:19:25.847038+00:00'
synced_hash: d43a45e24fd64749a576cfa41d58d60f191bed7f37ed98dae16a1eff5e211c66
title: Add a pipen operator to manage pens
type: Feature
assignee: '@me'
labels:
- phase/done
context:
  relates_to:
  - PI-001
  - PI-003
---

As a developer, I want `pipen up` to create a single Pen resource and have an operator bring the environment up, so that the CLI needs no template files or cluster-shape knowledge, and my token can no longer create raw (privileged) pods.

Today the CLI renders pod/Service templates and creates four objects itself (PVC, env ConfigMap, Pod, Service), then polls Pod readiness and `Service.status.loadBalancer.ingress` for the hostname — so the dev token needs broad namespace RBAC, and the pod spec shape lives in client-side templates. A cluster-wide Go operator (installed once by its own chart) watching a namespaced `Pen` CRD takes over object creation and status; the dev-namespace chart grants it per-namespace Roles and shrinks the dev Role to Pens. Pen updates and other lifecycle events are out of scope for the MVP.

### To Do

- Define the `pens.pipen.dev/v1alpha1` Pen CRD (spec: image/homeSize/cpu/memory; status: phase/ready/hostname/node) and a Go controller-runtime operator that reconciles Pens using cluster-shape defaults from its own ConfigMap
- Add `charts/pipen-operator/` (CRD, Deployment, ServiceAccount, leader-election RBAC, operator config) and extend `charts/dev-namespace/` with the operator's per-ns Role/RoleBinding and the reduced dev Role
- Rewire the pipen CLI to manage Pens only (up/down/ls/ssh from CR status; logs unchanged) and delete `devenv/templates/`

## AC

- [ ] A single operator Deployment (installed by its own chart) reconciles Pens in any provisioned developer namespace with no cluster-scoped write RBAC — cluster-scoped read-only list/watch on Pens plus per-ns Roles created by dev-namespace, nothing else
- [ ] `pipen up <name>` creates one Pen CR and the env comes up (kata pod on a storage node, mirrored home PVC, tailnet Service) with the ssh line printed from CR status; the CLI requires no template files, checkout, or tailnet_domain
- [ ] The dev token can no longer create pods/services/PVCs in its namespace (`kubectl auth can-i` verified); it can manage Pens and read pod logs
- [ ] `pipen down <name>` removes the Pod/Service and leaves the home PVC and its data, same as today

## Related tickets

- caktus/hosted-llm#37 (original herdr env implementation; closed/superseded by this repo)

<!-- tickets:research -->

## Research

### What the CLI does today (source: `cli/pipen.py`, `devenv/templates/`)

- `up` creates four objects directly: PVC `<pen>-home` (`ensure_pvc`), per-env ConfigMap
  `env-<pen>-config` (`ensure_env_configmap`, copied from the dev ConfigMap), the kata Pod
  (rendered from `devenv/templates/pod.yaml`), and the tailnet LoadBalancer Service
  (`devenv/templates/service.yaml`). Then `wait_pod_ready` polls the Pod and a second loop
  polls `Service.status.loadBalancer.ingress` for the hostname.
- Config file keys: `api_base`, `namespace`, `dev_user`, `tailnet_domain`, `token`;
  `PIPEN_TEMPLATES` (or a repo checkout) is required for `up`.
- Consequence: the dev Role (`charts/dev-namespace/templates/role.yaml`) grants CRUD on
  pods/pods-log/pods-exec/services/configmaps/secrets + pvc get/list/create/watch. The pod
  template is `privileged: true` — i.e. today a dev token can create _any_ privileged pod
  in its namespace.

### Topology facts

- CRDs are cluster-scoped. A per-developer-namespace operator would mean N operator
  deployments and N chart releases fighting over one CRD. One operator install (one
  release) owns the CRD via the chart's `crds/` dir — no multi-release ownership problem.
- A namespaced RoleBinding may reference a ServiceAccount in _another_ namespace
  (subject `namespace` field) — confirmed against the k8s RoleBinding docs
  (kubernetes.io/docs/reference/kubernetes-api/rbac/role-binding-v1) — so
  `charts/dev-namespace/` can grant the operator SA per-ns Roles with no ClusterRole.
- **controller-runtime cannot scope its cache to dynamically-discovered namespaces**:
  `cache.Options.DefaultNamespaces` is a static name map, and label-selector namespace
  scoping is an open, unimplemented feature (controller-runtime issue #3178). A per-ns
  static cache would require an operator restart whenever a developer is provisioned —
  a non-starter. Hence the cluster-wide watch: cluster-scoped read-only list/watch on
  Pens (informers list across namespaces) + all writes in per-ns Roles. The reconciler
  additionally checks the namespace's `herdr.dev/owner` label and marks Pens in
  un-provisioned namespaces `Failed` ("namespace not provisioned") instead of 403-spamming.
- controller-runtime leader election needs a small Role in the operator's own namespace
  (leases, events).
- The pod/env shape is cluster-specific (kata runtime, `openebs.io/engine: mayastor`
  nodeSelector, `mayastor-2` StorageClass, tailscale Service annotations). These belong
  in operator ConfigMap values written by the chart (the repo's existing convention:
  generic chart, environment-specific values at deploy time) — the operator binary stays
  cluster-generic.
- The dev's runtime inputs the pod needs today (authorized_keys Secret
  `herdr-dev-<dev>`, pi config + litellm key ConfigMap `herdr-dev-<dev>`) are read by the
  operator directly and projected into the pod — the per-env ConfigMap goes away.
- `DEV_USER` is the namespace name (the chart's `dev` value); the operator can derive it
  from the Pen's namespace.

### Tailscale operator interaction (source: HO-037 research, verified live)

- The operator's per-Service sidecar proxy is owner-referenced to the Service: it
  follows the Service (created → sidecar up; deleted → sidecar gone) and is untouched by
  who owns the _Pod_. The Service the operator creates is shape-identical to the one the
  CLI creates today (`LoadBalancer` + `loadBalancerClass: tailscale` + expose/hostname
  annotations), so the proven L4 exposure behavior carries over unchanged.
- The assigned MagicDNS hostname may be suffixed by the operator (observed `<name>` →
  `<name>-1`); the source of truth is `status.loadBalancer.ingress` (hostname, else ip)
  — the reconcile loop reads exactly that, which is also where the CLI's `status.hostname`
  comes from.

### Live cluster test feedback (2026-11-20)

Source: live cluster test of the published dev-namespace chart (0.2.0),
the pipen CLI, and this branch (fresh per-dev namespace, pen up/adopt,
no in-cluster registry, GHCR pulls). The operator flow works end to end
and the CLI UX is good. Findings, roughly by severity:

1. **Helm 4 does not install `crds/`.** The operator chart relies on
   Helm's `crds/` auto-install. Helm 4 (tested: 4.3.2) ignores `crds/`
   at all, so on a Helm 4 cluster the CRD silently never exists: the
   operator pod starts, acquires its leader lease, looks healthy — and
   every CLI call 404s. The README's "applied at install only"
   describes Helm 3 behavior.
2. **The operator image is a required chart value but nothing
   publishes it.** `image: ""` is required; the branch CI verifies the
   build, but the publish workflow only covers the dev image. A
   first-time installer must build the operator and stage it on
   schedulable node(s) by hand.
3. **Version downgrade on merge.** The branch's dev-namespace chart is
   0.1.0 while the published one is 0.2.0. Merging and republishing
   would be a version _downgrade_ with a different Role (pens verbs
   replace the pod/Service verbs).
4. **Pens Role migration wrinkle.** A dev namespace upgraded to the
   pens Role can no longer manage pre-existing raw pod/Service envs
   (the dev token loses pod create/delete), and the new CLI has no Pen
   for them — `kubectl delete pod` is the only teardown. (Decision
   2026-11-20: there are no live namespaces to upgrade, so no upgrade
   note is needed; document only the 0.3.0 break.)

Working well (keep doing): PVC adoption on pen create (a leftover
`<env>-home` from a partial run is reused, not re-provisioned); the Pen
CR status (phase + message + hostname) is exactly the right surface
for the CLI poll loop; `down` preserving the PVC and printing the
admin wipe command; the deploy-token design (Helm never manages the
controller-populated `data.token`; deleting the Secret rotates) across
idempotent re-runs; the specific, actionable CLI error messages.

### Repo state the fixes must land on (recon, 2026-11-20)

- `main` is 12 commits ahead of the branch. It holds the published
  dev-namespace 0.2.0, `.github/workflows/publish-chart.yml`
  (chart-releaser to the gh-pages branch; `charts_dir: charts`, so it
  publishes _any_ chart under `charts/`), `scripts/chart_index.py`,
  `charts/dev-namespace/README.md`, and a verify.yml check that
  requires a `dev-namespace/Chart.yaml` version bump with any
  `charts/` change (dev-namespace only).
- Consequences: the 0.3.0 bump must land on top of a merge of
  `main`, and the bump check must generalize to every changed chart.
- Local machine has helm 4.3.2 (the bug-report version) for
  verification; go and docker available.
- For the operator CRD check unit test: `k8s.io/client-go v0.33.0`
  `discovery/fake.FakeDiscovery` gets `Resources` promoted from its
  embedded `*testing.Fake`, so construct it as
  `&discoveryfake.FakeDiscovery{Fake: &clientgotesting.Fake{Resources:
[]*metav1.APIResourceList{{GroupVersion: "pens.pipen.dev/v1alpha1"}}}}`;
  `ServerGroups()` derives the group list from `Resources`.

<!-- tickets:approaches -->

## Approaches

### Operator implementation

Chosen (operator, 2026-11-20): **Go + sigs.k8s.io/controller-runtime**.

- Standard operator stack: typed CRDs, reconcile loops, finalizers, leader election,
  envtest for controller unit tests. Independent deployable, so the repo's second
  language is a contained cost.
- Rejected: **Python (kopf)** — one repo language, but weaker operator story (finalizers,
  webhooks, community maturity) for little gain.
- Rejected: **no CRD** (operator watches plain objects, e.g. annotated Services) — loses
  the typed spec/status that drives the whole UX win (CLI reads `status.hostname` etc.).

### Topology

Chosen (operator, 2026-11-20): **one cluster-wide operator + per-namespace RBAC**.

- One `charts/pipen-operator/` install: single Deployment to run/upgrade, single CRD
  owner, operator reachable only in provisioned dev namespaces (its write RBAC is a set
  of per-ns Roles created by `charts/dev-namespace/`; plus cluster-scoped read-only
  list/watch on Pens only).
- Rejected: per-namespace operator (original sketch) — N deployments, CRD multi-release
  ownership problem, and no real isolation gain (RBAC already scopes writes per-ns).
- Accepted trade-off: one operator down blocks _all_ devs (per-ns operators would
  isolate the failure). Fine for a team dev tool; leader election is on so a future
  2-replica HA flip is cheap.

### PR feedback fixes (2026-11-20)

**CRD install (feedback 1) — chosen: move the CRD into
`templates/`.** Helm 3 and 4 both install it as a normal release
resource. v1alpha1 is frozen, so the rendered bytes never change and
`helm upgrade` is a no-op diff. The operator also discovery-checks the
`pens.pipen.dev` API group at startup and exits with a self-explanatory
error when it is missing (defense in depth: the failure names itself
instead of surfacing as an opaque informer error).

- Rejected: keep `crds/` + documented kubectl step. The Helm 4 break
  stays the default for every installer.
- Rejected: readiness gate only. The pod goes unhealthy but the cause
  stays opaque, and the fix is still manual.
- Accepted cost: removing the CRD from the chart would make Helm delete
  the CRD (data loss) — noted in NOTES.txt. Schema changes need
  kubectl while v1alpha1 is frozen.

**Operator image (feedback 2) — chosen: publish to GHCR like the dev
image (user decision).** A `publish-operator` workflow mirrors
`publish.yml` (push to main, nightly 04:00, manual dispatch; no cache;
digest compare against `:latest`; push only on change; keep the 14
newest date tags). Tags: `YYYYMMDD` + `:latest` (no pi-version
component; the operator carries none). The chart's `image:` defaults to
`ghcr.io/caktus/pipen-operator:latest`; the `requireImage` failure goes
away. A new `nodeSelector` values passthrough pins the Deployment to
node(s) that hold a locally staged image. The README keeps a short
"building the operator image yourself" fallback (build + `--set
image`, `nodeSelector` for staging).

- Rejected: build + staging recipe only. It keeps the one-node
  staging problem for every cluster.
- One-time setup: set the package `caktus/pipen-operator` visibility to
  public (anonymous pull), same as the dev image.

**Chart versioning (feedback 3) — chosen: merge main, bump
dev-namespace to 0.3.0.** The published 0.2.0 keeps the old Role;
0.3.0 is the pens Role. The break (dev tokens manage Pens only; pod,
Service and PVC creation move to the operator ServiceAccount) is
documented in the chart README and NOTES.txt. `pipen-operator`
publishes for the first time at 0.1.0.

**Chart publishing — chosen: publish both charts to the Pages repo
(user decision).** `publish-chart.yml` already uses `charts_dir:
charts`, so `pipen/pipen-operator` ships for free. The chart is
cluster-generic and carries no secrets (storage class, node selector
are overridable values).

**Migration note (feedback 4) — dropped (user decision).** No live
namespaces to upgrade; the 0.3.0 break is documented, no upgrade
runbook.

**CI — add:** `helm lint` + `helm template` for `pipen-operator` in
verify.yml (9 manifests, assert the CRD kind is present); generalize
the bump check so every changed chart requires a `version:` bump in its
own Chart.yaml.

<!-- tickets:plan -->

## Plan

### CRD

`pens.pipen.dev/v1alpha1`, kind `Pen`, Namespaced, printer columns (phase, hostname,
node).

- `spec`: `image` (string, optional — empty = operator default), `homeSize`
  (default `10Gi`), `cpu` (default `4`), `memory` (default `8Gi`).
- `status`: `observedGeneration`, `phase` (`Pending|Creating|Ready|Failed`), `ready`
  (bool), `hostname`, `node`, `message`.
- Subresource `status`.

### Operator (`operator/`, Go + controller-runtime)

- `api/v1alpha1/pen_types.go` — types + `groupversion_info.go` (scheme, CRD manifest
  generation via controller-gen into the chart's `crds/`).
- `internal/config/config.go` — read ConfigMap `pipen-operator-config` from the
  operator's own namespace (watched; changes re-reconcile all Pens): `image`,
  `homeSize`, `cpu`, `memory`, `nodeSelector` (JSON), `storageClass`,
  `serviceAnnotations` (JSON), `tailnetDomain` (optional, informational env).
- `internal/pen/reconcile.go` — per-Pen: ensure PVC `<pen>-home` (create-if-absent,
  **never delete**); ensure Service `<pen>` (LoadBalancer, port 22, annotations from
  config, ownerRef → Pen); ensure Pod `env-<pen>` (runtimeClass `kata`, privileged,
  resources from spec, `DEV_USER` = namespace name, projected `herdr-dev-<ns>`
  Secret/ConfigMap, home PVC at `/home`, `herdr.dev/env: <pen>` label kept on Pod and
  Service for tooling continuity, imagePullPolicy from config — `IfNotPresent`
  default, `Always` for `latest` tags). When the Pod is ready, copy
  `Service.status.loadBalancer.ingress[0]` (hostname, else ip) into
  `status.hostname` and set `phase: Ready`. Not ready yet → requeue with backoff.
  `herdr-dev-<ns>` ConfigMap/Secret absent → `phase: Pending` + message, requeue with
  backoff (not a permanent failure); other errors → `phase: Failed` + `message`.
  Pens in a namespace without the `herdr.dev/owner` label → `phase: Failed`,
  "namespace not provisioned". Reconcile is **create-or-leave**: it never mutates
  specs of existing Pod/Service/PVC (update propagation is out of scope), so operator
  config changes apply to newly created pens only.
- Delete: ownerRef garbage collection removes Pod/Service; the PVC is intentionally
  left (admin wipe = `kubectl delete pvc`, unchanged). No finalizers in the MVP.
- `main.go` — manager with leader election on (single replica in the chart),
  cluster-wide Pen watch.
- `Dockerfile` — multi-stage `golang:1.24` → `gcr.io/distroless/static`, non-root user.

### RBAC

- Operator: ClusterRole `pens get/list/watch` + `namespaces get` (read-only,
  for the provisioned-namespace check; cluster-scoped reads are the only
  cluster grants — the plan's original list omitted `pens/status update`
  and `namespaces get`, which the final review found; a Pen in an
  un-provisioned namespace gets no status write, by design: the reconcile
  errors with backoff and only a cluster-admin can create such Pens). Per-ns
  Role in each dev namespace (created by dev-namespace): `pens/status`
  (update), pods (create/get/list/watch/patch/update/delete), services
  (same minus patch), persistentvolumeclaims (create/get/list/watch — **no
  delete**), configmaps (get), secrets (get). Plus a Role in the operator
  namespace (leases, events, configmaps get/list/watch for the
  namespace-scoped config informer). All non-Pen reads in the reconciler go
  through the API reader (direct reads); the manager cache holds only Pens
  plus the operator's own ConfigMap (scoped to the operator namespace),
  which is what makes the per-ns grants sufficient.
- Dev (`deploy` Role, shrunk): pens (pipen.dev) get/list/watch/create/update/delete,
  pens/status get, pods get, pods/log get.

### Charts

- **`charts/pipen-operator/`** (new, installed once): `crds/` (generated Pen CRD),
  Namespace `pipen`, ServiceAccount `pipen-operator`, leader-election Role/Binding,
  ClusterRole + cluster-scoped RoleBinding (pens read), Deployment (1 replica, image
  from values), ConfigMap `pipen-operator-config` rendered from values
  (`image`, `homeSize`, `cpu`, `memory`, `nodeSelector`, `storageClass`,
  `serviceAnnotations`, `tailnetDomain`, `imagePullPolicy`), NOTES.txt. Installed
  exactly once per cluster: its cluster-scoped template objects (ClusterRole,
  RoleBinding) would conflict across releases, and `crds/` CRDs are applied at install
  only — `helm upgrade` never updates the CRD schema (v1alpha1 is frozen for the MVP,
  schema changes would need a manual migration note in NOTES).
- **`charts/dev-namespace/`** (modified): values `operator: {namespace: pipen}`;
  new `operator-role.yaml` + `operator-rolebinding.yaml` (subject: SA
  `pipen-operator` in `operator.namespace`); `role.yaml` shrunk per RBAC
  above. Assumption: the operator is installed first (a dangling binding is
  harmless; until the operator exists, pens in that namespace simply never
  reconcile — pre-operator namespaces are not a supported state).

### CLI (`cli/pipen.py`)

- `up <name> [--image X] [--size 10Gi] [--cpu 4] [--memory 8Gi] [--timeout 1800]` —
  POST the Pen (spec; `--image` pins, empty = operator default), poll
  `GET pens/<name>` status until `ready` → print `ssh <dev>@<hostname>`.
- `ls` — list Pens with phase/node/hostname from status.
- `ssh <name>` — hostname from Pen status.
- `down <name>` — DELETE the Pen.
- `logs <name>` — unchanged (`pods/log`).
- Config file: drop `tailnet_domain` (required keys now `api_base`, `namespace`,
  `dev_user`, `token`).
- Delete: `templates_dir()`, `PIPEN_TEMPLATES`, `render()`, `ensure_pvc()`,
  `ensure_env_configmap()`, `wait_pod_ready()`, `service_hostname()` — and
  `devenv/templates/pod.yaml` + `service.yaml`. Stdlib-only stays.

### Build + CI

- Operator image: build in CI alongside the dev image (`.github/workflows/verify.yml`:
  add a `go` job — `gofmt`/`go vet`/`go build` + `go test` (envtest or table-driven
  reconcile tests) — and a second image build for `operator/`). The operator image
  follows the dev image's distribution flow; when PI-003 (nightly GHCR builds) lands,
  both images move to GHCR together.
- pre-commit: add `gofmt`/`go vet` hooks for `operator/`.
- README.md: admin flow becomes (1) install pipen-operator once, (2) install
  dev-namespace per developer, (3) ConfigMap, (4) hand over the CLI config
  (no more `tailnet_domain`); CLI section drops `PIPEN_TEMPLATES`.

### Test strategy (in place on the hosted-llm cluster, fenced)

Blast radius: operator ns `pipen` (admin, deletable), scratch dev namespaces
`op-<a>`/`op-<b>` (all objects chart-created + Pens), no other namespace touched.

1. Build the operator image locally; import/push via the existing registry pattern.
2. `helm install pipen-operator charts/pipen-operator -n pipen` → CRD present,
   Deployment ready.
3. `helm install op-a charts/dev-namespace --set dev=op-a …` → `kubectl auth can-i`
   as the dev SA: `create pods` **no**, `create pens.pipen.dev/pens` **yes**.
4. `pipen up smoke` → Pen status Ready: PVC bound on a storage node, kata pod, LB
   Service with hostname in status; ssh from the tailnet; pi.dev + litellm key work
   inside (proves projection).
5. `pipen down smoke` → Pod/Service gone, PVC + data remain; recreate → re-attach.
6. `pipen up` in `op-b` → the single operator serves both namespaces.
7. `kubectl delete pen` ≡ `pipen down` (GC via ownerRef).

### Out of scope (MVP)

Pen updates/rollouts (image change propagation), home wipe via the API, per-namespace
config overrides, operator metrics, HA beyond leader election, CRD versioning past
v1alpha1.

### PR feedback implementation plan (2026-11-20)

One commit per task (Conventional Commits, Simple English), `pre-commit
run --all-files` before each commit. Local helm is 4.3.2; use it for
all chart verification (the bug-report version).

**1. Merge main.** `git fetch origin && git merge origin/main`.
Conflicts: `charts/dev-namespace/role.yaml` → the branch's pens Role
wins (pens CRUD, pens/status get, pods get, pods/log get).
`Chart.yaml` → main's 0.2.0. README/NOTES/verify.yml → main's, with
the branch's operator-flow text folded back where both sides added
content. Verify: both charts lint, dev-namespace renders 8 kinds,
`go test ./...` in `operator/`, pre-commit green. Commit: `chore: merge
main into the operator branch`.

**2. CRD into templates + loud startup check.**

- `git mv charts/pipen-operator/crds/pens.pipen.dev.yaml charts/pipen-operator/templates/crd.yaml`.
- `operator/hack/generate.sh`: controller-gen output dir
  `../charts/pipen-operator/templates`, rename target `crd.yaml` (keep
  the prettier step pointed at the new path).
- `operator/main.go`: new
  `crdGroupAvailable(ctx context.Context, d discovery.DiscoveryInterface) (bool, error)`
  — `ServerGroups`, match `v1alpha1.GroupVersion.Group`. In `main()`,
  before the manager starts: discovery client from
  `ctrl.GetConfigOrDie()`, and when the group is missing print a loud
  error (name the chart and `templates/crd.yaml`) and exit 1. TDD:
  `operator/main_test.go` with two tests (group present → true, absent
  → false) built with `discoveryfake.FakeDiscovery` (see recon note on
  the `Resources` field).
- Verify: `helm template` renders 9 kinds including
  `CustomResourceDefinition`; `helm template --show-only
templates/crd.yaml` is byte-identical to the old `crds/` file
  (frozen-schema guarantee).
- NOTES.txt + README: the CRD is a release-managed resource, installed
  on Helm 3 and 4 alike; upgrades are no-ops while v1alpha1 is frozen;
  removing the CRD from the chart would make Helm delete it.
- Commit: `fix: install the Pen CRD from templates for Helm 3 and 4`.

**3. Operator image publish + chart defaults.**

- `.github/workflows/publish-operator.yml` mirrors `publish.yml`:
  `IMAGE: ghcr.io/caktus/pipen-operator`, `docker buildx build
--no-cache --load -t pipen-operator:candidate operator/`, tag
  `$(date -u +%Y%m%d)` (no pi-version extraction), digest compare,
  push date tag + `:latest`, crane prune of `^[0-9]{8}$` tags keeping
  the 14 newest.
- values: `image: "ghcr.io/caktus/pipen-operator:latest"`, new
  `nodeSelector: {}`. Deployment: drop the `requireImage` include and
  helper; `imagePullPolicy` is `Always` for a `:latest` reference,
  `IfNotPresent` otherwise; render `nodeSelector` under `with`.
- Verify: default render (no `--set image`) shows the default image,
  `Always`, no nodeSelector; `--set image=local/op:v1 --set
nodeSelector.kubernetes.io/hostname=node1` shows the pinned image,
  `IfNotPresent`, and the rendered nodeSelector.
- README Admins step 2: published image default, one-time GHCR
  public-visibility setup line, short self-build subsection.
- Commits: `ci: publish the operator image to GHCR on main merges`,
  then `feat: default the operator chart to the published image`.

**4. dev-namespace 0.3.0.** Bump `Chart.yaml` to 0.3.0 (appVersion
stays 0.1.0). Chart README Objects section → the pens Role and the
`pipen-operator` Role; a short "0.3.0 changed the role" section (no
upgrade runbook). One line in NOTES.txt. Verify: lint + 8 kinds.
Commit: `feat: bump dev-namespace to 0.3.0 for the pens role`.

**5. verify.yml.** Replace the dev-only bump check with a per-chart
loop: every chart directory touched by the PR diff must carry a
`^[+-]version:` change in its own Chart.yaml. Add `helm lint
charts/pipen-operator` + a template/parse step (9 manifests, assert
`CustomResourceDefinition` among the kinds). Verify the loop body with
a synthetic git repo dry run (no-bump change fails, bumped change
passes). Commit: `ci: bump-check every changed chart and render the
operator chart`.

- Post-merge manual verification (no commit): releases
  `dev-namespace-0.3.0` and `pipen-operator-0.1.0` exist; the gh-pages
  `index.yaml` lists both; a merge with no bump publishes nothing;
  set the GHCR package `caktus/pipen-operator` visibility to public.

**6. README.** Chart-repo section: both charts published
(`pipen/dev-namespace`, `pipen/pipen-operator`). Admins step 2 leads
with the repo install, checkout path as fallback. Commit: `docs:
document both published charts in the README`.
