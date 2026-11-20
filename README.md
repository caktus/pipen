# pipen

pipen runs isolated development environments on a Kubernetes cluster. Each
environment is a pen: one machine per project, up with one command.

Run `pipen up <name>`, then run `ssh <you>@<hostname>`. Each pen is a kata pod
on a Mayastor storage node. kata is a virtual machine runtime. The pen runs an
Ubuntu 26.04 guest. The guest has the full development toolchain, docker, and
pi.dev. pi.dev connects to the litellm proxy that runs inside the cluster.
litellm is a model routing proxy. Your home directory stays across pod
restarts. You reach the pen by name on the tailnet. The tailnet is the
Tailscale private network.

This repo holds the general toolchain. Your cluster's private deployment repo
holds the cluster-specific parts: storage nodes, litellm keys, and
tailnet domain.

- `devenv/` holds the pen definition: the dev image (Dockerfile, entrypoint,
  docker daemon configuration)
- `operator/` holds the cluster-wide Go operator: the Pen CRD types, the
  reconciler, and its image. It watches Pens and brings each pen's PVC, kata
  pod and tailnet Service up
- `charts/pipen-operator/` installs the operator once per cluster (CRD,
  Deployment, RBAC, operator config)
- `charts/dev-namespace/` is a mini Helm chart. It provisions one developer
  namespace: Namespace, ServiceAccount, namespace-scoped Role/RoleBinding, a
  ServiceAccount token, and the ssh-key Secret. It also grants the operator
  per-namespace write access
- `cli/` holds the developer CLI (`pipen.py`). It installs as the `pipen`
  command and manages only the Pen CR

## What a pen gives you

- A kata pod with 4 CPU and 8 GiB memory. It runs on the Mayastor storage
  nodes (label `openebs.io/engine=mayastor`). Only those nodes can attach the
  home volume
- Ubuntu 26.04 with the standard dev toolchain: `git curl build-essential htop
docker.io direnv npm unzip`, uv, fnm, Node 24, and
  `@earendil-works/pi-coding-agent` (`pi`)
- docker inside the pen (dockerd in a privileged kata guest)
- pi.dev against the cluster-internal litellm proxy
  (`http://litellm.litellm.svc.cluster.local:4000`). The guest needs no
  tailnet DNS. Your per-developer key lives in `~/.litellm_key`
- A persistent `/home` on a mirrored `mayastor-2` PVC (10 GiB by default). It
  survives pod deletion. It re-attaches on either storage node
- ssh to the pen over the tailnet. The Tailscale operator gives each pen a
  MagicDNS hostname. The operator can add a suffix, for example
  `<dev>-<name>-1`. The CLI always prints the hostname that the operator
  assigned
- Environment names carry your developer or namespace as a prefix. The CLI
  adds the prefix when it is missing. Hostnames do not collide across
  developers

## Developers

An admin bootstraps you once (see Admins) and gives you the
`.herdr-<you>.yaml` file.

1. Install the file:

   ```sh
   mkdir -p ~/.config/pipen
   cp .herdr-<you>.yaml ~/.config/pipen/config.yaml   # 0600
   ```

2. Install the CLI. Use a checkout of this repo:

   ```sh
   uv run cli/pipen.py --help
   ```

   Or install it:

   ```sh
   uv tool install <checkout>/cli
   ```

3. Work with pens:

   ```sh
   pipen up myenv          # creates one Pen; the operator brings the env up
   pipen ssh myenv         # or: ssh <you>@<hostname> (hostname printed by up)
   pipen ls                # pens: phase, node, hostname
   pipen logs myenv        # pod logs (entrypoint + sshd)
   pipen down myenv        # deletes the Pen; the operator removes pod + Service, the PVC survives
   ```

   The pen starts on the operator's default image (see Admins). To pin
   a specific build, pass its full reference:
   `pipen up myenv --image ghcr.io/caktus/pipen:20261121-pi1.6.0`

   `pipen up myenv` run as `<you>` creates env/service **`<you>-myenv`**. The
   name carries your developer or namespace as a prefix. The CLI adds the
   prefix when it is missing. The pen is reachable at
   `<you>-myenv.<tailnet-domain>`. The operator can add a suffix (for example
   `<you>-myenv-1`). The CLI prints the assigned hostname.
   `pipen down myenv` and `pipen down <you>-myenv` target the same pen. A name
   that starts with someone else's prefix still gets yours.

