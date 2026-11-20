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
| `config.nodeSelector`       | object | The only nodes that can attach the home volume.                                                                                   |
| `config.storageClass`       | string | Home PVC storage class.                                                                                                           |
| `config.serviceAnnotations` | object | Annotations on every pen Service (tailscale L4 exposure). The `tailscale.com/hostname` annotation is set per pen by the operator. |
| `config.tailnetDomain`      | string | Informational tailnet domain (`TAILNET_HOSTNAME` env, logged hostnames).                                                          |
| `config.imagePullPolicy`    | string | Pod image pull policy.                                                                                                            |
| `config.loadBalancerClass`  | string | `loadBalancerClass` for pen Services (the Tailscale operator).                                                                    |
| `config.runtimeClass`       | string | Runtime class for env pods (`kata`). Empty means no runtime class, for clusters that run no kata runtime.                         |
