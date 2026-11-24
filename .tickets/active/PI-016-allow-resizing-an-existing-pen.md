---
id: PI-016
github_issue: 19
github_url: https://github.com/caktus/pipen/issues/19
last_synced: "2026-11-24T04:39:19.841509+00:00"
synced_hash: e76942c1409f1ed45089d2ede874f61e6cd6ea9a24ed32e77caff06fc314367f
title: Allow resizing an existing pen
assignee: "@me"
labels:
  - phase/draft
context:
  relates_to: []
---

As a developer, I want to resize an existing pen, so that I can match its CPU, memory and home size to my workload without rebuilding the pen.

Today the operator is create-or-leave: it never changes an existing pod or PVC, so the only way to change a pen's size is `pipen down` + `pipen up`, which tears the pod down and brings a fresh one up.

### To Do

- Verify the production Mayastor storage class sets `allowVolumeExpansion: true` (the EKS context is not reachable from the dev box)
- Add a `pipen resize` command and make the reconciler converge a changed spec

## AC

- [ ] `pipen resize <name> --cpu 8 --memory 16Gi --size 20Gi` updates the Pen; the pod comes back with the new CPU and memory, the home PVC grows to 20Gi, and the pen is Ready again with its home data intact
- [ ] Changing only one of the three fields leaves the other two untouched
- [ ] A home-size shrink is refused with a message that says home size can only grow

<!-- tickets:research -->

All findings verified live on 2026-11-22 on the local kind cluster `harmony-local-cluster` (k8s v1.32.2, kind v0.27.0). No image registry is reachable from the dev box, so no kind node image newer than v1.32.2 is available, and the production EKS context has no aws credentials on this machine.

- The reconciler is create-or-leave: `ensurePVC`, `ensureService` and `ensurePod` return immediately when the object exists, so editing `spec.cpu`/`spec.memory`/`spec.homeSize` on a Pen today changes nothing (`operator/internal/pen/reconcile.go`, package doc + the three ensure functions).
- In-place pod resize is not usable: patching a running pod's resources on v1.32 is rejected by the API server ("pod updates may not change fields other than image..."). In-place pod vertical scaling (KEP-1287) is an alpha feature gate in k8s 1.33 (CI's kind 0.27 runs 1.33, `.github/workflows/verify.yml`) and on-by-default only from 1.35. The production EKS version is unverified.
- A kata pen is a VM: the VM is sized at creation, so even on a cluster that supports in-place container resize, the pen's VM would not grow. No kata runtime is available on kind, so this could not be tested.
- Pod recreation keeps home data (live test in a scratch namespace): a marker file written into a 2Gi PVC survived deleting the consuming pod and recreating it with double the CPU and memory.
- PVC expansion has three states (live tests):
  - Storage class without `allowVolumeExpansion`: the API server rejects the patch — "only dynamically provisioned pvc can be resized and the storageclass that provisions the pvc must support resize" (the cluster's `standard`/rancher.io/local-path SC is in this state).
  - SC allows it but the provisioner does not implement expansion: the PVC stays Bound at the old size with only a "waiting for an external controller to expand this PVC" event — a silent stall the operator must detect.
  - The kind default SC used by CI (csi-hostpath-provisioner) sets `allowVolumeExpansion: true`, so the kind e2e can exercise a real expansion.
- RBAC gaps: the dev-namespace operator Role already grants `pods:delete` but no `patch`/`update` on `persistentvolumeclaims` (`charts/dev-namespace/templates/operator-role.yaml`); the operator ClusterRole has no `storageclasses:get` (`charts/pipen-operator/templates/clusterrole.yaml`).
- No CRD change needed: `PenSpec` already carries `cpu`, `memory` and `homeSize` (`operator/api/v1alpha1/pen_types.go`).
- CLI: `Api.request` supports PUT and `cmd_up`'s poll loop is reusable for a resize command (`cli/pipen.py`).

<!-- tickets:approaches -->

- A. In-place pod vertical scaling: patch the running pod's resources where the cluster supports it. Rejected: absent or alpha-gated on every k8s version we can verify; a kata VM is sized at creation, so it would not resize the pen's VM; it would add a second divergent code path for a feature most clusters do not offer.
- B. Converge by recreation (chosen): the reconciler compares the existing pod's resources with the spec; on a mismatch it deletes the pod and the existing recreate-on-missing path builds a fresh one with the new size. Home data is on the PVC and survives (verified live). Cost: one pod-boot of downtime, announced in Pen status.
- C. CLI-side down+up that keeps the PVC: rejected — it races the operator, bypasses Pen status, and gives no stuck-detection for the PVC grow.

<!-- tickets:plan -->

1. Reconciler (`operator/internal/pen/reconcile.go`)
   - `ensurePod`: if the pod exists and its env container's requests/limits differ from the desired, delete the pod (graceful) and report `Creating` with a message (for example "recreating pod for new resources"). The existing recreate-on-missing path then builds it with the new spec.
   - `ensurePVC`: if the PVC exists and the desired homeSize differs:
     - desired smaller than current -> fail with "home size can only grow"
     - desired larger: read the storage class; if `allowVolumeExpansion` is false -> fail with a message naming the storage class. Otherwise patch `spec.resources.requests.storage` and watch `pvc.status.capacity`; if it does not reach the requested size within a timeout (provisioners can stall silently), fail with a message.
   - Status: reuse `PhaseCreating` with a message while a resize runs (open point: a dedicated `Resizing` phase would need a CRD + printcolumn change).
2. RBAC and charts
   - `charts/dev-namespace/templates/operator-role.yaml`: add `patch`, `update` to the `persistentvolumeclaims` rule.
   - `charts/pipen-operator/templates/clusterrole.yaml`: add `storageclasses` `get` (storage.k8s.io).
   - Bump both chart versions with an upgrade note.
3. CLI (`cli/pipen.py`)
   - New `pipen resize <name> [--cpu] [--memory] [--size]`: GET the pen, merge only the provided fields into the spec, PUT, then wait with the same poll loop as `cmd_up`.
4. Tests
   - Reconcile unit tests: no-op when the spec is unchanged; pod recreated on a cpu/memory change; PVC grow patches the claim; shrink refused; SC without `allowVolumeExpansion` refused; a stalled expansion times out to `Failed`.
   - CLI tests: `resize` merges only the provided fields; a missing pen errors.
   - kind e2e (`verify.yml`): after the pen is Ready at 100m/128Mi/1Gi, resize it to 200m/256Mi/2Gi and assert the pod's resources, the PVC capacity, and Ready again. The kind default SC supports expansion.
5. Pre-check before rolling out to production: the Mayastor storage class must set `allowVolumeExpansion: true` (`kubectl get sc mayastor-2` on the real cluster).
