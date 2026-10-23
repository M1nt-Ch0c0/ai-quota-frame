# BLE discovery, images and application packages

**Baseline and route:** the documented `0e94fab` sender uses the compatibility
host PNG extension. The approved replacement uses photoframe's ABI 2.1 service,
and is pending in this baseline. Keep discovery/auth/app OTA in the host. The
[developer Wiki](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/wiki/Home.md)
defines the framework boundary; do not treat these legacy push commands as the
unimplemented app-owned protocol.

The host firmware must include the resident PhotoPainter BLE v1 service. BLE
finds the device by its stable ID, pushes PNG images and installs applications
without an IP route. Set `PHOTOFRAME_PUSH_TRANSPORT=ble` for a device path that
does not require Wi-Fi. The default remains `http` for existing deployments.
The computer still needs Internet access to collect quota data.

## Configuration

Scan explicitly to find the stable 12-digit device ID:

```sh
ai-quota-frame ble scan
```

Scan results are untrusted discovery metadata. Configure `PHOTOFRAME_DEVICE_ID`
in the service's process environment, alongside its existing independent
`PHOTOFRAME_PUSH_TOKEN`. BLE image mode requires an ID or saved binding and a host
supporting the image extension. It never uses `PHOTOFRAME_PUSH_URL`, discovers an
IP target, or falls back to HTTP. In HTTP mode, `PHOTOFRAME_PUSH_URL` becomes an
optional initial/fallback address when a device ID is configured. Without an ID
the existing HTTP-only configuration continues to work. Do not pass tokens in command-line arguments.
The program does not read, rewrite or save `.env`, and does not save a new IP to
disk. Continue using the existing trusted launcher to load environment variables.

Alternatively, after loading the token through that launcher, bind once without
editing `.env`:

```sh
ai-quota-frame ble bind --device-id 001122334455
```

Bind authenticates the device before atomically saving only its public ID in
`ai-quota-frame/ble-device.json` under the OS user configuration directory
(`%APPDATA%` on Windows). The next service start uses this ID automatically.
The explicit environment ID takes precedence. `ble unbind` removes the binding;
restart the service to apply. `PHOTOFRAME_BLE_BINDING_FILE` may override the path
with an absolute filename. This separate JSON never contains the token, IP or
Wi-Fi credentials; a damaged record is rejected rather than silently replaced.