### What stays, and what does not

- `/home/<you>` lives on the mirrored PVC. It survives `pipen down` + `up`, pod
  deletion, and node moves. The `WaitForFirstConsumer` volume re-attaches on
  the storage node where the new pod lands
- Everything else lives on the temporary kata rootfs. In particular, docker
  images and containers that you build inside a pen vanish on every pod
  recreation. The data root for dockerd is `/var/lib/docker`, on the rootfs.
  Re-running `up` restores your home, not your docker state
- An admin wipes the home data for a pen (see Admins). `pipen down` never
  touches the PVC. The role for your token holds no pvc delete. That role is
  enforced when the cluster runs `--authorization-mode=RBAC`

### Updating pi inside a pen

`pi update` works inside a pen. The image bakes pi into its own npm prefix
(`/opt/pi`). That prefix is writable by your user, so the in-place
`npm install` for the self-update works. The update lives on the temporary
kata rootfs. A pod recreation resets pi to the image-baked version. To change
the baked pi version for everyone, edit `devenv/Dockerfile`. The nightly build picks the change up. To run an older build, pin it with
`pipen up --image ghcr.io/caktus/pipen:<tag>`.

## Admins

### 1. The dev image (GHCR, built nightly)

The dev image builds in this repo's GitHub Actions workflow
(`.github/workflows/publish.yml`) on every merge to main, nightly at 04:00
UTC, and on manual dispatch (Actions tab). It pushes to the GHCR package
`ghcr.io/caktus/pipen`. Tags are
`YYYYMMDD-pi<pi-version>` (for example `20261121-pi1.6.0`) plus `latest`.
The version part comes from the pi-dev the image actually carries. A new tag
appears only when the image content changed. The workflow keeps the 14 newest
date tags and deletes the rest.

One-time setup: open the package page for `caktus/pipen` and set its
visibility to **public**. GitHub creates packages pushed this way as private
by default, and the pen pods pull anonymously.

The pod (created by the operator) pulls `ghcr.io/caktus/pipen` with the
image from the operator config (default `ghcr.io/caktus/pipen:latest`). The
operator always pulls `latest` tags and uses `IfNotPresent` for pinned tags.
There is no in-cluster registry and no image build on a node anymore.

### 2. Install the operator (once per cluster)

```sh
helm install pipen-operator charts/pipen-operator --set image=<operator image>
```

This creates, in namespace `pipen`:

- The `pens.pipen.dev/v1alpha1` Pen CRD. It is a release-managed resource
  (`templates/crd.yaml`): Helm installs it on Helm 3 and Helm 4 alike, and
  `helm upgrade` keeps it in sync. While v1alpha1 is frozen the rendered
  manifest never changes, so upgrades are no-ops. Do not remove the CRD
  from the chart: Helm would delete it
- ServiceAccount + Deployment (one replica; leader election is on)
- A cluster-scoped READ-ONLY ClusterRole/RoleBinding on Pens only, and the
  leader-election Role in `pipen`
- The ConfigMap `pipen-operator-config` with the cluster shape: default
  image, home size, cpu/memory, node selector, storage class, tailscale
  Service annotations, tailnet domain. Change it (or the chart values) and
  the operator picks it up. Existing pens keep their specs; the change
  applies to pens created afterwards

Install it exactly once per cluster. Its cluster-scoped objects and the CRD
belong to a single release.

### The chart repository (GitHub Pages)

The `dev-namespace` chart publishes to `https://caktus.github.io/pipen`.
A GitHub Actions workflow (`.github/workflows/publish-chart.yml`) runs
chart-releaser on every merge to main. It creates a release for each new
chart version and pushes the package and `index.yaml` to the `gh-pages`
branch, which GitHub Pages serves. The release trigger is a `version`
bump in `charts/dev-namespace/Chart.yaml`. A chart change without a bump
publishes nothing and fails `verify.yml`. The site root also carries a
human-readable index of the published charts. One-time setup: set the
Pages source to the `gh-pages` branch in the repo settings.

### 3. Bootstrap a developer

