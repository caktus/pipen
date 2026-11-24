"""Unit tests for the pipen CLI's pure helpers (stdlib unittest)."""

import io
import os
import sys
import tempfile
import unittest
from datetime import datetime, timezone
from unittest import mock

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

import pipen  # noqa: E402


CONFIG = {
    "api_base": "https://api",
    "namespace": "tobias",
    "dev_user": "tobias",
    "token": "abc",
}
PEN_PATH = "/apis/pens.pipen.dev/v1alpha1/namespaces/tobias/pens/tobias-smoke"


class FakeApi:
    """Return queued API responses and record requests for assertions."""

    def __init__(self, responses):
        self.responses = list(responses)
        self.requests = []

    def request(self, method, path, body=None, stream=False):
        self.requests.append((method, path, body))
        return self.responses.pop(0)

    def pens_path(self, ns, name=""):
        path = f"/apis/pens.pipen.dev/v1alpha1/namespaces/{ns}/pens"
        return path if not name else f"{path}/{name}"


def run_cli(argv, api):
    with (
        mock.patch.object(sys, "argv", ["pipen"] + argv),
        mock.patch.object(pipen, "load_config", return_value=CONFIG),
        mock.patch.object(pipen, "Api", return_value=api),
    ):
        return pipen.main()


def smoke_pen():
    return {
        "apiVersion": "pens.pipen.dev/v1alpha1",
        "kind": "Pen",
        "metadata": {
            "name": "tobias-smoke",
            "namespace": "tobias",
            "resourceVersion": "17",
        },
        "spec": {
            "image": "ghcr.io/caktus/pipen:test",
            "homeSize": "20Gi",
            "cpu": "8",
            "memory": "16Gi",
            "paused": False,
            "ttl": "8h",
            "egress": True,
        },
        "status": {
            "phase": "Ready",
            "ready": True,
            "hostname": "smoke.ts.net",
            "node": "node-1",
            "message": "ready",
        },
    }


def with_config(lines, fn):
    """Run fn with PIPEN_CONFIG pointing at a temp config file."""
    with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
        f.write(lines)
        path = f.name
    old = os.environ.get("PIPEN_CONFIG")
    os.environ["PIPEN_CONFIG"] = path
    try:
        return fn()
    finally:
        os.unlink(path)
        if old is None:
            os.environ.pop("PIPEN_CONFIG", None)
        else:
            os.environ["PIPEN_CONFIG"] = old


class LoadConfigTest(unittest.TestCase):
    def test_missing_required_key_exits(self):
        cfg = "api_base: https://api\nnamespace: tobias\ndev_user: tobias\n"
        with self.assertRaises(SystemExit):
            with_config(cfg, pipen.load_config)

    def test_tailnet_domain_not_required(self):
        cfg = "api_base: https://api\nnamespace: tobias\ndev_user: tobias\ntoken: abc\n"
        cfg = with_config(cfg, pipen.load_config)
        self.assertEqual(cfg["token"], "abc")
        self.assertNotIn("tailnet_domain", cfg)


class PenSpecTest(unittest.TestCase):
    def test_full_spec(self):
        spec = pipen.pen_spec(
            "ghcr.io/caktus/pipen:20261121-pi1.6.0", "20Gi", "8", "16Gi"
        )
        self.assertEqual(
            spec,
            {
                "image": "ghcr.io/caktus/pipen:20261121-pi1.6.0",
                "homeSize": "20Gi",
                "cpu": "8",
                "memory": "16Gi",
            },
        )

    def test_empty_image_omitted(self):
        spec = pipen.pen_spec("", "10Gi", "4", "8Gi")
        self.assertNotIn("image", spec)
        self.assertEqual(spec["homeSize"], "10Gi")

    def test_pen_body(self):
        body = pipen.pen_body(
            "tobias-smoke", "tobias", pipen.pen_spec("", "10Gi", "4", "8Gi")
        )
        self.assertEqual(body["kind"], "Pen")
        self.assertEqual(body["apiVersion"], "pens.pipen.dev/v1alpha1")
        self.assertEqual(body["metadata"]["name"], "tobias-smoke")
        self.assertEqual(body["metadata"]["namespace"], "tobias")


