#!/usr/bin/env bash
# herdr env entrypoint (PID 1, runs as root inside the kata guest).
#
# Projects the per-env inputs, then runs dockerd (background) and sshd
# (foreground, key-only):
#   /run/herdr/secret/authorized_keys  — dev Secret `herdr-dev-<namespace>` (bootstrap)
#   /run/herdr/config/{models.json,settings.json,litellm_key}
#                                       — env ConfigMap built from
#                                         `herdr-dev-<namespace>` (bootstrap)
# The config files are each optional: a pen whose ConfigMap carries none of
# them still boots, and the developer signs in to their own model-provider
# account inside.
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
    src="/run/herdr/config/${name}"
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

# --- per-env ssh host keys (stable across pod recreation) --------------------
# The image bakes one static host-key set (ssh-keygen -A, under /etc/ssh on the
# ephemeral kata rootfs), so every env presents the same key and host-key
# verification cannot tell envs apart. On first boot, generate a set for this
# env on the /home PVC (which survives pod recreation), then copy it onto the
# /etc/ssh/ssh_host_*_key paths before sshd starts. The base sshd_config leaves
# the HostKey lines commented out, so sshd loads exactly those paths. The env
# shows its own key from its first boot and keeps it for its whole life, so a
# client sees one known_hosts prompt per env, ever.
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

# --- sshd (foreground, key-only, no root login) ------------------------------
install -d /run/sshd
cat > /etc/ssh/sshd_config.d/99-herdr.conf <<'EOF'
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
EOF

exec /usr/sbin/sshd -D -e