Install the chart from the chart repository:

```sh
helm repo add pipen https://caktus.github.io/pipen
helm repo update pipen
helm install <dev> pipen/dev-namespace --version <version> \
  --set dev=<dev> \
  --set 'authorizedKeys[0]=<ssh public key 1>' \
  --set 'authorizedKeys[1]=<ssh public key 2>'
```

Or install by path from a checkout:

```sh
helm install <dev> charts/dev-namespace \
  --set dev=<dev> \
  --set 'authorizedKeys[0]=<ssh public key 1>' \
  --set 'authorizedKeys[1]=<ssh public key 2>'
```

This creates, in namespace `<dev>` (label `herdr.dev/owner`):

- ServiceAccount `deploy` and a namespace-scoped Role/RoleBinding. The role
  covers the pens CRD (`pens`, `pens/status`), `get` on `pods`, and `get`
  on `pods/log`. Nothing else. In particular it holds **no** pod, Service
  or pvc create — the operator's own ServiceAccount does those, via the
  separate `pipen-operator` Role the same chart creates (pods, services,
  pvc create, configmap/secret read, and still **no pvc delete**). The
  admin-only data wipe stays with the admin. Nothing is cluster-wide, and
  nothing reaches other namespaces
- The explicit ServiceAccount token Secret `deploy-token`
- The Secret `herdr-dev-<dev>` (the ssh keys)

The dev chart grants the operator ServiceAccount (namespace `pipen`) the
per-namespace writes. Install the operator first (step 2); until it exists
the binding dangles harmlessly and pens in the namespace simply never
reconcile

The token controller fills `deploy-token` `data.token` after a delay. Make
sure that it is filled before you read it.

### 4. Create the dev ConfigMap

The ConfigMap `herdr-dev-<dev>` (namespace `<dev>`) holds the runtime
configuration that the operator projects into each pen:

- The rendered pi configuration: `models.json` (litellm baseUrl rewritten to
  the cluster-internal address) and `settings.json`
- The per-developer litellm key (`litellm_key`)

The rendering and key management belong to your cluster tooling (the private
deployment repo).

### 5. Hand the CLI configuration to the developer

Read the token and write `.herdr-<dev>.yaml` (0600, never committed):

```sh
kubectl -n <dev> get secret deploy-token -o jsonpath='{.data.token}' | base64 -d
```

```yaml
api_base: https://<cluster-api-host>:<port> # e.g. the tailnet API address
namespace: <dev>
dev_user: <dev>
token: <sa-token>
```

### Revoke a developer

1. Revoke the litellm key (litellm console or `/key/delete`)
2. Run `helm uninstall <dev>` (removes the chart's namespace, RBAC, and
   secrets)
3. Delete `.herdr-<dev>.yaml`
4. The home PVCs (and their data) go with the namespace when you delete it.
   Run `kubectl delete ns <dev>`. This deletes the home data

### Wipe the home data for one pen

```sh
kubectl -n <dev> delete pvc <pen>-home     # frees the mirrored volume
```

## Capacity

Your storage sets the ceiling for pen count. At 10 GiB mirrored per pen, a
`mayastor-2`-class tier with ~2 TiB usable holds about 70+ pens. Make sure
that the DiskPool has free capacity before you provision at scale.

## CLI notes

- The CLI talks to the cluster API at `api_base` with the namespace-scoped
  ServiceAccount token. It turns off TLS certificate verification for that
  hop. The API server certificate carries no tailnet SAN. Token
  authentication over the private tailnet is the documented posture
- Every verb that the CLI uses is granted by the bootstrap role, and nothing
  more: the pens CRD, `get` on `pods`, and `get` on `pods/log`. That scoping
  works when the cluster runs `--authorization-mode=RBAC`. On an AlwaysAllow
  cluster, the cluster itself is the boundary. In particular, the role for
  the token holds no pvc delete and no pod create. The data wipe and the pod
  creation stay with the admin and the operator
- `up` prints the hostname from Pen status. The operator reads the
  tailnet-assigned hostname (or IP) from the Service and copies it into
  `status.hostname`. The Tailscale operator can add a suffix (for example
  `<you>-myenv-1`); the CLI always prints the name that was actually
  assigned
