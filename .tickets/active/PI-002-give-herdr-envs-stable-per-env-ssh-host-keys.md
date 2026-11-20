---
id: PI-002
github_issue: 2
github_url: https://github.com/caktus/pipen/issues/2
last_synced: "2026-11-20T15:37:52.224207+00:00"
synced_hash: 9f6a4cc505128d9066351149a90384ec9e933261b1bb3b9b33a7837de0fa8f5e
title: Give herdr envs stable per-env ssh host keys
type: Task
assignee: "@me"
labels:
  - phase/draft
context:
  relates_to:
    - PI-001
---

As a developer, I want each herdr env to have its own ssh host key that is stable across restarts, so that host-key verification distinguishes envs and I get one known_hosts prompt per env — instead of every env, on every boot, presenting the same image-baked key (ssh offers to confirm it "known by other names" for each new hostname).

The dev image runs `ssh-keygen -A` at build time, so all envs share one static host-key set. Beyond the prompt churn, host-key verification currently cannot tell envs apart: it only proves "this is _a_ herdr env."

### To Do

- Validate the per-env-key design against the current mechanism (baked keys, sshd HostKey paths, boot order in `devenv/entrypoint.sh`): key directory on the /home PVC, generated on first boot if absent, keys replacing the distro-default `/etc/ssh` paths before sshd starts
- Implement in the entrypoint (+ image rebuild) and document the one-time known_hosts transition for existing envs in README.md

## AC

- [ ] A fresh env presents a host key with a fingerprint unique to that env, and the same fingerprint after pod recreation (one prompt per env, ever)
- [ ] Existing envs pick up their own key on the next image rebuild with a single prompt, then stay stable
- [ ] sshd stays key-only with no root login; nothing else in the image changes

## Related tickets

- caktus/hosted-llm#39
