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
  up <name>    Create a Pen (spec: image/homeSize/cpu/memory/ttl/egress)
               and wait for status ready; print the ssh line from the
               operator-assigned tailnet hostname. The default image
               is the operator's config default; pin one with --image.
  pause <name> Stop compute and keep the Pen, home PVC, and hostname.
  resume <name> Resume a paused Pen and wait for Ready.
  resize <name> Set the pen cpu, memory and home size. The pod restarts
                with the new size. The home grows. A shrink is refused.
  ssh <name>   Resolve the hostname from Pen status and ssh in.
  down <name>  Delete the Pen. The operator's garbage collection removes
               the pod and Service; the home PVC and its data remain
               (admin wipe: kubectl -n <ns> delete pvc <name>-home).
  ls           List Pens in the dev namespace (phase/TTL/node/hostname).
  logs <name>  Stream the env pod logs.
  config       Write the CLI config file (admin bootstrap; refuses to
               overwrite an existing one).

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
from datetime import datetime, timezone

# Keep in sync with cli/pyproject.toml
__version__ = "0.1.0"

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


SIZE_RE = re.compile(r"^(\d+)([KMGT][i]?)?$")
SIZE_FACTORS = {
    "": 1,
    "K": 1000,
    "M": 10**6,
    "G": 10**9,
    "T": 10**12,
    "Ki": 1024,
    "Mi": 1024**2,
    "Gi": 1024**3,
    "Ti": 1024**4,
}


def home_size_bytes(value: str):
    """A Kubernetes size string (10Gi, 500M, 1024) as bytes, or None when
    the value does not parse. The operator re-checks sizes; this is the
    client-side shrink guard."""
    m = SIZE_RE.match(value) if isinstance(value, str) else None
    if not m:
        return None
    return int(m.group(1)) * SIZE_FACTORS[m.group(2) or ""]


def pen_spec(
    image: str, size: str, cpu: str, memory: str, ttl: str = "", egress: bool = False
) -> dict:
    """Pen spec body; an empty image leaves the operator's default."""
    spec = {"homeSize": size, "cpu": cpu, "memory": memory}
    if image:
        spec["image"] = image
    if ttl:
        spec["ttl"] = ttl
    if egress:
        spec["egress"] = True
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
        "observedGeneration": status.get("observedGeneration"),
    }


def get_pen(api: Api, ns: str, env: str) -> "tuple[int, dict]":
    status, body = api.request("GET", api.pens_path(ns, env))
    if status == 404:
        die(f"pen {env} not found (is the env up? `pipen up {env}`)")
    if status != 200:
        die(f"cannot read pen {env}: HTTP {status} {body}")
    return status, body


def poll_pen(
    api: Api, ns: str, env: str, timeout: int, done, timeout_message: str, fail=None
) -> dict:
    """Poll the pen until done(status fields) is true, printing each new
    phase or message once. fail(status fields) runs after done and may die
    when the pen fails. Dies on timeout. Returns the final status fields."""
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        _, body = get_pen(api, ns, env)
        f = pen_status_fields(body)
        if done(f):
            return f
        if fail is not None:
            fail(f)
        info = f["phase"] + (f": {f['message']}" if f["message"] else "")
        if info != last:
            print(f"pipen: {env} {info} …", flush=True)
            last = info
        time.sleep(POLL_SECONDS)
    die(timeout_message)
    return {}  # unreachable


def image_tag(image: str) -> str:
    """The tag of an image reference. Ports the operator's Go rule
    (operator/internal/pen/reconcile.go, pullPolicy): the tag is the part
    after the last ':' only when that ':' comes after the last '/', so a
    registry port (registry:5000/img) is not a tag separator. A reference
    with no tag is the implicit 'latest'. A digest (@sha256:...) yields the
    digest part, which is not 'latest' and counts as pinned."""
    i = image.rfind(":")
    if i > image.rfind("/"):
        return image[i + 1 :]
    return "latest"


def up_image_warning(image: str, config_image: str) -> str:
    """The up warning for a floating image tag, or '' when the image is
    pinned. `image` is the --image flag value, `config_image` the optional
    image key from the CLI config ('' means absent). Warning only: the pen
    is still created."""
    reference = image or config_image
    if reference:
        tag = image_tag(reference)
        if tag == "latest":
            return (
                f"pipen: the image tag {tag!r} is not pinned, so the image "
                "can change under the pen"
            )
        return ""
    return (
        "pipen: the image is the operator default and the CLI cannot check "
        "that it is pinned. Set the image key in the CLI config to enable "
        "the check."
    )


