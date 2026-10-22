# BLE discovery and application packages

The host firmware must include the resident PhotoPainter BLE v1 service. BLE
finds the device when its Wi-Fi address changes and can install applications
without an IP route. **PNG pushes still use HTTP**: BLE does not bypass hotspot
client isolation, provide a cross-subnet route, or carry images in this version.

## Configuration

Scan explicitly to find the stable 12-digit device ID:

```sh
ai-quota-frame ble scan
```

Scan results are untrusted discovery metadata. Configure `PHOTOFRAME_DEVICE_ID`
in the service's process environment, alongside its existing independent
`PHOTOFRAME_PUSH_TOKEN`. `PHOTOFRAME_PUSH_URL` becomes an optional initial/fallback
address when a device ID is configured. Without an ID the existing HTTP-only
configuration continues to work. Do not pass tokens in command-line arguments.
The program does not read, rewrite or save `.env`, and does not save a new IP to
disk. Continue using the existing trusted launcher to load environment variables.

At startup and after a push failure the serialized worker scans for that ID,
connects and performs mutual HMAC authentication, then uses the **authenticated**
private IPv4 record to update its in-memory `/api/push` target. A spoofed broadcast
alone cannot redirect a Bearer-bearing request. Discovery is throttled to once
per 30 seconds, has a 30-second outer deadline and a 15-second scan window. A
failed scan keeps the last address. A successful scan does not immediately repeat
a failed physical push: existing status classification, deduplication, latest
snapshot behavior, retry delay and the 11-minute HTTP deadline still apply.

The device's SSID is represented by a truncated SHA-256 hash. It is not a secret
or authentication mechanism; common names can be guessed. Public/loopback/
multicast/unconfigured IP addresses are never adopted for automatic HTTP pushes.
If DHCP assigns an address outside RFC1918, use the explicit configured URL and
BLE application management; automatic target adoption intentionally requires a
private IPv4 address.

## Commands

These commands use the process environment's `PHOTOFRAME_PUSH_TOKEN`. They do
not require Chrome, quota credentials, an HTTP endpoint or a running pusher.
Stop the automatic pusher before deliberate activation/display validation.

```sh
ai-quota-frame ble info --device-id 001122334455
ai-quota-frame ble status --device-id 001122334455
ai-quota-frame ble inspect --file module.pkg
ai-quota-frame ble install --device-id 001122334455 --file module.pkg
ai-quota-frame ble activate --device-id 001122334455 --app photoframe
ai-quota-frame ble switch --device-id 001122334455 --app color-test
ai-quota-frame ble rollback --device-id 001122334455 --app photoframe
ai-quota-frame ble remove --device-id 001122334455 --app unused-app
```

`--device-id` can be omitted when `PHOTOFRAME_DEVICE_ID` is set. Package files
must come from the host's unchanged `tools/module.py package` format (4096-byte
header, ELF payload, per-payload SHA-256, format 1/2 and ABI 1/2). The client checks
the envelope/hash; the host additionally validates ELF/manifest/imports.

Install accepts at most 1 MiB, reports acknowledged upload progress on stderr,
and **only stages a candidate**. Explicit activate requests trial/confirmation
through the host's existing manager. An ABI 2 application confirms after a valid
START and required first display; ABI 1 still requires its first valid HTTP PNG.
Successful staging is not a completed display or confirmed version.

The transport supports MTU 23, up to 512-byte logical frames and 400-byte package
chunks. Full-package SHA-256 protects the transfer in addition to the existing
payload hash. Reconnect and resend the whole package after a broken session.
Queries do not activate anything. Never blindly repeat activation following a
timeout: reconnect and inspect status first, because the manager can finish an
already-started operation after the client disconnects. Installation has a
25-minute outer deadline; other operations retain an 11-minute bound.

The BLE link is not encrypted. The token is never transmitted, and nonces,
direction-specific HMACs and monotonic sequence numbers protect commands and
responses from modification and replay. Packages and status remain observable.
Use a strong random device token; do not reuse quota/management/OAuth secrets.
See the host's [wire protocol](https://github.com/M1nt-Ch0c0/photopainter-host/blob/codex/ble-discovery-app-ota/docs/ble-protocol.md).

## Platforms and validation

The dependency is pinned to `tinygo.org/x/bluetooth v0.16.0`. Windows uses native
WinRT and builds with `CGO_ENABLED=0`; operations initialize and release WinRT on
one locked OS thread. Linux uses BlueZ/D-Bus. macOS requires a native **CGO-enabled**
build, Xcode command-line tools and permission for Bluetooth in System Settings:

```sh
GOTOOLCHAIN=go1.26.0 make check
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o dist/ai-quota-frame.exe ./cmd/ai-quota-frame
# On macOS:
CGO_ENABLED=1 go build -o dist/ai-quota-frame ./cmd/ai-quota-frame
```

A macOS build with CGO disabled retains HTTP service functionality and reports
BLE unavailable. Some native platform calls cannot be cancelled by the library;
the caller's deadline still returns, while one retained adapter slot prevents
launching additional native operations until cleanup completes. This avoids an
unbounded accumulation of blocked scan/connect goroutines.

Local tests cover protocol vectors shared with the C host, wrong token/identity,
spoofed unsigned or modified responses, replay, package framing/checksums,
transfer loss, cancellation, explicit activation and frozen status pages. Pusher
tests cover rediscovery without an immediate repeat, fallback, throttle and
startup. `make check` runs tests, race tests, vet and a static build; rendering
tests requiring Chrome are skipped when Chrome is absent. Windows cross-build
passes. Native Windows/macOS radio behavior still requires the authorized
hardware checklist in the host's `docs/validation-ble.md`; no radio was opened
to develop or run these tests.

No listening port was added. The Windows watchdog must continue checking only
whether `ai-quota-frame.exe` exists; the retired `:8787/healthz` flow stays removed.
