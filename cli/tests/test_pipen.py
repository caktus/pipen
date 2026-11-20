"""Unit tests for the pipen CLI's pure helpers (stdlib unittest)."""

import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

import pipen  # noqa: E402


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


class ValidateNameTest(unittest.TestCase):
    def test_rejects_uppercase(self):
        with self.assertRaises(SystemExit):
            pipen.validate_name("Bad")

    def test_accepts_rfc1123(self):
        self.assertEqual(pipen.validate_name("tobias-smoke"), "tobias-smoke")


if __name__ == "__main__":
    unittest.main()