def cmd_up(args, cfg, api) -> int:
    ns, dev = cfg["namespace"], cfg["dev_user"]
    env = validate_name(resolve_env(args.name, dev))

    warning = up_image_warning(args.image, cfg.get("image") or "")
    if warning:
        print(warning, file=sys.stderr)

    status, body = api.request(
        "POST",
        api.pens_path(ns),
        pen_body(
            env,
            ns,
            pen_spec(
                args.image, args.size, args.cpu, args.memory, args.ttl, args.egress
            ),
        ),
    )
    if status in (200, 201):
        print(f"pipen: created pen {env} (operator brings the env up)")
    elif status == 409:
        print(f"pipen: pen {env} already exists (pipen down {env} + up to rebuild)")
    else:
        die(f"pen create failed: HTTP {status} {body}")

    f = poll_pen(
        api,
        ns,
        env,
        args.timeout,
        lambda f: f["ready"],
        f"pen {env} not ready within {args.timeout}s — `pipen ls` shows status",
        fail=lambda f: die(f"pen {env} failed: {f['message']}")
        if f["phase"] == "Failed"
        else None,
    )
    print()
    print(f"pipen: ready — ssh {dev}@{f['hostname']}")
    return 0


def set_paused(args, cfg, api, paused: bool, expected_phase: str) -> int:
    ns, dev = cfg["namespace"], cfg["dev_user"]
    env = validate_name(resolve_env(args.name, dev))
    _, body = get_pen(api, ns, env)
    body.setdefault("spec", {})["paused"] = paused
    status, response = api.request("PUT", api.pens_path(ns, env), body)
    if status != 200:
        die(f"pen update failed: HTTP {status} {response}")
    poll_pen(
        api,
        ns,
        env,
        args.timeout,
        lambda f: f["phase"] == expected_phase,
        f"pen {env} not {expected_phase.lower()} within {args.timeout}s — `pipen ls` shows status",
    )
    print(f"pipen: pen {env} {expected_phase.lower()}")
    return 0


def cmd_pause(args, cfg, api) -> int:
    return set_paused(args, cfg, api, True, "Suspended")


def cmd_resume(args, cfg, api) -> int:
    return set_paused(args, cfg, api, False, "Ready")


def put_generation(response):
    """The pen generation the API server assigned to a PUT, or None when the
    response carries none. With None the wait falls back to ready-only."""
    if not isinstance(response, dict):
        return None
    generation = response.get("metadata", {}).get("generation")
    return generation if isinstance(generation, int) else None


def acknowledged(fields: dict, generation: int) -> bool:
    """True when the status shows the operator has seen the spec generation
    the resize PUT returned (status.observedGeneration >= generation)."""
    observed = fields["observedGeneration"]
    return isinstance(observed, int) and observed >= generation


def cmd_resize(args, cfg, api) -> int:
    ns, dev = cfg["namespace"], cfg["dev_user"]
    env = validate_name(resolve_env(args.name, dev))
    if not (args.cpu or args.memory or args.size):
        die("give at least one of --cpu, --memory or --size")
    _, body = get_pen(api, ns, env)
    spec = body.setdefault("spec", {})
    if args.size:
        new_size = home_size_bytes(args.size)
        if new_size is None:
            die(f"invalid home size {args.size!r}")
        current_size = home_size_bytes(spec.get("homeSize", ""))
        if current_size is not None and new_size < current_size:
            die(f"home size can only grow (pen {env} homeSize is {spec['homeSize']})")
        spec["homeSize"] = args.size
    if args.cpu:
        spec["cpu"] = args.cpu
    if args.memory:
        spec["memory"] = args.memory
    status, response = api.request("PUT", api.pens_path(ns, env), body)
    if status != 200:
        die(f"pen update failed: HTTP {status} {response}")
    generation = put_generation(response)
    # A stale Ready (observedGeneration below the PUT generation) is the
    # pre-resize status; waiting on ready alone would report ready before
    # the operator has seen the new spec. When the PUT response carries no
    # usable generation, the wait falls back to ready-only.

    def done(f) -> bool:
        return f["ready"] and (generation is None or acknowledged(f, generation))

    f = poll_pen(
        api,
        ns,
        env,
        args.timeout,
        done,
        f"pen {env} not ready within {args.timeout}s — `pipen ls` shows status",
        fail=lambda f: die(f"pen {env} failed: {f['message']}")
        if f["phase"] == "Failed"
        else None,
    )
    print(f"pipen: pen {env} ready (ssh {dev}@{f['hostname']})")
    return 0


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


def now() -> datetime:
    return datetime.now(timezone.utc)


def remaining_ttl(shutdown_at: object, current_time: datetime) -> str:
    if not shutdown_at:
        return "-"
    if not isinstance(shutdown_at, str):
        return "unknown"
    try:
        shutdown_time = datetime.fromisoformat(shutdown_at.replace("Z", "+00:00"))
    except ValueError:
        return "unknown"
    if shutdown_time.tzinfo is None or shutdown_time.utcoffset() is None:
        return "unknown"
    try:
        seconds = max(0, int((shutdown_time - current_time).total_seconds()))
    except (TypeError, ValueError, OverflowError):
        return "unknown"
    hours, remainder = divmod(seconds, 3600)
    minutes, seconds = divmod(remainder, 60)
    if hours:
        return f"{hours}h{minutes}m"
    if minutes:
        return f"{minutes}m"
    return f"{seconds}s"


