---
id: PI-010
github_issue: 10
github_url: https://github.com/caktus/pipen/issues/10
last_synced: "2026-11-20T18:39:34.604427+00:00"
synced_hash: 68888f60e27ca9560c29cb97ee2e777a5358ce44a2266238958a7fe625c74ec9
title: Lock down dev namespaces with network policies
type: Feature
assignee: "@me"
labels:
  - phase/plan-ready
context:
  relates_to: []
---

As a cluster admin, I want developer namespaces to allow public internet egress by default and nothing else in-cluster, with chart params to allow specific in-cluster namespaces and CIDRs, so that dev envs keep working while they can no longer reach other namespaces or cluster services unless I allow it.

The dev-namespace chart creates no NetworkPolicies today, and env pods run as privileged kata VMs, so any dev env can reach every other namespace on the cluster. Lockdown is standard `networking.k8s.io/v1` NetworkPolicies rendered by the chart at namespace install; no operator or CLI changes.

### To Do

- Add NetworkPolicy templates to `charts/dev-namespace`: default-deny ingress and egress, egress allow for public space (`ipBlock 0.0.0.0/0` with `except` for RFC1918, CGNAT, loopback, link-local), DNS to kube-dns, and ingress for the tailscale ssh proxy
- Add the chart values for in-cluster allows (namespace + pod selector + port, or CIDR) and the tailscale operator namespace, and document them in the chart README

## AC

- [ ] A freshly installed dev namespace keeps public internet egress and blocks egress to other namespaces and unlisted cluster services; ssh from the tailnet still works
- [ ] Setting a chart value that allows an in-cluster namespace/pod/port (e.g. litellm) or a CIDR (e.g. 100.64.0.0/10) makes that target reachable from a dev pod, and removing the value blocks it again
- [ ] A dev env still boots and works under the policies: image pull, home PVC mount, and litellm access (verified on the hosted-llm cluster)

<!-- tickets:research -->

## Standard NetworkPolicy mechanics (k8s.io/v1)

- Egress is allow-only: there are no deny rules. "Allow public, block
  internal" is expressed as a default-deny egress policy plus an allow rule
  for the public space (kubernetes.io/docs/concepts/services-networking/
  network-policies).
- `ipBlock.except` is a standard field: `to: ipBlock: {cidr: 0.0.0.0/0,
except: [...]}` is the canonical "internet only" recipe (stackoverflow
  57789969). Validation requires every `except` entry to be a subnet of the
  `cidr` and entries not to overlap (kubernetes/kubernetes PR #86578).
- One `NetworkPolicyPeer` allows exactly one of `podSelector`,
  `namespaceSelector`, or `ipBlock` (k8s API reference). Different peers in
  one rule, and all rules across all policies, are ORed (union).
- A `podSelector` in a `from`/`to` entry without a `namespaceSelector`
  matches pods in the policy's own namespace only.

## Egress allow set

- Public egress: single ipBlock `0.0.0.0/0` with
  `except: [10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10,
127.0.0.0/8, 169.254.0.0/16]`. (The widely-copied SO example uses
  172.16.0.0/20, which is wrong; RFC1918 is 172.16.0.0/12.)
- DNS: the kube-dns ClusterIP sits in the (private) service CIDR, so it is
  blocked by default and must be allowed explicitly: port 53 UDP+TCP to
  kube-system pods `k8s-app: kube-dns`, selected via
  `namespaceSelector` on `kubernetes.io/metadata.name: kube-system`.
  That automatic namespace label is GA in k8s 1.31 (beta 1.28) — the
  hosted-llm cluster version must be checked before relying on it.
- Image pulls: containerd pulls on the host (node network), not the pod
  netns, so policies should not block them; verify in the test plan.
- Home PVC data path (open question): the home PVCs use the mayastor-2
  StorageClass. If the kata guest's rbd client talks to the Mayastor
  gateway through the pod netns, default-deny egress breaks storage and an
  allow rule for the gateway (namespace/selector or CIDR) is needed.
  Verify live before rollout.

## Ingress (tailscale ssh)

- The tailscale-operator L3 proxy for a `LoadBalancer`/`tailscale` Service
  is a single-replica StatefulSet created in the operator namespace (default
  `tailscale`), not the dev namespace (tailscale.com/docs/kubernetes-
  operator/ingress; cmd/k8s-operator/sts.go). Note: PI-004's research says
  the proxy is owner-referenced to the Service; both are consistent
  (cross-namespace ownerRef). Verify placement on the hosted-llm cluster.
- Proxy pod labels (sts.go): `tailscale.com/parent-resource-type:
ingress_service`, `tailscale.com/parent-resource: <service>`,
  `tailscale.com/parent-resource-ns: <ns>`, `app: <service UID>`. The dev
  namespace chart cannot know per-pen service names at install time, so the
  ingress allow is per-namespace: from the tailscale operator namespace
  (by `kubernetes.io/metadata.name`), optionally narrowed by
  `tailscale.com/parent-resource-type: ingress_service`, to port 22.
- Source-IP caveat: the proxy DNATs its tailnet IP to the Service ClusterIP
  (tailscale docs). With kube-proxy in iptables mode and the default
  `masqueradeAll: true`, in-cluster traffic to ClusterIPs is SNATed to the
  node IP, so the connection arriving at the env pod may show the node IP as
  source, not the proxy pod IP. eBPF datapaths (Calico eBPF, Cilium)
  preserve the pod source IP. Verify on the cluster; if the source is the
  node IP, the ingress allow must be an ipBlock of the node CIDR on port 22
  (wider, but scoped to one namespace) or the eBPF datapath must be used.

## In-cluster allow param design

- Standard NetworkPolicy cannot match FQDNs. The chart param takes entries
  of either shape, rendered as separate `to` peers:
  - `{namespace, podSelector, port}` — declarative, no IP resolution,
    stable across service re-creation; the right shape for litellm/pi.dev.
  - `{cidr}` — for fixed ranges like 100.64.0.0/10 (tailscale CGNAT).
- CIDR entries that overlap the `except` list (e.g. 100.64.0.0/10) are
  simply additional `to` peers; union semantics make this safe.
- FQDN-based egress would need Cilium (`toFQDNs`, docs.cilium.io
  /en/stable/security/dns) or install-time DNS resolution; out of scope.

## CNI / cluster prerequisites

- Enforcement is CNI-dependent. Calico and Cilium support standard
  NetworkPolicy including `except` (Calico also supports negation
  natively); kind's default CNI kindnet implements NetworkPolicy via
  kube-network-policies (kindnet.sigs.k8s.io/docs/user/network-policies) —
  so a kind cluster is a viable local/CI test bed.
- Unverified at research time (no hosted-llm cluster credentials): CNI and
  datapath, k8s version (for `kubernetes.io/metadata.name`), kube-proxy
  mode (SNAT behavior), tailscale operator namespace and proxy placement,
  mayastor guest data path. First implementation step is a live audit of
  these on the hosted-llm cluster; each maps to a value or an added allow
  rule, not a design change, with the node-CIDR ingress fallback as the
  only structural contingency.

## Out of scope

Ingress other than ssh, locking down the operator/`pipen` namespace,
ResourceQuota/LimitRange/pod-security labels, IPv6 policy, FQDN egress.
