# BLE discovery, images and application packages

**配对 v2 尚未实机验证。** This guide describes the current source contract.
Offline tests/builds and historical v1 radio results do not prove v2 physical
button approval, reboot persistence, revocation or display completion.

**Route:** BLE images prefer photoframe's ABI 2.2 CoC service on supported clients and retain its ABI 2.1 GATT path. The old
host PNG sender remains a compatibility implementation and regression baseline.
Discovery/auth/app OTA stay in the host. See the [application protocol](https://github.com/M1nt-Ch0c0/photoframe/blob/main/docs/ble-image-input.md)
and [developer Wiki](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/wiki/Home.md).

The v2 host firmware advertises protocol version 2 and supports physical pairing.
BLE finds the device by stable ID, pushes PNG images and installs applications
without a Wi-Fi route. New installations default to `PHOTOFRAME_PUSH_TRANSPORT=ble`.
The computer still needs Internet access for quota collection.

## Physical pairing

```sh
ai-quota-frame ble scan
ai-quota-frame ble pair --device-id 001122334455 --name "PhotoPainter desktop"
```

Use the scanned stable ID. Compare the six digits on the computer and frame,
then briefly press the GPIO4 function button to approve (not BOOT or power).
The device commits its single owner
relationship before reporting success. The client saves its candidate key before
arming the device prompt, so a lost approval reply can recover by authenticated
reconnection without repeating the physical ceremony.

Future BLE operations automatically use the saved relationship. No device token
is required. `ble paired` prints only public pairing metadata; `ble pair-status`
reads advisory device pairing state; `ble forget` only removes this computer's
credential. `ble bind` / `ble unbind` are aliases for physical pairing / local
forgetting. To change owners, hold the GPIO4 function button for at least eight seconds
and release it. The request is latched even during display, immediately blocking
new operations; an already-started physical refresh finishes before the owner is
cleared. This resets the relationship without changing Wi-Fi
or installed applications. Browser and native clients are distinct owners.

Native credentials are atomically saved to `ble-bonds/` beside the public
`ble-device.json` selection file under the OS user configuration directory.
Unix files use mode 0600 with file/directory sync; Windows uses write-through
publication and the user-profile directory's access control. Custom Windows
directories must have private ACLs. Each device has an OS file lock shared by
native processes. Candidate publication never overwrites an existing candidate;
confirmation and cleanup match the exact identity/key, so a late result cannot
resurrect a locally forgotten relationship. `PHOTOFRAME_BLE_BOND_DIR` may override the private directory with an
absolute path. Credentials are never included in CLI/status JSON. An explicit
`PHOTOFRAME_DEVICE_ID` selects a device but does not supply or bypass its bond.
The most recently confirmed pairing is the default when that setting is absent.

Every connection uses a fresh challenge and a key derived from the saved bond.
The pairing BID is pinned alongside image request identity; after reset or
re-pairing, an earlier owner's pending picture cannot be queried or redrawn.
If no bond exists, the background worker reports pairing required without
opening the radio or spending the image retry budget.

The v2 host rejects legacy op-1 fixed-token authentication and returns HTTP 410
for old token-based device management/mutations. Historical v1 firmware remains
available only through explicit legacy HTTP or low-level compatibility calls.
The native BLE daemon ignores a leftover `PHOTOFRAME_PUSH_TOKEN`; it never falls
back from a failed saved relationship to that value.

## Commands

On macOS, the terminal wrapper uses the installed app identity, privately loads
the existing configuration and streams BLE command output back to the terminal:

```sh
alias pp='python3 /absolute/path/ai-quota-frame/deploy/pp.py'
pp scan
pp pair --device-id 001122334455 --name "PhotoPainter Mac"
pp paired
pp status
```

The checkout path in the alias must match your computer. The wrapper needs no
rebuild or firmware update. It uses private temporary output files because
LaunchServices cannot reliably attach an app to `/dev/stdout` or `/dev/stderr`.
As with `open -W`, its exit code describes launching/waiting; inspect the BLE
CLI output and authenticated status to establish the device operation result.

These commands use the saved physical pairing. They do not require Chrome,
quota credentials, an HTTP endpoint or a running pusher.
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

The BLE link is not encrypted. The negotiated root key is never transmitted, and nonces,
direction-specific HMACs and monotonic sequence numbers protect commands and
responses from modification and replay. Images, packages and status remain observable.
Physical approval and matching six-digit comparison codes establish ownership;
quota/management/OAuth credentials never enter that ceremony. See the host's
[pairing protocol](https://github.com/M1nt-Ch0c0/photopainter-host/blob/main/docs/ble-pairing-v2.md)
and [signed command protocol](https://github.com/M1nt-Ch0c0/photopainter-host/blob/main/docs/ble-protocol.md).

## Image completion and recovery

`ble push` and the background BLE sender validate the complete PNG before radio
work: at most 5 MiB, 800×480, non-interlaced, opaque exact six colors, valid CRCs
and no trailing bytes. The host independently validates it through the same app
decoder and managed display path. v2 HTTP control is retired; its 410 response
is not a usable image transport. The app must already be selected;
an image does not switch applications. Existing ABI 1/2 PNG apps need no rebuild.

The sender negotiates image capabilities, creates a random request ID and uploads
authenticated chunks with a whole-image SHA-256. Only a signed completion result
after physical refresh and final POWER_OFF counts as success. The service saves
`last-frame.png` only then. Busy, partial reception and upload progress do not
prove display completion. The operation budget is 90 minutes to accommodate slow
BLE links; the existing single-worker/latest-snapshot queue remains in control.

If reception fails before COMMIT, a later attempt can resend the image. After an
ambiguous COMMIT, the same sender reconnects and queries the original receipt;
it does not send another draw command. The photoframe app keeps four receipts in RAM per
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
make check
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o dist/ai-quota-frame.exe ./cmd/ai-quota-frame
# On macOS:
CGO_ENABLED=1 go build -o dist/ai-quota-frame ./cmd/ai-quota-frame
```

A macOS build with CGO disabled retains HTTP service functionality and reports
BLE unavailable. Some native platform calls cannot be cancelled by the library;
the caller's deadline still returns, while one retained adapter slot prevents
launching additional native operations until cleanup completes. This avoids an
unbounded accumulation of blocked scan/connect goroutines.

Local tests cover physical-pairing vectors shared with C/OpenSSL/WebCrypto,
durable candidates, lost approval recovery, revoked binding identities,
legacy wrong-token/identity regression cases,
spoofed unsigned or modified responses, replay, package framing/checksums,
transfer loss, cancellation, explicit activation and frozen status pages. Pusher
tests cover rediscovery without an immediate repeat, fallback, throttle and
startup. `make check` runs tests, race tests, vet and a build (native CGO on macOS,
static elsewhere); rendering
tests requiring Chrome are skipped when Chrome is absent. Windows cross-build
passes. Pairing v2 remains **尚未实机验证**. Historical authorized Windows radio tests and their exact results are recorded in
the host's `docs/validation-ble.md`. Mac offline-image validation is recorded in
[`macos-deployment.md`](macos-deployment.md).

The historical v1 Windows test completed a 1 MiB package transfer in 554.5 seconds, verified
staging through the existing installer, removed the test candidate and preserved
the original running app. Subsequent authenticated discoveries took 5.2 and
8.7 seconds. This passed with the device on independent USB power. The same
device had POWERON resets on computer USB despite cable/port replacement and a
temporary selective-suspend comparison; the exact cause remains unresolved.
The Windows power setting was restored and the existing scheduled pusher resumed
after validation. These observations are not a long-duration endurance claim.

The following opt-in fixed-token hardware harness is retained for historical
v1 firmware validation; it is not the v2 pairing acceptance test.
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


## Package BEGIN admission (2026-10-27)

The package installer distinguishes connection, BEGIN, DATA and COMMIT failures and reports bytes actually sent. An authenticated BEGIN `BUSY=3` proves the request was not admitted, so it may be retried at 75 ms intervals for about two seconds. Transport errors, DATA and COMMIT are not automatically replayed by this rule. Post-operation signed status may establish that the requested state was reached despite a missing reply.
