#!/usr/bin/env python3
"""pipen — per-developer CLI for herdr kata dev environments.

Stdlib only. Reads ~/.config/pipen/config.yaml (written by the admin
bootstrap, see README.md) and talks to the cluster API at api_base
with the namespace-scoped SA token. Every verb this CLI uses is granted by
the bootstrap Role, and no more — but that scope is only ENFORCED once the
cluster runs --authorization-mode=RBAC; while it runs AlwaysAllow, any
authenticated token can do anything, so the cluster itself is the perimeter
until the flip.

Verbs:
  up <name>    Create the home PVC (if absent), the env ConfigMap, the kata
               pod and the tailnet-exposed Service; wait for pod ready and
               the operator-assigned MagicDNS hostname; print the ssh line.
               The pod pulls ghcr.io/caktus/pipen, tag `latest` by default,
               or the tag you pass with --tag.
  ssh <name>   Resolve the hostname from the live Service status and ssh in.
  down <name>  Delete pod + Service + env ConfigMap. The PVC and its data
               remain (admin wipe: kubectl -n <ns> delete pvc <name>-home).
  ls           List env pods/services in the dev namespace.
  logs <name>  Stream the env pod logs.

Env names are force-prefixed with your developer/namespace so the tailnet
hostnames are collision-proof across devs: `pipen up test-smoke` as tobias
addresses env/service `tobias-test-smoke` (hostname
tobias-test-smoke.<tailnet-domain>); a name that already starts with
`<dev>-` passes through unchanged, so `pipen down test-smoke` and
`pipen down tobias-test-smoke` address the same env. A name starting with
someone else's prefix gets your prefix too (tobias-bob-foo) — intended,
no special-casing.

The operator may suffix the assigned MagicDNS hostname (e.g. <name> ->
<name>-1), so this CLI always reads status.loadBalancer.ingress — never the
requested name — and prints the hostname it actually found.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import socket
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

CONFIG_PATH = os.environ.get(
    "PIPEN_CONFIG", os.path.expanduser("~/.config/pipen/config.yaml")
)
TOKEN_RE = re.compile(r"\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}")
NAME_RE = re.compile(r"^[a-z0-9]([a-z0-9-]*[a-z0-9])?$")
TAG_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")


def die(msg: str, code: int = 1):
    print(f"pipen: {msg}", file=sys.stderr)
    sys.exit(code)


def load_config() -> dict:
    """Parse the flat `key: value` config file (the bootstrap play writes a
    fixed shape; a YAML parser is out of scope for a stdlib-only CLI)."""
    try:
        with open(CONFIG_PATH, encoding="utf-8") as f:
            lines = f.read().splitlines()
    except OSError as e:
        die(f"cannot read {CONFIG_PATH}: {e} — copy deployment/.herdr-<dev>.yaml there")
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
    for required in ("api_base", "namespace", "dev_user", "token", "tailnet_domain"):
        if not cfg.get(required):
            die(f"{CONFIG_PATH} is missing {required!r}")
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
            if isinstance(body, str):  # rendered k8s template (YAML)
                data = body.encode()
                headers["Content-Type"] = "application/yaml"
            else:  # dict (PVC / env ConfigMap) -> JSON
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


def templates_dir() -> str:
    env = os.environ.get("PIPEN_TEMPLATES")
    if env:
        if os.path.isfile(os.path.join(env, "pod.yaml")):
            return env
        die(f"PIPEN_TEMPLATES={env} has no pod.yaml")
    d = os.getcwd()
    while True:  # walk up: run-from-checkout works from any subdirectory
        cand = os.path.join(d, "devenv", "templates")
        if os.path.isfile(os.path.join(cand, "pod.yaml")):
            return cand
        parent = os.path.dirname(d)
        if parent == d:
            break
        d = parent
    die(
        "cannot find devenv/templates — run from the repo checkout or set PIPEN_TEMPLATES"
    )


def render(template: str, values: dict) -> str:
    with open(os.path.join(templates_dir(), template), encoding="utf-8") as f:
        text = f.read()

    def sub(m: re.Match) -> str:
        key = m.group(1)
        if key not in values:
            die(f"internal: unresolved template token '{key}' in {template}")
        return str(values[key])

    rendered = TOKEN_RE.sub(sub, text)
    if TOKEN_RE.search(rendered):
        die("internal: template has unresolved tokens")
    return rendered


def service_hostname(api: Api, ns: str, name: str) -> "str | None":
    status, body = api.request("GET", api.ns_path(ns, f"/services/{name}"))
    if status == 404:
        return None
    if status != 200:
        die(f"cannot read service {name}: HTTP {status} {body}")
    ingress = (body.get("status", {}).get("loadBalancer") or {}).get("ingress") or []
    if not ingress:
        return None
    # hostname first; the tailscale ingress may report a VIP `ip` with no
    # hostname — that is still directly reachable, so fall back to it
    # instead of burning the full wait and dying.
    return ingress[0].get("hostname") or ingress[0].get("ip")


def ensure_pvc(api: Api, ns: str, name: str, env: str, size: str) -> None:
    status, _ = api.request("GET", api.ns_path(ns, f"/persistentvolumeclaims/{name}"))
    if status == 200:
        return
    if status != 404:
        die(f"cannot read pvc {name}: HTTP {status}")
    body = {
        "apiVersion": "v1",
        "kind": "PersistentVolumeClaim",
        "metadata": {"name": name, "namespace": ns, "labels": {"herdr.dev/env": env}},
        "spec": {
            "accessModes": ["ReadWriteOnce"],
            "storageClassName": "mayastor-2",
            "resources": {"requests": {"storage": size}},
        },
    }
    status, body = api.request("POST", api.ns_path(ns, "/persistentvolumeclaims"), body)
    if status not in (200, 201):
        die(f"pvc create failed: HTTP {status} {body}")
    print(f"pipen: created pvc {name} ({size}, mayastor-2 mirrored)")


def ensure_env_configmap(api: Api, ns: str, env: str, data: dict) -> None:
    name = f"env-{env}-config"
    base = {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": {"name": name, "namespace": ns, "labels": {"herdr.dev/env": env}},
        "data": data,
    }
    status, body = api.request("GET", api.ns_path(ns, f"/configmaps/{name}"))
    if status == 404:
        status, body = api.request("POST", api.ns_path(ns, "/configmaps"), base)
    elif status == 200:
        if (body.get("data") or {}) != data:
            base["metadata"]["resourceVersion"] = body["metadata"]["resourceVersion"]
            status, body = api.request(
                "PUT", api.ns_path(ns, f"/configmaps/{name}"), base
            )
    if status not in (200, 201):
        die(f"configmap {name} update failed: HTTP {status} {body}")


def wait_pod_ready(api: Api, ns: str, name: str, timeout: int) -> None:
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        status, body = api.request("GET", api.ns_path(ns, f"/pods/{name}"))
        if status == 404:
            die(
                f"pod {name} vanished (HTTP 404) — `pipen logs` before it dies shows why"
            )
        if status != 200:
            die(f"cannot read pod {name}: HTTP {status} {body}")
        pod_status = body.get("status", {})
        phase = pod_status.get("phase", "Unknown")
        cs = (pod_status.get("containerStatuses") or [{}])[0]
        ready = bool(cs.get("ready"))
        info = phase
        waiting = (cs.get("state") or {}).get("waiting")
        if not ready and waiting:
            info = f"{phase}: {waiting.get('reason', 'waiting')}"
        if info != last:
            print(f"pipen: pod {name} {info} …", flush=True)
            last = info
        if ready and phase == "Running":
            print(
                f"pipen: pod {name} ready on {body.get('spec', {}).get('nodeName', '?')}"
            )
            return
        time.sleep(5)
    die(f"pod {name} not ready within {timeout}s — `pipen logs {name}`")


def cmd_up(args, cfg, api) -> int:
    ns, dev = cfg["namespace"], cfg["dev_user"]
    env = validate_name(resolve_env(args.name, dev))

    status, body = api.request("GET", api.ns_path(ns, f"/configmaps/herdr-dev-{dev}"))
    if status == 404:
        die(
            f"no ConfigMap herdr-dev-{dev} in ns {ns} — the admin must run the herdr bootstrap first"
        )
    if status != 200:
        die(f"cannot read herdr-dev-{dev}: HTTP {status} {body}")
    cm_data = body.get("data") or {}
    if not TAG_RE.match(args.tag):
        die(f"invalid image tag {args.tag!r} (docker tag charset, max 128 chars)")
    image_tag = args.tag
    env_config = {
        k: cm_data[k]
        for k in ("models.json", "settings.json", "litellm_key")
        if k in cm_data
    }

    wanted = f"{env}.{cfg['tailnet_domain']}"
    try:  # advisory only: the operator may still assign a suffixed hostname
        socket.getaddrinfo(wanted, 22)
        print(
            f"pipen: note: {wanted} already resolves on the tailnet — expect a suffixed hostname (e.g. {env}-1)"
        )
    except socket.gaierror:
        pass

    ensure_pvc(api, ns, f"{env}-home", env, args.size)
    ensure_env_configmap(api, ns, env, env_config)

    status, _ = api.request("GET", api.ns_path(ns, f"/pods/env-{env}"))
    if status == 200:
        print(f"pipen: pod env-{env} already exists (pipen down {env} + up to rebuild)")
    else:
        pod = render(
            "pod.yaml",
            {
                "name": env,
                "namespace": ns,
                "image_tag": image_tag,
                "dev_user": dev,
                "hostname": wanted,
                "cpu": args.cpu,
                "memory": args.memory,
            },
        )
        status, body = api.request("POST", api.ns_path(ns, "/pods"), pod)
        if status not in (200, 201):
            die(f"pod create failed: HTTP {status} {body}")
        print(f"pipen: created pod env-{env} (image ghcr.io/caktus/pipen:{image_tag})")

    status, _ = api.request("GET", api.ns_path(ns, f"/services/{env}"))
    if status == 200:
        print(f"pipen: service {env} already exists")
    else:
        svc = render("service.yaml", {"name": env, "namespace": ns})
        status, body = api.request("POST", api.ns_path(ns, "/services"), svc)
        if status not in (200, 201):
            die(f"service create failed: HTTP {status} {body}")
        print(f"pipen: created service {env} (tailscale L4 exposure)")

    wait_pod_ready(api, ns, f"env-{env}", args.timeout)

    hostname, deadline = None, time.time() + 600
    while time.time() < deadline:
        hostname = service_hostname(api, ns, env)
        if hostname:
            break
        time.sleep(5)
    if not hostname:
        die(
            f"pod is ready but service {env} has no tailnet hostname yet — retry `pipen up {env}` in a moment"
        )

    print()
    print(f"pipen: ready — ssh {dev}@{hostname}")
    if hostname != wanted:
        print(
            f"pipen: (the operator assigned {hostname}; the requested name was {env})"
        )
    return 0


def cmd_ssh(args, cfg, api) -> int:
    ns, dev = cfg["namespace"], cfg["dev_user"]
    env = validate_name(resolve_env(args.name, dev))
    hostname = service_hostname(api, ns, env)
    if not hostname:
        die(f"service {env} has no hostname (is the env up? `pipen up {env}`)")
    subprocess.run(["ssh", f"{dev}@{hostname}"] + args.ssh_args)
    return 0


def cmd_down(args, cfg, api) -> int:
    ns, dev = cfg["namespace"], cfg["dev_user"]
    env = validate_name(resolve_env(args.name, dev))
    failures = []
    for kind, name in (
        ("pods", f"env-{env}"),
        ("services", env),
        ("configmaps", f"env-{env}-config"),
    ):
        status, body = api.request("DELETE", api.ns_path(ns, f"/{kind}/{name}"))
        # 404 = already gone (an external delete, or a second `down`)
        if status not in (200, 202, 404):
            failures.append((kind, name, status, body))
    if failures:
        for kind, name, status, body in failures:
            print(f"pipen: delete {kind}/{name}: HTTP {status} {body}", file=sys.stderr)
        return 1
    print(f"pipen: env {env} down. PVC {env}-home (and its data) remains in ns {ns}.")
    print(f"pipen: admin data wipe: kubectl -n {ns} delete pvc {env}-home")
    return 0


def cmd_ls(args, cfg, api) -> int:
    ns = cfg["namespace"]
    sel = urllib.parse.quote("herdr.dev/env", safe="")
    status, pods = api.request("GET", api.ns_path(ns, f"/pods?labelSelector={sel}"))
    if status != 200:
        die(f"cannot list pods: HTTP {status} {pods}")
    status, services = api.request(
        "GET", api.ns_path(ns, f"/services?labelSelector={sel}")
    )
    if status != 200:
        die(f"cannot list services: HTTP {status} {services}")
    hosts = {
        s["metadata"]["name"]: service_hostname(api, ns, s["metadata"]["name"])
        for s in services.get("items", [])
    }
    if not pods.get("items"):
        print("(no herdr envs)")
        return 0
    print(f"{'ENV':<24} {'POD':<10} {'NODE':<16} HOSTNAME")
    for p in pods.get("items", []):
        env = p["metadata"]["labels"]["herdr.dev/env"]
        pod_status = p.get("status", {})
        cs = (pod_status.get("containerStatuses") or [{}])[0]
        state = pod_status.get("phase", "Unknown")
        if not cs.get("ready"):
            waiting = (cs.get("state") or {}).get("waiting")
            if waiting:
                state = f"{state}/{waiting.get('reason', 'waiting')}"
        node = p.get("spec", {}).get("nodeName", "-")
        host = hosts.get(env) or "-"
        print(f"{env:<24} {state:<10} {node:<16} {host}")
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
        "up", help="bring up env <name> (PVC + pod + tailnet Service)"
    )
    p_up.add_argument("name")
    p_up.add_argument("--size", default="10Gi", help="home PVC size (default: 10Gi)")
    p_up.add_argument(
        "--tag",
        default="latest",
        help="image tag on ghcr.io/caktus/pipen (default: latest; pin a dated tag like 20261121-pi1.6.0)",
    )
    p_up.add_argument("--cpu", default="4", help="cpu request/limit (default: 4)")
    p_up.add_argument(
        "--memory", default="8Gi", help="memory request/limit (default: 8Gi)"
    )
    p_up.add_argument(
        "--timeout",
        type=int,
        default=1800,
        help="pod-ready timeout in seconds (default: 1800)",
    )

    p_ssh = sub.add_parser(
        "ssh", help="ssh into env <name> (hostname from live Service status)"
    )
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
