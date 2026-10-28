#!/usr/bin/env python3
"""Privately migrate a retired Google gateway client to existing management keys.

The existing server key travels only through encrypted SSH and child-process
memory. No key is printed, passed in argv or written outside the 0600 runtime
configuration and its private backup. The device token and unrelated settings
are preserved byte-for-byte. This tool never starts the daemon or touches BLE.
"""

import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import shlex
import stat
import subprocess
import sys
import tempfile
from urllib.parse import urlsplit


REMOTE_KEY_READER = r'''
import json
from pathlib import Path
import time
from urllib.request import Request, build_opener, ProxyHandler
config=json.loads(Path("/etc/cpa-manager-plus/config.json").read_text())
key=Path(config["adminKeyFile"]).read_text().strip()
if not key or any(ord(c)<33 or ord(c)>126 for c in key):
    raise SystemExit(1)
opener=build_opener(ProxyHandler({}))
for url in (
    "http://127.0.0.1:8317/v0/management/auth-files",
    "http://127.0.0.1:18317/v0/management/dashboard/summary?today_start_ms="+str(int(time.time()*1000)-60000)+"&now_ms="+str(int(time.time()*1000)),
):
    with opener.open(Request(url, headers={"Authorization":"Bearer "+key}),timeout=15) as response:
        if response.status!=200: raise SystemExit(1)
print(json.dumps({"key":key}))
'''


def launch_environment(path):
    result = subprocess.run(
        ["/bin/sh", "-c", 'set -a; . "$1"; set +a; exec /usr/bin/env -0',
         "source-auth-migration", str(path)],
        cwd=path.parent, capture_output=True, check=True,
    )
    return dict(
        field.decode().split("=", 1)
        for field in result.stdout.split(b"\0") if b"=" in field
    )


def replace_assignments(raw, replacements):
    text = raw.decode("utf-8")
    for name, value in replacements.items():
        pattern = re.compile(r"^(?:export[ \t]+)?" + re.escape(name) + r"=.*$", re.M)
        matches = list(pattern.finditer(text))
        if len(matches) > 1:
            raise ValueError("duplicate target assignment")
        line = name + "=" + shlex.quote(value)
        if matches:
            text = pattern.sub(lambda _: line, text)
        else:
            if text and not text.endswith("\n"):
                text += "\n"
            text += line + "\n"
    return text.encode("utf-8")


def private_write(path, data):
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as output:
        output.write(data)
        output.flush()
        os.fsync(output.fileno())


def migrate(args):
    if not re.fullmatch(r"[a-zA-Z0-9_.-]+@[a-zA-Z0-9.-]+", args.ssh_host):
        raise ValueError("invalid SSH host")
    target = urlsplit(args.manager_url)
    if target.scheme != "https" or not target.hostname or target.username or target.password or target.query or target.fragment or target.path not in ("", "/"):
        raise ValueError("manager URL must be an HTTPS origin")
    path = args.env_file.expanduser().absolute()
    metadata = path.lstat()
    if not stat.S_ISREG(metadata.st_mode) or metadata.st_uid != os.getuid() or stat.S_IMODE(metadata.st_mode) != 0o600:
        raise ValueError("runtime configuration must be an owned regular 0600 file")
    before = path.read_bytes()
    environment = launch_environment(path)
    result = subprocess.run(
        ["ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=15", args.ssh_host,
         "python3 -c " + shlex.quote(REMOTE_KEY_READER)],
        capture_output=True, check=True, timeout=50,
    )
    key = json.loads(result.stdout)["key"]
    if not isinstance(key, str) or not key or any(ord(c) < 33 or ord(c) > 126 for c in key):
        raise ValueError("invalid server credential")
    bypass = []
    for value in (environment.get("NO_PROXY", ""), environment.get("no_proxy", ""), target.hostname):
        for host in value.split(","):
            if host.strip() and host.strip() not in bypass:
                bypass.append(host.strip())
    replacements = {
        "SOURCE_AUTH_MODE": "management-key",
        "CLIPROXY_BASE_URL": args.manager_url.rstrip("/"),
        "CPAMP_BASE_URL": args.manager_url.rstrip("/"),
        "CLIPROXY_MANAGEMENT_KEY": key,
        "CPAMP_ADMIN_KEY": key,
        "NO_PROXY": ",".join(bypass),
        "no_proxy": ",".join(bypass),
    }
    candidate = replace_assignments(before, replacements)
    environment.update(replacements)
    probe = subprocess.run(
        [str(args.probe_binary.absolute()), "source-check"],
        env=environment, cwd=path.parent, capture_output=True, timeout=150,
    )
    report = json.loads(probe.stdout)
    safe = {name: report[name] for name in (
        "auth_mode", "observed_at", "quota_ok", "usage_ok", "account_count",
        "warning_count", "account_states", "elapsed_ms",
        "quota_fresh", "fresh_account_count",
    ) if name in report}
    safe["configuration_changed"] = False
    if probe.returncode or not report.get("quota_ok") or not report.get("usage_ok") or not report.get("quota_fresh"):
        safe["migration_ready"] = False
        print(json.dumps(safe))
        return 1
    safe["migration_ready"] = True
    if args.apply:
        if path.read_bytes() != before:
            raise ValueError("configuration changed during verification")
        timestamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
        backup = path.with_name(path.name + ".before-source-auth-" + timestamp)
        private_write(backup, before)
        descriptor, temporary = tempfile.mkstemp(prefix=".source-auth-", dir=path.parent)
        try:
            with os.fdopen(descriptor, "wb") as output:
                output.write(candidate)
                output.flush()
                os.fsync(output.fileno())
            os.chmod(temporary, 0o600)
            os.replace(temporary, path)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)
        safe["configuration_changed"] = True
        safe["backup_path"] = str(backup)
    print(json.dumps(safe))
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ssh-host", required=True)
    parser.add_argument("--manager-url", required=True)
    parser.add_argument("--env-file", type=Path, default=Path.home() / "Library/Application Support/ai-quota-frame/.env")
    parser.add_argument("--probe-binary", type=Path, required=True)
    parser.add_argument("--apply", action="store_true", help="atomically save only after source verification succeeds")
    args = parser.parse_args()
    try:
        return migrate(args)
    except Exception:
        # Child stderr, response bodies and exception strings may contain
        # credentials. Never forward them to a terminal or service log.
        print("source authentication migration failed; configuration not replaced", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
