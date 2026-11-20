# herdr dev toolchain image — bakes the standard dev toolchain into
# ubuntu:26.04 so a kata env pod starts ready in seconds instead of a
# 3-6 min cloud-init-style install path.
#
# NO tailscale package — the Tailscale operator owns the tailnet side
# (per-Service sidecar proxy, L4 exposure).
FROM ubuntu:26.04

# Pipes in the RUN steps below must fail the build, not hide the first
# error (hadolint DL4006).
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ENV DEBIAN_FRONTEND=noninteractive

# dev_packages (git curl build-essential htop docker.io direnv npm unzip) +
# openssh-server (key-only ssh endpoint) + sudo (the entrypoint grants the
# dev user passwordless sudo). Unpinned on purpose: a dev toolchain image
# tracks distro current (hadolint DL3008 suppressed below).
# hadolint ignore=DL3008
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        git curl build-essential htop docker.io direnv npm unzip \
        openssh-server sudo ca-certificates \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# SSH host keys (defensive: slim container builds may skip the postinst)
RUN ssh-keygen -A

# uv, system-wide for all users (the dev-VM path installs it per-user into
# ~/.local; the env's dev user is created at container start, so it must be
# on the system PATH here).
RUN curl -LsSf https://astral.sh/uv/install.sh | env UV_INSTALL_DIR=/usr/local/bin sh

# fnm + Node 24 (the dev_runcmd steps), system-wide under /usr/local/fnm so
# the entrypoint-created dev user inherits them. The fnm install script
# ignores FNM_DIR (it hardwires ~/.local/share/fnm), so fetch the release
# binary directly — what the script does internally, at a fixed location.
# FNM_DIR and FNM_DATA_DIR pin where fnm puts the alias symlinks and the
# node installation store: both default to $HOME/.local/share/fnm, which at
# build time is /root and at runtime is the dev user's home — neither of
# which is shared (a build without FNM_DIR silently installs everything
# under /root). The default-alias binaries are symlinked into /usr/local/bin
# so they work in non-login (e.g. `ssh host 'cmd'`) shells, not just
# profile.d login shells; the final check fails the build if pi is not
# runnable.
#
# pi-coding-agent goes into its OWN npm prefix (/opt/pi) instead of the fnm
# alias bin: that alias tree is root-owned, and `pi update` (dev-user
# self-update) does an in-place npm install, which fails against a root-only
# tree ("install path is not writable"). The entrypoint chowns /opt/pi to
# the dev user on boot, so the self-update works; node/npm/npx stay on the
# fnm default alias.
ENV FNM_DIR=/usr/local/fnm
ENV FNM_DATA_DIR=/usr/local/fnm/data
# hadolint ignore=DL3016 # pi tracks upstream latest — that is the point of a dev image
RUN mkdir -p /usr/local/fnm \
    && curl -sL https://github.com/Schniz/fnm/releases/latest/download/fnm-linux.zip -o /tmp/fnm.zip \
    && unzip -o -q /tmp/fnm.zip -d /usr/local/fnm \
    && rm /tmp/fnm.zip \
    && eval "$(/usr/local/fnm/fnm env --shell bash)" \
    && /usr/local/fnm/fnm install 24 \
    && /usr/local/fnm/fnm default 24 \
    && npm install -g --prefix /opt/pi --ignore-scripts @earendil-works/pi-coding-agent \
    # The dev user's `pi update` runs npm reify against this prefix and must
    # DELETE the old version's tree — that needs write on every subdirectory,
    # i.e. the whole prefix must be user-writable. A boot-time chown -R is
    # correct but blocks sshd for 10+ min on the virtiofs rootfs (measured:
    # even `find /opt/pi` times out), so make the tree world-writable at
    # build time instead: single-user kata guest, the dev user already has
    # passwordless sudo, and the rootfs resets every boot (files npm creates
    # are user-owned from the update onwards).
    && chmod -R a+rwX /opt/pi \
    && npm cache clean --force \
    && ln -sf /usr/local/fnm/aliases/default/bin/node /usr/local/bin/node \
    && ln -sf /usr/local/fnm/aliases/default/bin/npm /usr/local/bin/npm \
    && ln -sf /usr/local/fnm/aliases/default/bin/npx /usr/local/bin/npx \
    && ln -sf /opt/pi/bin/pi /usr/local/bin/pi \
    && /usr/local/bin/pi --version

# PATH wiring for login shells (fnm env puts the default Node on PATH).
# Quoted heredoc: the profile.d file must carry the literal eval line.
RUN cat > /etc/profile.d/fnm.sh <<'EOF'
export FNM_DIR=/usr/local/fnm
eval "$(/usr/local/fnm/fnm env --shell bash)"
EOF

# hc (herdr core), operator-provided install line. The script honors no
# target-dir flag; it installs under $HOME (=/root at build time): the app
# bundle in /root/.local/share/herdr-app and the binaries (herdr-core, hc)
# in /root/.local/bin. The dev user must reach the core from the system
# PATH — but /root is 0700, so instead of symlinking across into /root,
# install herdr-core world-readable into /usr/local/bin and make `hc` a
# relative symlink next to it. The final check fails the build if hc is
# missing.
RUN curl -fsSL https://releases.herdr.dev/app/install.sh | sh \
    && test -x /root/.local/bin/herdr-core \
    && install -m 0755 /root/.local/bin/herdr-core /usr/local/bin/herdr-core \
    && ln -sf herdr-core /usr/local/bin/hc \
    && /usr/local/bin/hc --version

# dockerd storage: the kata guest's root (and thus /var/lib/docker) is
# virtiofs, which cannot host an overlayfs upperdir — the default
# overlayfs snapshotter fails every container start with
# "mount overlay ... invalid argument". The native snapshotter is
# directory-based (no mounts), so it works on virtiofs.
COPY docker-daemon.json /etc/docker/daemon.json
COPY entrypoint.sh /usr/local/bin/herdr-entrypoint
RUN chmod 0755 /usr/local/bin/herdr-entrypoint

ENTRYPOINT ["/usr/local/bin/herdr-entrypoint"]
