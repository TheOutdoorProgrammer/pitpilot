# 4. Use scoped collectors and application-owned Smartcar connections

Date: 2026-10-08

## Status

Accepted.

## Context and Problem Statement

PitPilot needs native Raspberry Pi collection, automatic updates and Smartcar connections without exposing household or provider credentials. Offline retries must preserve observation identity. Existing vehicle pipelines must remain operational during rollout.

## Considered Options

1. Household API credentials on collectors and legacy per-user Smartcar OAuth.
2. Scoped device enrollment, signed collector releases and Smartcar V3 application grants.
3. Public webhook-only ingestion for every provider.

## Decision Outcome

Chosen: **option 2**.

Use one-time 15-minute enrollment tokens to issue hashed, revocable device credentials bound to one vehicle. Check authorization in the same transaction as ingestion and namespace observation identities by device. Keep immutable offline batches until acknowledgment. Collect only after clock synchronization. Verify Ed25519-signed platform manifests against a pinned public key, preserve queue and configuration during updates, and retain a trusted health rollback. Smartcar uses server-only application credentials, native Connect completion with state validation, explicit vehicle binding, and a durable polling worker. Household owners may explicitly adopt an existing application grant without revoking the legacy integration. Keep integration credentials and bindings outside portable JSON export; private database recovery also requires the deployment encryption key. Preserve the legacy pipeline until replacement behavior is verified.

## Consequences

### Good

- Device credentials cannot read the garage or write another vehicle.
- Stable batch identities make retry after network or power loss safe.
- Smartcar V3 needs no per-user refresh tokens or public webhook receiver.
- Signed releases and health rollback constrain automatic updates.

### Bad

- A lost enrollment response requires another pairing after revoking the abandoned device.
- Sampling waits for synchronized time, leaving an explicit boot coverage gap.
- Polling has provider latency and quota costs.
- Encrypted integration recovery needs a separately backed-up key.

### Rejected because

- Household credentials exceed collector authority; Smartcar V2 refresh-token assumptions do not apply to V3.
- Webhook-only ingestion adds public exposure and cannot recover provider events lost after retry exhaustion.
