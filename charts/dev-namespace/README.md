# dev-namespace

Provisions one developer namespace on a pipen cluster: Namespace,
ServiceAccount, a namespace-scoped Role/RoleBinding, an explicit
ServiceAccount token Secret, the ssh-key Secret, and two NetworkPolicies
that lock the namespace down. The dev pods reach the public internet and
kube-dns. They reach no other namespace and no cluster service, except the
in-cluster allows you set and the tailscale ssh proxy.

## Install

```sh
helm install <dev> pipen/dev-namespace --version <version> \
  --set dev=<dev> \
  --set 'authorizedKeys[0]=<ssh public key 1>' \
  --set 'authorizedKeys[1]=<ssh public key 2>'
```

Or from a repo checkout: `helm install <dev> charts/dev-namespace ...`.

## Values

| Name                  | Type   | Description                                                                                                                                                                                                             |
| --------------------- | ------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `dev`                 | string | Developer identity. It is the namespace label `herdr.dev/owner` and the prefix the CLI force-adds to every pen name (`<dev>-<env>`). The namespace itself is named `<namespacePrefix><dev>`.                            |
| `namespacePrefix`     | string | Prefix of the namespace name, default `env-` (namespace `env-tobias` for dev `tobias`). The composed name must be RFC 1123 and 63 characters or fewer.                                                                  |
| `authorizedKeys`      | list   | ssh public keys, written to the `authorized_keys` Secret key of `herdr-dev-<namespace>`.                                                                                                                                |
| `operator.namespace`  | string | Namespace of the cluster-wide operator that the chart grants per-namespace write access to (default `pipen`).                                                                                                           |
| `tailscale.namespace` | string | Namespace where the tailscale-operator L3 ssh proxy runs. The ingress policy allows it to reach env pods on port 22 (default `tailscale`).                                                                              |
| `egress.cidrAllows`   | list   | Fixed CIDR ranges a dev pod may reach, as strings. For example `100.64.0.0/10`, the tailscale CGNAT.                                                                                                                    |
| `egress.podAllows`    | list   | In-cluster targets a dev pod may reach. Each entry is `namespace`, `podSelector` (a label map), and `port`. Omit `port` to allow all ports. For example the litellm proxy.                                              |
| `ingress.podAllows`   | list   | In-cluster sources that may reach a dev pod. Each entry is `namespace`, `podSelector` (a label map), and `port`. Omit `port` to allow all ports. For example a tailscale per-Service proxy pod on a port other than 22. |

## Objects

- `Namespace` `<namespacePrefix><dev>` (label `herdr.dev/owner=<dev>`)
- `ServiceAccount` `deploy`
- `Role`/`RoleBinding` `deploy`: CRUD on `pens.pipen.dev/pens`, `get` on
  `pens/status`, `get` on `pods`, `get` on `pods/log` (no pod, Service or PVC
  create, no pvc delete)
- `Role`/`RoleBinding` `pipen-operator` (in the same namespace): the
  per-namespace writes for the operator's ServiceAccount (Sandbox, Service,
  PVC, and NetworkPolicy management, pens/status update, pod reads,
  configmap/secret get limited to the `herdr-dev-<namespace>` objects)
- `Secret` `deploy-token` (`kubernetes.io/service-account-token`); the token
  controller fills `data.token` after a delay
- `Secret` `herdr-dev-<namespace>`: the ssh public keys
- `NetworkPolicy` `default-deny-egress`: denies all egress. It then allows
  the public internet (`0.0.0.0/0` minus the private, CGNAT, loopback and
  link-local ranges), DNS to kube-dns (port 53 tcp+udp), and the egress
  allows from `egress.cidrAllows` and `egress.podAllows`
- `NetworkPolicy` `default-deny-ingress`: denies all ingress. It then allows
  the tailscale ssh proxy (from `tailscale.namespace`, pod label
  `tailscale.com/parent-resource-type` equal to `ingress_service`, the
  Ingress-object proxy, or `svc`, the per-Service proxy) on port 22, plus
  the sources from `ingress.podAllows`

The per-developer litellm key and the rendered pi configuration live in a
ConfigMap that your cluster tooling creates. See the repo README (Admins).

## 0.3.0 changed the role

Before 0.3.0 the dev token managed raw pod, Service, configmap and secret
objects directly. Since 0.3.0 it manages Pens only: the dev token creates and
deletes Pens and reads their status. The operator, on its own
ServiceAccount, creates the pod, Service and PVC for each Pen.

## 0.5.0 adds the namespace prefix

`namespacePrefix` (default `env-`) goes in front of the namespace name.
For dev `tobias` the Namespace is `env-tobias`, and all namespaced
objects live in it. `dev` keeps meaning the developer identity: the
namespace label `herdr.dev/owner` holds the bare `dev`, and it is
the prefix the CLI force-adds to every pen name (`<dev>-<env>`).

The naming convention the chart, the operator, and the bootstrap share:

- The Namespace is `<namespacePrefix><dev>`.
- The ssh-key Secret (this chart) and the dev ConfigMap (your cluster
  tooling) are both named `herdr-dev-<namespace>`. The operator waits
  for `herdr-dev-<pen namespace>` before it creates a pen, so both
  objects use the composed namespace name, not the bare `dev`.
- The pod env `DEV_USER` is the `herdr.dev/owner` label value (the bare
  `dev`), so the unix user in each env is the dev name, for example
  `tobias`, not the namespace name `env-tobias`.

The render fails when the composed name is not RFC 1123 or is longer
than 63 characters.

### Cutover for an existing dev

A Helm upgrade cannot rename a namespace. The cutover is per dev: create
the prefixed namespace and keep the home PVCs in it.

