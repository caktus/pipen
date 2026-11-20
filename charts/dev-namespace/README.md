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

| Name                  | Type   | Description                                                                                                                                                                |
| --------------------- | ------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `dev`                 | string | Developer name. Becomes the namespace name. RFC 1123.                                                                                                                      |
| `authorizedKeys`      | list   | ssh public keys, written to the `authorized_keys` Secret key of `herdr-dev-<dev>`.                                                                                         |
| `operator.namespace`  | string | Namespace of the cluster-wide operator that the chart grants per-namespace write access to (default `pipen`).                                                              |
| `tailscale.namespace` | string | Namespace where the tailscale-operator L3 ssh proxy runs. The ingress policy allows it to reach env pods on port 22 (default `tailscale`).                                 |
| `egress.cidrAllows`   | list   | Fixed CIDR ranges a dev pod may reach, as strings. For example `100.64.0.0/10`, the tailscale CGNAT.                                                                       |
| `egress.podAllows`    | list   | In-cluster targets a dev pod may reach. Each entry is `namespace`, `podSelector` (a label map), and `port`. Omit `port` to allow all ports. For example the litellm proxy. |

## Objects

- `Namespace` `<dev>` (label `herdr.dev/owner=<dev>`)
- `ServiceAccount` `deploy`
- `Role`/`RoleBinding` `deploy`: CRUD on `pens.pipen.dev/pens`, `get` on
  `pens/status`, `get` on `pods`, `get` on `pods/log` (no pod, Service or PVC
  create, no pvc delete)
- `Role`/`RoleBinding` `pipen-operator` (in the same namespace): the
  per-namespace writes for the operator's ServiceAccount (pods, services, pvc
  create, pens/status update, configmap/secret get, plus reads on all of
  them)
- `Secret` `deploy-token` (`kubernetes.io/service-account-token`); the token
  controller fills `data.token` after a delay
- `Secret` `herdr-dev-<dev>`: the ssh public keys
- `NetworkPolicy` `default-deny-egress`: denies all egress. It then allows
  the public internet (`0.0.0.0/0` minus the private, CGNAT, loopback and
  link-local ranges), DNS to kube-dns (port 53 tcp+udp), and the egress
  allows from `egress.cidrAllows` and `egress.podAllows`
- `NetworkPolicy` `default-deny-ingress`: denies all ingress. It then allows
  the tailscale ssh proxy (from `tailscale.namespace`, pod label
  `tailscale.com/parent-resource-type=ingress_service`) on port 22

The per-developer litellm key and the rendered pi configuration live in a
ConfigMap that your cluster tooling creates. See the repo README (Admins).

## 0.3.0 changed the role

Before 0.3.0 the dev token managed raw pod, Service, configmap and secret
objects directly. Since 0.3.0 it manages Pens only: the dev token creates and
deletes Pens and reads their status. The operator, on its own
ServiceAccount, creates the pod, Service and PVC for each Pen.

## 0.4.0 locks the namespace down

The two NetworkPolicies render with the namespace install. They need a CNI
that enforces NetworkPolicy (Calico, Cilium, or kindnet). The kube-dns and
namespace allows use the automatic namespace label
`kubernetes.io/metadata.name`, which is GA in kubernetes 1.31. On an older
cluster, replace that label with `matchNames: [kube-system]`. For the
tailscale rule use `matchNames` with the operator namespace name. Check
this before rollout.

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