class UpOptionsTest(unittest.TestCase):
    def test_ttl_and_egress_merge_into_the_pen_spec(self):
        api = FakeApi([(201, {}), (200, smoke_pen())])

        result = run_cli(
            [
                "up",
                "tobias-smoke",
                "--image",
                "ghcr.io/caktus/pipen:test",
                "--size",
                "20Gi",
                "--cpu",
                "8",
                "--memory",
                "16Gi",
                "--ttl",
                "8h",
                "--egress",
            ],
            api,
        )

        self.assertEqual(result, 0)
        self.assertEqual(
            api.requests[0][:2],
            ("POST", "/apis/pens.pipen.dev/v1alpha1/namespaces/tobias/pens"),
        )
        self.assertEqual(
            api.requests[0][2],
            {
                "apiVersion": "pens.pipen.dev/v1alpha1",
                "kind": "Pen",
                "metadata": {"name": "tobias-smoke", "namespace": "tobias"},
                "spec": {
                    "image": "ghcr.io/caktus/pipen:test",
                    "homeSize": "20Gi",
                    "cpu": "8",
                    "memory": "16Gi",
                    "ttl": "8h",
                    "egress": True,
                },
            },
        )


class PenStatusTest(unittest.TestCase):
    def test_ready_status_fields(self):
        body = {
            "status": {
                "phase": "Ready",
                "ready": True,
                "hostname": "h.ts.net",
                "node": "n1",
                "message": "ready",
            }
        }
        f = pipen.pen_status_fields(body)
        self.assertTrue(f["ready"])
        self.assertEqual(f["hostname"], "h.ts.net")
        self.assertEqual(f["phase"], "Ready")
        self.assertEqual(f["node"], "n1")

    def test_empty_status(self):
        f = pipen.pen_status_fields({})
        self.assertFalse(f["ready"])
        self.assertEqual(f["hostname"], "")
        self.assertEqual(f["phase"], "Unknown")


class RemainingTtlTest(unittest.TestCase):
    def test_formats_a_human_duration(self):
        now = datetime(2026, 11, 21, 12, 0, tzinfo=timezone.utc)
        self.assertEqual(
            pipen.remaining_ttl("2026-11-21T20:00:00Z", now),
            "8h0m",
        )

    def test_uses_a_dash_when_shutdown_time_is_unset(self):
        now = datetime(2026, 11, 21, 12, 0, tzinfo=timezone.utc)
        self.assertEqual(pipen.remaining_ttl(None, now), "-")

    def test_returns_unknown_for_invalid_shutdown_times(self):
        now = datetime(2026, 11, 21, 12, 0, tzinfo=timezone.utc)
        for value in ("not-a-time", "2026-11-21T20:00:00", 123):
            with self.subTest(value=value):
                self.assertEqual(pipen.remaining_ttl(value, now), "unknown")

    def test_clamps_past_shutdown_time_to_zero_seconds(self):
        now = datetime(2026, 11, 21, 12, 0, tzinfo=timezone.utc)
        self.assertEqual(pipen.remaining_ttl("2026-11-21T11:00:00Z", now), "0s")

    def test_formats_near_future_in_seconds(self):
        now = datetime(2026, 11, 21, 12, 0, tzinfo=timezone.utc)
        self.assertEqual(pipen.remaining_ttl("2026-11-21T12:00:30Z", now), "30s")


