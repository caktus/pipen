#!/usr/bin/env bash
# herdr env bootstrap (root, runs as pen-setup.service — Type=oneshot,
# RemainAfterExit=yes — ordered before docker.service and ssh.service by
# their drop-ins). systemd is PID 1 in the pen: this script used to be
# the container entrypoint that tail-called dockerd (background) and
# sshd (foreground); those are now the stock units.
#
# Projects the per-env inputs, then leaves. docker.service and
# ssh.service both carry After=pen-setup.service, so every root write to
# /home below lands before either daemon starts.
#   /var/lib/herdr/secret/authorized_keys  — dev Secret `herdr-dev-<namespace>` (bootstrap)
#   /var/lib/herdr/config/{models.json,settings.json,litellm_key}
#   /var/lib/herdr/credentials/*, user Secret the pen names by
#   annotation (all keys, optional)
# (the k8s mounts land at /run/herdr; herdr-boot copies them to
# /var/lib/herdr before systemd remounts /run — see herdr-boot)
#                                       — env ConfigMap built from
#                                         `herdr-dev-<namespace>` (bootstrap)
# The config files are each optional: a pen whose ConfigMap carries none of
# them still boots, and the developer signs in to their own model-provider
# account inside.
# Env: DEV_USER (required), TAILNET_HOSTNAME (informational). The k8s
# container env is the PID 1 (systemd) process environment, which systemd
# does not pass to services; pen-env-import.service copies it into the
# manager environment before this unit runs (see pen-setup.service).
#
# /home is the per-env mayastor-2 PVC: the dev user's home lives there and
# survives pod recreation. Everything else (incl. dockerd's /var/lib/docker
# data-root) is the ephemeral kata rootfs — see README.md.
set -euo pipefail

: "${DEV_USER:?DEV_USER env is required}"
DEV_HOME="/home/${DEV_USER}"

# --- dev user (uid 1000) + persistent home on the /home PVC -----------------
# -M (no skel copy): /home is the mounted volume, so create the home dir
# explicitly instead of letting useradd guess.
# (install -d, not mkdir -p -m: with -p the mode applies to the deepest dir
# only — the parent dirs of a missing home would get 0755.)
if ! id -u "${DEV_USER}" >/dev/null 2>&1; then
    # No pinned UID: the ubuntu:26.04 base image already ships a default
    # user at UID 1000, and useradd would fail with "UID 1000 is not
    # unique". Let useradd pick a free UID; the rest of the entrypoint
    # chowns by user name, so the home ends up owned by whatever UID the
    # user got.
    useradd -d "${DEV_HOME}" -M -s /bin/bash "${DEV_USER}"
fi
# docker group membership: dockerd runs as root in the guest and its socket
# is group-docker, so the dev user gets `docker` without sudo (dev-VM parity)
usermod -aG docker "${DEV_USER}"
# journal read without sudo: the journal files are root:systemd-journal
usermod -aG systemd-journal "${DEV_USER}"
install -d -m 700 "${DEV_HOME}"
# Seed the /etc/skel dotfiles into the home. useradd -M skips the skel copy
# because /home is the mounted volume and the home dir is created by hand
# above. No clobber: an entry the user already has is never touched, so an
# edited file survives every boot and a pen created before this fix gains
# the missing dotfiles on its next boot. The chown -R below picks up
# ownership of the copied entries.
# nullglob keeps a missing /etc/skel a no-op; dotglob keeps the dotfiles
shopt -s nullglob dotglob
for src in /etc/skel/*; do
    dest="${DEV_HOME}/${src##*/}"
    # no clobber: an existing entry (file, dir, or a dangling link) is the
    # user's and stays untouched
    if [ -e "${dest}" ] || [ -L "${dest}" ]; then
        continue
    fi
    cp -a "${src}" "${dest}"
done
shopt -u nullglob dotglob
chown -R "${DEV_USER}:${DEV_USER}" "${DEV_HOME}"

# Note: /opt/pi (pi's npm prefix) is world-writable in the image (see
# Dockerfile) so the dev user can run `pi update` with no boot-time work —
# a per-boot `chown -R /opt/pi` takes 10+ minutes on the virtiofs rootfs
# and would block sshd.

