# AI quota frame agent guide

**Documentation-only publication on main.** This guide describes the published feature baseline `0e94fab` and the app-owned receive direction. This commit does not merge those feature sources into main. Select the appropriate code branch from the [version/status matrix](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/wiki/Status-and-Evidence.md) before building or assuming SDK capabilities. New app-owned image code was verified on the Mac local feature branch but was not yet pushed at the status check.

Read this file before changing the PC-side service. For blank-machine setup, cross-repository integration, deployment, or hardware diagnosis, also read the complete project Skill in the sibling host checkout: `../photopainter-host/.agents/skills/develop-photopainter-stack/SKILL.md`.

## Repository role

This repository owns quota collection, optional usage collection, snapshot freshness, Chrome-based rendering, strict six-color PNG production, latest-frame queueing, and active pushes to the PhotoPainter. It does not own device firmware, ELF loading, E6 timing, Wi-Fi provisioning, or an inbound service.

See the [developer Wiki](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/wiki/Home.md) for framework/app boundaries. The 0e94fab image sender is the tested host-specific compatibility transport. The approved next route calls the photoframe ABI 2.1 application service, keeping discovery/auth/OTA generic; that route is not yet implemented in this documentation baseline. Keep app business protocol descriptions with the app, and client invocation/deployment here.

## Non-negotiable constraints

- Keep the process outbound-only. Do not restore the retired `:8787` listener or add a WebUI, album, OTA, Home Assistant, or device-pull flow.
- Render exactly 800×480 non-interlaced PNG with only opaque black, white, yellow, red, blue, and green pixels.
- HTTP sends raw `image/png` to `/api/push` with the independent device token. Explicit BLE mode sends authenticated PNG chunks without an IP request or HTTP fallback. Never send management, OAuth, CPAMP, or Wi-Fi secrets to the device.
- Do not use environment proxies or follow redirects for device pushes.
- Keep one serialized worker and at most the newest pending successful snapshot. Failed quota refreshes must invalidate obsolete queued work.
- Count only HTTP 200 or an authenticated BLE image-completion receipt as physical refresh success. HTTP keeps its 11-minute deadline; large BLE transfers have a bounded 90-minute operation budget.
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

`PHOTOFRAME_PUSH_TOKEN` must be 32–128 printable non-whitespace ASCII bytes and different from every management key. Do not place real values in tests, examples, process command lines, logs, or screenshots.

## Hardware tests

Stop the continuously running service before a one-shot test so two clients cannot race. Use the host repository's doctor for HTTP liveness; an unauthenticated 401 probe must not change the screen. After a real push, require HTTP 200 or an authenticated BLE completion result, plus human visual confirmation. After an ambiguous BLE COMMIT, recover its receipt without retransmitting the draw. Restart the service only after the test result and transport stability are understood.
