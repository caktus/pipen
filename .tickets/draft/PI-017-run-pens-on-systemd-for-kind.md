---
id: PI-017
github_issue: null
github_url: null
last_synced: null
synced_hash: null
title: Run pens on systemd for kind
type: Task
assignee: "@me"
labels:
  - phase/draft
context:
  relates_to: []
---

As a developer who runs a kata pen, I want `kind create cluster` to work inside my pen, so that I can develop against a local Kubernetes cluster without leaving the pen.

On guest kernel >= 6.13 the pod-container cgroup becomes a thread root when dockerd enables the threadable controllers while container tasks sit in it. The kernel then refuses `+memory` on that subtree permanently, and a kind node's systemd dies at boot. A guest-kernel downgrade is not an option, so the fix belongs in the pen image: systemd as PID 1 with Docker on the systemd cgroup driver, plus a thin derived kind node image for the kata guest device gap (`/dev/kmsg` is missing, kubelet hard-fails without it).

Validated on a local microk8s + kata 3.29.0 cluster (guest kernel 6.18.15) on branch `fix/ho052-systemd-kata`: the candidate image boots, memory delegation works, and a nested kind cluster reaches Ready with `KIND_EXPERIMENTAL_CONTAINERD_SNAPSHOTTER=native` and the thin node image. `kubectl exec` into a kata pen fails with EBUSY after the cutover (the kata agent joins the pod-container cgroup); the accepted workflow is SSH.

### To Do

- Commit the candidate on this branch (systemd pen: `herdr-boot` /run projection copy, `pen-env-import` + `pen-setup` units, docker/ssh unit drop-ins, explicit `native.cgroupdriver=systemd`; `kind-node/` thin image; CI probes; docs)
- Decide: bake kind/kubectl into the pen image or document the two-curl install; document that in-pen `docker build`/`docker commit` is broken by the virtiofs rootfs (no xattr) and images must be built outside and loaded in

## AC

- [ ] The pen boots with systemd as PID 1; `pen-setup` oneshot, docker, and ssh are active services; the dev user is created from the `DEV_USER` container env
- [ ] The pod-container cgroup stays a delegable domain (not `domain threaded`) after docker and kind run; a `--memory`-limited container gets its limit in its scope
- [ ] `KIND_EXPERIMENTAL_CONTAINERD_SNAPSHOTTER=native kind create cluster --image pipen-kind-node:<tag>` completes in a pen; the node reaches Ready and a smoke pod runs
- [ ] SSH key login works with a rotated Secret (new key in, old key out); existing pens recover via new image + pod recreation, no guest rebuild

## Related tickets

- caktus/hosted-llm#52