def cmd_ls(args, cfg, api) -> int:
    ns = cfg["namespace"]
    status, body = api.request("GET", api.pens_path(ns))
    if status != 200:
        die(f"cannot list pens: HTTP {status} {body}")
    items = body.get("items", [])
    if not items:
        print("(no herdr envs)")
        return 0
    print(f"{'ENV':<24} {'PHASE':<10} {'TTL':<10} {'NODE':<16} HOSTNAME")
    current_time = now()
    for pen in items:
        env = pen["metadata"]["name"]
        f = pen_status_fields(pen)
        ttl = remaining_ttl(pen.get("status", {}).get("shutdownAt"), current_time)
        print(
            f"{env:<24} {f['phase']:<10} {ttl:<10} "
            f"{f['node'] or '-':<16} {f['hostname'] or '-'}"
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


def cmd_config(args) -> int:
    """Write a flat `key: value` CLI config file the admin runs once at
    bootstrap. Refuses to overwrite: the CLI never rewrites a config, so an
    older CLI version keeps showing its own version in its pens' configs."""
    if os.path.exists(args.out):
        die(f"cannot write {args.out}: the file exists, so remove it first")
    lines = [
        f"# generated by pipen {__version__}",
        f"api_base: {args.api_base}",
        f"namespace: {args.namespace}",
        f"dev_user: {args.dev_user}",
        f"token: {args.token}",
    ]
    if args.image:
        lines.append(f"image: {args.image}")
    lines.append(f"cli_version: {__version__}")
    if args.operator_version:
        lines.append(f"operator_version: {args.operator_version}")
    if args.chart_version:
        lines.append(f"chart_version: {args.chart_version}")
    try:
        with open(args.out, "w", encoding="utf-8") as f:
            f.write("\n".join(lines) + "\n")
    except OSError as e:
        die(f"cannot write {args.out}: {e}")
    print(f"pipen: wrote {args.out}")
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
        help=(
            "full image reference (default: the operator's configured image, "
            "which the shipped chart sets to the pinned date tag "
            "ghcr.io/caktus/pipen:20261124-pi1.6.0)"
        ),
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
    p_up.add_argument(
        "--ttl", default="", help="time until the env shuts down, for example 8h"
    )
    p_up.add_argument(
        "--egress", action="store_true", help="allow only approved network destinations"
    )

    p_pause = sub.add_parser("pause", help="pause env <name> (keeps the PVC + data)")
    p_pause.add_argument("name")
    p_pause.add_argument(
        "--timeout",
        type=int,
        default=1800,
        help="pen-suspended timeout in seconds (default: 1800)",
    )

    p_resume = sub.add_parser("resume", help="resume paused env <name>")
    p_resume.add_argument("name")
    p_resume.add_argument(
        "--timeout",
        type=int,
        default=1800,
        help="pen-ready timeout in seconds (default: 1800)",
    )

    p_resize = sub.add_parser(
        "resize", help="resize env <name> cpu, memory or home (the pod restarts)"
    )
    p_resize.add_argument("name")
    p_resize.add_argument("--cpu", default="", help="new cpu request/limit")
    p_resize.add_argument("--memory", default="", help="new memory request/limit")
    p_resize.add_argument("--size", default="", help="new home PVC size (grow only)")
    p_resize.add_argument(
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

    p_config = sub.add_parser(
        "config",
        help="write a CLI config file (refuses to overwrite an existing one)",
        description=(
            "Write a flat key: value CLI config file. Refuses to overwrite "
            "an existing file."
        ),
    )
    p_config.add_argument(
        "--out", required=True, help="path to write the config file to"
    )
    p_config.add_argument("--api-base", required=True, help="cluster API base URL")
    p_config.add_argument("--namespace", required=True, help="the dev namespace")
    p_config.add_argument("--dev-user", required=True, help="your developer name")
    p_config.add_argument("--token", required=True, help="the dev ServiceAccount token")
    p_config.add_argument(
        "--image", default="", help="default pen image reference (optional)"
    )
    p_config.add_argument(
        "--operator-version",
        default="",
        help="operator image tag (optional)",
    )
    p_config.add_argument(
        "--chart-version",
        default="",
        help="dev-namespace chart version (optional)",
    )

    sub.add_parser("ls", help="list envs in the dev namespace")

    p_logs = sub.add_parser("logs", help="stream env <name> pod logs")
    p_logs.add_argument("name")

    args = parser.parse_args()
    # config generates a config file, so it must not read one first
    if args.cmd == "config":
        return cmd_config(args)
    cfg = load_config()
    api = Api(cfg)
    return {
        "up": cmd_up,
        "pause": cmd_pause,
        "resume": cmd_resume,
        "resize": cmd_resize,
        "ssh": cmd_ssh,
        "down": cmd_down,
        "ls": cmd_ls,
        "logs": cmd_logs,
    }[args.cmd](args, cfg, api)


if __name__ == "__main__":
    sys.exit(main())
