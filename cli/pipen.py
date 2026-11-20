#!/usr/bin/env python3
"""pipen — per-developer CLI for herdr kata dev environments.

Stdlib only. Reads ~/.config/pipen/config.yaml (written by the admin
bootstrap, see README.md) and talks to the cluster API at api_base
with the namespace-scoped SA token. The CLI manages only the Pen CR
(think of it as a ticket the cluster operator acts on): `up` creates
one Pen and the cluster-wide pipen operator brings the PVC, kata pod
and tailnet Service up, reporting the result on Pen status. The dev
token can no longer create raw (privileged) pods.

Verbs:
  up <name>    Create a Pen (spec: image/homeSize/cpu/memory) and wait
               for status ready; print the ssh line from the
               operator-assigned tailnet hostname. The default image
               is the operator's config default; pin one with --image.
  ssh <name>   Resolve the hostname from Pen status and ssh in.
  down <name>  Delete the Pen. The operator's garbage collection removes
               the pod and Service; the home PVC and its data remain
               (admin wipe: kubectl -n <ns> delete pvc <name>-home).
  ls           List Pens in the dev namespace (phase/node/hostname).
  logs <name>  Stream the env pod logs.

Env names are force-prefixed with your developer/namespace so the tailnet
hostnames are collision-proof across devs: `pipen up test-smoke` as tobias
addresses pen `tobias-test-smoke`; a name that already starts with `<dev>-`
passes through unchanged, so `pipen down test-smoke` and
`pipen down tobias-test-smoke` address the same pen. A name starting with
someone else's prefix gets your prefix too (tobias-bob-foo) — intended,
no special-casing.

The Tailscale operator may suffix the assigned MagicDNS hostname (e.g.
<name> -> <name>-1); the operator copies the assigned name into
status.hostname, so this CLI always reads that — never the requested
name.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.request

NAME_RE = re.compile(r"^[a-z0-9]([a-z0-9-]*[a-z0-9])?$")
PENS_GROUP = "pens.pipen.dev/v1alpha1"
POLL_SECONDS = 5


def die(msg: str, code: int = 1):
    print(f"pipen: {msg}", file=sys.stderr)
    sys.exit(code)


def config_path() -> str:
    return os.environ.get(
        "PIPEN_CONFIG", os.path.expanduser("~/.config/pipen/config.yaml")
    )


def load_config() -> dict:
    """Parse the flat `key: value` config file (the bootstrap play writes a
    fixed shape; a YAML parser is out of scope for a stdlib-only CLI)."""
    path = config_path()
    try:
        with open(path, encoding="utf-8") as f:
            lines = f.read().splitlines()
    except OSError as e:
        die(f"cannot read {path}: {e} — copy the CLI config the admin gave you there")
    cfg = {}
    for line in lines:
        line = line.strip()
        if not line or line.startswith("#") or ":" not in line:
            continue
        key, _, value = line.partition(":")
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        cfg[key.strip()] = value
    for required in ("api_base", "namespace", "dev_user", "token"):
        if not cfg.get(required):
            die(f"{path} is missing {required!r}")
    return cfg


def _as_json(text: str):
    try:
        return json.loads(text)
    except (ValueError, TypeError):
        return text


class Api:
    """Minimal Kubernetes API client (urllib + SA bearer token).

    TLS certificate verification is disabled for this hop: the kube API
    server cert has no tailnet SAN, and auth is the namespace-scoped SA
    token on a private tailnet (documented posture, README.md)."""

    def __init__(self, cfg: dict):
        self.base = cfg["api_base"].rstrip("/")
        self.token = cfg["token"]
        self.ctx = ssl.create_default_context()
        self.ctx.check_hostname = False
        self.ctx.verify_mode = ssl.CERT_NONE

    def request(self, method: str, path: str, body=None, stream: bool = False):
        data = None
        headers = {
            "Authorization": f"Bearer {self.token}",
            "Accept": "application/json",
        }
        if body is not None:
            data = json.dumps(body).encode()
            headers["Content-Type"] = "application/json"
        req = urllib.request.Request(
            self.base + path, data=data, method=method, headers=headers
        )
        try:
            resp = urllib.request.urlopen(req, context=self.ctx, timeout=60)
        except urllib.error.HTTPError as e:
            return e.code, _as_json(e.read().decode(errors="replace"))
        except urllib.error.URLError as e:
            die(f"cannot reach {self.base}: {e.reason}")
        if stream:
            return 200, resp
        return resp.status, _as_json(resp.read().decode())

    def ns_path(self, ns: str, suffix: str) -> str:
        return f"/api/v1/namespaces/{ns}{suffix}"

    def pens_path(self, ns: str, name: str = "") -> str:
        base = f"/apis/{PENS_GROUP}/namespaces/{ns}/pens"
        return base if not name else f"{base}/{name}"


def validate_name(name: str) -> str:
    if not NAME_RE.match(name) or len(name) > 63:
        die(
            f"invalid env name {name!r} (RFC 1123: lowercase alphanumerics and '-', max 63)"
        )
    return name


def resolve_env(name: str, dev: str) -> str:
    """Force-prefix env names with the developer/namespace so tailnet
    hostnames are collision-proof across devs. A name that already starts
    with `<dev>-` passes through unchanged; a name starting with someone
    ELSE's prefix gets the dev prefix too (tobias-bob-foo) — intended,
    no special-casing."""
    prefix = f"{dev}-"
    if not name.startswith(prefix):
        resolved = prefix + name
        print(
            f"pipen: env {name} -> {resolved} (names are force-prefixed with your namespace)"
        )
        return resolved
    return name


def pen_spec(image: str, size: str, cpu: str, memory: str) -> dict:
    """Pen spec body; an empty image leaves the operator's default."""
    spec = {"homeSize": size, "cpu": cpu, "memory": memory}
    if image:
        spec["image"] = image
    return spec


