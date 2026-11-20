---
id: PI-004
github_issue: 4
github_url: https://github.com/caktus/pipen/issues/4
last_synced: "2026-11-20T15:38:40.856605+00:00"
synced_hash: d43a45e24fd64749a576cfa41d58d60f191bed7f37ed98dae16a1eff5e211c66
title: Add a pipen operator to manage pens
type: Feature
assignee: "@me"
labels:
  - phase/draft
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