# passwordless sudo, matching the dev-VM convention
printf '%s ALL=(ALL) NOPASSWD:ALL\n' "${DEV_USER}" > /etc/sudoers.d/90-herdr
chmod 0440 /etc/sudoers.d/90-herdr

# --- ssh (key-only): authorized_keys from the projected Secret --------------
install -d -m 700 -o "${DEV_USER}" -g "${DEV_USER}" "${DEV_HOME}/.ssh"
install -m 600 -o "${DEV_USER}" -g "${DEV_USER}" \
    /var/lib/herdr/secret/authorized_keys "${DEV_HOME}/.ssh/authorized_keys"

# --- pi.dev config from the projected ConfigMap -----------------------------
# Each key is optional, and a zero-byte entry reads as absent: a pen whose
# ConfigMap carries none of them still boots, and the developer signs in to
# their own model-provider account inside. Placed files are 0600 (models.json
# carries the model provider key) and leave a sha256 marker under
# /home/.herdr/projected. A later boot whose ConfigMap dropped the key
# removes the placed copy while it still matches the marker; a file the
# developer wrote or edited has a different hash (or no marker) and stays.
MARKER_DIR="/home/.herdr/projected"
install -d -m 700 "${MARKER_DIR}"
for name in models.json settings.json litellm_key; do
    src="/var/lib/herdr/config/${name}"
    marker="${MARKER_DIR}/${name}.sha256"
    if [ "${name}" = "litellm_key" ]; then
        dest="${DEV_HOME}/.litellm_key"   # same placement as the dev-VM flow
    else
        dest="${DEV_HOME}/.pi/agent/${name}"
    fi
    if [ -s "${src}" ]; then
        if [ "${name}" != "litellm_key" ]; then
            # the .pi/agent dir appears only when a pi config file lands
            install -d -m 700 -o "${DEV_USER}" -g "${DEV_USER}" "${DEV_HOME}/.pi/agent"
        fi
        install -m 600 -o "${DEV_USER}" -g "${DEV_USER}" "${src}" "${dest}"
        printf '%s\n' "$(sha256sum "${src}" | cut -d' ' -f1)" > "${marker}"
        chown "${DEV_USER}:${DEV_USER}" "${marker}"
    elif [ -e "${marker}" ]; then
        # the key is absent or zero-byte: drop the placed copy only while
        # it still matches the marker (the developer never touched it)
        if [ -f "${dest}" ] && \
            [ "$(sha256sum "${dest}" | cut -d' ' -f1)" = "$(cat "${marker}")" ]; then
            rm -f "${dest}"
        fi
        rm -f "${marker}"
    fi
done

# --- credentials from the projected user Secret (all keys, as-is) ----------
# No name list here: every file in the dir lands in
# ${DEV_HOME}/.herdr/credentials/ under its own name (dir 0700, files 0600)
# and leaves a sha256 marker under ${DEV_HOME}/.herdr/projected-credentials.
# A key absent or zero-byte on a later boot drops the placed copy while it
# still matches the marker. A file the developer wrote or edited has a
# different hash (or no marker) and stays. A missing dir and empty files
# read as absent: no install, no marker. Dangling entries never reach
# /var/lib/herdr (herdr-boot skips them).
CRED_DIR="${DEV_HOME}/.herdr/credentials"
install -d -m 700 -o "${DEV_USER}" -g "${DEV_USER}" "${DEV_HOME}/.herdr"
install -d -m 700 -o "${DEV_USER}" -g "${DEV_USER}" "${CRED_DIR}"
CRED_MARKER_DIR="${DEV_HOME}/.herdr/projected-credentials"
install -d -m 700 "${CRED_MARKER_DIR}"
# dot-named keys (.env, .netrc) are legal Secret keys, so dotglob keeps the
# globs below seeing them, and nullglob keeps a missing dir a no-op
shopt -s nullglob dotglob
for src in /var/lib/herdr/credentials/*; do
    [ -f "${src}" ] || continue
    name="${src##*/}"
    dest="${CRED_DIR}/${name}"
    marker="${CRED_MARKER_DIR}/${name}.sha256"
    if [ -s "${src}" ]; then
        install -m 600 -o "${DEV_USER}" -g "${DEV_USER}" "${src}" "${dest}"
        printf '%s\n' "$(sha256sum "${src}" | cut -d' ' -f1)" > "${marker}"
        chown "${DEV_USER}:${DEV_USER}" "${marker}"
    fi
