---
id: PI-025
github_issue: 25
github_url: https://github.com/caktus/pipen/issues/25
last_synced: "2026-11-25T01:34:39.614577+00:00"
synced_hash: 9490c49df6e62513e2ed9f3bd6bf93e76fc4f22ba2a6ca8fbfbe0bb237a78c3b
title: Copy skel dotfiles into the pen home
type: Bug
assignee: "@me"
labels:
  - phase/done
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
- Re-pin the operator chart default image tag (the PI-017 pin) to the new
  date tag once this image publishes, so new pens get the fixed entrypoint

## AC

- [x] A new pen's dev home carries .bashrc, .profile and .bash_logout from
      /etc/skel, owned by the dev user
- [x] An ssh login shell in a new pen sources .bashrc (the skel .profile
      does)
- [x] A user's existing home files are not overwritten on later boots

<!-- tickets:research -->

Reproduced at container level (no kind): the image's real entrypoint run as root with DEV_USER=dev and a fake projected authorized_keys creates a dev home with no .profile and a 27-byte .bashrc containing only the direnv line, against an 117-line /etc/skel/.bashrc. A login-shell probe shows the loss: the direnv hook is absent on the login path (no .profile to source .bashrc), HISTSIZE stays 500 instead of the skel 1000, and ~/bin and ~/.local/bin stay off PATH. A control user created with skel shows all three restored with the same direnv line.

Root cause: devenv/entrypoint.sh creates the user with useradd -M (line 46). -M is deliberate (the home is a mounted PVC; the entrypoint creates it with install -d -m 700), but -M also skips the /etc/skel copy, and no other code path seeds dotfiles. PS1 still works via the system /etc/bash.bashrc; the losses are the skel bashrc content, .profile, and the login-shell sourcing chain.

Full evidence with raw commands and outputs: .superpowers/sdd/pi-025-plan/reproduction.md in the branch worktree (raw logs on the host under /tmp/pi025/).

<!-- tickets:approaches -->

Controller rulings (2026-11-24):

- Fix in the entrypoint, not the image: seed the /etc/skel dotfiles into the dev home in pen-setup, right after the home is created and before the chown -R (so the existing chown picks up ownership). Keep useradd -M as is.
- Copy only missing entries (no clobber): a file or dir the user already has is never touched. This makes the seed idempotent across boots and self-healing for existing pens: a pen created before the fix gains the missing dotfiles on its next boot with the new image.
- The direnv hook block stays where it is: with the skel .bashrc now present, the existing grep-and-append finds the file and appends once. The skel .profile then sources .bashrc on the login path, so the hook finally runs where ssh lands.
- Release is automatic: publish.yml builds the pen image on the merge to main and pushes a new date tag plus :latest when the layers change. The operator chart default pin (PI-017) keeps naming the old tag until the next chart re-pin; that is recorded in the ticket To Do.
- Pre-fix pens keep their machine-made one-line .bashrc (the no-clobber ruling never replaces a user-visible file). They gain .profile and .bash_logout on their next boot, so their login shell then sources .bashrc; they do not gain the skel HISTSIZE, aliases, or PATH additions until the developer copies /etc/skel/.bashrc over the old file. That is intended, not a bug.

<!-- tickets:plan -->

Task 1: seed the skel dotfiles in the entrypoint. In devenv/entrypoint.sh, between the install -d of the dev home and the chown -R, copy every entry of /etc/skel into the home, creating only entries that are missing (no clobber), handling dotfiles and dirs. The worker re-runs the container-level reproduction from the research doc and proves: a fresh home gains .bashrc, .profile and .bash_logout owned by the dev user; the login-shell probe now matches the skel control (direnv hook present on the login path, HISTSIZE 1000, PATH entries); a home with a pre-existing user-edited .bashrc keeps its content on a second run.

Task 2: the boot probe assertion. In .github/workflows/verify.yml, the e2e
pen runs the pause image and is not ssh-able, so the assertion rides the
image job's dev-image boot probes. A new probe step boots the dev image
and asserts over a real key ssh that the dev home carries the skel
dotfiles: .profile exists, .bashrc carries the skel header comment, and a
login shell sees the skel HISTSIZE (the .profile-to-.bashrc chain works).
