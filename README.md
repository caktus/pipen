# pipen

pipen runs isolated development environments on a Kubernetes cluster. Each
environment is a pen: one machine per project, up with one command.

Run `pipen up <name>`, then run `ssh <you>@<hostname>`. The operator runs
each pen on a Sandbox. A Sandbox is an object from the agent-sandbox
project. It owns the pen pod and its lifecycle. The pen's Sandbox is named
`env-<pen>`, and its pod carries the same name. `pipen logs` reads that
pod, which is why it works. The pen runs an Ubuntu 26.04 guest. The guest
has the full development toolchain, docker, and pi.dev. pi.dev runs in
shared mode against the litellm proxy in the cluster, or in self-provider
mode against your own model-provider account with no proxy. litellm is a
model routing proxy. Your home directory stays across pod restarts. You reach the pen by name on the
tailnet. The tailnet is the Tailscale private network.

This repo holds the general toolchain. Your cluster's private deployment repo
holds the cluster-specific parts: storage nodes, litellm keys, and
tailnet domain.

- `devenv/` holds the pen definition: the dev image (Dockerfile, entrypoint,
  docker daemon configuration)
- `operator/` holds the cluster-wide Go operator: the Pen CRD types, the
  reconciler, and its image. It watches Pens and brings each pen's PVC,
  Sandbox and tailnet Service up
- `charts/pipen-operator/` installs the operator once per cluster (CRD,
  Deployment, RBAC, operator config) and the pinned agent-sandbox controller
- `charts/dev-namespace/` is a mini Helm chart. It provisions one developer
  namespace: Namespace, ServiceAccount, namespace-scoped Role/RoleBinding, a
  ServiceAccount token, and the ssh-key Secret. It also grants the operator
  per-namespace write access
- `cli/` holds the developer CLI (`pipen.py`). It installs as the `pipen`
  command and manages only the Pen CR

## What a pen gives you

- A pod with 4 CPU and 8 GiB memory. It runs on nodes that can attach the
  home volume
- Ubuntu 26.04 with the standard dev toolchain: `git curl build-essential htop
docker.io direnv npm unzip`, uv, fnm, Node 24, and
  `@earendil-works/pi-coding-agent` (`pi`)
- docker inside the pen (dockerd in a privileged guest)
- pi.dev against the cluster-internal litellm proxy
  (`http://litellm.litellm.svc.cluster.local:4000`) in shared mode. The
  guest needs no tailnet DNS. Your per-developer key lives in
  `~/.litellm_key`. In self-provider mode the pen carries the provider
  configuration for your own account and no proxy is involved
- A persistent `/home` on a PVC (10 GiB by default). It
  survives pod deletion. It re-attaches on a node that can access the volume
- ssh to the pen over the tailnet. The Tailscale operator gives each pen a
  MagicDNS hostname. The operator can add a suffix, for example
  `<dev>-<name>-1`. The CLI always prints the hostname that the operator
  assigned
- Pause. `pipen pause <name>` stops the pen pod and keeps the Pen, the home
  PVC, and the tailnet hostname. `pipen resume <name>` starts the pod again
  with a fresh TTL
- Resize. `pipen resize <name> --cpu 8 --memory 16Gi --size 20Gi` changes the
  pen's CPU, memory, or home size. Pass only the flags that change. A CPU or
  memory change restarts the pen pod with the new size. The pen passes
  through the Suspended phase and comes back with a fresh TTL, like a
  resume. A home size change grows the PVC in place, and the home data
  stays. The home size can only grow. A smaller `--size` is refused
- Auto shutdown after the window. A TTL (a time limit, for example `8h`)
  shuts the pen down when the window ends. Set one per pen with
  `pipen up --ttl 8h`, or set the operator default
  (`operatorConfig.defaultTTL`). `pipen ls` shows the remaining time. An
  expired pen that is not paused starts again on its own with a fresh TTL.
  Pause it to keep it down
