#!/usr/bin/env python3
"""Install a loopback-only SSH source tunnel as a per-user macOS LaunchAgent."""

import argparse
import os
import plistlib
import subprocess
import sys
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--ssh-host", required=True, help="preconfigured SSH host alias"
    )
    parser.add_argument(
        "--dry-run", action="store_true", help="print the plist without installing"
    )
    args = parser.parse_args()
    if (
        not args.ssh_host
        or args.ssh_host.startswith("-")
        or any(c.isspace() for c in args.ssh_host)
    ):
        parser.error(
            "--ssh-host must be an SSH host alias, not options or shell syntax"
        )
    home = Path.home()
    label = "com.m1ntch0c0.photopainter-source-tunnel"
    logs = home / "Library/Logs"
    job = {
        "Label": label,
        "ProgramArguments": [
            "/usr/bin/ssh",
            "-N",
            "-o",
            "BatchMode=yes",
            "-o",
            "StrictHostKeyChecking=yes",
            "-o",
            "ExitOnForwardFailure=yes",
            "-o",
            "ServerAliveInterval=15",
            "-o",
            "ServerAliveCountMax=3",
            "-L",
            "127.0.0.1:28317:127.0.0.1:8317",
            "-L",
            "127.0.0.1:28318:127.0.0.1:18317",
            args.ssh_host,
        ],
        "RunAtLoad": True,
        "KeepAlive": True,
        "ThrottleInterval": 15,
        "StandardOutPath": str(logs / "photopainter-source-tunnel.log"),
        "StandardErrorPath": str(logs / "photopainter-source-tunnel.error.log"),
    }
    payload = plistlib.dumps(job)
    if args.dry_run:
        sys.stdout.buffer.write(payload)
        return
    if sys.platform != "darwin":
        parser.error("installation requires macOS")
    logs.mkdir(parents=True, exist_ok=True)
    plist = home / "Library/LaunchAgents" / f"{label}.plist"
    plist.parent.mkdir(parents=True, exist_ok=True)
    domain = f"gui/{os.getuid()}"
    subprocess.run(
        ["launchctl", "bootout", domain + "/" + label], capture_output=True, check=False
    )
    plist.write_bytes(payload)
    subprocess.run(["launchctl", "enable", domain + "/" + label], check=True)
    subprocess.run(["launchctl", "bootstrap", domain, str(plist)], check=True)
    print(
        "Source tunnel installed; inspect launchctl status and tunnel logs for connectivity"
    )


if __name__ == "__main__":
    main()