In HTTP mode, at startup and after a push failure the serialized worker scans for that ID,
connects and performs mutual HMAC authentication, then uses the **authenticated**
private IPv4 record to update its in-memory `/api/push` target. A spoofed broadcast
alone cannot redirect a Bearer-bearing request. Discovery is throttled to once
per 30 seconds and has a 30-second outer deadline. A CLI connection allows up to
30 seconds for scanning, including asynchronous Windows disconnect delays. A
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
ai-quota-frame ble push --device-id 001122334455 --file frame.png --app photoframe
ai-quota-frame ble inspect --file module.pkg
ai-quota-frame ble install --device-id 001122334455 --file module.pkg
ai-quota-frame ble activate --device-id 001122334455 --app photoframe
ai-quota-frame ble switch --device-id 001122334455 --app color-test
ai-quota-frame ble rollback --device-id 001122334455 --app photoframe
ai-quota-frame ble remove --device-id 001122334455 --app unused-app
```

`--device-id` can be omitted when `PHOTOFRAME_DEVICE_ID` or a saved binding is set. Package files
must come from the host's unchanged `tools/module.py package` format (4096-byte
header, ELF payload, per-payload SHA-256, format 1/2 and ABI 1/2). The client checks
the envelope/hash; the host additionally validates ELF/manifest/imports.

Install accepts at most 1 MiB, reports acknowledged upload progress on stderr,
and **only stages a candidate**. Explicit activate requests trial/confirmation
through the host's existing manager. An ABI 2 application confirms after a valid
START and required first display; ABI 1 still requires its first valid HTTP or BLE PNG.
Successful staging is not a completed display or confirmed version.

The transport supports MTU 23, up to 512-byte logical frames and 400-byte package
chunks. Full-package SHA-256 protects the transfer in addition to the existing
payload hash. Reconnect and resend the whole package after a broken session.
Queries do not activate anything. Never blindly repeat activation following a
timeout: reconnect and inspect status first, because the manager can finish an
already-started operation after the client disconnects. Installation has a
90-minute outer deadline to accommodate large packages at MTU 23; other operations
retain an 11-minute bound. The host still releases idle transfers after 60 seconds.

A custom GATT database change can invalidate Windows handles during activation.
The CLI records the expected app, bank, slot and version before a management
command. If its result becomes ambiguous while time remains, it reconnects once
and checks authenticated status against that expectation. It reports success
only when the requested running version or removal is proven; it never repeats
the mutation. An explicit device error such as busy is returned directly.

The BLE link is not encrypted. The token is never transmitted, and nonces,
direction-specific HMACs and monotonic sequence numbers protect commands and
responses from modification and replay. Images, packages and status remain observable.
Use a strong random device token; do not reuse quota/management/OAuth secrets.
See the host's [wire protocol](https://github.com/M1nt-Ch0c0/photopainter-host/blob/main/docs/ble-protocol.md).

## Image completion and recovery

`ble push` and the background BLE sender validate the complete PNG before radio
work: at most 5 MiB, 800×480, non-interlaced, opaque exact six colors, valid CRCs
and no trailing bytes. The host independently validates it through the same app
decoder and `app_manager_render()` used by HTTP. The app must already be selected;
an image does not switch applications. Existing ABI 1/2 PNG apps need no rebuild.

The sender negotiates image capabilities, creates a random request ID and uploads
authenticated chunks with a whole-image SHA-256. Only a signed completion result
after physical refresh and final POWER_OFF counts as success. The service saves
`last-frame.png` only then. Busy, partial reception and upload progress do not
prove display completion. The operation budget is 90 minutes to accommodate slow
BLE links; the existing single-worker/latest-snapshot queue remains in control.

If reception fails before COMMIT, a later attempt can resend the image. After an
ambiguous COMMIT, the same sender reconnects and queries the original receipt;
it does not send another draw command. The host keeps four receipts in RAM per
boot. A changed boot, missing receipt or metadata mismatch permanently rejects
that image in the current process, requiring inspection before an explicit new
attempt. This is not a durable exactly-once guarantee across process restarts.
An older host reports the missing capability explicitly; there is no HTTP fallback.

## Platforms and validation

Scanning and the Unix adapters use `tinygo.org/x/bluetooth`, pinned to official
commit `d679711fe223f8b4631cc9e172b49d193149711b`
(`v0.16.1-0.20261017194140-d679711fe223`). This unreleased revision includes
Windows advertisement UUID and scan-response fixes. The v0.16.0 disconnect
callback could release a session twice and crashed during device validation;
see [upstream fix](https://github.com/tinygo-org/bluetooth/commit/a0233b3d7010a9327ab34e346b0faf0f9e213922).

Windows GATT connections use `winrt-go` directly, with explicit ownership of
device, session, service and characteristic references. Operations initialize
and release WinRT on one locked OS thread. Uncached discovery establishes the
connection; cleanup releases characteristic references and closes services
before turning off `MaintainConnection`, closing the session and device. No
connection callback frees a live object. Sustained authenticated installs request
Windows throughput preference; unsupported adapters retain their defaults.
The build uses `CGO_ENABLED=0`. Linux uses BlueZ/D-Bus. macOS requires a native **CGO-enabled**
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
startup. `make check` runs tests, race tests, vet and a build (native CGO on macOS,
static elsewhere); rendering
tests requiring Chrome are skipped when Chrome is absent. Windows cross-build
passes. Authorized Windows radio tests and their exact results are recorded in
the host's `docs/validation-ble.md`. Mac offline-image validation is recorded in
[`macos-deployment.md`](macos-deployment.md).

The Windows test completed a 1 MiB package transfer in 554.5 seconds, verified
staging through the existing installer, removed the test candidate and preserved
the original running app. Subsequent authenticated discoveries took 5.2 and
8.7 seconds. This passed with the device on independent USB power. The same
device had POWERON resets on computer USB despite cable/port replacement and a
temporary selective-suspend comparison; the exact cause remains unresolved.
The Windows power setting was restored and the existing scheduled pusher resumed
after validation. These observations are not a long-duration endurance claim.

Device checks are separately compiled with `-tags photopainter_hardware`; normal
`make check` never opens a radio or displays an image. The opt-in tests require
the token only through the trusted process environment:

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -tags photopainter_hardware -c -o dist/photopainter-ble-hardware.exe ./internal/ble
```

