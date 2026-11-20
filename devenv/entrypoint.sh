#!/usr/bin/env bash
# herdr env entrypoint (PID 1, runs as root inside the kata guest).
#
# Projects the per-env inputs, then runs dockerd (background) and sshd
# (foreground, key-only):
#   /run/herdr/secret/authorized_keys  — dev Secret `herdr-dev-<namespace>` (bootstrap)
#   /run/herdr/config/{models.json,settings.json,litellm_key}
#                                       — env ConfigMap built from
#                                         `herdr-dev-<namespace>` (bootstrap)
# Env: DEV_USER (required), TAILNET_HOSTNAME (informational).
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
install -d -m 700 "${DEV_HOME}"
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
    /run/herdr/secret/authorized_keys "${DEV_HOME}/.ssh/authorized_keys"

# --- pi.dev config from the projected ConfigMap (0600: models.json carries
# --- the litellm key)
install -d -m 700 -o "${DEV_USER}" -g "${DEV_USER}" "${DEV_HOME}/.pi/agent"
install -m 600 -o "${DEV_USER}" -g "${DEV_USER}" \
    /run/herdr/config/models.json "${DEV_HOME}/.pi/agent/models.json"
install -m 600 -o "${DEV_USER}" -g "${DEV_USER}" \
    /run/herdr/config/settings.json "${DEV_HOME}/.pi/agent/settings.json"

# --- litellm key (0600), same placement as the dev-VM flow
install -m 600 -o "${DEV_USER}" -g "${DEV_USER}" \
    /run/herdr/config/litellm_key "${DEV_HOME}/.litellm_key"

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

# --- dockerd (background; data-root on the ephemeral rootfs by design) ------
dockerd > /var/log/dockerd.log 2>&1 &

# --- sshd (foreground, key-only, no root login) ------------------------------
install -d /run/sshd
cat > /etc/ssh/sshd_config.d/99-herdr.conf <<'EOF'
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
EOF

exec /usr/sbin/sshd -D -e
