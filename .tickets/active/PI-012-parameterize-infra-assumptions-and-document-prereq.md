---
id: PI-012
github_issue: 12
github_url: https://github.com/caktus/pipen/issues/12
last_synced: "2026-11-21T00:46:05.144160+00:00"
synced_hash: 8f9e0bbdea6b7d55f995ee1f02013f2710bf9f680faebdd137c64644a996b394
title: Parameterize infra assumptions and document prerequisites
type: Task
assignee: "@me"
labels:
  - phase/review
context:
  relates_to: []
---

As an admin deploying pipen to a cluster, I want every infrastructure assumption (litellm, storage, kata, org images) to be a configuration parameter and every real prerequisite to be documented, so that pipen deploys on a different cluster without source edits. Tailscale is a hard requirement, not a parameter.

### To Do

- Add a Prerequisites section to the README that lists each external dependency with its helm chart, repo, and the optional ones (the litellm proxy is optional: developers can log in to their own model-provider accounts instead).
- Make the devenv entrypoint tolerate a dev ConfigMap with no litellm key or models.json, and document the mayastor/kata/caktus-org defaults as overridable cluster-shape values. Document the full tailscale-operator prerequisite chain (chart + admin-console setup) in this repo's README as a hard requirement.

## AC

- [ ] The README Prerequisites section lists every required chart and cluster feature (tailscale-operator, CNI that enforces NetworkPolicy, kubernetes version, storage, runtime class), names the litellm proxy chart as optional, and a fresh admin can install pipen from it alone.
- [ ] A pen boots with a dev ConfigMap that has no `litellm_key` and no `models.json`, and the developer signs in to their own provider account inside the pen.

## Related tickets

- caktus/hosted-llm#23 (tailscale operator + tailnet ingress, the worked example of the operator prerequisites)
- caktus/hosted-llm#19 (litellm proxy on microk8s, the optional dependency)

<!-- tickets:research -->

Scout pass over this repo and its dependency on the `caktus/hosted-llm` stack (2026-11-21). hosted-llm itself is fine carrying its own assumptions; this ticket covers what pipen hard-codes.

## Where pipen assumes the hosted-llm / caktus stack

1. **Tailscale operator (hard requirement; document, do not parameterize).**
   - `operator/internal/config/config.go`: `LoadBalancerClass` defaults to `"tailscale"`, applied to every pen Service.
   - `charts/pipen-operator/values.yaml`: `serviceAnnotations: {tailscale.com/expose: "true"}` and `loadBalancerClass: "tailscale"` are chart defaults.
   - `charts/dev-namespace/templates/networkpolicy-ingress.yaml`: the only ingress allow is the tailscale-operator L3 ssh proxy (namespace `tailscale`, pod label `tailscale.com/parent-resource-type=ingress_service`, port 22). The NetworkPolicy always renders this rule; there is no flag to turn tailnet exposure off.
   - Prerequisite chain (documented only in the hosted-llm README, not here): tailscale-operator helm chart from pkgs.tailscale.com/helmcharts, a tailnet admin-console OAuth client, `tagOwners` for `tag:k8s-operator`/`tag:k8s`, and `autoApprovers` for services.
2. **LiteLLM (assumed, should be optional).**
   - `devenv/entrypoint.sh`: projects `models.json`, `settings.json`, and `litellm_key` from the dev ConfigMap unconditionally. The script runs `set -euo pipefail`, so a pen whose ConfigMap lacks any of the three files fails to boot.
   - `README.md` (Admins, step 4): the dev ConfigMap is described as carrying the litellm baseUrl rewrite and the per-developer litellm key; "Revoke a developer" step 1 is "revoke the litellm key".
   - `charts/dev-namespace/values.yaml`: the egress example is the litellm proxy pod.
   - A developer who just logs in to their own provider account (Anthropic, OpenRouter, ...) needs none of this, but the entrypoint and docs assume it.
3. **Storage (assumed in defaults).** `charts/pipen-operator/values.yaml` defaults: `nodeSelector: {openebs.io/engine: mayastor}` and `storageClass: "mayastor-2"` — the OpenEBS Mayastor install that hosted-llm provisions. Overridable, but a cluster without OpenEBS must know to change both.
4. **Kata (assumed in defaults).** `runtimeClass: "kata"` is the operator and chart default; only the kind e2e job sets it empty.
5. **Org (documented, overridable).** Image defaults `ghcr.io/caktus/pipen` / `ghcr.io/caktus/pipen-operator` and chart repo `https://caktus.github.io/pipen`. Fine as defaults for this repo, but an external org must set them and the README does not say so in one place.
6. **No consolidated prerequisites.** Cluster requirements are scattered: CNI that enforces NetworkPolicy + kubernetes >= 1.31 (`kubernetes.io/metadata.name` label) and the kube-proxy `masqueradeAll` caveat in `charts/dev-namespace/README.md` (0.4.0 section); mayastor capacity in README "Capacity"; GHCR package visibility in the two install sections; the tailscale chain only in the other repo.

## Helm charts the stack truly depends on

