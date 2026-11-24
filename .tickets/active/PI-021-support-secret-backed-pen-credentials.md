---
id: PI-021
github_issue: 21
github_url: https://github.com/caktus/pipen/issues/21
last_synced: "2026-11-24T04:39:25.150985+00:00"
synced_hash: ea513ad490b07d0465118c41450d8a09311fb450115ac7e3c749d492b580d2e2
title: Support Secret-backed pen credentials
assignee: "@me"
labels:
  - phase/draft
context:
  relates_to: []
---

As a user, I want pens to take credentials from a Secret, so that a credential in the namespace is not held in a plain ConfigMap.

Today a value a user wants inside a pen, such as a model API key, is delivered through a plain ConfigMap, because the operator's reconcile contract expects that object shape. Any principal that can read ConfigMaps in the namespace can read the credential, and Secrets are the conventional object for credentials even where RBAC adds no per-object difference.

### To Do

- Explore how the operator could consume a Secret in place of or alongside the ConfigMap (for example an annotation that references a Secret)
- Confirm the API surface and RBAC a Secret-backed path needs

## AC

- [ ] A user can deliver a pen credential through a Secret, and the pen receives it
- [ ] The existing ConfigMap path still works for non-sensitive values