class LsTest(unittest.TestCase):
    def test_lists_suspended_phase_and_remaining_ttl(self):
        suspended = smoke_pen()
        suspended["status"].update(
            {
                "phase": "Suspended",
                "ready": False,
                "message": "paused",
                "shutdownAt": "2026-11-21T20:00:00Z",
            }
        )
        without_ttl = smoke_pen()
        without_ttl["metadata"]["name"] = "tobias-other"
        api = FakeApi([(200, {"items": [suspended, without_ttl]})])
        now = datetime(2026, 11, 21, 12, 0, tzinfo=timezone.utc)
        output = io.StringIO()
        with (
            mock.patch.object(pipen, "now", return_value=now),
            mock.patch.object(sys, "stdout", output),
        ):
            result = run_cli(["ls"], api)

        self.assertEqual(result, 0)
        lines = output.getvalue().splitlines()
        self.assertEqual(lines[0].split(), ["ENV", "PHASE", "TTL", "NODE", "HOSTNAME"])
        self.assertEqual(
            lines[1].split(),
            ["tobias-smoke", "Suspended", "8h0m", "node-1", "smoke.ts.net"],
        )
        self.assertEqual(
            lines[2].split(),
            ["tobias-other", "Ready", "-", "node-1", "smoke.ts.net"],
        )

    def test_invalid_ttl_does_not_hide_other_pens(self):
        invalid = smoke_pen()
        invalid["status"]["shutdownAt"] = "not-a-time"
        valid = smoke_pen()
        valid["metadata"]["name"] = "tobias-other"
        valid["status"]["shutdownAt"] = "2026-11-21T20:00:00Z"
        api = FakeApi([(200, {"items": [invalid, valid]})])
        now = datetime(2026, 11, 21, 12, 0, tzinfo=timezone.utc)
        output = io.StringIO()
        with (
            mock.patch.object(pipen, "now", return_value=now),
            mock.patch.object(sys, "stdout", output),
        ):
            result = run_cli(["ls"], api)

        self.assertEqual(result, 0)
        lines = output.getvalue().splitlines()
        self.assertEqual(
            lines[1].split(),
            ["tobias-smoke", "Ready", "unknown", "node-1", "smoke.ts.net"],
        )
        self.assertEqual(
            lines[2].split(),
            ["tobias-other", "Ready", "8h0m", "node-1", "smoke.ts.net"],
        )

    def test_lists_past_and_near_future_shutdown_times(self):
        past = smoke_pen()
        past["status"]["shutdownAt"] = "2026-11-21T11:00:00Z"
        near_future = smoke_pen()
        near_future["metadata"]["name"] = "tobias-other"
        near_future["status"]["shutdownAt"] = "2026-11-21T12:00:30Z"
        api = FakeApi([(200, {"items": [past, near_future]})])
        now = datetime(2026, 11, 21, 12, 0, tzinfo=timezone.utc)
        output = io.StringIO()
        with (
            mock.patch.object(pipen, "now", return_value=now),
            mock.patch.object(sys, "stdout", output),
        ):
            result = run_cli(["ls"], api)

        self.assertEqual(result, 0)
        lines = output.getvalue().splitlines()
        self.assertEqual(
            lines[1].split(),
            ["tobias-smoke", "Ready", "0s", "node-1", "smoke.ts.net"],
        )
        self.assertEqual(
            lines[2].split(),
            ["tobias-other", "Ready", "30s", "node-1", "smoke.ts.net"],
        )


class PauseTest(unittest.TestCase):
    def test_pause_sets_only_spec_paused_and_waits_for_suspended(self):
        suspended = smoke_pen()
        suspended["spec"]["paused"] = True
        suspended["status"].update({"phase": "Suspended", "ready": False})
        api = FakeApi([(200, smoke_pen()), (200, {}), (200, suspended)])

        result = run_cli(["pause", "tobias-smoke"], api)

        self.assertEqual(result, 0)
        self.assertEqual(
            [(method, path) for method, path, _ in api.requests],
            [("GET", PEN_PATH), ("PUT", PEN_PATH), ("GET", PEN_PATH)],
        )
        self.assertEqual(
            api.requests[1][2],
            {
                "apiVersion": "pens.pipen.dev/v1alpha1",
                "kind": "Pen",
                "metadata": {
                    "name": "tobias-smoke",
                    "namespace": "tobias",
                    "resourceVersion": "17",
                },
                "spec": {
                    "image": "ghcr.io/caktus/pipen:test",
                    "homeSize": "20Gi",
                    "cpu": "8",
                    "memory": "16Gi",
                    "paused": True,
                    "ttl": "8h",
                    "egress": True,
                },
                "status": {
                    "phase": "Ready",
                    "ready": True,
                    "hostname": "smoke.ts.net",
                    "node": "node-1",
                    "message": "ready",
                },
            },
        )


