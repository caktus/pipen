---
id: PI-020
github_issue: 18
github_url: https://github.com/caktus/pipen/issues/18
last_synced: "2026-11-24T04:34:19.029653+00:00"
synced_hash: a61ed8cd1bef137859b44287a7f5f84b4abad0d45130aa03c3c197078d15bf66
title: Reconcile pens as agent-sandbox Sandbox resources
type: Feature
assignee: "@me"
labels:
  - phase/plan-ready
context:
  relates_to: []
---

As a developer, I want my pen to run on an agent-sandbox `Sandbox` resource, so that I can pause it, let it shut itself down when idle, and restrict its network without rebuilding it.

Today the operator creates a raw kata `Pod` per Pen and only `pipen down` stops the compute. The agent-sandbox project (`kubernetes-sigs/agent-sandbox`) provides a `Sandbox` resource (`agents.x-k8s.io/v1beta1`) with pause (`operatingMode`), `shutdownTime`, and lifecycle controls. The home PVC must survive both `pipen down` and auto-shutdown, so it stays a standalone claim mounted into the sandbox instead of a `volumeClaimTemplate`, which would cascade deletion.

### To Do

- Implement per the `plan` section: target `v1beta1` on agent-sandbox v1.0.12, name the sandbox `env-<pen>`, keep every optional Sandbox feature off by default with opt-in through pipen-operator Helm values.

## AC

- [ ] `pipen up` creates an agent-sandbox `Sandbox` with a kata runtime, the tailscale Service, and the home PVC; `pipen ls`, `pipen ssh`, `pipen logs`, and `pipen down` behave as today, and the home PVC survives `pipen down`
- [ ] `pipen pause <name>` sets the sandbox to `operatingMode: Suspended`, stopping compute while keeping the Pen, the home PVC, and the tailnet hostname; `pipen resume <name>` returns the pen to Ready
- [ ] A pen with `spec.ttl` set (or the operator-wide default TTL) shuts itself down after the window and `pipen ls` shows the remaining TTL
- [ ] A pen with egress hardening enabled gets a `NetworkPolicy` that allows only the approved destinations (LiteLLM proxy, DNS, package registries, Git hosts) and blocks lateral cluster access
- [ ] The `Sandbox` is created with `spec.service: false` and no `volumeClaimTemplates`; the headless service and other optional Sandbox features stay off unless enabled through pipen-operator Helm values

<!-- tickets:research -->

All findings verified by direct source read on 2026-11-24:

- `kubernetes-sigs/agent-sandbox` cloned to `/tmp/agent-sandbox`; newest release **v1.0.12** (commit `4863a629`), plus tag **v0.3.10** for the v1alpha1 comparison.
- pipen paths cited inline (repo at `/home/tobias/pipen`).

## Version: the task's GVK is the older API

- `agents.x-k8s.io/v1alpha1` exists only in releases up to v0.3.x (tag v0.3.10, `api/v1alpha1/sandbox_types.go`). Its `SandboxSpec`: required `podTemplate` (a full `corev1.PodSpec`), `volumeClaimTemplates`, inline `Lifecycle`, and `replicas` — `*int32`, only 0 or 1, default 1, wired to the scale subresource (`:110-127`, `:189`).
- The current release v1.0.12 serves **only `v1beta1`** (single CRD version, `storage: true`; `k8s/crds/agents.x-k8s.io_sandboxes.yaml:29,4168-4169`). Its `SandboxSpec`: `podTemplate`, `volumeClaimTemplates`, `service *bool`, `failedPodPolicy`, inline `Lifecycle`, and `operatingMode` (`Running` default, `Suspended`) — no `replicas` (`api/v1beta1/sandbox_types.go`, `SandboxSpec`/`SandboxBlueprint`).
- Pause semantics are equivalent in both: v1alpha1 `replicas: 0` makes the controller delete the backing pod (v0.3.10: `controllers/sandbox_controller.go:486-492`); v1beta1 `operatingMode: Suspended` does the same and retains the Sandbox and its volumes (`api/v1beta1/sandbox_types.go:247-260`).
- **Decision (2026-11-24):** target v1beta1 on the current release — it is the only version the CRD serves, it is actively released, and it has `spec.service: false` to sidestep the Service collision below, which v1alpha1 cannot.

## Design facts

