#!/usr/bin/env python3
"""Run the installed macOS BLE CLI with its app identity and terminal output.

LaunchServices must open real files for stdout/stderr: /dev/stdout refers to
the launching service's descriptors, not reliably to the calling terminal.
The open exit status only reports launching/waiting; device results remain in
the underlying BLE CLI output.
"""

from pathlib import Path
import subprocess
import sys
import tempfile
import time


def main() -> int:
    if sys.platform != "darwin":
        print("pp requires macOS; use ai-quota-frame ble on other platforms", file=sys.stderr)
        return 2
    app = Path.home() / "Applications/PhotoPainter Renderer.app"
    if not app.is_dir():
        print("PhotoPainter Renderer is not installed in ~/Applications", file=sys.stderr)
        return 2
    if not sys.argv[1:] or sys.argv[1] in ("-h", "--help"):
        print("Usage: pp scan|pair|paired|pair-status|forget|info|status|install|activate|switch|rollback|remove|push [options]")
        print('Examples: pp pair --device-id 001122334455 --name "PhotoPainter Mac"')
        print("          pp paired; pp status; pp install --file /absolute/path/app.pkg")
        print("          pp activate --app my-app; pp switch --app photoframe")
        return 0

    with tempfile.TemporaryDirectory(prefix="photopainter-cli-") as directory:
        output = Path(directory) / "stdout"
        errors = Path(directory) / "stderr"
        output.touch(mode=0o600)
        errors.touch(mode=0o600)
        with output.open("rb") as out, errors.open("rb") as err:
            process = subprocess.Popen([
                "/usr/bin/open", "-n", "-W",
                "--stdout", str(output), "--stderr", str(errors),
                str(app), "--args", "ble", *sys.argv[1:],
            ])

            def forward() -> None:
                for source, target in ((out, sys.stdout), (err, sys.stderr)):
                    data = source.read()
                    if data:
                        target.buffer.write(data)
                        target.flush()

            try:
                while process.poll() is None:
                    forward()
                    time.sleep(0.1)
                forward()
                return process.returncode
            except KeyboardInterrupt:
                # Stopping open does not prove an admitted device job stopped.
                process.terminate()
                process.wait()
                forward()
                print("\n终端等待已中断；设备操作可能仍在执行，请用 pp status 核对结果。", file=sys.stderr)
                return 130


if __name__ == "__main__":
    raise SystemExit(main())