- Egress hardening. `pipen up --egress` adds a NetworkPolicy for the pen.
  A NetworkPolicy filters a pod's network traffic. The policy allows only
  the hosts in the operator value `operatorConfig.egressHosts` and cluster
  DNS
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

2. Install the CLI. It installs straight from GitHub, no checkout needed:

   ```sh
   uv tool install "git+https://github.com/caktus/pipen#subdirectory=cli"
   ```

   Or run it without an install. `uvx` builds a throwaway environment:

   ```sh
   uvx --from "git+https://github.com/caktus/pipen#subdirectory=cli" pipen --help
   ```

3. Work with pens:

   ```sh
   pipen up myenv          # creates one Pen; the operator brings the env up
   pipen up myenv --ttl 8h # shuts down after 8h, then starts again with a fresh TTL
   pipen up myenv --egress # allow only the approved egress destinations
   pipen ssh myenv         # or: ssh <you>@<hostname> (hostname printed by up)
   pipen pause myenv       # stop the pod; the Pen, PVC and hostname stay
   pipen resume myenv      # start a paused pen again; the TTL starts fresh
   pipen resize myenv      # change cpu, memory or home size
   pipen ls                # pens: phase, TTL, node, hostname
   pipen logs myenv        # pod logs (entrypoint + sshd)
   pipen down myenv        # deletes the Pen; the Sandbox, pod, Service and egress policy go (when egress is on), the PVC survives
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

- `pipen down <name>` removes the Pen, the Sandbox, the pod, the tailnet
  Service, and the egress NetworkPolicy (when egress is on). The home PVC
  and its data stay
- `/home/<you>` lives on the persistent PVC. It survives `pipen down` + `up`, pod
  deletion, and node moves. The `WaitForFirstConsumer` volume re-attaches on
  a node that can attach the volume
- Everything else lives on the temporary rootfs. In particular, docker
  images and containers that you build inside a pen vanish on every pod
  recreation. The data root for dockerd is `/var/lib/docker`, on the rootfs.
  Re-running `up` restores your home, not your docker state
- An admin wipes the home data for a pen (see Admins). `pipen down` never
  touches the PVC. The role for your token holds no pvc delete. That role is
  enforced when the cluster runs `--authorization-mode=RBAC`

### ssh host keys

Each pen presents its own ssh host key. The key lives on the `/home` PVC, so
it survives pod recreation. The pen generates the key on its first boot and
reuses it on every later boot. You get one `known_hosts` prompt per pen, not
one per boot.

The first image that gives each pen its own host key changes what an existing
pen presents. On the next boot after that image is live, the pen switches from
the old shared key to its own key. You get one `known_hosts` prompt for each
existing pen at that switch. After the prompt, the key stays the same for the
life of the pen. Wiping the home data destroys the key, and the next boot
presents a new one with one more prompt.

### Updating pi inside a pen

`pi update` works inside a pen. The image bakes pi into its own npm prefix
(`/opt/pi`). That prefix is writable by your user, so the in-place
`npm install` for the self-update works. The update lives on the temporary
rootfs. A pod recreation resets pi to the image-baked version. To change
the baked pi version for everyone, edit `devenv/Dockerfile`. The nightly build picks the change up. To run an older build, pin it with
`pipen up --image ghcr.io/caktus/pipen:<tag>`.

## Working on pipen

If you edit this repo, work from a checkout instead of the GitHub install.

1. Run the CLI from the checkout:

   ```sh
   uv run cli/pipen.py --help
   ```

2. If you want `pipen` on your PATH, install the local copy:

   ```sh
   uv tool install --force cli
   ```

   Use `--force` on every reinstall so the local copy replaces the GitHub
   one

## Admins

### Prerequisites

What the cluster needs before the install. A fresh admin can install pipen
from this page alone.

Hard requirements (pipen does not run without them):

- **tailscale-operator** — the only ingress path. It exposes each pen
  Service on the tailnet (L4) and runs the L3 ssh proxy that the
  dev-namespace NetworkPolicy admits. A cluster that cannot run it is
  unsupported. Install once, in namespace `tailscale`:

  ```sh
  helm repo add tailscale https://pkgs.tailscale.com/helmcharts
  helm install tailscale-operator tailscale/tailscale-operator
  ```

  If the operator runs in another namespace, set `tailscale.namespace` in
  the dev-namespace chart. One-time admin-console setup: create an OAuth
  client tagged `tag:k8s-operator` with write scope on General/Services,
  Devices/Core, and Keys/Auth Keys. Grant `tag:k8s` to `tag:k8s-operator`
  in `tagOwners`. Allow the service advertisement with `autoApprovers`
  services for `tag:k8s`. The hosted-llm repo shows one working chain with
  values (caktus/hosted-llm#23)

- **A CNI that enforces NetworkPolicy** (Calico, Cilium, or kindnet) and
  kubernetes 1.22 or newer. The 1.22 floor is the automatic namespace label
  `kubernetes.io/metadata.name` that the policies use. On a cluster older
  than 1.22, add the static label `kubernetes.io/metadata.name=<name>` to
  every namespace a policy matches: `kube-system` (DNS), the tailscale
  operator namespace, and each `egress.podAllows` target namespace. Keep
  `matchLabels` in the dev-namespace templates. kindnet checks pod-to-pod
  traffic, but it applies no policy to traffic addressed to a Service
  (ClusterIP). Run Calico or Cilium when you need egress to in-cluster
  Services denied
- **kube-proxy check**: with iptables mode and `masqueradeAll: true`, the
  tailscale proxy's connections may arrive at the pen pod from the node IP
  instead of the proxy pod. If so, change the ingress allow to an ipBlock
  of the node CIDR on port 22, or use the eBPF datapath. Check before
  rollout
- **Public GHCR packages** for `ghcr.io/caktus/pipen` (the dev image) and
  `ghcr.io/caktus/pipen-operator` (see steps 1 and 2)

Default-install requirements (any alternative works when you set the
matching value):

- **Storage for pen homes** — Any StorageClass works: set `config.storageClass` and
  `config.nodeSelector`. When the home-volume data path uses the pod network,
  allow its endpoint with `egress.podAllows` or `egress.cidrAllows` (dev-namespace chart).
- **Runtime class for pods** — Set `config.runtimeClass` to use a runtime class.

Optional:

- **litellm proxy** — a shared model endpoint with per-developer keys (the
  BerriAI `litellm` helm chart). Without it, each developer signs in to
  their own provider account inside the pen. Reach the proxy pod from a dev
  namespace with `egress.podAllows` (the example in the dev-namespace values
  shows the rule; the same rule pattern serves any in-cluster model
  endpoint)

Reference configuration (the defaults from earlier versions):

- For Mayastor storage nodes, set `config.nodeSelector` to `{openebs.io/engine: mayastor}`
- For Mayastor storage, set `config.storageClass` to `"mayastor-2"`
- For kata runtime, set `config.runtimeClass` to `"kata"`

The chart defaults assume this repo's reference cluster. An external org
sets these values and nothing else:

| Value                                          | Chart          | Default                                | Meaning                                                |
| ---------------------------------------------- | -------------- | -------------------------------------- | ------------------------------------------------------ |
| `image`                                        | pipen-operator | `ghcr.io/caktus/pipen-operator:latest` | the operator image                                     |
| `config.image`                                 | pipen-operator | `ghcr.io/caktus/pipen:latest`          | the pen pod image                                      |
| `config.storageClass`                          | pipen-operator | `""`                                   | the home PVC class                                     |
| `config.nodeSelector`                          | pipen-operator | `{}`                                   | the home volume nodes                                  |
| `config.runtimeClass`                          | pipen-operator | `""`                                   | empty runs no runtime class                            |
| `config.loadBalancerClass`                     | pipen-operator | `tailscale`                            | pen Service exposure                                   |
| `config.serviceAnnotations`                    | pipen-operator | `tailscale.com/expose: "true"`         | pen Service annotations                                |
| `config.tailnetDomain`                         | pipen-operator | empty                                  | informational, printed hostnames                       |
| `tailscale.namespace`                          | dev-namespace  | `tailscale`                            | the operator namespace                                 |
| `egress.podAllows`, `egress.cidrAllows`        | dev-namespace  | empty                                  | in-cluster model endpoints and other egress            |
| `dev`, `namespacePrefix`, `operator.namespace` | dev-namespace  | required, `env-`, `pipen`              | naming. `dev` is required: the render fails without it |

The full value lists and defaults live in each chart README.

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

The operator image is published to the GHCR package
`ghcr.io/caktus/pipen-operator` by this repo's GitHub Actions workflow
(`.github/workflows/publish-operator.yml`) on every merge to main, nightly
at 04:00 UTC, and on manual dispatch (Actions tab). Tags are the UTC date
(`YYYYMMDD`) plus `latest`. A new tag appears only when the image content
changed. The workflow keeps the 14 newest date tags and deletes the rest.
The chart defaults to `ghcr.io/caktus/pipen-operator:latest`, so the
install needs no `--set image`. The operator pod always pulls `latest`
tags and uses `IfNotPresent` for pinned tags.

One-time setup: open the package page for `caktus/pipen-operator` and set
its visibility to **public**. GitHub creates packages pushed this way as
private by default, and the operator pod pulls anonymously.

The chart is published in the chart repository (see the section below):

```sh
helm install pipen-operator pipen/pipen-operator
# or from a checkout:
helm install pipen-operator charts/pipen-operator
```

This creates, in namespace `pipen`:

- The `pens.pipen.dev/v1alpha1` Pen CRD. It is a release-managed resource
  (`templates/crd.yaml`): Helm installs it on Helm 3 and Helm 4 alike, and
  `helm upgrade` keeps it in sync. While v1alpha1 is frozen the rendered
  manifest never changes, so upgrades are no-ops. Do not remove the CRD
  from the chart: Helm would delete it
- The agent-sandbox controller in the `agent-sandbox-system` namespace. The
  chart vendors the pinned v1.0.12 release asset. It creates the pen pod
  from each Sandbox. The chart README documents the upgrade path
- ServiceAccount + Deployment (one replica; leader election is on)
- A cluster-scoped READ-ONLY ClusterRole/RoleBinding on pens, namespaces
  and storageclasses, and the leader-election Role in `pipen`
- The ConfigMap `pipen-operator-config` with the cluster shape: default
  image, home size, cpu/memory, node selector, storage class, tailscale
  Service annotations, tailnet domain, default TTL, egress hosts. Change it (or the chart values) and
  the operator picks it up. Existing pens keep their specs; the change
  applies to pens created afterwards

Install it exactly once per cluster. Its cluster-scoped objects and the CRD
belong to a single release.

#### Building the operator image yourself

To build the image yourself and pin the operator to the node(s) that hold
it, add the flags to the install:

```sh
docker build -t <ref> operator/
helm install pipen-operator charts/pipen-operator \
  --set image=<ref> \
  --set-json 'nodeSelector={"kubernetes.io/hostname":"<node>"}'