1. **`podTemplate` is a full `corev1.PodSpec` in both versions** — `runtimeClassName` (kata), `persistentVolumeClaim` volumes, secret volumes, env, nodeSelector, resources, securityContext all allowed (`api/v1beta1/sandbox_types.go`, `PodTemplate`; v0.3.10: `api/v1alpha1/sandbox_types.go`). pipen's pod spec moves over unchanged.
2. **Backing pod name = Sandbox name** (v1.0.12: `controllers/sandbox_controller.go:198-205`; v0.3.10: `:640`). pipen's pod is `env-<pen>` (`operator/internal/pen/reconcile.go:373`) and `pipen logs` streams `/pods/env-<pen>/log` (`cli/pipen.py:288`). Naming the Sandbox `env-<pen>` keeps both working.
3. **Service-name collision, a blocker if missed:** the agent-sandbox controller looks up a Service named `<sandbox-name>`. If one exists and is owned by a different controller, the v1alpha1 controller hard-errors with `service ... is owned by ..., not by sandbox` (v0.3.10: `controllers/sandbox_controller.go:357-363`), and v1beta1 does the same while `spec.service` is unset (v1.0.12: `controllers/sandbox_controller.go:1541-1547`). pipen owns the tailscale Service `<pen>` from the Pen (`operator/internal/pen/reconcile.go:192-228`). So the Sandbox must never be named after the pen; naming it `env-<pen>` avoids the collision (no Service carries that name), and on v1beta1 `spec.service: false` additionally guarantees the controller leaves external Services alone (`:1531-1538`).
4. **PVC retention confirmed:** `volumeClaimTemplates` PVCs are named `<template>-<sandbox>` and carry a controller ref to the Sandbox (`v1.0.12: controllers/sandbox_controller.go:2246`), are retained across suspension and expiry, and are garbage-collected when the Sandbox is deleted (`api/v1beta1/sandbox_types.go`, `VolumeClaimTemplates` comment). A standalone home PVC is therefore the correct retention mechanism, and pipen's already is: `<pen>-home` has only an owner label, no owner ref, so it survives `pipen down` today (`reconcile.go:161-190`).
5. **Expiry:** `shutdownTime` is RFC3339 (`*metav1.Time`, `Format="date-time"`). On expiry the controller always deletes the pod (and any sandbox-owned Service); `shutdownPolicy` governs only the Sandbox object: `Delete` removes it, `Retain` (default) keeps it with `Ready=False reason=SandboxExpired` (v1.0.12: `api/v1beta1/sandbox_types.go`, `Lifecycle`; same shape in v0.3.10: `api/v1alpha1/sandbox_types.go:129-152`). The home PVC survives expiry either way because the Pen, not the Sandbox, owns it.
6. **Status to read:** v1beta1: conditions `Ready`/`Suspended`/`PodScheduled`/`Finished`/`Expired` with stable reasons (`DependenciesReady`, `DependenciesNotReady`, `SandboxExpired`, `PodTerminated`, `ReconcilerError`, ...), plus `status.nodeName`, `podIPs`, `service`, `labelSelector` (`api/v1beta1/sandbox_types.go`). v1alpha1: `Ready` condition plus `status.replicas`.
7. **Install:** no webhooks, no cert-manager in the core. Helm chart (`helm/`) or plain manifests (`k8s/controller.yaml`, `k8s/rbac.generated.yaml`, `k8s/crds/`). Kubernetes >= 1.29 recommended because CRD CEL validation is stable (`README.md:91`). The kind e2e (`.github/workflows/verify.yml`, kind 0.27 / k8s 1.33) must install the agent-sandbox CRD and controller before the pipen charts.

## Current pipen behavior the refactor must preserve (verified locally)

- Create-or-leave reconciler over: PVC `<pen>-home` (mayastor-2, RWO, no owner ref; `reconcile.go:161-190`), Service `<pen>` (tailscale LB class, `tailscale.com/hostname=<pen>`, controller ref to Pen, so it dies with the pen; `:192-228`), pod `env-<pen>` (kata runtimeClass, mayastor nodeSelector, home + ssh-key + litellm-config mounts, controller ref to Pen; `:230-372`).
- `pipen logs` hits `/pods/env-<pen>/log` with the dev token; the per-namespace developer Role grants `pods: get` and `pods/log: get` (`cli/pipen.py:288`, `charts/dev-namespace/templates/role.yaml:22-26`).
- Operator RBAC for pod CRUD lives in the per-namespace Role from the dev-namespace chart (`charts/dev-namespace/templates/operator-role.yaml`): that is where `agents.x-k8s.io/sandboxes` permissions go and where pod verbs shrink to `get, list, watch`.
- Operator defaults come from the `pipen-operator-config` ConfigMap (`operator/internal/config/config.go`); the cluster-wide default TTL goes there plus the pipen-operator Helm values.

