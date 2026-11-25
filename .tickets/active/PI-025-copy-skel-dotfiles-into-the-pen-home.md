---
id: PI-025
github_issue: 25
github_url: https://github.com/caktus/pipen/issues/25
last_synced: "2026-11-25T00:52:37.742033+00:00"
synced_hash: 9cad8d4383f692d46b5cb99e60aa9e32ebf913fa6fba2fcaa84c4b9ff0077672
title: Copy skel dotfiles into the pen home
type: Bug
assignee: "@me"
labels:
  - phase/in-progress
context:
  relates_to: []
---

As a pen developer, I want the standard dotfiles from /etc/skel in my home
directory, so that my shell behaves like a fresh VM.

The pen bootstrap creates the dev user with `useradd -M`, which skips the
/etc/skel copy, and it seeds no .bashrc or .profile of its own. The direnv
hook appends to a .bashrc that was never created, and with no .profile an
ssh login shell never sources .bashrc at all.

### To Do

- Reproduce in a live pen: inspect the dev home dotfiles and the login
  shell behavior
- Seed the /etc/skel dotfiles into the dev home at bootstrap, without
  overwriting files the user already has

## AC

- [ ] A new pen's dev home carries .bashrc, .profile and .bash_logout from
      /etc/skel, owned by the dev user
- [ ] An ssh login shell in a new pen sources .bashrc (the skel .profile
      does)
- [ ] A user's existing home files are not overwritten on later boots