| Chart                          | Repo                               | Required?                      | Notes                                                                                                                                      |
| ------------------------------ | ---------------------------------- | ------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------ |
| tailscale-operator             | pkgs.tailscale.com/helmcharts      | Required                       | L4 pen exposure + L3 ssh proxy; needs admin-console OAuth client, tagOwners, autoApprovers (caktus/hosted-llm#23 documents the full chain) |
| openebs (Mayastor)             | openebs.github.io/openebs          | Required by the default values | provides the `mayastor-2` storage class the default `storageClass` names                                                                   |
| kata-containers runtime        | (node-level, not a helm chart)     | Required by the default values | `runtimeClass: kata`; set empty to disable                                                                                                 |
| litellm (berriai litellm-helm) | github.com/BerriAI/litellm (helm/) | Optional                       | shared model endpoint + per-dev keys; without it developers use their own provider accounts                                                |
| pipen-operator, dev-namespace  | this repo (chart-releaser)         | Required                       | the product itself                                                                                                                         |

<!-- tickets:plan -->

Plan for an implementer. Steps are ordered; each is independently verifiable. No step changes default behavior.

## 1. README: one Prerequisites section (docs, no code)

New "Prerequisites" section in the Admins area, before "1. The dev image". For each dependency: what it is, the helm chart (repo + release name + where the version pin lives) or node requirement, and a required/optional marker.

Hard requirements (pipen does not run without them):

- **tailscale-operator** — `helm repo add tailscale https://pkgs.tailscale.com/helmcharts; helm install tailscale-operator tailscale/tailscale-operator` in namespace `tailscale`. It is the only ingress path (L4 pen exposure + the L3 ssh proxy the dev-namespace NetworkPolicy admits), so clusters that cannot run it are unsupported. Admin-console prerequisites: an OAuth client tagged `tag:k8s-operator` (write on General/Services, Devices/Core, Keys/Auth Keys), `tagOwners` granting `tag:k8s` to `tag:k8s-operator`, and `autoApprovers` services for `tag:k8s`. Cite caktus/hosted-llm#23 as the worked example. The existing `tailscale.namespace` dev-namespace value already covers operators that run in a non-default namespace; that stays the only tailscale parameter.

Required (default install):

- **A CNI that enforces NetworkPolicy** (Calico, Cilium, or kindnet) and kubernetes >= 1.31 for the `kubernetes.io/metadata.name` namespace label; note the kube-proxy `masqueradeAll: true` caveat (move it up out of the dev-namespace 0.4.0 change note).
- **Storage for pen homes** — the defaults assume OpenEBS Mayastor (`openebs/openebs` chart from openebs.github.io/openebs, storage class `mayastor-2`); any StorageClass works if `config.storageClass` and `config.nodeSelector` are set.
- **Kata containers runtime** on the nodes, for the default `config.runtimeClass: kata`; set the value empty where absent.
- **Public GHCR packages** for the dev image and the operator (already documented; keep one pointer here).

Optional:

- **litellm proxy** (BerriAI `litellm` helm chart) — shared model endpoint with per-developer keys. Without it, each developer logs in to their own provider account inside the pen; nothing else in pipen needs the proxy. The dev namespace egress to the proxy pod then comes from `egress.podAllows` (already the designed path).

## 2. Make the dev ConfigMap provider-agnostic (devenv + docs)

- `devenv/entrypoint.sh`: guard each of `models.json`, `settings.json`, `litellm_key` with a `-e` test on the projected path and skip the install when the file is absent (keep 0600/700 modes). The comment block names `litellm_key`; rename the concept to "model provider key" in the comment and keep the file name (it is a contract with cluster tooling).
- README step 4: describe the ConfigMap as "the pi agent configuration" with two documented modes — litellm-backed (current: baseUrl + `litellm_key`) and self-provider (a `models.json` / auth file for the developer's own account, no key file). "Revoke a developer" step 1 becomes conditional ("if the cluster uses the litellm proxy, revoke the key").
- Verify: `docker run` the devenv image with an empty config dir; sshd starts, the dev user exists, and pi runs after the developer pastes their own key.

## 3. Defaults hygiene

- Keep `ghcr.io/caktus/*`, mayastor, and kata as defaults (this repo is the caktus product), but have the Prerequisites section show the `--set` overrides for each in one place so an external org sees the whole override surface at once.
- `charts/dev-namespace/values.yaml`: keep the litellm egress example (it is the common case), add one line that the same rule pattern serves any in-cluster model endpoint.

## No new chart values

The scout pass found no unparameterized assumption left in the charts, so this ticket adds no helm values. The existing values are the interface; the Prerequisites section documents them as one override table (`config.storageClass`, `config.nodeSelector`, `config.runtimeClass`, `config.image`, `config.loadBalancerClass`, `config.serviceAnnotations`, `config.tailnetDomain`, `tailscale.namespace`, `egress.podAllows`/`egress.cidrAllows`, `dev`, `namespacePrefix`, `operator.namespace`). The litellm option lives in the dev ConfigMap, which cluster tooling creates, not in a chart.

## Out of scope

- Any flag to disable tailscale exposure. Tailscale is a hard requirement (decision 2026-11-21); the loadBalancerClass, Service annotations, and ingress NetworkPolicy rule stay as they are.
- Changes inside caktus/hosted-llm (its assumptions stay).
- A general multi-provider key system in the entrypoint beyond "file present or not".
- RBAC, monitoring, and storage-engine work in the other repo.
