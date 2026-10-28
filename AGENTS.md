# AI quota frame agent guide

**Current source baseline (2026-10-29):** Physical pairing v2 replaces manual BLE tokens; browser and native clients share the original Go collection/rendering implementation through Go/WASM. **尚未实机验证**: the new pairing, reset, reboot persistence and image path still need the morning hardware acceptance. Historical ABI 2.2 CoC/photoframe v20 receipts do not validate this new firmware. The SwiftUI console remains in `photopainter-console`; source and evidence are tracked in the [status matrix](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/wiki/Status-and-Evidence.md).

Read this file before changing the PC-side service. For blank-machine setup, cross-repository integration, deployment, or hardware diagnosis, also read the complete project Skill in the sibling host checkout: `../photopainter-host/.agents/skills/develop-photopainter-stack/SKILL.md`.

## Repository role

This repository owns quota collection, optional usage collection, snapshot freshness, Chrome-based rendering, strict six-color PNG production, latest-frame queueing, and active pushes to the PhotoPainter. It does not own device firmware, ELF loading, E6 timing, Wi-Fi provisioning, or an inbound network service. The independent photopainter-console macOS app uses this backend through a filesystem-protected Unix socket and the same serialized BLE queue.

**User-authorized browser plugin scope (2026-10-29):** `web/` owns the Web Bluetooth client and UI around the shared Go/WASM collector and renderer; `plugins/photopainter/` owns the CPA wrapper, static resources and authenticated xAI subject projection. Use existing CPA routes and preserve the official CPM frontend. Browser and native clients are distinct owners: pause the previous owner's automatic pushes and release its connection before a physical ownership transfer. Do not automatically resume a former owner after the browser takes ownership. Persist only protected, negotiated bond material; never send management/OAuth credentials over BLE. Browser image transactions must be durable before COMMIT; ambiguous completion permits QUERY of the original request only.

See the [developer Wiki](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/wiki/Home.md) for framework/app boundaries. BLE image sending prefers the photoframe ABI 2.2 CoC service on supported clients and retains its ABI 2.1 GATT path, keeping discovery/auth/OTA generic. The older host-specific transport remains as a compatibility implementation and regression baseline. Keep app business protocol descriptions with the app, and client invocation/deployment here.

## Non-negotiable constraints

**Latest user scope (2026-10-29):** The browser plugin must reuse the full existing Go data and rendering implementation, including all themes, platform aggregation and usage statistics. A shared Go/WASM engine is permitted for that exact reuse. Replace manual fixed-token BLE setup with physically approved persistent pairing. Derived bond keys may be stored in protected native files or non-exportable browser CryptoKeys; no device token input or legacy-token fallback is allowed on v2 hosts. The host owns the physical button, pairing display, NVS owner binding and reset. These explicit requirements supersede the earlier fixed-token browser MVP and its reduced snapshot/canvas pipeline. Product copy must show actionable state, not permanent protocol disclaimers.

- Keep daemon network traffic outbound-only; local console IPC may use the protected Unix socket. The explicitly authorized CPM browser plugin uses existing CPA resource routes. Do not restore the retired `:8787` listener or add a device WebUI, album, OTA, Home Assistant, or device-pull flow.
- Render exactly 800×480 non-interlaced PNG with only opaque black, white, yellow, red, blue, and green pixels.
- New installations use paired BLE without a device token, IP request or HTTP fallback. Explicit legacy HTTP mode sends raw `image/png` to old v1 firmware only; v2 host control routes return 410. Never send management, OAuth, CPAMP, or Wi-Fi secrets to the device.
- Do not use environment proxies or follow redirects for device pushes.
- Keep one serialized worker and at most the newest pending successful snapshot. Failed quota refreshes must invalidate obsolete queued work.
- Count only a matching authenticated BLE image-completion receipt as v2 physical refresh success. Historical HTTP success requires 200 and its 11-minute deadline; large BLE transfers retain a bounded 90-minute operation budget.
- Retry transport failures and documented transient statuses, but avoid uncontrolled repeated physical refreshes when a network can drop the final response.
- Keep device push rejection and render failures from becoming tight retry loops.

## Build and test

Use the Go version declared by `go.mod` and a locally installed Chrome or Chromium.

```bash
make check
```

`make check` must run unit tests, race tests, vet, and a build (static on Linux/Windows; native CGO on macOS for CoreBluetooth). Test rendering, cancellation, deduplication, replacement, retries, redirect rejection, and status classification with local test servers before involving hardware.

For a Windows build:

```powershell
$env:CGO_ENABLED = "0"
go test ./...
go vet ./...
go build -buildvcs=false -trimpath -ldflags "-s -w" `
  -o dist/ai-quota-frame.exe ./cmd/ai-quota-frame
```

## Configuration and secrets

Never open, print, commit, or paste `.env` or its secret values. Trusted launch processes may consume it without displaying values. It is safe to report whether required variables are set and to report the non-secret device URL only when needed.

`PHOTOFRAME_PUSH_TOKEN` is only for explicitly selected legacy v1 HTTP compatibility and must then be 32–128 printable non-whitespace ASCII bytes, distinct from management keys. BLE ignores it. New bond files are private, atomically published and locked per device; candidate creation cannot overwrite a pending relationship, and stale confirmation/cleanup must not overwrite or resurrect another one. Do not place real keys in tests, examples, process arguments, logs or screenshots.

## Hardware tests

Stop automatic pushes before a one-shot test so clients cannot race. Historical HTTP 401 and v2 retired-route 410 probes only establish reachability, not application readiness. After a v2 push require a matching authenticated BLE completion result and human visual confirmation. After an ambiguous COMMIT, query the original receipt without redrawing. Resume only an explicitly selected, currently paired owner after the operation is understood. Native pending image identity is process-local; do not restart a sender to discard an uncertain result.
