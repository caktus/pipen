# pipen-kind-node

A thin derived [kind](https://kind.sigs.k8s.io/) node image so `kind
create cluster` works inside a pipen kata pen.

## Why the image exists

Two kata-guest gaps stop stock `kindest/node` from booting in a pen
(neither is a cgroup issue):

1. **`/dev/kmsg` is missing** from the kata guest device list, and the
   node's kubelet (v1.32) hard-fails without it. The image adds one
   oneshot unit that `mknod`s the node before kubelet starts (a no-op on
   hosts that already have `/dev/kmsg`).
2. **The snapshotter**: on a FUSE-backed pen rootfs the node entrypoint
   auto-selects `fuse-overlayfs`, which needs `/dev/fuse` — also absent.
   This is _not_ fixed in the image; use the upstream env override
   instead (see below).

## Versions (keep in lockstep)

| Component           | Version                                                                                                                       |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| base `kindest/node` | `v1.32.2`, digest `sha256:142f543559cc55d64e1ab9341df08e5ced84bd2e893736da8f51320f26f5950b` (pinned in the Dockerfile `FROM`) |
| kind                | `0.27.0` (ships k8s 1.32 nodes)                                                                                               |

To update: bump the digest in `Dockerfile` (same tag) and the kind
version together, and re-run the local kata validation.

## Use

```sh
# from inside the pen (kind installed locally in the pen for this use):
KIND_EXPERIMENTAL_CONTAINERD_SNAPSHOTTER=native \
  kind create cluster --name myenv --image pipen-kind-node:ho052
```

kind forwards `KIND_EXPERIMENTAL_CONTAINERD_SNAPSHOTTER` into the node
container and the node entrypoint applies it to the containerd config at
boot, so the dir-based `native` snapshotter (no mounts — the pen rootfs
is FUSE/virtiofs-backed) is used for the nested containerd.

## Build (local)

```sh
docker build -t pipen-kind-node:ho052 kind-node/
```

Load into a pen with `docker save` + `docker load` (do not build inside a
pen: the FUSE rootfs has no xattr support, which breaks image build and
commit — see the hosted-llm HO-052 validation notes).

The `ghcr.io/caktus/pipen-kind-node` name is reserved for future
publication; nothing is published yet.