def pen_body(name: str, namespace: str, spec: dict) -> dict:
    return {
        "apiVersion": PENS_GROUP,
        "kind": "Pen",
        "metadata": {"name": name, "namespace": namespace},
        "spec": spec,
    }


def pen_status_fields(body: dict) -> dict:
    status = body.get("status", {})
    return {
        "phase": status.get("phase", "Unknown"),
        "ready": bool(status.get("ready")),
        "hostname": status.get("hostname") or "",
        "node": status.get("node") or "",
        "message": status.get("message") or "",
    }


def get_pen(api: Api, ns: str, env: str) -> "tuple[int, dict]":
    status, body = api.request("GET", api.pens_path(ns, env))
    if status == 404:
        die(f"pen {env} not found (is the env up? `pipen up {env}`)")
    if status != 200:
        die(f"cannot read pen {env}: HTTP {status} {body}")
    return status, body


def cmd_up(args, cfg, api) -> int:
    ns, dev = cfg["namespace"], cfg["dev_user"]
    env = validate_name(resolve_env(args.name, dev))

    status, body = api.request(
        "POST",
        api.pens_path(ns),
        pen_body(env, ns, pen_spec(args.image, args.size, args.cpu, args.memory)),
    )
    if status in (200, 201):
        print(f"pipen: created pen {env} (operator brings the env up)")
    elif status == 409:
        print(f"pipen: pen {env} already exists (pipen down {env} + up to rebuild)")
    else:
        die(f"pen create failed: HTTP {status} {body}")

    deadline = time.time() + args.timeout
    last = None
    while time.time() < deadline:
        _, body = get_pen(api, ns, env)
        f = pen_status_fields(body)
        if f["ready"]:
            print()
            print(f"pipen: ready — ssh {dev}@{f['hostname']}")
            return 0
        if f["phase"] == "Failed":
            die(f"pen {env} failed: {f['message']}")
        info = f["phase"] + (f": {f['message']}" if f["message"] else "")
        if info != last:
            print(f"pipen: {env} {info} …", flush=True)
            last = info
        time.sleep(POLL_SECONDS)
    die(f"pen {env} not ready within {args.timeout}s — `pipen ls` shows status")
    return 0  # unreachable