1. Note each pen's home PVC in the old namespace (name, size, volume
   name).
2. Install the chart for the same dev under a new release name, for
   example `env-<dev>`. The old release `<dev>` still exists, and a
   Helm release name cannot be re-used. The default prefix gives the
   new namespace `env-<dev>`.
3. Recreate each home PVC in the new namespace with the same name and
   the same volume name. The new PVC binds the kept volume, and the
   home data stays.
4. Give the developer the new CLI config (`namespace` is the new
   namespace, `dev_user` is the bare dev) and recreate the pens.
5. When the new pens work and nothing points at the old namespace,
   run `helm uninstall <dev>`. That removes the old namespace and the
   old home PVCs.

## 0.4.0 locks the namespace down

The two NetworkPolicies render with the namespace install. They need a CNI
that enforces NetworkPolicy (Calico, Cilium, or kindnet). The kube-dns and
namespace allows use the automatic namespace label
`kubernetes.io/metadata.name`. The version floor and the older-cluster
fallback live in the repo README (Admins, Prerequisites).

Two things must be checked on the cluster before rollout, because both can
change a rule:

- The tailscale proxy DNATs its tailnet IP to the Service. With kube-proxy in
  iptables mode and `masqueradeAll: true`, the connection may arrive at the
  env pod from the node IP, not the proxy pod. If so, the ingress allow must
  be an ipBlock of the node CIDR on port 22, or the eBPF datapath must be
  used.
- If the kata guest talks to the Mayastor home-PVC gateway through the pod
  network, default-deny egress blocks storage. Add an allow for the gateway
  (namespace/selector or CIDR) under `egress.podAllows` or `egress.cidrAllows`.

## 0.5.1 values comment only

The `egress.podAllows` comment notes that the same rule pattern serves any
in-cluster model endpoint, not just the litellm proxy. No rendered object
changed.

## 0.6.0 closes the default-deny loophole

The two NetworkPolicies carried an empty rule (`- {}`). An empty rule
matches everything, so every env pod reached the whole cluster, and the
whole cluster reached every env pod. This release deletes the empty rules.
`policyTypes` alone default-denies, so the policies now block every
traffic direction that is not on an allow rule. The operator Role narrows
its configmap and secret `get` to the `herdr-dev-<namespace>` objects.

The upgrade turns on isolation for the first time. Run these checks
before the bump:

- Set `egress.podAllows` for the model endpoint (the litellm proxy pod,
  or the self-provider target). Without it, a pen with litellm-backed
  models loses model access on upgrade
- If the kata guest talks to the Mayastor home-PVC gateway through the pod
  network, allow the gateway under `egress.podAllows` or
  `egress.cidrAllows` (see the 0.4.0 note)
- With kube-proxy in iptables mode and `masqueradeAll: true`, check the
  tailscale proxy source address against the ingress rule (see the 0.4.0
  note). The connection can arrive at the env pod from the node IP
- If your cluster exposes env Services directly (a ProxyClass or the
  `tailscale.com/expose` annotation), the tailnet ssh proxy is a
  per-Service proxy pod, not the Ingress-object proxy. The 0.6.0 ingress
  rule only matched the Ingress-object proxy, so tailnet ssh times out on
  0.6.0 and no chart value can fix it. Upgrade to 0.6.1
- Check the CNI applies policy to Service traffic. kindnet checks
  pod-to-pod traffic, but it applies no policy to traffic addressed to a
  Service (ClusterIP). Run Calico or Cilium when you need egress to
  in-cluster Services denied

## 0.6.1 allows the per-Service tailscale proxy and adds `ingress.podAllows`

The 0.6.0 ingress allow matched only the Ingress-object proxy (pod label
`tailscale.com/parent-resource-type=ingress_service`). Clusters that
expose env Services directly (a ProxyClass or the `tailscale.com/expose`
annotation) get per-Service proxy pods labeled
`tailscale.com/parent-resource-type=svc`. The 0.6.0 rule blocked them,
and tailnet ssh timed out with no chart value to change that.

The ingress rule now matches both proxy shapes on port 22. Both are
single-replica proxy StatefulSets in the operator namespace, the same
trust domain. The new `ingress.podAllows` value mirrors
`egress.podAllows`: each entry is a `namespace`, a `podSelector`, and an
optional `port`. Use it for a proxy shape or port the default rule does
not cover, for example a per-Service proxy that serves an env Service
port other than 22.

The kind e2e job does not run the tailscale operator, so it cannot catch
proxy label drift. After a tailscale-operator upgrade, check the proxy
pod labels on your cluster against this rule:

```sh
kubectl -n <tailscale namespace> get pods \
  -l tailscale.com/parent-resource-type --show-labels
```

## 0.7.0 grants Sandbox and NetworkPolicy access

The operator now reconciles each pen as an agent-sandbox Sandbox. Its Role
can manage `sandboxes.agents.x-k8s.io` and `networkpolicies`. The pod rule
now allows only `get`, `list`, and `watch`. The operator needs pod reads
for pen status. The developer token reads logs. The agent-sandbox controller
creates and deletes the pods.

The developer Role does not change. It keeps `get` on `pods` and `pods/log`.

An existing cluster must meet these conditions before the upgrade:

- Upgrade the `pipen-operator` chart to 0.5.0 or later. It installs the
  agent-sandbox controller in `agent-sandbox-system`.
- Keep the old operator Role until both charts use the new versions. A new
  operator with the old Role cannot manage Sandboxes.
- Keep the home PVCs. This Role change does not delete them.

## 0.6.2 docs only

The `DEV_USER` bullet now says the guest unix user is the dev name (the
`herdr.dev/owner` label value), matching the operator that reads that
label. No rendered object changed.
