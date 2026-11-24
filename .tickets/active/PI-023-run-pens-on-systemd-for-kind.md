---
id: PI-023
github_issue: 23
github_url: https://github.com/caktus/pipen/issues/23
last_synced: "2026-11-24T21:11:14.290644+00:00"
synced_hash: 9790d0c751d72ba999fb0748fa51318d58ae4919cdec771dceea1bc0eacd106e
title: Run pens on systemd for kind
type: Task
assignee: "@me"
labels:
  - phase/in-progress
context:
  relates_to: []
---

As a developer who runs a kata pen, I want `kind create cluster` to work inside my pen, so that I can develop against a local Kubernetes cluster without leaving the pen.

On guest kernel >= 6.13 the pod-container cgroup becomes a thread root when dockerd enables the threadable controllers while container tasks sit in it. The kernel then refuses `+memory` on that subtree permanently, and a kind node's systemd dies at boot. A guest-kernel downgrade is not an option. The fix belongs in the pen image. It runs systemd as PID 1. Docker runs on the systemd cgroup driver. The fix also adds a thin derived kind node image. The image covers the kata guest device gap. The kata guest has no `/dev/kmsg`, and the kubelet hard-fails without it.

Validated on a local microk8s + kata 3.29.0 cluster. The guest kernel is 6.18.15. The branch is `fix/ho052-systemd-kata`. The candidate image boots. Memory delegation works. A nested kind cluster reaches Ready. It uses `KIND_EXPERIMENTAL_CONTAINERD_SNAPSHOTTER=native` and the thin node image. `kubectl exec` into a kata pen fails with EBUSY after the cutover. The kata agent joins the pod-container cgroup. The accepted workflow is SSH.

### To Do

- Commit the candidate on this branch
  - systemd pen: `herdr-boot` /run projection copy, `pen-env-import` + `pen-setup` units, docker/ssh unit drop-ins, explicit `native.cgroupdriver=systemd`
  - `kind-node/` thin image
  - CI probes
  - docs
- Decide: bake kind/kubectl into the pen image or document the two-curl install
- Document that in-pen `docker build`/`docker commit` is broken by the virtiofs rootfs. The rootfs has no xattr support. Images must be built outside the pen and loaded in

## AC

- [ ] The pen boots with systemd as PID 1. The `pen-setup` oneshot, docker, and ssh are active services. The dev user is created from the `DEV_USER` container env
- [ ] The pod-container cgroup stays a delegable domain (not `domain threaded`) after docker and kind run. A `--memory`-limited container gets its limit in its scope
- [ ] `KIND_EXPERIMENTAL_CONTAINERD_SNAPSHOTTER=native kind create cluster --image pipen-kind-node:<tag>` completes in a pen. The node reaches Ready and a smoke pod runs
- [ ] SSH key login works with a rotated Secret (new key in, old key out). Existing pens recover via new image + pod recreation, no guest rebuild

## Related tickets

- caktus/hosted-llm#52
