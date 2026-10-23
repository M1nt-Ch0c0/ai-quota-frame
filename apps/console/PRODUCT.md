# PhotoPainter Console

<!-- impeccable:product-schema 1 -->

## Platform

adaptive

The user confirmed native macOS desktop. No iOS or Android delivery is claimed.

## Stack

SwiftUI and the existing Go service, connected through a per-user Unix socket. The UI consumes explicit safe telemetry, not ordinary service logs.

## Users

The device owner wants to understand which software runs where, whether automatic updates work, and how to diagnose the real PhotoPainter without deciphering command output.

## Product Purpose

Explain the architecture through interaction and connect those explanations to live device and service evidence.

## Operating Context

One Mac, one ESP32-S3 PhotoPainter, authenticated BLE, and the existing 15-minute quota service. The device need not have Wi-Fi. The mint-skin display theme remains unchanged.

## Capabilities and Constraints

Clickable architecture, service and device status, upload versus physical-completion distinction, application catalog, deliberate switching, and safe diagnostic history. The existing service stays the sole BLE owner. No host flashing, serial opening, secret display, fabricated live data, or public network listener. Retained pixels do not prove the device is online. POWERON does not prove a USB fault.

## Product Principles

- Answer ordinary questions before showing protocol details.
- Distinguish current observations, last observations, and unknown states.
- Explain an action's effect before performing it.
- Preserve the existing update worker and receipt recovery.

## Evidence on Hand

The Go BLE implementation and tests, host ABI 2.1, photoframe v18 validation, and the unresolved switch reset. No GUI exists yet. Human confirmation of the latest display remains outstanding.
