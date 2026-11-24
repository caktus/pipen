# pipen-operator

Installs the pipen operator: the `pens.pipen.dev` CRD, the operator
Deployment in the `pipen` namespace, its ServiceAccount, the
ClusterRole/ClusterRoleBinding it needs, and the ConfigMap
`pipen-operator-config` that holds the cluster shape. The operator reads
that ConfigMap and watches it; a change re-reconciles all pens. The chart
also installs the pinned agent-sandbox controller and its Sandbox CRD in
the `agent-sandbox-system` namespace.

## Install

```sh
helm install operator pipen/pipen-operator --version <version>
```

Or from a repo checkout: `helm install operator charts/pipen-operator ...`.

## Objects

- Namespace `pipen`
- CustomResourceDefinition for `pens.pipen.dev`
- ServiceAccount `pipen-operator`
- ClusterRole/ClusterRoleBinding `pipen-operator` (cluster-wide read of
  pens, namespaces, and storageclasses)
- Role/RoleBinding for the per-namespace writes the dev chart grants
- Deployment `pipen-operator` (one replica, leader election on)
- ConfigMap `pipen-operator-config`
- Namespace `agent-sandbox-system`
- CustomResourceDefinition for `sandboxes.agents.x-k8s.io`
- ServiceAccount, ClusterRole, ClusterRoleBinding, Service, and Deployment
  for the agent-sandbox controller

The file `templates/agent-sandbox-sandbox.yaml` is the pinned agent-sandbox
v1.0.12 release asset. The chart renders it without changes. Its SHA-256 is
`9c400c9d875061088c28598525b7ec37a5c2cb50460804c6b6f53f5fc37301ff`.
The release asset installs the core controller only. It installs no
extensions.

## Values

| Name                            | Type   | Description                                                                                                                       |
| ------------------------------- | ------ | --------------------------------------------------------------------------------------------------------------------------------- |
| `image`                         | string | Operator image (full reference).                                                                                                  |
| `replicaCount`                  | int    | Replicas. One. Leader election makes a future 2-replica flip cheap.                                                               |
| `nodeSelector`                  | object | Pod-level scheduling for the Deployment. Empty: the cluster scheduler picks.                                                      |
| `config.image`                  | string | Default pod image for pens (the Pen spec `image` overrides per pen).                                                              |
| `config.homeSize`               | string | Home PVC size.                                                                                                                    |
| `config.cpu`                    | string | Pod CPU request/limit.                                                                                                            |
| `config.memory`                 | string | Pod memory request/limit.                                                                                                         |
| `config.nodeSelector`           | object | Node selector for home volume attachment. Empty (default): pens can attach the home PVC to any node.                              |
| `config.storageClass`           | string | Home PVC storage class. Empty (default): the cluster's default StorageClass applies.                                              |
| `config.serviceAnnotations`     | object | Annotations on every pen Service (tailscale L4 exposure). The `tailscale.com/hostname` annotation is set per pen by the operator. |
| `config.tailnetDomain`          | string | Informational tailnet domain (`TAILNET_HOSTNAME` env, logged hostnames).                                                          |
| `config.imagePullPolicy`        | string | Pod image pull policy.                                                                                                            |
| `config.loadBalancerClass`      | string | `loadBalancerClass` for pen Services (the Tailscale operator).                                                                    |
| `config.runtimeClass`           | string | Runtime class for env pods. Empty (default): no runtime class.                                                                    |
| `operatorConfig.defaultTTL`     | string | Default time before a pen shuts down, for example `8h`. Empty: no automatic shutdown. A pen's `ttl` value overrides this default. |
| `operatorConfig.sandboxService` | bool   | Let the agent-sandbox controller create a Service for each sandbox. False: only the tailscale Service exists.                     |
| `operatorConfig.egressHosts`    | list   | Approved egress FQDNs. A pen with egress enabled can reach these hosts. Empty: no per-pen egress policy.                          |

## Upgrade

### Version 0.5.1

The ClusterRole gains `get` on `storageclasses` (group
`storage.k8s.io`). The operator reads the storage class to check that
home expansion is allowed before it grows a pen's home PVC. The matching
`dev-namespace` chart 0.7.1 adds `patch` and `update` on
`persistentvolumeclaims` to the operator Role.

On upgrade, pens keep their size. Nothing changes at install time. The
upgrade is a plain `helm upgrade`. You do not need to touch any pen.

### Version 0.5.0

The chart installs agent-sandbox v1.0.12 in the `agent-sandbox-system`
namespace. This includes the Sandbox CRD and the agent-sandbox controller.
The operator now reconciles each pen as a Sandbox instead of creating the
pen pod directly.

Before the upgrade, remove any separate agent-sandbox installation. Two
installations of the same cluster-scoped objects conflict. Keep the pinned
release asset as the source of these objects.

Upgrade an existing cluster in this order:

1. Upgrade every `dev-namespace` release to 0.7.0 or later. Its operator
   Role grants Sandbox and NetworkPolicy access and removes pod write
   access.
2. Upgrade the `pipen-operator` release to 0.5.0 or later. This installs
   the agent-sandbox controller and starts the new operator.
3. For each existing pen, delete the pre-upgrade pod so the sandbox
   controller can create its replacement: `kubectl -n <namespace> delete
pod env-<pen>` (or run `pipen down <pen>` and `pipen up <pen>` again).
   Until then the pen shows Failed: its pod is owned by the Pen, not by
   the new Sandbox. Data in the home PVC is not touched.

The first step creates a short failure window until the second step
finishes. The old operator cannot create or delete pods. Existing pens can
show reconcile errors. New pens cannot start. You do not need to pause the
operator. Complete the second step promptly.

No data is lost in this window. The home PVC and Service stay in place.
The Role still grants PVC create and read access and Service write access.

Keep the home PVCs. The Sandbox uses the existing home PVC and creates no
`volumeClaimTemplates`.

The new configuration keys have these defaults:

- `operatorConfig.defaultTTL` is empty. Automatic shutdown stays off. Set
  a duration such as `8h` to enable it.
- `operatorConfig.sandboxService` is false. Only the tailscale Service
  exists for each pen.
- `operatorConfig.egressHosts` is empty. No per-pen egress policy exists.
  Set the approved FQDNs before enabling egress on a pen.

To upgrade agent-sandbox later, replace
`templates/agent-sandbox-sandbox.yaml` with the next pinned release asset.
Update the pinned tag and SHA-256 in the file header and this README. Bump
the chart version. The kind e2e job must download the same release asset.

### Version 0.4.0

Neutral defaults: `config.nodeSelector` to `{}`, `config.storageClass` to `""`, and `config.runtimeClass` to `""`.

An existing cluster must set explicit values or its pens change behavior:

- `config.nodeSelector: {openebs.io/engine: mayastor}` for Mayastor nodes
- `config.storageClass: "mayastor-2"` for Mayastor storage
- `config.runtimeClass: "kata"` for kata runtime

## Rollout

The operator reconciles a Sandbox for each pen. The agent-sandbox controller
creates the pen pod from the Sandbox pod template. The pod keeps the name
`env-<pen>`, and the home PVC stays a separate claim.

The operator updates the operating mode, shutdown time, and Service setting
on an existing Sandbox. On a cpu or memory resize it also updates the
container resources in the stored pod template and sets the Sandbox to
Suspended, so the pod comes back from the new template. A paused pen gets
the template update only. A pod recreation always uses the stored Sandbox
pod template. The home PVC stays a separate claim through pod recreation.
