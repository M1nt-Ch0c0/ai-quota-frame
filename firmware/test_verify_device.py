"""Checks the acceptance client's stop conditions, not the ESP32 hardware."""

import contextlib
import io
from pathlib import Path
import unittest
from unittest.mock import Mock

from verify_device import Device, verify


class VerifyClientTests(unittest.TestCase):
    def test_refuses_unprotected_firmware_before_mutation(self):
        device = Mock(token="a" * 32)
        device.request.return_value = (200, b"private configuration")
        with self.assertRaisesRegex(RuntimeError, "expected 401"):
            verify(device)
        self.assertEqual(device.request.call_count, 1)
        self.assertEqual(device.request.call_args.args, ("GET", "/api/config"))

    def test_checks_image_preservation_and_explicit_push(self):
        png = b"\x89PNG\r\n\x1a\nfixture"
        device = Mock(token="a" * 32)
        device.request.side_effect = (
            [(401, b""), (401, b""), (200, b"private config"), (200, b"before")]
            + [(401, b"")] * 8
            + [(415, b""), (413, b""), (400, b""), (200, b"before"),
               (200, b"ok"), (200, png)]
        )
        image = Mock(spec=Path)
        image.read_bytes.return_value = png
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            verify(device, image)
        self.assertIn("exact current-image readback", output.getvalue())
        self.assertNotIn("private config", output.getvalue())
        push = device.request.call_args_list[-2]
        self.assertEqual(push.args, ("POST", "/api/push"))
        self.assertEqual(push.kwargs["body"], png)
        self.assertEqual(push.kwargs["token"], device.token)

    def test_stops_if_rejected_upload_changes_current_image(self):
        device = Mock(token="a" * 32)
        device.request.side_effect = (
            [(401, b""), (401, b""), (200, b""), (200, b"before")]
            + [(401, b"")] * 8
            + [(415, b""), (413, b""), (400, b""), (200, b"changed")]
        )
        with contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(RuntimeError, "changed the current image"):
                verify(device)

    def test_rejects_ambiguous_origin_and_bad_token(self):
        for url in ("http://user:password@device", "http://device/path", "file:///x",
                    "http://device?x=y", "http://device/#fragment"):
            with self.assertRaises(ValueError):
                Device(url, "a" * 32)
        for token in ("", "a" * 31, "a" * 129, "a" * 31 + "\n"):
            with self.assertRaises(ValueError):
                Device("http://device", token)


if __name__ == "__main__":
    unittest.main()
