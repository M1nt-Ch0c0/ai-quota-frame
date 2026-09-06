#!/usr/bin/env python3
"""Install a per-user launchd renderer; consumes a shell-quoted env file privately."""

import argparse
import os
import plistlib
import shutil
import subprocess
from pathlib import Path

p = argparse.ArgumentParser(description=__doc__)
p.add_argument("--env-file", required=True, type=Path)
p.add_argument(
    "--binary",
    type=Path,
    default=Path(__file__).resolve().parents[1] / "dist/ai-quota-frame",
)
p.add_argument(
    "--no-start",
    action="store_true",
    help="install files without loading the service; use until the panel passes a physical refresh",
)
a = p.parse_args()
if not a.binary.is_file() or not a.env_file.is_file():
    p.error("build binary and configure env file first")
home = Path.home()
dest = home / "Library/Application Support/ai-quota-frame"
dest.mkdir(parents=True, exist_ok=True)
label = "com.m1ntch0c0.ai-quota-frame"
domain = f"gui/{os.getuid()}"
subprocess.run(
    ["launchctl", "bootout", domain + "/" + label], capture_output=True, check=False
)
shutil.copy2(a.binary, dest / "ai-quota-frame.new")
os.replace(dest / "ai-quota-frame.new", dest / "ai-quota-frame")
if a.env_file.resolve() != (dest / ".env").resolve():
    fd = os.open(dest / ".env.new", os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "wb") as f, a.env_file.open("rb") as source:
        shutil.copyfileobj(source, f)
    os.replace(dest / ".env.new", dest / ".env")
(dest / ".env").chmod(0o600)
# Avoid placing any credential in launchd EnvironmentVariables or argv.
launcher = dest / "start.sh"
launcher.write_text("""#!/bin/sh
set -a
. "./.env"
set +a
exec /usr/bin/caffeinate -i -s ./ai-quota-frame
""")
launcher.chmod(0o700)
# macOS 15+ requires a responsible app identity for a user LaunchAgent's local
# network access. Follow Apple TN3179 instead of relying on Terminal privileges.
bundle = home / "Applications/PhotoPainter Renderer.app"
contents = bundle / "Contents"
executable = contents / "MacOS/photopainter-launcher"
executable.parent.mkdir(parents=True, exist_ok=True)
(contents / "Info.plist").write_bytes(
    plistlib.dumps(
        {
            "CFBundleIdentifier": label,
            "CFBundleName": "PhotoPainter Renderer",
            "CFBundleDisplayName": "PhotoPainter Renderer",
            "CFBundleExecutable": executable.name,
            "CFBundlePackageType": "APPL",
            "CFBundleVersion": "1",
            "LSUIElement": True,
            "NSLocalNetworkUsageDescription": "Send rendered quota images to your PhotoPainter on the local Wi-Fi network.",
        }
    )
)
subprocess.run(
    [
        "xcrun",
        "clang",
        "-Wall",
        "-Wextra",
        "-Werror",
        "-O2",
        str(Path(__file__).with_name("macos-launcher.c")),
        "-o",
        str(executable),
    ],
    check=True,
)
subprocess.run(
    ["codesign", "--force", "--sign", "-", "--identifier", label, str(bundle)],
    check=True,
)
subprocess.run(
    [
        "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister",
        "-f",
        str(bundle),
    ],
    check=True,
)
logs = home / "Library/Logs"
logs.mkdir(exist_ok=True)
plist = home / "Library/LaunchAgents" / f"{label}.plist"
plist.parent.mkdir(parents=True, exist_ok=True)
plist.write_bytes(
    plistlib.dumps(
        {
            "Label": label,
            "AssociatedBundleIdentifiers": [label],
            "ProgramArguments": [str(executable)],
            "WorkingDirectory": str(dest),
            "RunAtLoad": True,
            "KeepAlive": True,
            "ThrottleInterval": 30,
            "StandardOutPath": str(logs / "photopainter-render.log"),
            "StandardErrorPath": str(logs / "photopainter-render.error.log"),
        }
    )
)
if a.no_start:
    # Keep it unloaded across login/reboot until an explicit normal install.
    subprocess.run(["launchctl", "disable", domain + "/" + label], check=True)
    print(
        "PhotoPainter renderer installed but disabled; rerun without --no-start after hardware verification"
    )
else:
    subprocess.run(["launchctl", "enable", domain + "/" + label], check=True)
    subprocess.run(["launchctl", "bootstrap", domain, str(plist)], check=True)
    print("PhotoPainter renderer installed and started under launchd")
