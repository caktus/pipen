---
id: PI-016
github_issue: 19
github_url: https://github.com/caktus/pipen/issues/19
last_synced: "2026-11-24T18:18:10.133644+00:00"
synced_hash: e76942c1409f1ed45089d2ede874f61e6cd6ea9a24ed32e77caff06fc314367f
title: Allow resizing an existing pen
assignee: "@me"
labels:
  - phase/review
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

All findings verified 2026-11-24 on main after PI-020 (pens now reconcile as agent-sandbox Sandbox resources named env-<pen>; the operator does not write pods).

- The reconciler converges the Sandbox spec: operatingMode, shutdownTime/TTL, and service. It does NOT converge the podTemplate: editing spec.cpu or spec.memory today changes nothing (operator/internal/pen/reconcile.go, ensureSandbox converge path).
- The agent-sandbox v1.0.12 controller builds a fresh pod from the stored podTemplate (the create path in reconcilePod). For an existing owned pod it patches metadata only (labels, annotations). A podTemplate change never restarts the running pod (controllers/sandbox_controller.go, reconcilePod; the PodCacheTransform doc comment states the pod spec is written from the template on the create path, never from the cached pod).
- The restart mechanism is the operatingMode cycle: the controller deletes the pod when the mode is Suspended, and creates a fresh pod from the current podTemplate on the next pass once the pod is gone and the mode is Running.
- The Sandbox Suspended condition distinguishes the states the operator needs, so no pod reads are required: Status=True reason=SuspendedPodTerminated (pod gone, safe to flip back to Running), reason=SuspendedPodTerminating (pod still terminating, not safe yet), reason=SuspendedPodNotOwned (refused, stuck; this is the pre-upgrade migration case documented in the chart READMEs).
- The current resume flip (resumed := !pen.Spec.Paused && mode==Suspended) fires on the first reconcile after a mode flip, before the controller has necessarily deleted the pod. A resize that sets mode=Suspended and then flips back on the next pass can race: the controller sees Running with the old pod still present and keeps it, so the new size is never applied. The flip must be gated on the pod being gone (Suspended condition True with reason SuspendedPodTerminated). For the user pause/resume flow this gate changes nothing observable: pipen pause already polls until the pod is terminated.
- In-place pod vertical scaling (KEP-1287) remains unusable: alpha-gated on k8s 1.33, on by default only from 1.35, production EKS version unverified. Approach A stays rejected.
- PVC expansion is unchanged from the earlier research: the storage class must set allowVolumeExpansion; a provisioner that does not implement expansion stalls silently, so the operator must time out. The kind default SC (csi-hostpath-provisioner) allows expansion, so the e2e can exercise a real grow.
- RBAC after PI-020: the dev-namespace operator Role grants PVC verbs create, get, list, watch (no patch or update) and pods get, list, watch only. The pipen-operator ClusterRole is read-only pens and namespace gets; no storageclasses rule exists anywhere.
- PenSpec already carries cpu, memory and homeSize; no CRD field change is needed. A dedicated Resizing phase would need a CRD, printcolumn, and CLI changes; the earlier plan left it open. This plan skips it: the pen shows Suspended during the restart cycle (the existing mapping, truthful) and a status message while the home grows.
- The operator has no pod write verbs by design (the PI-020 RBAC swap and the chart README migration note both state this). The operator cannot delete a pod to force a restart; the suspend cycle is the only mechanism that stays inside the Sandbox control plane.

<!-- tickets:approaches -->

- A. In-place pod vertical scaling: patch the running pod's resources where the cluster supports it. Rejected: alpha-gated or absent on every k8s version we can verify; the agent-sandbox controller owns the pod and the operator must not write pods, so there is no clean patch path even where the feature gate exists.
- B. Converge by recreation (chosen, re-mapped to the Sandbox reconciler): the operator converges the Sandbox podTemplate's env container resources to the Pen spec, and a suspend/resume cycle restarts the pod through the sandbox controller. The resume flip is gated on the Suspended condition reporting the pod terminated, so the fresh pod is always built from the updated template. Home data is on the PVC and survives (verified live in the earlier research). Cost: one pod-boot of downtime, visible as the pen passing through Suspended.
- C. CLI-side down+up that keeps the PVC: rejected — it races the operator, bypasses Pen status, and gives no stuck-detection for the PVC grow.
- D. Operator deletes the pod directly on a resource mismatch: rejected — it re-introduces pod write verbs, contradicting the PI-020 RBAC design (the operator does not write pods) and the documented upgrade migration. The suspend cycle achieves the same recreation without pod access.