`PHOTOFRAME_HARDWARE_TEST=stage-only` runs `TestHardwareBLEUpload` with an explicit
`PHOTOFRAME_HARDWARE_PACKAGE` for a previously absent `ble-echo` application; it
stages and removes the candidate without activation. `PHOTOFRAME_HARDWARE_HTTP=busy-only`
also checks HTTP 409 during reception. `discovery-only` runs
`TestHardwareDiscoveryDeadline`; `echo-probe`/`echo-absent` run
`TestHardwareApplicationGATT` after deliberate activation/switch-out.
`display-once` runs `TestHardwareHTTPDisplay`, requiring an explicit validated
5 MiB `PHOTOFRAME_HARDWARE_PNG` and the original photoframe selected. This last
mode performs **one physical refresh**, requires separate hardware authorization
and human visual confirmation, and must never be retried automatically after an
ambiguous result. Stop the automatic pusher/watchdog before all device checks.
`http-rejections` runs only that test's 401/413/422 probes and never sends the
valid image; it is suitable for finishing rejection checks after a completed
physical refresh without needlessly repainting the panel.
On Windows, `idle-expiry` runs `TestHardwareIdleExpiry`: it reserves a receive
buffer without committing a package, observes physical disconnection at the
60-second idle deadline, and checks fresh authentication, released memory and
manager admission. It checks uptime to distinguish expiration from a reboot.

`image-offline` runs `TestHardwareOfflineBLEImage` with an explicit valid PNG.
It requires the authenticated device IP to remain `0.0.0.0`, checks a partial
5 MiB PSRAM reservation and disconnect cleanup, rejects an invalid PNG, and
performs exactly one refresh. It deliberately discards the completed COMMIT
reply and recovers the receipt over a new connection, checking one COMMIT and
unchanged uptime/reset reason. It requires visual confirmation and must not be
blindly repeated after an ambiguous result.

No listening port was added. The Windows watchdog must continue checking only
whether `ai-quota-frame.exe` exists; the retired `:8787/healthz` flow stays removed.

## Application-owned BLE characteristics (ABI 2.1)

The host's ABI 2.1 extension lets the current application register a bounded custom
GATT service. The provided `ble-echo` example echoes a value without displaying
anything. With it active:

```sh
ai-quota-frame ble app-write --device-id 001122334455 --app ble-echo --characteristic 0 --text hello
ai-quota-frame ble app-read --device-id 001122334455 --app ble-echo --characteristic 0
```

The read prints JSON with text and an exact base64 byte representation. The CLI
checks that the named app is running, obtains its generation/service revision,
and sends authenticated op 13/14 through the permanent host channel. Index is
0–3; the app may expose fewer characteristics and values are at most 128 bytes.
Write success waits for the app's event completion; no immediate physical refresh
is implied. A switch invalidates the previous generation and service revision.
The CLI uses signed reads, rather than trusting untagged GATT notifications.