class PollingTest(unittest.TestCase):
    def test_pause_suppresses_repeated_status_messages(self):
        waiting = smoke_pen()
        waiting["spec"]["paused"] = True
        waiting["status"].update(
            {"phase": "Creating", "ready": False, "message": "waiting"}
        )
        suspended = smoke_pen()
        suspended["spec"]["paused"] = True
        suspended["status"].update({"phase": "Suspended", "ready": False})
        api = FakeApi(
            [
                (200, smoke_pen()),
                (200, {}),
                (200, waiting),
                (200, waiting),
                (200, suspended),
            ]
        )
        output = io.StringIO()
        with (
            mock.patch.object(pipen.time, "sleep"),
            mock.patch.object(sys, "stdout", output),
        ):
            result = run_cli(["pause", "tobias-smoke"], api)

        self.assertEqual(result, 0)
        self.assertEqual(output.getvalue().count("Creating: waiting"), 1)


class ResumeTest(unittest.TestCase):
    def test_resume_sets_only_spec_paused_and_waits_for_ready(self):
        current = smoke_pen()
        current["spec"]["paused"] = True
        current["status"].update(
            {"phase": "Suspended", "ready": False, "message": "paused"}
        )
        ready = smoke_pen()
        api = FakeApi([(200, current), (200, {}), (200, ready)])

        result = run_cli(["resume", "tobias-smoke"], api)

        self.assertEqual(result, 0)
        self.assertEqual(
            [(method, path) for method, path, _ in api.requests],
            [("GET", PEN_PATH), ("PUT", PEN_PATH), ("GET", PEN_PATH)],
        )
        self.assertEqual(
            api.requests[1][2],
            {
                "apiVersion": "pens.pipen.dev/v1alpha1",
                "kind": "Pen",
                "metadata": {
                    "name": "tobias-smoke",
                    "namespace": "tobias",
                    "resourceVersion": "17",
                },
                "spec": {
                    "image": "ghcr.io/caktus/pipen:test",
                    "homeSize": "20Gi",
                    "cpu": "8",
                    "memory": "16Gi",
                    "paused": False,
                    "ttl": "8h",
                    "egress": True,
                },
                "status": {
                    "phase": "Suspended",
                    "ready": False,
                    "hostname": "smoke.ts.net",
                    "node": "node-1",
                    "message": "paused",
                },
            },
        )


def observed_pen(observed_generation):
    pen = smoke_pen()
    pen["status"]["observedGeneration"] = observed_generation
    return pen


class HomeSizeBytesTest(unittest.TestCase):
    def test_parses_binary_and_decimal_suffixes(self):
        self.assertEqual(pipen.home_size_bytes("20Gi"), 20 * 1024**3)
        self.assertEqual(pipen.home_size_bytes("500M"), 500 * 10**6)
        self.assertEqual(pipen.home_size_bytes("1024"), 1024)

    def test_returns_none_for_unparseable_values(self):
        for value in ("", "big", "10Xi", "1Gi2Gi", None):
            with self.subTest(value=value):
                self.assertIsNone(pipen.home_size_bytes(value))