```

Use a full reference that does not end in `:latest`. The operator pod then
uses `IfNotPresent` and runs the locally staged image instead of pulling.

### The chart repository (GitHub Pages)

Both charts in `charts/` publish to the chart repository you add with
`helm repo add pipen https://caktus.github.io/pipen`:
`pipen/dev-namespace` and `pipen/pipen-operator`. A GitHub Actions
workflow (`.github/workflows/publish-chart.yml`) runs chart-releaser on
every merge to main. It creates a release for each new chart version and
pushes the package and `index.yaml` to the `gh-pages` branch, which
GitHub Pages serves. The release trigger is a `version` bump in a
chart's `Chart.yaml`. A chart change without a bump publishes nothing and
fails `verify.yml`. The site root also carries a
human-readable index of the published charts. One-time setup: set the
Pages source to the `gh-pages` branch in the repo settings.

### 3. Bootstrap a developer

Install the chart from the chart repository:

```sh
helm repo add pipen https://caktus.github.io/pipen
helm repo update pipen
helm install <dev> pipen/dev-namespace --version 0.6.0 \
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

This creates, in namespace `<namespacePrefix><dev>` (default `env-<dev>`; the label `herdr.dev/owner` holds the bare `dev`):

- ServiceAccount `deploy` and a namespace-scoped Role/RoleBinding. The role
  covers the pens CRD (`pens`, `pens/status`), `get` on `pods`, and `get`
  on `pods/log`. Nothing else. In particular it holds **no** pod, Service
  or pvc create. The operator's own ServiceAccount brings the pen up, via
  the separate `pipen-operator` Role the same chart creates (sandboxes,
  networkpolicies, services, pvc create, patch, update, pod reads,
  configmap/secret read, and still **no pvc delete**). The agent-sandbox controller creates the
  pod. The admin-only data wipe stays with the admin. Nothing is cluster-wide, and
  nothing reaches other namespaces
- The explicit ServiceAccount token Secret `deploy-token`
- The Secret `herdr-dev-<namespace>` (the ssh keys. Under the default prefix that is `herdr-dev-env-<dev>`)

The dev chart grants the operator ServiceAccount (namespace `pipen`) the
per-namespace writes. Install the operator first (step 2); until it exists
the binding dangles harmlessly and pens in the namespace simply never
reconcile

The token controller fills `deploy-token` `data.token` after a delay. Make
sure that it is filled before you read it.

### 4. Create the dev ConfigMap

The ConfigMap `herdr-dev-<namespace>` (namespace `env-<dev>` under the default prefix) holds the runtime
configuration that the operator projects into each pen. The ConfigMap must
exist even when empty: the operator waits for the object before it creates
a pen. Every entry is
optional: a pen whose ConfigMap carries none of them still boots, and the
developer signs in to their own provider account inside.

Two supported modes:

- **litellm-backed (the shared proxy):** `models.json` (the litellm baseUrl
  rewritten to the cluster-internal address) and `settings.json`, plus the
  per-developer litellm key as `litellm_key`. The entrypoint places the key
  at `~/.litellm_key` (0600)
- **Self-provider:** `models.json` (and optionally `settings.json`) for the
  developer's own provider account, no key file. The developer pastes their
  own key inside the pen

The rendering and key management belong to your cluster tooling (the private
deployment repo).

### 5. Hand the CLI configuration to the developer

Read the token and write `.herdr-<dev>.yaml` (0600, never committed):

```sh
kubectl -n env-<dev> get secret deploy-token -o jsonpath='{.data.token}' | base64 -d
```

```yaml
api_base: https://<cluster-api-host>:<port> # e.g. the tailnet API address
namespace: env-<dev> # the composed name, namespacePrefix + dev
dev_user: <dev> # the bare dev
token: <sa-token>
```

### Revoke a developer

1. If the cluster uses the litellm proxy, revoke the developer's key (litellm console or `/key/delete`)
2. Run `helm uninstall <dev>` (removes the chart's namespace, RBAC, and
   secrets)
3. Delete `.herdr-<dev>.yaml`
4. The home PVCs (and their data) go with the namespace when you delete it.
   Run `kubectl delete ns env-<dev>`. This deletes the home data. Any
   `~/.litellm_key` file left on a pen home goes with it. The key is inert
   once revoked server-side

### Wipe the home data for one pen

```sh
kubectl -n env-<dev> delete pvc <pen>-home     # frees the home volume
```

## Capacity

Your storage sets the ceiling for pen count. At 10 GiB per pen, storage with
~2 TiB usable holds about 70+ pens. Make sure that the storage has free
capacity before you provision at scale. This example assumes mirrored storage;
the actual capacity depends on your StorageClass.

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
