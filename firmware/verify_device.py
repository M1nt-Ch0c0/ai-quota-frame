#!/usr/bin/env python3
"""Opt-in LAN hardware acceptance checks; never follows HTTP redirects."""

import argparse
import http.client
import os
from pathlib import Path
import sys
from urllib.parse import urlsplit

MAX_IMAGE = 5 * 1024 * 1024


class Device:
    def __init__(self, address, token):
        url = urlsplit(address)
        if (url.scheme not in ("http", "https") or not url.hostname
                or url.username is not None or url.password is not None
                or url.path not in ("", "/") or url.query or url.fragment):
            raise ValueError("Use only the device origin, e.g. http://192.168.1.50")
        if not 32 <= len(token) <= 128 or any(not 33 <= ord(c) <= 126 for c in token):
            raise ValueError("PHOTOFRAME_PUSH_TOKEN must contain 32–128 printable non-space ASCII characters")
        self.url = url
        self.token = token

    def request(self, method, path, body=None, token=None, content_type=None, length=None):
        connection_type = (http.client.HTTPSConnection if self.url.scheme == "https"
                           else http.client.HTTPConnection)
        connection = connection_type(self.url.hostname, self.url.port, timeout=90)
        headers = {"Connection": "close"}
        if token is not None:
            headers["Authorization"] = "Bearer " + token
        if content_type:
            headers["Content-Type"] = content_type
        if length is not None:
            headers["Content-Length"] = str(length)
        try:
            connection.request(method, path, body=body, headers=headers)
            response = connection.getresponse()
            payload = response.read(MAX_IMAGE + 1)
            if len(payload) > MAX_IMAGE:
                raise RuntimeError("Response exceeded the image size limit")
            return response.status, payload
        finally:
            connection.close()


def expect(device, status, method, path, **kwargs):
    actual, payload = device.request(method, path, **kwargs)
    if actual != status:
        # Never include config bodies, device credentials, or tokens in logs.
        raise RuntimeError(f"{method} {path}: expected {status}, got {actual}; stopped")
    print(f"PASS {method} {path}: {actual}")
    return payload


def verify(device, image=None):
    bad_token = ("0" if device.token[0] != "0" else "1") + device.token[1:]
    # Fail against old/unconfigured firmware before testing any mutation route.
    for token in (None, bad_token):
        expect(device, 401, "GET", "/api/config", token=token)
    expect(device, 200, "GET", "/api/config", token=device.token)
    before = device.request("GET", "/api/current_image")
    if before[0] not in (200, 404):
        raise RuntimeError("Current-image endpoint is unavailable; stopped")
    for token in (None, bad_token):
        for path in ("/api/push", "/api/rotate", "/api/config", "/api/display-image"):
            expect(device, 401, "POST", path, token=token, body=b"{}",
                   content_type="application/json")
    expect(device, 415, "POST", "/api/push", token=device.token,
           body=b"{}", content_type="application/json")
    expect(device, 413, "POST", "/api/push", token=device.token,
           body=b"", content_type="image/png", length=MAX_IMAGE + 1)
    expect(device, 400, "POST", "/api/push", token=device.token,
           body=b"not a png", content_type="image/png")
    if device.request("GET", "/api/current_image") != before:
        raise RuntimeError("Rejected uploads changed the current image")
    print("PASS rejected requests preserve current-image bytes")
    if image is None:
        print("Display test NOT RUN (use --image PATH --allow-display)")
        return
    payload = image.read_bytes()
    if payload.startswith(b"\x89PNG\r\n\x1a\n"):
        content_type = "image/png"
    elif payload.startswith(b"\xff\xd8\xff"):
        content_type = "image/jpeg"
    else:
        raise ValueError("Only a real PNG or JPEG image is accepted")
    if len(payload) > MAX_IMAGE:
        raise ValueError("Image exceeds 5 MiB")
    expect(device, 200, "POST", "/api/push", token=device.token,
           body=payload, content_type=content_type)
    displayed = expect(device, 200, "GET", "/api/current_image")
    if displayed != payload:
        raise RuntimeError("Current-image bytes differ from the pushed original (SD required)")
    print("PASS authenticated push and exact current-image readback")
    print("Still confirm the physical panel visually; this does not test Wi-Fi reboot/failover.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("device", help="Explicit device origin; trusted LAN only")
    parser.add_argument("--image", type=Path)
    parser.add_argument("--allow-display", action="store_true",
                        help="Authorize replacing the current screen with --image")
    args = parser.parse_args()
    if bool(args.image) != args.allow_display:
        parser.error("--image and --allow-display must be used together")
    try:
        verify(Device(args.device, os.environ.get("PHOTOFRAME_PUSH_TOKEN", "")), args.image)
    except (ValueError, OSError, RuntimeError, http.client.HTTPException) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