class ResizeTest(unittest.TestCase):
    def test_resize_merges_only_the_provided_fields(self):
        api = FakeApi(
            [
                (200, smoke_pen()),
                (200, {"metadata": {"generation": 18}}),
                (200, observed_pen(18)),
            ]
        )

        result = run_cli(
            ["resize", "tobias-smoke", "--cpu", "16", "--memory", "32Gi"], api
        )

        self.assertEqual(result, 0)
        self.assertEqual(
            [(method, path) for method, path, _ in api.requests],
            [("GET", PEN_PATH), ("PUT", PEN_PATH), ("GET", PEN_PATH)],
        )
        put = api.requests[1][2]
        self.assertEqual(put["metadata"]["resourceVersion"], "17")
        self.assertEqual(
            put["spec"],
            {
                "image": "ghcr.io/caktus/pipen:test",
                "homeSize": "20Gi",
                "cpu": "16",
                "memory": "32Gi",
                "paused": False,
                "ttl": "8h",
                "egress": True,
            },
        )

    def test_resize_missing_pen_errors_without_put(self):
        api = FakeApi([(404, {"message": "not found"})])
        err = io.StringIO()
        with mock.patch.object(sys, "stderr", err):
            with self.assertRaises(SystemExit):
                run_cli(["resize", "tobias-smoke", "--cpu", "16"], api)

        self.assertIn("not found", err.getvalue())
        self.assertEqual([method for method, _, _ in api.requests], ["GET"])

    def test_resize_refuses_home_shrink_before_any_put(self):
        api = FakeApi([(200, smoke_pen())])
        err = io.StringIO()
        with mock.patch.object(sys, "stderr", err):
            with self.assertRaises(SystemExit):
                run_cli(["resize", "tobias-smoke", "--size", "10Gi"], api)

        self.assertIn("home size can only grow", err.getvalue())
        self.assertEqual(
            [(method, path) for method, path, _ in api.requests],
            [("GET", PEN_PATH)],
        )

    def test_resize_without_flags_errors_without_requests(self):
        api = FakeApi([])
        with self.assertRaises(SystemExit):
            run_cli(["resize", "tobias-smoke"], api)

        self.assertEqual(api.requests, [])

    def test_resize_keeps_polling_until_the_operator_acknowledges(self):
        api = FakeApi(
            [
                (200, smoke_pen()),
                (200, {"metadata": {"generation": 18}}),
                (200, observed_pen(17)),
                (200, observed_pen(18)),
            ]
        )
        with mock.patch.object(pipen.time, "sleep"):
            result = run_cli(["resize", "tobias-smoke", "--cpu", "16"], api)

        self.assertEqual(result, 0)
        self.assertEqual(
            [method for method, _, _ in api.requests],
            ["GET", "PUT", "GET", "GET"],
        )

    def test_resize_dies_when_the_pen_fails_after_a_stale_ready(self):
        failed = observed_pen(17)
        failed["status"].update({"phase": "Failed", "ready": False, "message": "boom"})
        api = FakeApi(
            [
                (200, smoke_pen()),
                (200, {"metadata": {"generation": 18}}),
                (200, observed_pen(17)),
                (200, failed),
            ]
        )
        err = io.StringIO()
        with (
            mock.patch.object(pipen.time, "sleep"),
            mock.patch.object(sys, "stderr", err),
        ):
            with self.assertRaises(SystemExit):
                run_cli(["resize", "tobias-smoke", "--cpu", "16"], api)

        self.assertIn("failed: boom", err.getvalue())

    def test_resize_without_a_put_generation_waits_for_ready_only(self):
        api = FakeApi([(200, smoke_pen()), (200, {}), (200, smoke_pen())])

        result = run_cli(["resize", "tobias-smoke", "--size", "30Gi"], api)

        self.assertEqual(result, 0)
        self.assertEqual(
            [method for method, _, _ in api.requests],
            ["GET", "PUT", "GET"],
        )


class ResizeGenerationTest(unittest.TestCase):
    def test_put_generation_reads_metadata_generation(self):
        self.assertEqual(pipen.put_generation({"metadata": {"generation": 18}}), 18)

    def test_put_generation_returns_none_when_unusable(self):
        for response in (
            {},
            {"metadata": {}},
            {"metadata": {"generation": "18"}},
            "oops",
        ):
            with self.subTest(response=response):
                self.assertIsNone(pipen.put_generation(response))

    def test_acknowledged_needs_an_observed_generation_at_least_generation(self):
        self.assertTrue(pipen.acknowledged({"observedGeneration": 18}, 18))
        self.assertTrue(pipen.acknowledged({"observedGeneration": 19}, 18))
        self.assertFalse(pipen.acknowledged({"observedGeneration": 17}, 18))
        self.assertFalse(pipen.acknowledged({"observedGeneration": None}, 18))


class ValidateNameTest(unittest.TestCase):
    def test_rejects_uppercase(self):
        with self.assertRaises(SystemExit):
            pipen.validate_name("Bad")

    def test_accepts_rfc1123(self):
        self.assertEqual(pipen.validate_name("tobias-smoke"), "tobias-smoke")


if __name__ == "__main__":
    unittest.main()