<!-- tickets:plan -->

1. Reconciler: resource convergence and the restart cycle (operator/internal/pen/reconcile.go)
   - ensureSandbox converge path: compare the stored env container requests/limits with the desired cpu/memory (orDefault with the operator defaults). On a mismatch:
     - patch the podTemplate container resources in the pending update
     - if the pen is not paused, also set operatingMode=Suspended in the same update (this is the restart trigger; a paused pen just gets the template update, and the fresh pod picks the size up on the next resume)
   - Gate the resume flip: resumed becomes !pen.Spec.Paused && mode==Suspended && the Suspended condition is Status=True with reason=SuspendedPodTerminated. The fresh-TTL stamp still rides on the same one-shot transition. Update the suspended-sandbox test helpers to carry the condition; add a test that a Terminating-condition sandbox does not flip and does not re-stamp.
   - While the pen is paused or the cycle is running, the existing Suspended/Ready status mapping applies; no new phase.
2. Reconciler: home PVC expansion (same file)
   - ensurePVC: desired smaller than current -> fail "home size can only grow".
   - desired larger: read the StorageClass named by the PVC; if AllowVolumeExpansion is not true -> fail naming the storage class. Otherwise patch spec.resources.requests.storage to the desired size and stamp an annotation (pipen.dev/home-expand-at, RFC3339) recording when the grow started.
   - Each pass while status.capacity is below the desired size: if the annotation is older than 5 minutes -> fail "home expansion stalled" (the timeout is a constant, not user-configurable). When capacity reaches the desired size, delete the annotation.
   - Status message "growing home to <size>" while the grow is in flight (phase stays Ready; the pod still runs).
3. RBAC and charts
   - charts/dev-namespace/templates/operator-role.yaml: add patch and update to the persistentvolumeclaims rule.
   - charts/pipen-operator/templates/clusterrole.yaml: add storageclasses get (storage.k8s.io).
   - Bump both chart versions with an upgrade note (the note states pens keep their size on upgrade; nothing changes at install time).
4. CLI (cli/pipen.py)
   - New pipen resize <name> [--cpu C] [--memory M] [--size S]: GET the pen, merge only the provided fields into the spec, refuse a home shrink client-side with "home size can only grow", PUT, then wait with the same poll loop as cmd_up until the pen is Ready again.
5. Tests
   - Reconcile unit tests (fixed clock where time matters): no-op when the spec is unchanged (no Update, ResourceVersion stable); a cpu/memory mismatch patches the template and sets Suspended; a paused pen gets the template without a mode change; a Terminating-condition sandbox does not flip or re-stamp; a Terminated-condition sandbox flips and stamps once.
   - PVC: grow patches the claim and stamps the annotation; shrink fails with the grow-only message; a storage class without allowVolumeExpansion fails naming the class; a stalled expansion (annotation past 5 minutes, capacity unchanged) fails; a completed grow clears the annotation.
   - CLI: resize merges only the provided fields; a missing pen errors; a shrink is refused before any PUT.
   - kind e2e (verify.yml): after the pen is Ready, write a marker file into the pod (kubectl exec touch /home/resize-marker), resize it to a larger cpu/memory/home, and assert the pod's resources, the PVC capacity, Ready again, and the marker file still present.
6. Docs
   - Root README: a resize bullet in the commands section and a short "Resizing a pen" note: the pod restarts with the new size (passes through Suspended, gets a fresh TTL), the home grows in place, a shrink is refused, data is kept.
   - Chart READMEs: the new RBAC verbs and chart versions.
7. Pre-check before production rollout (unchanged, still blocked): the Mayastor storage class on EKS must set allowVolumeExpansion: true (kubectl get sc on the real cluster; no aws credentials on the dev box).
