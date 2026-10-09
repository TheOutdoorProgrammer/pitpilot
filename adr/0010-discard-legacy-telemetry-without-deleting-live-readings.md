# 10. Discard legacy telemetry without deleting live readings

Date: 2026-10-09

## Status

Accepted. Supersedes [ADR-0009](0009-recover-receiver-history-through-immutable-event-archives-an.md).

## Context and Problem Statement

The owner rejected the recovered legacy history and requested removing imported LubeLogger datapoints while restoring the prior chart presentation.
Recovered events share the Pi source with genuine live collection, and some recovered projections reuse observations that existed before recovery.
A database rollback would also undo unrelated live writes and records, while a client-only filter would leave unwanted history in APIs and caches.

## Considered Options

1. Preview and atomically delete proven legacy-owned rows, with durable vehicle-scoped replay blocking
2. Restore the entire database to a pre-import snapshot
3. Delete all Pi and LubeLogger source rows
4. Hide imported values only in the native app

## Decision Outcome

Chosen: **option 1**.

Provide a local administrative preview/apply command bound to the current target state. Delete LubeLogger observations and contexts, plus only receiver-created Pi rows validated against original archived events and deterministic ownership keys. Preserve exact reused native rows, batch deduplication markers, import ledgers, converted-note evidence and all journal/vehicle/odometer data. Remove receiver event projections only after ownership validation, with verified independent backups retaining recovery evidence. Record a vehicle-scoped discard policy that rejects later legacy ingestion, summary refresh and receiver recovery; ordinary Pi and Smartcar ingestion remains available. Export this policy. Restore the preceding chart presentation and clear obsolete telemetry caches without wiping offline journal data or preferences. Expose a stable discard revision so an online client also invalidates history cached before the server cutover.

## Consequences

### Good

- Removes the unwanted history from native charts, APIs and metrics without rewinding live data.
- Explicit ownership preserves preexisting native rows reused by recovery and makes corruption an atomic conflict.
- Durable replay prevention and client revision checks keep deleted legacy history from reappearing.

### Bad

- A further schema migration and retained discard policy are required; old binaries need a matching pre-upgrade backup.
- Discarded history is unavailable in active queries, and restoring it requires an explicit recovery decision using protected backups.
- The client must invalidate telemetry caches and fetch current data after cutover.

### Rejected because

- A full snapshot restore would also discard legitimate post-backup writes and could regress credentials or odometer state.
- Deleting every Pi row cannot distinguish actual live collection from imported receiver history or its reused overlaps.
- A client-only filter would leave unwanted datapoints in server APIs, metrics and previously saved history.
