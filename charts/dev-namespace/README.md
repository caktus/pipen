# dev-namespace

Provisions one developer namespace on a pipen cluster: Namespace,
ServiceAccount, a namespace-scoped Role/RoleBinding, an explicit
ServiceAccount token Secret, and the ssh-key Secret.

## Install

```sh
helm install <dev> pipen/dev-namespace --version <version> \
  --set dev=<dev> \
  --set 'authorizedKeys[0]=<ssh public key 1>' \
  --set 'authorizedKeys[1]=<ssh public key 2>'
```

Or from a repo checkout: `helm install <dev> charts/dev-namespace ...`.

## Values

| Name             | Type   | Description                                                                        |
| ---------------- | ------ | ---------------------------------------------------------------------------------- |
| `dev`            | string | Developer name. Becomes the namespace name. RFC 1123.                              |
| `authorizedKeys` | list   | ssh public keys, written to the `authorized_keys` Secret key of `herdr-dev-<dev>`. |

## Objects

- `Namespace` `<dev>` (label `herdr.dev/owner=<dev>`)
- `ServiceAccount` `deploy`
- `Role`/`RoleBinding` `deploy`: CRUD on pods/services/configmaps/secrets,
  get/list/create/watch on persistentvolumeclaims, no pvc delete
- `Secret` `deploy-token` (`kubernetes.io/service-account-token`); the token
  controller fills `data.token` after a delay
- `Secret` `herdr-dev-<dev>`: the ssh public keys

The per-developer litellm key and the rendered pi configuration live in a
ConfigMap that your cluster tooling creates. See the repo README (Admins).