done
# drop-on-delete: a marker whose key is gone (or zero-byte) now removes the
# placed copy while it still matches the marker, then the marker
for marker in "${CRED_MARKER_DIR}"/*.sha256; do
    [ -f "${marker}" ] || continue
    name="${marker##*/}"
    name="${name%.sha256}"
    src="/var/lib/herdr/credentials/${name}"
    if [ ! -s "${src}" ]; then
        dest="${CRED_DIR}/${name}"
        if [ -f "${dest}" ] && \
            [ "$(sha256sum "${dest}" | cut -d' ' -f1)" = "$(cat "${marker}")" ]; then
            rm -f "${dest}"
        fi
        rm -f "${marker}"
    fi
done
shopt -u nullglob dotglob

# direnv hook in the dev user's .bashrc (dev-VM convention; idempotent).
# Plain shell — not `bash -c '…' -u HOME=…`: with -c the operands after the
# command string bind to $0/$1, so that form set no environment at all and
# the CWD-relative .bashrc landed in / on the ephemeral kata rootfs, read
# by no one. Absolute path + chown, written by root before the first login.
# shellcheck disable=SC2016 # intentional: the literal text IS the hook
if ! grep -qxF 'eval "$(direnv hook bash)"' "${DEV_HOME}/.bashrc" 2>/dev/null; then
    echo 'eval "$(direnv hook bash)"' >> "${DEV_HOME}/.bashrc"
fi
chown "${DEV_USER}:${DEV_USER}" "${DEV_HOME}/.bashrc"

# dockerd runs as docker.service (After=pen-setup.service); its data-root
# is /var/lib/docker on the ephemeral rootfs by design, and the cgroup
# driver is pinned in /etc/docker/daemon.json.

# --- per-env ssh host keys (stable across pod recreation) --------------------
# The image bakes one static host-key set (ssh-keygen -A, under /etc/ssh on
# the ephemeral kata rootfs), so every env presents the same key and
# host-key verification cannot tell envs apart. On first boot, generate a
# set for this env on the /home PVC (which survives pod recreation), then
# copy it onto the /etc/ssh/ssh_host_*_key paths before sshd starts. The
# base sshd_config leaves the HostKey lines commented out, so sshd loads
# exactly those paths. The env shows its own key from its first boot and
# keeps it for its whole life, so a client sees one known_hosts prompt per
# env, ever.
HOST_KEYS_DIR="/home/.herdr/ssh-host-keys"
install -d -m 700 "${HOST_KEYS_DIR}"
ensure_ssh_host_key() {
    # $1 is a key type (ed25519, ecdsa, rsa). Create the key on the PVC if it
    # is missing or if a half-written file fails the load test, re-derive its
    # .pub when it is missing or does not match, then copy both onto the
    # distro-default /etc/ssh path sshd reads.
    local key_type="$1"
    local host_key="${HOST_KEYS_DIR}/ssh_host_${key_type}_key"
    if [ ! -f "${host_key}" ] || ! ssh-keygen -y -f "${host_key}" >/dev/null 2>&1; then
        rm -f "${host_key}" "${host_key}.pub"
        ssh-keygen -q -t "${key_type}" -N '' -f "${host_key}"
    fi
    if [ ! -f "${host_key}.pub" ] || \
        ! cmp -s <(ssh-keygen -y -f "${host_key}") "${host_key}.pub"; then
        ssh-keygen -y -f "${host_key}" > "${host_key}.pub"
    fi
    install -m 600 "${host_key}" "/etc/ssh/ssh_host_${key_type}_key"
    install -m 644 "${host_key}.pub" "/etc/ssh/ssh_host_${key_type}_key.pub"
}
for key_type in ed25519 ecdsa rsa; do
    ensure_ssh_host_key "${key_type}"
done

# --- sshd (key-only, no root login): ssh.service reads this drop-in ---------
# Written before ssh.service starts (After=pen-setup.service), not at sshd
# exec time as the old entrypoint did.
cat > /etc/ssh/sshd_config.d/99-herdr.conf <<'EOF'
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
EOF
