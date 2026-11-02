# ai-quota-frame agent guide

Read this file before changing this repository. For blank-machine setup, cross-repository work, firmware flashing or hardware diagnosis, also read the project skill in the sibling host checkout: `../photopainter-host/.agents/skills/develop-photopainter-stack/SKILL.md`.

Product documentation (overview, usage, protocol, maintenance) lives in the esp32s3 repository: https://github.com/M1nt-Ch0c0/esp32s3/blob/main/README.md. Repository-specific references are listed in `README.md`.

## Repository role

This repository owns:

- quota and optional usage collection, snapshot freshness, Chrome-based rendering, strict six-color PNG production, latest-frame queueing and active BLE pushes (`cmd/ai-quota-frame`, `internal/`);
- the `aqf` debugging client and the daemon's two local Unix sockets (`internal/aqf`, `internal/console`);
- the browser plugin: `web/` (Web Bluetooth client and UI around the shared Go/WASM engine) and `plugins/photopainter/` (CPA wrapper, static resources, authenticated xAI subject projection).

It does not own device firmware, ELF loading, display timing, Wi-Fi provisioning, the pairing ceremony or the photoframe image protocol. The host owns the physical button, pairing display, NVS owner binding and reset. App protocol descriptions stay in the photoframe repository; client invocation and deployment stay here. The macOS GUI is the separate `photopainter-console` repository and talks to the daemon only through the console socket.

## Constraints

- Daemon network traffic is outbound only. Local IPC uses filesystem-protected Unix sockets. Do not add a TCP listener, device WebUI, album, OTA, Home Assistant or device-pull flow.
- The browser plugin reuses the full Go collector and renderer through WASM, including all themes, platform aggregation and usage statistics. Do not reimplement rendering in TypeScript. Use only existing CPA plugin routes and leave the official CPM frontend unchanged.
- Render exactly 800×480 non-interlaced PNG containing only opaque black, white, yellow, red, blue and green pixels.
- New installations use paired BLE (pairing v2): no device token, no IP lookup, no HTTP fallback. Explicit `PHOTOFRAME_PUSH_TRANSPORT=http` exists only for v1 firmware; v2 host HTTP control routes return 410.
- Never send management, OAuth, CPAMP or Wi-Fi secrets to the device. Persist only negotiated bond material: native files mode 0600 with per-device locking, browser keys as non-exportable `CryptoKey` in IndexedDB.
- Device pushes ignore environment proxies and do not follow redirects.
- Keep one serialized push worker and at most the newest pending successful snapshot. A failed quota refresh invalidates queued work.
- Only a matching signed image-completion receipt counts as display success. BLE image and package operations have a 90-minute budget; other device operations 11 minutes.
- After an ambiguous COMMIT, query the original request only; never redraw automatically. Browser transactions must be durably stored (IndexedDB `strict`) before COMMIT. Native pending identity is process-local, so do not restart a sender to discard an uncertain result.
- Never blindly repeat a management mutation (activate, switch, rollback, remove) after a lost reply. Reconnect and read signed status instead.
- Retry only transport failures and explicit signed BUSY rejections; keep render failures and device rejections out of tight retry loops.
- Browser and native clients are distinct owners. Before an ownership transfer, pause the previous owner's automatic pushes and release its connection. Do not resume a former owner automatically.
- User-facing copy shows actionable state, not protocol disclaimers.

## Build and test

Use the Go version declared in `go.mod` and a local Chrome or Chromium (`CHROME_BIN` to override).

```sh
make check
```

`make check` runs `go test ./...`, `go test -race ./...`, `go vet ./...` and builds `dist/ai-quota-frame` and `dist/aqf` (native CGO on macOS for CoreBluetooth, static elsewhere). Test rendering, cancellation, deduplication, replacement, retries, redirect rejection and status classification with local test servers before involving hardware.

Windows build:

```powershell
$env:CGO_ENABLED = "0"
go test ./...
go vet ./...
go build -buildvcs=false -trimpath -ldflags "-s -w" -o dist/ai-quota-frame.exe ./cmd/ai-quota-frame
```

Browser plugin (Node 22.18+):

```sh
cd web
npm ci --ignore-scripts
npm run check
node tools/check-frame.mjs
node tools/check-ui.mjs
cd ..
./plugins/photopainter/build.sh
```

Production plugin builds target Linux amd64; build them with the pinned Debian 12 Go image from `.github/workflows/web-plugin.yml` and verify with `plugins/photopainter/verify-native.py`. Keep `const version` in `plugins/photopainter/main.go` and `version` in `web/package.json` in sync. The CI workflow uploads `plugins/photopainter/README.md` and `web/README.md` with the build artifacts; keep both files.

## Configuration and secrets

Never open, print, commit or paste `.env` or its values. Trusted launch processes may consume it without displaying values. It is fine to report whether a required variable is set.

`PHOTOFRAME_PUSH_TOKEN` is only for explicit legacy v1 HTTP mode; it must be 32–128 printable non-whitespace ASCII bytes and differ from management keys. BLE mode ignores it. Do not place real keys in tests, examples, process arguments, logs or screenshots.

## Hardware tests

Hardware tests are compiled separately with `-tags photopainter_hardware` (see `docs/ble.md`); `make check` never opens a radio or draws.

- Stop automatic pushes (daemon and browser schedule) before any device test.
- Run display tests once. After an ambiguous result, read status and query the original receipt; do not rerun blindly.
- A completed push requires a matching signed receipt and human visual confirmation of content, orientation and colors.
- Resume automatic pushes only for an explicitly selected, currently paired owner after the outcome is understood.
- Back up the full 16 MiB flash (and the microSD card if present) before any flashing, following the host repository.
