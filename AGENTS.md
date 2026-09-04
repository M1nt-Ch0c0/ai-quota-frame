# AI quota frame agent guide

Read this file before changing the PC-side service. For blank-machine setup, cross-repository integration, deployment, or hardware diagnosis, also read the complete project Skill in the sibling host checkout: `../photopainter-host/.agents/skills/develop-photopainter-stack/SKILL.md`.

## Repository role

This repository owns quota collection, optional usage collection, snapshot freshness, Chrome-based rendering, strict six-color PNG production, latest-frame queueing, and active pushes to the PhotoPainter. It does not own device firmware, ELF loading, E6 timing, Wi-Fi provisioning, or an inbound service.

## Non-negotiable constraints

- Keep the process outbound-only. Do not restore the retired `:8787` listener or add a WebUI, album, OTA, Home Assistant, or device-pull flow.
- Render exactly 800×480 non-interlaced PNG with only opaque black, white, yellow, red, blue, and green pixels.
- Send raw `image/png` to the complete device `/api/push` URL with the independent Bearer token. Never send management, OAuth, CPAMP, or Wi-Fi secrets to the device.
- Do not use environment proxies or follow redirects for device pushes.
- Keep one serialized worker and at most the newest pending successful snapshot. Failed quota refreshes must invalidate obsolete queued work.
- Count only HTTP 200 as a completed physical refresh. Preserve the 11-minute client deadline unless the device timing contract changes.
- Retry transport failures and documented transient statuses, but avoid uncontrolled repeated physical refreshes when a network can drop the final response.
- Keep device push rejection and render failures from becoming tight retry loops.

## Build and test

Use the Go version declared by `go.mod` and a locally installed Chrome or Chromium.

```bash
make check
```

`make check` must run unit tests, race tests, vet, and a static build. Test rendering, cancellation, deduplication, replacement, retries, redirect rejection, and status classification with local test servers before involving hardware.

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

Stop the continuously running service before a one-shot test so two clients cannot race. Use the host repository's doctor for liveness; an unauthenticated 401 probe must not change the screen. After a real push, require HTTP 200 and human visual confirmation. Restart the service only after the test result and network stability are understood.