def cmd_ssh(args, cfg, api) -> int:
    ns, dev = cfg["namespace"], cfg["dev_user"]
    env = validate_name(resolve_env(args.name, dev))
    _, body = get_pen(api, ns, env)
    f = pen_status_fields(body)
    if not f["hostname"]:
        die(f"pen {env} has no tailnet hostname yet (phase {f['phase']})")
    subprocess.run(["ssh", f"{dev}@{f['hostname']}"] + args.ssh_args)
    return 0


def cmd_down(args, cfg, api) -> int:
    ns, dev = cfg["namespace"], cfg["dev_user"]
    env = validate_name(resolve_env(args.name, dev))
    status, body = api.request("DELETE", api.pens_path(ns, env))
    # 404 = already gone (an external delete, or a second `down`)
    if status not in (200, 202, 404):
        die(f"pen delete failed: HTTP {status} {body}")
    print(f"pipen: env {env} down. PVC {env}-home (and its data) remains in ns {ns}.")
    print(f"pipen: admin data wipe: kubectl -n {ns} delete pvc {env}-home")
    return 0


def cmd_ls(args, cfg, api) -> int:
    ns = cfg["namespace"]
    status, body = api.request("GET", api.pens_path(ns))
    if status != 200:
        die(f"cannot list pens: HTTP {status} {body}")
    items = body.get("items", [])
    if not items:
        print("(no herdr envs)")
        return 0
    print(f"{'ENV':<24} {'PHASE':<10} {'NODE':<16} HOSTNAME")
    for pen in items:
        env = pen["metadata"]["name"]
        f = pen_status_fields(pen)
        print(
            f"{env:<24} {f['phase']:<10} {f['node'] or '-':<16} {f['hostname'] or '-'}"
        )
    return 0


def cmd_logs(args, cfg, api) -> int:
    ns, dev = cfg["namespace"], cfg["dev_user"]
    env = validate_name(resolve_env(args.name, dev))
    status, resp = api.request(
        "GET",
        api.ns_path(ns, f"/pods/env-{env}/log?follow=true&tailLines=-1"),
        stream=True,
    )
    if status != 200:
        die(f"cannot read logs for env-{env}: HTTP {status} {resp}")
    shutil.copyfileobj(resp, sys.stdout)
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(prog="pipen", description=__doc__.splitlines()[0])
    sub = parser.add_subparsers(dest="cmd", required=True)

    p_up = sub.add_parser(
        "up", help="bring up env <name> (creates one Pen; the operator does the rest)"
    )
    p_up.add_argument("name")
    p_up.add_argument(
        "--image",
        default="",
        help="full image reference (default: the operator's configured image, ghcr.io/caktus/pipen:latest)",
    )
    p_up.add_argument("--size", default="10Gi", help="home PVC size (default: 10Gi)")
    p_up.add_argument("--cpu", default="4", help="cpu request/limit (default: 4)")
    p_up.add_argument(
        "--memory", default="8Gi", help="memory request/limit (default: 8Gi)"
    )
    p_up.add_argument(
        "--timeout",
        type=int,
        default=1800,
        help="pen-ready timeout in seconds (default: 1800)",
    )

    p_ssh = sub.add_parser("ssh", help="ssh into env <name> (hostname from Pen status)")
    p_ssh.add_argument("name")
    p_ssh.add_argument(
        "ssh_args", nargs=argparse.REMAINDER, help="arguments passed to ssh"
    )

    p_down = sub.add_parser("down", help="tear down env <name> (keeps the PVC + data)")
    p_down.add_argument("name")

    sub.add_parser("ls", help="list envs in the dev namespace")

    p_logs = sub.add_parser("logs", help="stream env <name> pod logs")
    p_logs.add_argument("name")

    args = parser.parse_args()
    cfg = load_config()
    api = Api(cfg)
    return {
        "up": cmd_up,
        "ssh": cmd_ssh,
        "down": cmd_down,
        "ls": cmd_ls,
        "logs": cmd_logs,
    }[args.cmd](args, cfg, api)


if __name__ == "__main__":
    sys.exit(main())
