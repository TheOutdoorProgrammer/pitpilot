# 12. Prioritize durable delivery near a configured home geofence

Date: 2026-10-11

## Status

Accepted.

## Context and Problem Statement

An ignition-switched Pi may lose power shortly after arriving home.
The existing bbolt queue synchronizes each transaction, but upload failures can leave the collector waiting through a five-minute backoff just as home Wi-Fi becomes available.
Older collectors and independent updaters strictly decode their configuration, and home coordinates are private.

## Considered Options

1. Add a separately cached device policy that changes delivery scheduling near home
2. Shut down or remount storage read-only whenever the Pi enters home
3. Add geofence fields to the existing strict device configuration

## Decision Outcome

Chosen: **option 1**.

Use an administrator-configured radius and center delivered through a separate device-authenticated power-policy endpoint. Persist the policy privately, bound to both vehicle and device, before use. Reuse the existing GPS distance calculation and validated fixes. A fresh fix inside the radius wakes ordinary upload backoff and adds a queue/directory sync barrier. While inside, new queued data wakes idle delivery and retries cap their local backoff at five seconds, while server Retry-After remains authoritative. Retain per-transaction synchronization everywhere and delete only acknowledged data. Expire priority after 30 seconds without a fresh fix and use a 25-meter exit margin to avoid boundary jitter. Continue collection throughout; do not change power, networking, sampling cadence or receiver configuration.

## Consequences

### Good

- Home Wi-Fi reconnection can drain queued data promptly while the vehicle is still powered.
- Offline starts retain the last scoped policy without putting coordinates in telemetry or public configuration.
- The separate endpoint keeps old collector and updater configuration responses unchanged.

### Bad

- GPS availability and positioning error limit geofence detection, and the exit margin slightly extends priority beyond the configured boundary.
- Offline devices cannot receive policy changes until their next successful contact.
- More frequent bounded retries cost network traffic near home.
- Filesystem synchronization cannot guarantee SD-card survival under abrupt power loss.

### Rejected because

- Home entry also occurs while leaving or passing the house; shutting down or remounting storage would interrupt valid collection without proving engine-off.
- Adding fields to the existing strict configuration risks stranding older collectors and their independent updater before they can upgrade.