## Risks

- Controller version pin: pipen must know which API version the installed agent-sandbox controller serves; cleanest is for the deployment to install a pinned agent-sandbox release alongside the operator.
- Resume applies the _current_ `podTemplate` (v1beta1: "Resuming creates a new Pod from the current podTemplate"), so spec edits plus resume take effect on resume; that changes today's create-or-leave semantics (see resize ticket PI-016).

<!-- tickets:approaches -->

- A. Pin a v0.3.x controller and use `v1alpha1` + `spec.replicas` (the task text). Rejected: the current v1.0.x CRD serves only `v1beta1`, so the cluster would run an outdated controller, and v1alpha1 has no `spec.service` toggle — its controller hard-errors whenever a Service named after the Sandbox is owned by another controller, which our tailscale Service is.
- B. Target `v1beta1` (chosen, decided 2026-11-24): agent-sandbox v1.0.12 (commit `4863a629`), pause via `spec.operatingMode` (`Running`/`Suspended`), `spec.service: false` by default. Every optional Sandbox feature (headless service, `failedPodPolicy: Recreate`, the SandboxTemplate/SandboxClaim/SandboxWarmPool extensions) defaults to disabled and opts in through pipen-operator Helm values rendered into the `pipen-operator-config` ConfigMap.

<!-- tickets:plan -->

Target: agent-sandbox **v1.0.12** (commit `4863a629`), `agents.x-k8s.io/v1beta1` `Sandbox`. Optional Sandbox features default off, opt-in via pipen-operator Helm values.

Decisions locked:

- Sandbox name is `env-<pen>` — the backing pod is named after the sandbox, so `pipen logs` (`/pods/env-<pen>/log`) works unchanged, and no Service named `env-<pen>` exists (avoids the controller's same-name Service collision).
- `spec.service` is always a non-nil pointer (default `false`): the controller must never adopt, create, or error on a Service; it never touches the tailscale Service `<pen>`.
- `shutdownPolicy: Retain` (default): after auto-shutdown the Sandbox object stays (`Ready=False reason=SandboxExpired`), so the pen stays visible in `pipen ls` and resume is a plain spec update.
- Resume is idempotent TTL reset: every time the pen transitions from suspended/expired back to running, the reconciler stamps `shutdownTime = now + ttl`.
- The home PVC stays a standalone claim owned by no one (as today); the sandbox gets no `volumeClaimTemplates`.

1. API types (`operator/api/v1alpha1/pen_types.go` + regenerated deepcopy)

- `PenSpec`: add `Paused bool` (`paused,omitempty`), `TTL string` (`ttl,omitempty`, a duration like `8h`; empty falls back to the operator default), `Egress bool` (`egress,omitempty`, enables the per-pen egress NetworkPolicy).
- `Phase`: add `PhaseSuspended = "Suspended"` (Ready is false while suspended).
- `PenStatus`: add `ShutdownAt *metav1.Time` (`shutdownAt,omitempty`) — mirrors `spec.lifecycle.shutdownTime`; the CLI renders the remaining TTL from it.

2. Operator config (`operator/internal/config`)

- New `pipen-operator-config` keys with defaults: `defaultTTL` (default empty = no auto-shutdown), `sandboxService` (bool, default false), `egressHosts` (comma-separated FQDNs, default empty; the per-pen policy only applies when the pen sets `egress` and the list is non-empty).

3. Reconciler (`operator/internal/pen/reconcile.go`)

- `ensureSandbox` replaces `ensurePod`: create `Sandbox env-<pen>` with controller ref to the Pen, `spec.podTemplate.spec` = today's pod spec verbatim (runtimeClass, nodeSelector, automountSA false, container env `DEV_USER`/`TAILNET_HOSTNAME`, volumes home + dev-secret + dev-config, privileged, resources), `spec.operatingMode` = `Suspended` when `pen.Spec.Paused` else `Running`, `spec.service` = pointer to `sandboxService` config, `spec.lifecycle` = `{shutdownTime: now+ttl, shutdownPolicy: Retain}` when a TTL applies else nil.
- Converge instead of create-or-leave (same pattern PI-016 chose): patch `operatingMode` when `paused` changed; stamp a fresh `shutdownTime` when the TTL changed or the sandbox reports `Ready=False reason=SandboxExpired` while not paused.
- Status mapping from Sandbox conditions: `Ready=True reason=DependenciesReady` -> `PhaseReady`; `Suspended=True` or `operatingMode=Suspended` -> `PhaseSuspended`; `PodScheduled=False` -> `PhaseCreating` with the reason; `ReconcilerError` -> `PhaseFailed`. Keep reading the tailscale hostname from the Service and the node from the pod (pod read access stays for this and `pipen logs`).
- `ensureNetworkPolicy` (new, only when `pen.Spec.Egress` and `egressHosts` is non-empty): NetworkPolicy `<pen>-egress` in the dev namespace, controller ref to the Pen, `podSelector` on the existing pod label `herdr.dev/env=<pen>`, `policyTypes: [Egress]`, allow rules = kube-dns (port 53, the same rule the namespace chart uses) + one FQDN `ipBlock` per `egressHosts` entry (Kubernetes 1.11+ allows FQDN in `ipBlock.cidr`). Delete it when the flag is off. The namespace `default-deny-egress` stays; policies intersect.

4. RBAC and charts

- `charts/dev-namespace/templates/operator-role.yaml`: add `agents.x-k8s.io / sandboxes: [get, list, watch, create, update, patch, delete]`; add `networking.k8s.io / networkpolicies: [get, list, watch, create, update, delete]`; shrink `pods` verbs to `[get, list, watch]`.
- `charts/pipen-operator`: install agent-sandbox as a pinned dependency (its Helm chart, v1.0.12, extensions disabled) or the pinned `k8s/` core manifests — pick one, pin the tag, document the upgrade path; add values `operatorConfig.defaultTTL`, `operatorConfig.sandboxService` (false), `operatorConfig.egressHosts` (empty list) rendered into the ConfigMap; bump both chart versions with upgrade notes.
- RBAC note: the agent-sandbox controller runs under its own service account; no change to the developer Role (it keeps `pods: get` and `pods/log: get` for `pipen logs`).

5. CLI (`cli/pipen.py`)

- `pipen up <name> [--ttl 8h] [--egress]`: existing create + poll; the new flags merge into the spec and change nothing else.
- `pipen pause <name>` / `pipen resume <name>`: GET the pen, set `spec.paused` true/false only, PUT, poll for `Suspended` / `Ready`.
- `pipen ls`: show `Suspended` in the phase column and a TTL column rendered as `status.shutdownAt - now` (human duration, `-` when unset).
- `pipen ssh` and `pipen down` unchanged.

6. Tests

- `reconcile_test.go`: sandbox created with the full podTemplate (runtimeClass `kata`, nodeSelector, volumes, env) and no `volumeClaimTemplates`; `spec.service` false by default, true when config says so; `paused` maps to `operatingMode`; pen TTL overrides `defaultTTL`; an expired sandbox (`SandboxExpired`) gets a fresh `shutdownTime` on the next reconcile; `PhaseSuspended` while suspended; NetworkPolicy created only when `egress` is on (and deleted when off); PVC and Service assertions unchanged; deleting the sandbox recreates the pod from the current podTemplate with the home PVC reattached.
- Config: new keys parse with the documented defaults.
- `cli/tests`: pause/resume touch only `spec.paused`; `up --ttl`/`--egress` merge into the spec; `ls` renders the TTL column.
- kind e2e (`.github/workflows/verify.yml`): install the pinned agent-sandbox release before the pipen charts; drive up -> Ready (pod `env-<pen>`, hostname present), pause (pod gone, Service and PVC remain), resume (Ready again, fresh TTL), down (pen, sandbox and pod gone, PVC remains).

7. Docs

- Root README: new commands and flags, the Sandbox mapping (name, pause, TTL), what `pipen down` deletes and keeps.
- Chart READMEs: the new values and the pinned agent-sandbox dependency.
