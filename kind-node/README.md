# pipen-kind-node

A thin derived [kind](https://kind.sigs.k8s.io/) node image so `kind
create cluster` works inside a pipen kata pen.

## Why the image exists

Two kata-guest gaps stop stock `kindest/node` from booting in a pen
(neither is a cgroup issue):

1. The kata guest device list has no `/dev/kmsg`, and the node's
   kubelet (v1.32) hard-fails without it. The image adds one oneshot
   unit that `mknod`s `/dev/kmsg` before kubelet starts. The unit is a
   no-op on hosts that already have `/dev/kmsg`.
2. The node entrypoint picks `fuse-overlayfs` on a FUSE-backed pen
   rootfs. That snapshotter needs `/dev/fuse`, which the kata guest
   also lacks. The image does not fix that gap. Use the upstream env
   override instead (see below).

## Versions (keep in lockstep)

| Component           | Version                                                                                                                       |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| base `kindest/node` | `v1.32.2`, digest `sha256:142f543559cc55d64e1ab9341df08e5ced84bd2e893736da8f51320f26f5950b` (pinned in the Dockerfile `FROM`) |
| kind                | `0.27.0` (tested with the v1.32.2 node image)                                                                                 |

To update: bump the digest in `Dockerfile` (same tag) and the kind
version together. Re-run the local kata validation.

## Install kind and kubectl in the pen

The pen has public-internet egress by default. The pen has
passwordless sudo. You install both tools from inside the pen. The
pens are linux/amd64. The kubectl client (v1.32.2) matches the
node's k8s 1.32.

```sh
sudo curl -Lo /usr/local/bin/kind https://kind.sigs.k8s.io/dl/v0.27.0/kind-linux-amd64
sudo chmod +x /usr/local/bin/kind
sudo curl -Lo /usr/local/bin/kubectl https://dl.k8s.io/v1.32.2/bin/linux/amd64/kubectl
sudo chmod +x /usr/local/bin/kubectl
```

The tools live on the temporary rootfs, so rerun the install if the
pen's pod is recreated.

When you bump the lockstep table above, bump these two pins with it.

## Use

```sh
# from inside the pen:
KIND_EXPERIMENTAL_CONTAINERD_SNAPSHOTTER=native \
  kind create cluster --name myenv --image pipen-kind-node:ho052
```

kind forwards `KIND_EXPERIMENTAL_CONTAINERD_SNAPSHOTTER` into the node
container. The node entrypoint applies it to the containerd
configuration at boot. The nested containerd then uses the `native`
snapshotter. It is dir-based and makes no mounts. The pen rootfs is
FUSE/virtiofs-backed.

## Build (local)

```sh
docker build -t pipen-kind-node:ho052 kind-node/
```

Load the image into a pen with `docker save` + `docker load`. Do not
build inside a pen. The FUSE rootfs has no xattr support, so image
build and commit break. See the hosted-llm HO-052 validation notes.

The `ghcr.io/caktus/pipen-kind-node` name is reserved for future
publication. Nothing is published yet.
