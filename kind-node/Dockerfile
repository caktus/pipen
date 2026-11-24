# pipen-kind-node: a thin kindest/node derivative for running kind inside
# pipen kata pens.
#
# The kata guest device list has no /dev/kmsg, and the node's kubelet
# (v1.32) hard-fails at boot without it ("open /dev/kmsg: no such file or
# directory"). This image adds exactly one oneshot unit that mknod's the
# node before kubelet starts (mknod 1:11 is allowed in the privileged pen
# guest; on a host where /dev/kmsg already exists the unit is a no-op).
#
# Nothing else is baked in — in particular the containerd snapshotter is
# NOT pinned here. On a FUSE-backed pen rootfs the node entrypoint would
# auto-select the fuse-overlayfs snapshotter (the kata guest has no
# /dev/fuse), so create the cluster with the upstream env override:
#
#   KIND_EXPERIMENTAL_CONTAINERD_SNAPSHOTTER=native \
#     kind create cluster --name myenv --image pipen-kind-node:ho052
#
# kind forwards the variable into the node container (`-e` in
# pkg/cluster/internal/providers/docker/provision.go) and the node
# entrypoint applies it to the containerd config at boot — no kind fork,
# no image-level config change.
#
# Base: kindest/node:v1.32.2, digest-pinned. Bump base + kind version
# together (kind 0.27.0 ships k8s 1.32 nodes); see README.md.

# hadolint ignore=DL3006
FROM kindest/node@sha256:142f543559cc55d64e1ab9341df08e5ced84bd2e893736da8f51320f26f5950b

RUN printf '%s\n' \
      '[Unit]' \
      'Description=pipen: expose /dev/kmsg (kata guest device gap)' \
      'Before=kubelet.service' \
      '[Service]' \
      'Type=oneshot' \
      'RemainAfterExit=yes' \
      'ExecStart=/usr/bin/sh -c "test -e /dev/kmsg || mknod /dev/kmsg c 1 11; chmod 644 /dev/kmsg"' \
      '[Install]' \
      'WantedBy=multi-user.target' > /etc/systemd/system/kmsg-fix.service \
 && printf '%s\n' \
      '[Unit]' \
      'Wants=kmsg-fix.service' \
      'After=kmsg-fix.service' > /etc/systemd/system/kubelet.service.d/kmsg.conf \
 && systemctl enable kmsg-fix.service

# local build:
#   docker build -t pipen-kind-node:ho052 -t ghcr.io/caktus/pipen-kind-node kind-node/
# (the ghcr.io tag is the future publication name; no publication is
# configured yet — the local tag is what the pen loads)
