# pipen-operator

Installs the pipen operator: the `pens.pipen.dev` CRD, the operator
Deployment in the `pipen` namespace, its ServiceAccount, the
ClusterRole/ClusterRoleBinding it needs, and the ConfigMap
`pipen-operator-config` that holds the cluster shape. The operator reads
that ConfigMap and watches it; a change re-reconciles all pens.

## Install

```sh
helm install operator pipen/pipen-operator --version <version>
```

Or from a repo checkout: `helm install operator charts/pipen-operator ...`.

## Objects

- Namespace `pipen`
- CustomResourceDefinition for `pens.pipen.dev`
- ServiceAccount `pipen-operator`
- ClusterRole/ClusterRoleBinding `pipen-operator` (cluster-wide Pen watch)
- Role/RoleBinding for the per-namespace writes the dev chart grants
- Deployment `pipen-operator` (one replica, leader election on)
- ConfigMap `pipen-operator-config`

## Values

| Name                        | Type   | Description                                                                                                                       |
| --------------------------- | ------ | --------------------------------------------------------------------------------------------------------------------------------- |
| `image`                     | string | Operator image (full reference).                                                                                                  |
| `replicaCount`              | int    | Replicas. One. Leader election makes a future 2-replica flip cheap.                                                               |
| `nodeSelector`              | object | Pod-level scheduling for the Deployment. Empty: the cluster scheduler picks.                                                      |
| `config.image`              | string | Default pod image for pens (the Pen spec `image` overrides per pen).                                                              |
| `config.homeSize`           | string | Home PVC size.                                                                                                                    |
| `config.cpu`                | string | Pod CPU request/limit.                                                                                                            |
| `config.memory`             | string | Pod memory request/limit.                                                                                                         |
| `config.nodeSelector`       | object | Node selector for home volume attachment. Empty (default): pens can attach the home PVC to any node.                              |
| `config.storageClass`       | string | Home PVC storage class. Empty (default): the cluster's default StorageClass applies.                                              |
| `config.serviceAnnotations` | object | Annotations on every pen Service (tailscale L4 exposure). The `tailscale.com/hostname` annotation is set per pen by the operator. |
| `config.tailnetDomain`      | string | Informational tailnet domain (`TAILNET_HOSTNAME` env, logged hostnames).                                                          |
| `config.imagePullPolicy`    | string | Pod image pull policy.                                                                                                            |
| `config.loadBalancerClass`  | string | `loadBalancerClass` for pen Services (the Tailscale operator).                                                                    |
| `config.runtimeClass`       | string | Runtime class for env pods. Empty (default): no runtime class.                                                                    |

## Upgrade

### Version 0.4.0

Neutral defaults: `config.nodeSelector` to `{}`, `config.storageClass` to `""`, and `config.runtimeClass` to `""`.

An existing cluster must set explicit values or its pens change behavior:

- `config.nodeSelector: {openebs.io/engine: mayastor}` for Mayastor nodes
- `config.storageClass: "mayastor-2"` for Mayastor storage
- `config.runtimeClass: "kata"` for kata runtime

## Rollout

The operator creates the pen pod once and never updates it (create-or-
leave). After an operator update that changes the pod spec (for example a
change to the `DEV_USER` env), each env picks up the new value only after a
manual pod delete: `kubectl -n <namespace> delete pod env-<pen>`. The
operator recreates the pod on the next reconcile, and no home data is
migrated (the renamed user starts from an empty home). Any pod recreation,
not just the manual delete, for example a node drain or eviction, recreates
the pod with the current operator's `DEV_USER` and switches the guest user
the same way.
